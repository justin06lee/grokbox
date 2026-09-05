// Package server implements the grokbox room server: an HTTP service that
// holds rooms, checks keys, and hands every member the same stream of
// messages.
package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/justin06lee/grokbox/internal/proto"
)

// Build is stamped with the binary's version by the CLI.
var Build = "dev"

// RoomSpec declares a room the server should host. An empty Key means "reuse
// the stored key, or mint a fresh one".
type RoomSpec struct {
	Name string
	Key  string
}

// Config configures a Server.
type Config struct {
	Addr      string        // listen address, e.g. ":7777"
	Rooms     []RoomSpec    // rooms to host
	Open      bool          // allow joiners to create unknown rooms
	StoreDir  string        // "" for a memory-only server
	History   int           // messages retained (and replayed) per room
	Idle      time.Duration // drop a member unheard from for this long
	Advertise string        // public base URL to put in invites

	// TLS serves HTTPS. With no certificate supplied the server signs its
	// own and puts the hash of it in every invite, which is what lets a room
	// on a bare IP be private without anybody owning a domain. Turn it off
	// only when something in front of the server is already terminating TLS.
	TLS     bool
	TLSCert string
	TLSKey  string

	Logf func(string, ...any)
}

// Server hosts a set of rooms over HTTP.
type Server struct {
	cfg     Config
	store   *store
	started time.Time

	host        string // what goes in invites
	reach       Reach
	cert        *tls.Certificate
	fingerprint string

	mu    sync.RWMutex
	rooms map[string]*Room
}

// New builds a server, restoring rooms and transcripts from the store if one
// is configured.
func New(cfg Config) (*Server, error) {
	if cfg.History <= 0 {
		cfg.History = 200
	}
	if cfg.Idle <= 0 {
		cfg.Idle = 90 * time.Second
	}
	if cfg.Logf == nil {
		cfg.Logf = log.New(os.Stderr, "", log.Ltime).Printf
	}

	s := &Server{cfg: cfg, rooms: map[string]*Room{}, started: time.Now()}
	s.resolveHost()
	if err := s.setupTLS(); err != nil {
		return nil, err
	}

	if cfg.StoreDir != "" {
		st, err := openStore(cfg.StoreDir)
		if err != nil {
			return nil, err
		}
		s.store = st
	}

	// Keys already on disk keep working, so invites survive a restart.
	keys := map[string]string{}
	if s.store != nil {
		recs, err := s.store.loadRooms()
		if err != nil {
			return nil, err
		}
		for _, rec := range recs {
			keys[rec.Name] = rec.Key
		}
	}

	specs := cfg.Rooms
	for name := range keys {
		found := false
		for _, sp := range specs {
			if sp.Name == name {
				found = true
			}
		}
		if !found {
			specs = append(specs, RoomSpec{Name: name})
		}
	}

	for _, sp := range specs {
		name, err := proto.CleanRoom(sp.Name)
		if err != nil {
			return nil, fmt.Errorf("room %q: %w", sp.Name, err)
		}
		key := sp.Key
		if key == "" {
			key = keys[name]
		}
		if key == "" {
			key = proto.NewKey()
		}
		if len(key) > proto.MaxKeyLen {
			return nil, fmt.Errorf("room %q: key is longer than %d characters", name, proto.MaxKeyLen)
		}
		s.rooms[name] = s.newRoom(name, key)
		keys[name] = key
	}

	if err := s.persistRooms(); err != nil {
		return nil, err
	}
	if s.store != nil {
		if err := s.store.saveServerInfo(serverInfo{BaseURL: s.BaseURL(), Fingerprint: s.fingerprint}); err != nil {
			cfg.Logf("warn  cannot record the server address: %v", err)
		}
	}
	return s, nil
}

// StoredInvites rebuilds the invite for every room in a server's store,
// without starting anything. It is how you get the code again on a machine
// where the server is running as a service and its startup output is gone.
func StoredInvites(dir string) ([]proto.Invite, error) {
	st, err := openStore(dir)
	if err != nil {
		return nil, err
	}
	defer st.Close()

	info, err := st.loadServerInfo()
	if err != nil {
		return nil, fmt.Errorf("%s does not look like a grokbox server store: %w", dir, err)
	}
	rooms, err := st.loadRooms()
	if err != nil {
		return nil, err
	}
	out := make([]proto.Invite, 0, len(rooms))
	for _, r := range rooms {
		out = append(out, proto.Invite{
			Server:      info.BaseURL,
			Room:        r.Name,
			Key:         r.Key,
			Fingerprint: info.Fingerprint,
		})
	}
	return out, nil
}

func (s *Server) newRoom(name, key string) *Room {
	r := newRoom(name, key, s.cfg.History, s.store)
	if s.store != nil {
		r.seed(s.store.loadHistory(name, s.cfg.History))
	}
	return r
}

func (s *Server) persistRooms() error {
	if s.store == nil {
		return nil
	}
	s.mu.RLock()
	recs := make([]roomRecord, 0, len(s.rooms))
	for name, r := range s.rooms {
		recs = append(recs, roomRecord{Name: name, Key: r.Key()})
	}
	s.mu.RUnlock()
	return s.store.saveRooms(recs)
}

// resolveHost works out, once, what address invites should point at.
func (s *Server) resolveHost() {
	if s.cfg.Advertise != "" {
		s.host, s.reach = hostOf(s.cfg.Advertise), ReachAdvertised
		return
	}
	// An explicit listen host is a decision already made; only a wildcard
	// leaves the question open.
	if host, _ := splitAddr(s.cfg.Addr); host != "" && host != "0.0.0.0" && host != "::" && host != "[::]" {
		s.host, s.reach = host, ReachLocal
		if ip := net.ParseIP(host); ip != nil && isGloballyRoutable(ip) {
			s.reach = ReachPublic
		}
		return
	}
	s.host, s.reach = publicHost()
}

// setupTLS loads or mints the certificate this server presents.
func (s *Server) setupTLS() error {
	if !s.cfg.TLS {
		return nil
	}
	if s.cfg.TLSCert != "" && s.cfg.TLSKey != "" {
		cert, err := tls.LoadX509KeyPair(s.cfg.TLSCert, s.cfg.TLSKey)
		if err != nil {
			return fmt.Errorf("cannot load the certificate: %w", err)
		}
		// A certificate with an authority behind it verifies the ordinary
		// way, so the invite says nothing about it.
		s.cert = &cert
		return nil
	}
	cert, fingerprint, err := selfSigned(s.cfg.StoreDir, certHosts(s.host))
	if err != nil {
		return err
	}
	s.cert, s.fingerprint = &cert, fingerprint
	return nil
}

// TLSConfig is what this server presents, or nil when it is serving plain
// HTTP. It is exported so the handler can be mounted in somebody else's
// server without losing the certificate that its invites pin.
func (s *Server) TLSConfig() *tls.Config {
	if s.cert == nil {
		return nil
	}
	return &tls.Config{
		Certificates: []tls.Certificate{*s.cert},
		MinVersion:   tls.VersionTLS12,
	}
}

// Reach reports how far the advertised address goes.
func (s *Server) Reach() Reach { return s.reach }

// Fingerprint is the hash of a self-signed certificate, or empty when the
// server presents one that verifies on its own.
func (s *Server) Fingerprint() string { return s.fingerprint }

// Rooms lists the hosted rooms, alphabetically.
func (s *Server) Rooms() []*Room {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Room, 0, len(s.rooms))
	for _, r := range s.rooms {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func (s *Server) room(name string) *Room {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rooms[name]
}

// BaseURL is the address to hand out in invites.
func (s *Server) BaseURL() string {
	if s.cfg.Advertise != "" {
		return normalizeBase(s.cfg.Advertise, s.tls())
	}
	_, port := splitAddr(s.cfg.Addr)
	scheme := "http"
	if s.tls() {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(s.host, port))
}

// Invite renders the shareable code for one room.
func (s *Server) Invite(room *Room) proto.Invite {
	return proto.Invite{
		Server:      s.BaseURL(),
		Room:        room.Name(),
		Key:         room.Key(),
		Fingerprint: s.fingerprint,
	}
}

func (s *Server) tls() bool { return s.cert != nil }

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/join", s.handleJoin)
	mux.HandleFunc("POST /v1/send", s.handleSend)
	mux.HandleFunc("POST /v1/leave", s.handleLeave)
	mux.HandleFunc("GET /v1/messages", s.handleMessages)
	mux.HandleFunc("GET /v1/members", s.handleMembers)
	mux.HandleFunc("GET /v1/stream", s.handleStream)
	mux.HandleFunc("GET /v1/health", s.handleHealth)
	mux.HandleFunc("GET /", s.handleIndex)
	return mux
}

// Run serves until the context is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: /v1/stream is a long-lived response by design.
		IdleTimeout: 2 * time.Minute,
		ErrorLog:    log.New(discard{}, "", 0),
	}

	go s.janitor(ctx)

	errc := make(chan error, 1)
	go func() {
		if cfg := s.TLSConfig(); cfg != nil {
			srv.TLSConfig = cfg
			errc <- srv.ListenAndServeTLS("", "")
			return
		}
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		if s.store != nil {
			s.store.Close()
		}
		return nil
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func (s *Server) janitor(ctx context.Context) {
	// Sweep often enough that a short idle timeout means what it says, but
	// never faster than once a second.
	every := s.cfg.Idle / 3
	if every > 15*time.Second {
		every = 15 * time.Second
	}
	if every < time.Second {
		every = time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, r := range s.Rooms() {
				r.reapIdle(s.cfg.Idle)
			}
		}
	}
}

// ---------------------------------------------------------------- handlers

func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	var req proto.JoinRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Version != 0 && req.Version != proto.Version {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("this server speaks grokbox protocol v%d, your client speaks v%d — update one of them", proto.Version, req.Version))
		return
	}
	room, err := proto.CleanRoom(req.Room)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	name, err := proto.CleanName(req.Name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Key == "" {
		writeErr(w, http.StatusUnauthorized, "a room key is required")
		return
	}

	rm := s.room(room)
	if rm == nil {
		if !s.cfg.Open {
			// Do not distinguish "no such room" from "wrong key": that would
			// let anyone enumerate the rooms on a server.
			writeErr(w, http.StatusForbidden, errBadKey.Error())
			return
		}
		rm = s.createRoom(room, req.Key)
	}
	if !rm.checkKey(req.Key) {
		writeErr(w, http.StatusForbidden, errBadKey.Error())
		return
	}

	token, hist, members, seq, err := rm.Join(name, req.Token)
	if err != nil {
		code := http.StatusConflict
		if errors.Is(err, errRoomFull) {
			code = http.StatusServiceUnavailable
		}
		writeErr(w, code, err.Error())
		return
	}
	s.cfg.Logf("join  %s as %s", room, name)

	writeJSON(w, http.StatusOK, proto.JoinResponse{
		Token:   token,
		Name:    name,
		Room:    room,
		Seq:     seq,
		History: hist,
		Members: members,
		Server:  Build,
		Version: proto.Version,
	})
}

// createRoom adds a room on demand in --open mode, without clobbering a room
// another request created first.
func (s *Server) createRoom(name, key string) *Room {
	s.mu.Lock()
	if existing := s.rooms[name]; existing != nil {
		s.mu.Unlock()
		return existing
	}
	rm := s.newRoom(name, key)
	s.rooms[name] = rm
	s.mu.Unlock()

	s.cfg.Logf("room  %s created on join", name)
	if err := s.persistRooms(); err != nil {
		s.cfg.Logf("warn  cannot persist rooms: %v", err)
	}
	return rm
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	rm, token, ok := s.auth(w, r)
	if !ok {
		return
	}
	var req proto.SendRequest
	if !decode(w, r, &req) {
		return
	}
	text, err := proto.CleanText(req.Text)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	kind := proto.KindChat
	if req.Kind == proto.KindAction {
		kind = proto.KindAction
	}

	seq, err := rm.Say(token, kind, text)
	if err != nil {
		code := http.StatusForbidden
		if errors.Is(err, errRateLimited) {
			code = http.StatusTooManyRequests
			w.Header().Set("Retry-After", "1")
		}
		writeErr(w, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, proto.SendResponse{Seq: seq})
}

func (s *Server) handleLeave(w http.ResponseWriter, r *http.Request) {
	rm, token, ok := s.auth(w, r)
	if !ok {
		return
	}
	rm.Leave(token, "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	rm, _, ok := s.auth(w, r)
	if !ok {
		return
	}
	since := intParam(r, "since", 0)
	wait := time.Duration(intParam(r, "wait", 0)) * time.Second
	if wait > 60*time.Second {
		wait = 60 * time.Second
	}

	var msgs []proto.Message
	var seq int64
	if wait > 0 {
		msgs, seq = rm.Wait(r.Context(), since, wait)
	} else {
		msgs, seq = rm.Since(since)
	}
	writeJSON(w, http.StatusOK, proto.MessagesResponse{
		Messages: msgs,
		Seq:      seq,
		Members:  rm.Members(),
	})
}

func (s *Server) handleMembers(w http.ResponseWriter, r *http.Request) {
	rm, _, ok := s.auth(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": rm.Members()})
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	rm, token, ok := s.auth(w, r)
	if !ok {
		return
	}
	since := intParam(r, "since", 0)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // tell nginx-style proxies not to buffer
	w.WriteHeader(http.StatusOK)

	rc := http.NewResponseController(w)
	fmt.Fprintf(w, ": grokbox stream\nretry: 2000\n\n")
	_ = rc.Flush()

	for {
		msgs, _ := rm.Wait(r.Context(), since, 20*time.Second)
		if r.Context().Err() != nil {
			return
		}
		if rm.touch(token) == nil {
			fmt.Fprintf(w, "event: bye\ndata: {\"error\":\"session ended\"}\n\n")
			_ = rc.Flush()
			return
		}
		if len(msgs) == 0 {
			fmt.Fprint(w, ": ping\n\n") // keep proxies and NAT tables awake
			if err := rc.Flush(); err != nil {
				return
			}
			continue
		}
		for _, m := range msgs {
			b, err := json.Marshal(m)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", m.Seq, b)
			since = m.Seq
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"server":  Build,
		"version": proto.Version,
		"rooms":   len(s.Rooms()),
		"uptime":  time.Since(s.started).Round(time.Second).String(),
	})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeErr(w, http.StatusNotFound, "no such endpoint")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, `grokbox %s — a chat room behind a key.

This is not a website. Join it from a terminal:

    go install github.com/justin06lee/grokbox@latest
    grokbox join <invite-code> --name <your-name>

Ask whoever runs this server for the invite code.
`, Build)
}

// ------------------------------------------------------------------ helpers

// auth resolves the bearer token to a live session. Tokens carry their room
// name, so one lookup finds both.
func (s *Server) auth(w http.ResponseWriter, r *http.Request) (*Room, string, bool) {
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if token == "" {
		token = r.URL.Query().Get("token") // for curl and EventSource
	}
	roomName, _, found := strings.Cut(token, "~")
	if !found || roomName == "" {
		writeErr(w, http.StatusUnauthorized, "missing or malformed session token — join the room first")
		return nil, "", false
	}
	rm := s.room(roomName)
	if rm == nil || rm.touch(token) == nil {
		writeErr(w, http.StatusUnauthorized, "session expired — join the room again")
		return nil, "", false
	}
	return rm, token, true
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "cannot read request: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, proto.Error{Error: msg})
}

func intParam(r *http.Request, name string, def int64) int64 {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return def
	}
	return n
}

func splitAddr(addr string) (host, port string) {
	if addr == "" {
		return "", "7777"
	}
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, "7777"
	}
	if p == "" {
		p = "7777"
	}
	return h, p
}

func normalizeBase(base string, tls bool) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.Contains(base, "://") {
		return base
	}
	if tls {
		return "https://" + base
	}
	return "http://" + base
}

// LocalIP guesses the address other machines on this network can reach.
func LocalIP() string {
	// No packet is sent; the kernel just picks the route it would use.
	c, err := net.Dial("udp", "1.1.1.1:80")
	if err == nil {
		defer c.Close()
		if host, _, err := net.SplitHostPort(c.LocalAddr().String()); err == nil {
			return host
		}
	}
	return "127.0.0.1"
}

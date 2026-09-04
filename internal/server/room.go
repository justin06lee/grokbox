package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/justin06lee/grokbox/internal/proto"
)

// Errors a room can return to the HTTP layer.
var (
	errBadKey      = errors.New("unknown room or wrong key")
	errNameTaken   = errors.New("that name is already in the room")
	errRoomFull    = errors.New("room is full")
	errRateLimited = errors.New("you are sending messages too quickly")
)

// session is one connected participant.
type session struct {
	token    string
	name     string
	joined   time.Time
	lastSeen time.Time

	// token bucket: burst messages up front, then one per refillEvery.
	allowance float64
	lastSpend time.Time
}

const (
	rateBurst   = 10
	refillEvery = time.Second
)

// Room holds the state of one chat room: its key, its backlog, and whoever is
// currently in it. Every field is guarded by mu.
type Room struct {
	name string

	mu      sync.Mutex
	key     string
	seq     int64
	history []proto.Message
	byToken map[string]*session
	byName  map[string]*session

	// updated is closed (and replaced) every time the room gains a message,
	// which is how long-polls and streams learn there is something to read.
	updated chan struct{}

	historyN int
	store    *store
}

func newRoom(name, key string, historyN int, st *store) *Room {
	return &Room{
		name:     name,
		key:      key,
		byToken:  map[string]*session{},
		byName:   map[string]*session{},
		updated:  make(chan struct{}),
		historyN: historyN,
		store:    st,
	}
}

// Name reports the room's name.
func (r *Room) Name() string { return r.name }

// Key reports the room's key, for printing invites at startup.
func (r *Room) Key() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.key
}

func (r *Room) checkKey(key string) bool {
	r.mu.Lock()
	want := r.key
	r.mu.Unlock()
	return subtle.ConstantTimeCompare([]byte(key), []byte(want)) == 1
}

// Join admits a member and returns their session token plus the current
// backlog. The name must already be validated. A reclaim token belonging to
// the same name replaces that session silently, which is what a reconnecting
// client presents.
func (r *Room) Join(name, reclaim string) (token string, hist []proto.Message, members []proto.Member, seq int64, err error) {
	now := time.Now().UTC()

	r.mu.Lock()
	if len(r.byToken) >= proto.MaxRoomMember {
		r.mu.Unlock()
		return "", nil, nil, 0, errRoomFull
	}
	reconnect := false
	if old, clash := r.byName[name]; clash {
		if reclaim == "" || old.token != reclaim {
			r.mu.Unlock()
			return "", nil, nil, 0, errNameTaken
		}
		delete(r.byToken, old.token)
		delete(r.byName, name)
		reconnect = true
	}
	s := &session{
		// The room name rides along in the token so the HTTP layer can find
		// the room from the token alone. "~" is not legal in a room name.
		token:     r.name + "~" + proto.NewToken(),
		name:      name,
		joined:    now,
		lastSeen:  now,
		allowance: rateBurst,
		lastSpend: now,
	}
	r.byToken[s.token] = s
	r.byName[name] = s
	r.mu.Unlock()

	if !reconnect {
		r.post(proto.KindJoin, name, name+" joined")
	}

	// Snapshot after the join notice so the backlog a newcomer receives ends
	// with their own arrival, and their cursor starts there.
	r.mu.Lock()
	hist = append(hist, r.history...)
	seq = r.seq
	members = r.membersLocked()
	r.mu.Unlock()

	return s.token, hist, members, seq, nil
}

// Leave removes a member. It is safe to call for an unknown token.
func (r *Room) Leave(token, why string) {
	r.mu.Lock()
	s := r.byToken[token]
	if s == nil {
		r.mu.Unlock()
		return
	}
	delete(r.byToken, token)
	delete(r.byName, s.name)
	r.mu.Unlock()

	text := s.name + " left"
	if why != "" {
		text += " (" + why + ")"
	}
	r.post(proto.KindLeave, s.name, text)
}

// touch marks a session as alive and returns it, or nil if the token is
// unknown.
func (r *Room) touch(token string) *session {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.byToken[token]
	if s != nil {
		s.lastSeen = time.Now().UTC()
	}
	return s
}

// Say records a message from a member, subject to their rate limit.
func (r *Room) Say(token, kind, text string) (int64, error) {
	now := time.Now().UTC()

	r.mu.Lock()
	s := r.byToken[token]
	if s == nil {
		r.mu.Unlock()
		return 0, errors.New("not in this room")
	}
	s.lastSeen = now
	s.allowance += now.Sub(s.lastSpend).Seconds() / refillEvery.Seconds()
	if s.allowance > rateBurst {
		s.allowance = rateBurst
	}
	s.lastSpend = now
	if s.allowance < 1 {
		r.mu.Unlock()
		return 0, errRateLimited
	}
	s.allowance--
	name := s.name
	r.mu.Unlock()

	return r.post(kind, name, text), nil
}

// post appends a message and wakes every waiter.
func (r *Room) post(kind, from, text string) int64 {
	r.mu.Lock()
	r.seq++
	m := proto.Message{
		Seq:  r.seq,
		Room: r.name,
		Kind: kind,
		From: from,
		Text: text,
		Time: time.Now().UTC(),
	}
	r.history = append(r.history, m)
	if len(r.history) > r.historyN {
		r.history = append([]proto.Message(nil), r.history[len(r.history)-r.historyN:]...)
	}
	close(r.updated)
	r.updated = make(chan struct{})
	st := r.store
	r.mu.Unlock()

	if st != nil {
		st.append(r.name, m)
	}
	return m.Seq
}

// Since returns every retained message after the given cursor, plus the
// room's latest sequence number.
func (r *Room) Since(since int64) ([]proto.Message, int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sinceLocked(since), r.seq
}

func (r *Room) sinceLocked(since int64) []proto.Message {
	out := make([]proto.Message, 0, 8)
	for _, m := range r.history {
		if m.Seq > since {
			out = append(out, m)
		}
	}
	return out
}

// Wait blocks until there is at least one message after since, the context is
// cancelled, or the deadline passes. A nil slice means nothing new arrived.
func (r *Room) Wait(ctx context.Context, since int64, timeout time.Duration) ([]proto.Message, int64) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		r.mu.Lock()
		if msgs := r.sinceLocked(since); len(msgs) > 0 {
			seq := r.seq
			r.mu.Unlock()
			return msgs, seq
		}
		ch, seq := r.updated, r.seq
		r.mu.Unlock()

		select {
		case <-ch:
			// loop and collect
		case <-deadline.C:
			return nil, seq
		case <-ctx.Done():
			return nil, seq
		}
	}
}

// Members lists everyone currently in the room, alphabetically.
func (r *Room) Members() []proto.Member {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.membersLocked()
}

func (r *Room) membersLocked() []proto.Member {
	out := make([]proto.Member, 0, len(r.byName))
	for name, s := range r.byName {
		out = append(out, proto.Member{Name: name, Joined: s.joined})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// reapIdle drops sessions that have not been seen for the given duration.
func (r *Room) reapIdle(idle time.Duration) {
	cutoff := time.Now().UTC().Add(-idle)

	r.mu.Lock()
	var stale []*session
	for _, s := range r.byToken {
		if s.lastSeen.Before(cutoff) {
			stale = append(stale, s)
		}
	}
	for _, s := range stale {
		delete(r.byToken, s.token)
		delete(r.byName, s.name)
	}
	r.mu.Unlock()

	for _, s := range stale {
		r.post(proto.KindLeave, s.name, fmt.Sprintf("%s timed out", s.name))
	}
}

// seed replays persisted history into a fresh room without re-writing it.
func (r *Room) seed(msgs []proto.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(msgs) > r.historyN {
		msgs = msgs[len(msgs)-r.historyN:]
	}
	r.history = append(r.history, msgs...)
	if n := len(r.history); n > 0 {
		r.seq = r.history[n-1].Seq
	}
}

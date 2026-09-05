package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/justin06lee/grokbox/internal/client"
	"github.com/justin06lee/grokbox/internal/server"
)

// roomList collects repeated --room flags, each "name" or "name=key".
type roomList []server.RoomSpec

func (r *roomList) String() string {
	names := make([]string, 0, len(*r))
	for _, s := range *r {
		names = append(names, s.Name)
	}
	return strings.Join(names, ",")
}

func (r *roomList) Set(v string) error {
	name, key, _ := strings.Cut(v, "=")
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New(`room must look like "lounge" or "lounge=some-key"`)
	}
	*r = append(*r, server.RoomSpec{Name: name, Key: strings.TrimSpace(key)})
	return nil
}

func cmdServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	var rooms roomList
	addr := fs.String("addr", envOr("GROKBOX_ADDR", ":7777"), "address to listen on")
	fs.Var(&rooms, "room", `room to host, "name" or "name=key" (repeatable)`)
	key := fs.String("key", os.Getenv("GROKBOX_KEY"), "key for the room (default: generated once and remembered)")
	open := fs.Bool("open", false, "let joiners create unknown rooms, the first one in setting the key")
	store := fs.String("store", filepath.Join(client.UserDir(), "server"), `where to keep keys and transcripts ("" for memory only)`)
	history := fs.Int("history", 200, "messages kept and replayed per room")
	idle := fs.Duration("idle", 90*time.Second, "drop a member unheard from for this long")
	advertise := fs.String("advertise", os.Getenv("GROKBOX_ADVERTISE"), "public base URL to put in invites, e.g. https://chat.example.com")
	useTLS := fs.Bool("tls", true, "serve HTTPS; pass --tls=false only when something in front of this already terminates it")
	cert := fs.String("tls-cert", "", "TLS certificate file (default: sign one and pin it in the invite)")
	tlsKey := fs.String("tls-key", "", "TLS key file")
	quiet := fs.Bool("quiet", false, "only log errors")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "usage: grokbox serve [flags]\n\nhost one or more chat rooms and print an invite for each.\n\nflags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	if len(rooms) == 0 {
		rooms = roomList{{Name: envOr("GROKBOX_ROOM", "lounge")}}
	}
	if *key != "" {
		for i := range rooms {
			if rooms[i].Key == "" {
				rooms[i].Key = *key
				break
			}
		}
	}

	logf := func(format string, v ...any) {
		fmt.Fprintf(os.Stderr, time.Now().Format("15:04:05 ")+format+"\n", v...)
	}
	if *quiet {
		logf = func(string, ...any) {}
	}

	srv, err := server.New(server.Config{
		Addr:      *addr,
		Rooms:     rooms,
		Open:      *open,
		StoreDir:  *store,
		History:   *history,
		Idle:      *idle,
		Advertise: *advertise,
		TLS:       *useTLS,
		TLSCert:   *cert,
		TLSKey:    *tlsKey,
		Logf:      logf,
	})
	if err != nil {
		return err
	}

	if !*quiet {
		printServeBanner(srv, *addr, *store, *open)
	}
	return srv.Run(ctx)
}

func printServeBanner(srv *server.Server, addr, store string, open bool) {
	out := os.Stderr
	fmt.Fprintf(out, "\ngrokbox %s — listening on %s\n", version, addr)
	fmt.Fprintf(out, "reachable at %s   %s\n", srv.BaseURL(), reachNote(srv.Reach()))

	for _, r := range srv.Rooms() {
		fmt.Fprintf(out, "\n  room    %s\n  key     %s\n  invite  %s\n", r.Name(), r.Key(), srv.Invite(r).Encode())
	}

	if first := srv.Rooms(); len(first) > 0 {
		fmt.Fprintf(out, "\n  hand out the invite, then everyone runs:\n\n      grokbox join %s --name <their-name>\n\n", srv.Invite(first[0]).Encode())
	}

	switch {
	case srv.Fingerprint() != "":
		fmt.Fprintf(out, "  tls     self-signed, pinned by the invite (%s)\n", srv.Fingerprint())
	case strings.HasPrefix(srv.BaseURL(), "https://"):
		fmt.Fprintf(out, "  tls     on\n")
	default:
		fmt.Fprintf(out, "  tls     off — the room key crosses the network in the clear\n")
	}
	if store != "" {
		fmt.Fprintf(out, "  store   %s\n", store)
	} else {
		fmt.Fprintf(out, "  store   (memory only — keys and the certificate change on restart)\n")
	}
	if open {
		fmt.Fprintf(out, "  open    joiners may create new rooms\n")
	}

	if srv.Reach() == server.ReachLocal {
		fmt.Fprintf(out, "\n  that address only works on this network. To let people outside it in,\n")
		fmt.Fprintf(out, "  run this where it has a public address, or put the port behind a tunnel\n")
		fmt.Fprintf(out, "  and pass --advertise <that URL>.\n")
	}
	fmt.Fprintf(out, "\n  ctrl-c to stop\n\n")
}

// reachNote says how far the advertised address actually goes, because an
// invite that only works on one wifi network and an invite that works from
// anywhere look exactly alike.
func reachNote(r server.Reach) string {
	switch r {
	case server.ReachPublic:
		return "(public — anyone with the invite can reach it)"
	case server.ReachLocal:
		return "(this network only)"
	default:
		return "(as advertised)"
	}
}

// cmdRooms reprints the invites a server store holds.
func cmdRooms(args []string) error {
	fs := flag.NewFlagSet("rooms", flag.ContinueOnError)
	store := fs.String("store", filepath.Join(client.UserDir(), "server"), "the server's store directory")
	codes := fs.Bool("codes", false, "print only the invite codes, one per line")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "usage: grokbox rooms [flags]\n\nprint the invite for every room a server holds, without starting it.\nUse this to get a code again on a machine where grokbox runs as a service.\n\nflags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	invites, err := server.StoredInvites(*store)
	if err != nil {
		return err
	}
	if len(invites) == 0 {
		fmt.Fprintln(os.Stderr, "no rooms in "+*store)
		return nil
	}
	for _, in := range invites {
		if *codes {
			fmt.Println(in.Encode())
			continue
		}
		fmt.Printf("room    %s\nkey     %s\ninvite  %s\n\n", in.Room, in.Key, in.Encode())
	}
	return nil
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

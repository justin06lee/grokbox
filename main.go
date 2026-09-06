// Command grokbox is a chat room you can hand out with one code.
//
// One person runs the server, shares the invite it prints, and everyone else
// joins from their own machine with a name and that code.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/justin06lee/grokbox/internal/client"
	"github.com/justin06lee/grokbox/internal/proto"
	"github.com/justin06lee/grokbox/internal/server"
)

// version is stamped at build time by the Makefile. A `go install` build has
// no ldflags, so it reads its version from the module's build info instead.
var version = ""

func resolveVersion() string {
	if version != "" {
		return version
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			return s.Value[:7]
		}
	}
	return "dev"
}

const usage = `grokbox — a chat room behind a key.

usage:
  grokbox join    [invite] --name NAME        open the interactive chat
  grokbox send    [invite] --name NAME TEXT   say one thing and exit
  grokbox read    [invite] --name NAME        print what has been said since last time
  grokbox tail    [invite] --name NAME        stream messages as they arrive
  grokbox members [invite] --name NAME        list who is in the room
  grokbox hook    add|ls|rm|test              be woken when your name is said
  grokbox invite  [invite]                    show or decode an invite code
  grokbox health  [invite]                    check that a server is up
  grokbox leave   [invite]                    end this machine's session

  grokbox serve   [flags]                     host rooms and print their invites
  grokbox rooms   [flags]                     reprint a server's invites, without starting it
  grokbox version

the invite code carries the server address, the room, the key, and the hash of
the server's certificate. It is one string because it is one thing to share —
never take it apart and pass the pieces separately, or the certificate goes
unchecked. Leave it out entirely and grokbox reuses the last room this machine
joined.

flags common to the client commands:
  --name NAME     how you appear in the room              (env GROKBOX_NAME)
  --invite CODE   invite code                             (env GROKBOX_INVITE)
  --server URL    server base URL, instead of an invite   (env GROKBOX_SERVER)
  --room NAME     room name, instead of an invite         (env GROKBOX_ROOM)
  --key KEY       room key, instead of an invite          (env GROKBOX_KEY)
  --no-save       do not remember this room on disk

GROKBOX_HOME moves everything grokbox keeps on disk, which is also how you run
several independent members on one machine.

run "grokbox <command> -h" for the rest.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	version = resolveVersion()
	client.UserAgent = "grokbox/" + version
	server.Build = version

	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	args := os.Args[2:]
	var err error
	switch os.Args[1] {
	case "serve", "server", "host":
		err = cmdServe(ctx, args)
	case "rooms":
		err = cmdRooms(args)
	case "join", "chat":
		err = cmdJoin(ctx, args)
	case "send", "say":
		err = cmdSend(ctx, args)
	case "read":
		err = cmdRead(ctx, args)
	case "tail", "follow", "watch":
		err = cmdTail(ctx, args)
	case "members", "who":
		err = cmdMembers(ctx, args)
	case "hook", "hooks":
		err = cmdHook(ctx, args)
	case "leave", "part":
		err = cmdLeave(ctx, args)
	case "invite":
		err = cmdInvite(args)
	case "health", "ping":
		err = cmdHealth(ctx, args)
	case "version", "--version", "-version":
		fmt.Println("grokbox " + version)
	case "help", "--help", "-h", "-help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "grokbox: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}

	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		fmt.Fprintln(os.Stderr, "grokbox: "+err.Error())
		os.Exit(1)
	}
}

// ------------------------------------------------------------- target flags

// targetFlags are the flags every client command shares: which room, and who
// you are in it.
type targetFlags struct {
	invite string
	server string
	room   string
	key    string
	name   string
	noSave bool
}

func addTargetFlags(fs *flag.FlagSet) *targetFlags {
	t := &targetFlags{}
	fs.StringVar(&t.invite, "invite", os.Getenv("GROKBOX_INVITE"), "invite code")
	fs.StringVar(&t.server, "server", os.Getenv("GROKBOX_SERVER"), "server base URL")
	fs.StringVar(&t.room, "room", os.Getenv("GROKBOX_ROOM"), "room name")
	fs.StringVar(&t.key, "key", os.Getenv("GROKBOX_KEY"), "room key")
	fs.StringVar(&t.name, "name", os.Getenv("GROKBOX_NAME"), "your display name")
	fs.BoolVar(&t.noSave, "no-save", false, "do not write this room to the config file")
	return t
}

// parseTarget parses the flags, taking a leading invite code if there is one.
func parseTarget(fs *flag.FlagSet, args []string) (*targetFlags, error) {
	t := addTargetFlags(fs)
	if len(args) > 0 && strings.HasPrefix(args[0], "grokbox1-") {
		t.invite = args[0]
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if t.invite == "" && fs.NArg() > 0 && strings.HasPrefix(fs.Arg(0), "grokbox1-") {
		t.invite = fs.Arg(0)
	}
	return t, nil
}

// session is a resolved room membership: where to connect, as whom, and the
// saved state that goes with it.
type session struct {
	cl      *client.Client
	cfg     *client.Config
	prof    *client.Profile
	noSave  bool
	resumed bool
}

// resolve turns flags, environment and saved profiles into a client.
func (t *targetFlags) resolve(needName bool) (*session, error) {
	cfg, err := client.LoadConfig()
	if err != nil {
		return nil, err
	}

	var inv proto.Invite
	var prof *client.Profile

	switch {
	case t.invite != "":
		inv, err = proto.ParseInvite(t.invite)
		if err != nil {
			return nil, err
		}
	case t.server != "" && t.room != "" && t.key != "":
		inv = proto.Invite{Server: t.server, Room: t.room, Key: t.key}
	default:
		prof = cfg.Latest(t.room)
		if prof == nil {
			return nil, errors.New("no invite code given and no saved room — run: grokbox join <invite-code> --name <your-name>")
		}
		inv = prof.Invite()
	}

	// Explicit flags always win over whatever the invite or profile said.
	if t.server != "" {
		inv.Server = t.server
	}
	if t.room != "" {
		inv.Room = t.room
	}
	if t.key != "" {
		inv.Key = t.key
	}
	inv.Server = withScheme(inv.Server)

	if inv.Room, err = proto.CleanRoom(inv.Room); err != nil {
		return nil, err
	}

	// Reuse the saved session for this exact room when there is one.
	if prof == nil {
		for i := range cfg.Profiles {
			p := &cfg.Profiles[i]
			if p.Server == strings.TrimRight(inv.Server, "/") && p.Room == inv.Room {
				prof = p
				break
			}
		}
	}

	name := t.name
	if name == "" && prof != nil {
		name = prof.Name
	}
	if name == "" && needName {
		return nil, errors.New("who are you? pass --name <your-name> (or set GROKBOX_NAME)")
	}
	if name != "" {
		if name, err = proto.CleanName(name); err != nil {
			return nil, err
		}
	}

	s := &session{cl: client.New(inv, name), cfg: cfg, prof: prof, noSave: t.noSave}
	// A saved token means we may already be in the room; keys change, so only
	// reuse the token when the key we are about to present is the same one.
	if prof != nil && prof.Name == name && prof.Key == inv.Key &&
		prof.Fingerprint == inv.Fingerprint && prof.Token != "" {
		s.cl.SetToken(prof.Token)
		s.cl.SetSeq(prof.Seq)
		s.resumed = true
	}
	return s, nil
}

// save records the session so the next command can skip the invite code.
func (s *session) save() {
	if s.noSave {
		return
	}
	inv := s.cl.Invite()
	s.cfg.Remember(client.Profile{
		Server:      inv.Server,
		Room:        inv.Room,
		Key:         inv.Key,
		Fingerprint: inv.Fingerprint,
		Name:        s.cl.Name,
		Token:       s.cl.Token(),
		Seq:         s.cl.Seq(),
	})
	if err := s.cfg.Save(); err != nil {
		fmt.Fprintln(os.Stderr, "grokbox: cannot save config: "+err.Error())
	}
}

func withScheme(server string) string {
	server = strings.TrimRight(strings.TrimSpace(server), "/")
	if server == "" || strings.Contains(server, "://") {
		return server
	}
	return "http://" + server
}

// ----------------------------------------------------------- small commands

func cmdInvite(args []string) error {
	fs := flag.NewFlagSet("invite", flag.ContinueOnError)
	decode := fs.Bool("decode", false, "print the parts of the code instead of the code")
	fs.Usage = usageFor(fs, "invite", "print the invite code for a room, to pass on to somebody else,\nor take one apart to see where it points.")
	t, err := parseTarget(fs, args)
	if err != nil {
		return err
	}
	s, err := t.resolve(false)
	if err != nil {
		return err
	}
	inv := s.cl.Invite()
	if *decode {
		fmt.Printf("server  %s\nroom    %s\nkey     %s\n", inv.Server, inv.Room, inv.Key)
		if inv.Fingerprint != "" {
			fmt.Printf("cert    %s\n", inv.Fingerprint)
		}
		return nil
	}
	fmt.Println(inv.Encode())
	return nil
}

func cmdHealth(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("health", flag.ContinueOnError)
	fs.Usage = usageFor(fs, "health", "check that a server is up, without joining a room.")
	t, err := parseTarget(fs, args)
	if err != nil {
		return err
	}
	s, err := t.resolve(false)
	if err != nil {
		return err
	}
	info, err := s.cl.Health(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("%s is up — grokbox %v, protocol v%v, %v room(s), up %v\n",
		s.cl.Server, info["server"], info["version"], info["rooms"], info["uptime"])
	return nil
}

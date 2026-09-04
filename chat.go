package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/justin06lee/grokbox/internal/client"
	"github.com/justin06lee/grokbox/internal/proto"
	"github.com/justin06lee/grokbox/internal/ui"
)

func cmdJoin(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("join", flag.ContinueOnError)
	noColor := fs.Bool("no-color", os.Getenv("NO_COLOR") != "", "disable colour")
	noTime := fs.Bool("no-time", false, "hide timestamps")
	quiet := fs.Bool("quiet", false, "skip the header")
	fs.Usage = usageFor(fs, "join", "open the interactive chat window.")
	t, err := parseTarget(fs, args)
	if err != nil {
		return err
	}
	s, err := t.resolve(false)
	if err != nil {
		return err
	}
	if s.cl.Name == "" {
		name, err := askName()
		if err != nil {
			return err
		}
		s.cl.Name = name
	}

	jr, err := s.cl.Join(ctx)
	if err != nil {
		return joinHint(err)
	}
	s.save()
	defer s.save()

	return ui.Run(ctx, s.cl, jr.History, ui.Options{
		Color: !*noColor && term.IsTerminal(int(os.Stdout.Fd())),
		Clock: !*noTime,
		Quiet: *quiet,
	})
}

func cmdSend(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	text := fs.String("text", "", "what to say (default: the remaining arguments, or stdin)")
	action := fs.Bool("action", false, "send in the third person, like /me")
	asJSON := fs.Bool("json", false, "print the acknowledgement as JSON")
	fs.Usage = usageFor(fs, "send", "say one thing and exit. The session is kept, so\nrepeated sends do not spam the room with join notices.")
	t, err := parseTarget(fs, args)
	if err != nil {
		return err
	}

	body := *text
	if body == "" {
		rest := fs.Args()
		if len(rest) > 0 && rest[0] == t.invite {
			rest = rest[1:]
		}
		body = strings.Join(rest, " ")
	}
	if body == "" {
		if term.IsTerminal(int(os.Stdin.Fd())) {
			return errors.New("nothing to send: pass the message as arguments, --text, or on stdin")
		}
		b, err := io.ReadAll(io.LimitReader(os.Stdin, proto.MaxTextLen*4))
		if err != nil {
			return err
		}
		body = strings.TrimRight(string(b), "\r\n")
	}

	s, err := t.resolve(true)
	if err != nil {
		return err
	}
	if _, err := s.cl.Resume(ctx); err != nil {
		return joinHint(err)
	}
	defer s.save()

	var seq int64
	if *action {
		seq, err = s.cl.Act(ctx, body)
	} else {
		seq, err = s.cl.Say(ctx, body)
	}
	if err != nil {
		return err
	}
	// Our own line is now the newest thing we have seen; not advancing the
	// cursor would make the next read echo it back at us.
	if seq > s.cl.Seq() {
		s.cl.SetSeq(seq)
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(proto.SendResponse{Seq: seq})
	}
	return nil
}

func cmdRead(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("read", flag.ContinueOnError)
	since := fs.Int64("since", -1, "start after this sequence number (default: where this machine left off)")
	var wait patience
	fs.Var(&wait, "wait", "if there is nothing new, wait up to this long for something (max 60s)")
	limit := fs.Int("limit", 200, "print at most this many messages")
	asJSON := fs.Bool("json", false, "one JSON object per line")
	noTime := fs.Bool("no-time", false, "hide timestamps")
	fs.Usage = usageFor(fs, "read", "print what has been said since the last read, then exit.\nThe cursor is remembered, so a loop of reads never repeats itself.")
	t, err := parseTarget(fs, args)
	if err != nil {
		return err
	}
	s, err := t.resolve(true)
	if err != nil {
		return err
	}

	msgs, err := s.connect(ctx)
	if err != nil {
		return err
	}
	if *since >= 0 {
		s.cl.SetSeq(*since)
		msgs = nil
	}
	defer s.save()

	fresh, err := s.cl.Poll(ctx, wait.clamped())
	if err != nil {
		return err
	}
	msgs = append(msgs, fresh...)
	if len(msgs) > *limit {
		msgs = msgs[len(msgs)-*limit:]
	}

	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	for _, m := range msgs {
		if err := writeMessage(w, m, *asJSON, !*noTime); err != nil {
			return err
		}
	}
	return nil
}

func cmdTail(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("tail", flag.ContinueOnError)
	since := fs.Int64("since", -1, "start after this sequence number (default: where this machine left off)")
	asJSON := fs.Bool("json", false, "one JSON object per line")
	noTime := fs.Bool("no-time", false, "hide timestamps")
	noHistory := fs.Bool("no-history", false, "skip the backlog and only print new messages")
	fs.Usage = usageFor(fs, "tail", "stream messages as they arrive, until interrupted.")
	t, err := parseTarget(fs, args)
	if err != nil {
		return err
	}
	s, err := t.resolve(true)
	if err != nil {
		return err
	}

	backlog, err := s.connect(ctx)
	if err != nil {
		return err
	}
	if *since >= 0 {
		s.cl.SetSeq(*since)
		backlog = nil
	}
	if *noHistory {
		backlog = nil
	}
	defer s.save()
	defer func() {
		lctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.cl.Leave(lctx)
	}()

	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	for _, m := range backlog {
		if err := writeMessage(w, m, *asJSON, !*noTime); err != nil {
			return err
		}
	}
	w.Flush()

	err = s.cl.Follow(ctx, func(m proto.Message) error {
		if err := writeMessage(w, m, *asJSON, !*noTime); err != nil {
			return err
		}
		return w.Flush()
	}, func(note string) {
		fmt.Fprintln(os.Stderr, "grokbox: "+note)
	})
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func cmdMembers(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("members", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Usage = usageFor(fs, "members", "list who is in the room.")
	t, err := parseTarget(fs, args)
	if err != nil {
		return err
	}
	s, err := t.resolve(true)
	if err != nil {
		return err
	}
	if _, err := s.cl.Resume(ctx); err != nil {
		return joinHint(err)
	}
	defer s.save()

	members, err := s.cl.Members(ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(members)
	}
	for _, m := range members {
		you := ""
		if m.Name == s.cl.Name {
			you = "  (you)"
		}
		fmt.Printf("%s%s\n", m.Name, you)
	}
	return nil
}

func cmdLeave(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("leave", flag.ContinueOnError)
	fs.Usage = usageFor(fs, "leave", "end this machine's session so the room sees you go.")
	t, err := parseTarget(fs, args)
	if err != nil {
		return err
	}
	s, err := t.resolve(false)
	if err != nil {
		return err
	}
	if !s.resumed {
		fmt.Fprintln(os.Stderr, "grokbox: no active session to leave")
		return nil
	}
	if err := s.cl.Leave(ctx); err != nil {
		var ae *client.APIError
		if !errors.As(err, &ae) || !ae.Expired() {
			return err
		}
	}
	s.save()
	fmt.Fprintf(os.Stderr, "left %s\n", s.cl.Room)
	return nil
}

// ------------------------------------------------------------------ helpers

// connect makes sure the session is live and returns the backlog to print
// before anything new. A session that was still valid has no backlog: we have
// already seen everything up to our cursor.
func (s *session) connect(ctx context.Context) ([]proto.Message, error) {
	saved := s.cl.Seq()
	jr, err := s.cl.Resume(ctx)
	if err != nil {
		return nil, joinHint(err)
	}
	if jr == nil {
		return nil, nil
	}
	if !s.resumed || jr.Seq < saved {
		// First visit here, or the room's counter went backwards because the
		// server was restarted without its transcript. Take the lot.
		return jr.History, nil
	}
	// The session expired and we re-joined: print only what we missed.
	missed := make([]proto.Message, 0, len(jr.History))
	for _, m := range jr.History {
		if m.Seq > saved {
			missed = append(missed, m)
		}
	}
	return missed, nil
}

func writeMessage(w io.Writer, m proto.Message, asJSON, clock bool) error {
	if asJSON {
		b, err := json.Marshal(m)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%s\n", b)
		return err
	}
	_, err := fmt.Fprintln(w, plainLine(m, clock))
	return err
}

// plainLine renders a message for a pipe: no colour, no cursor tricks.
func plainLine(m proto.Message, clock bool) string {
	stamp := ""
	if clock {
		stamp = m.Time.Local().Format("15:04 ")
	}
	switch m.Kind {
	case proto.KindJoin:
		return stamp + "→ " + m.Text
	case proto.KindLeave:
		return stamp + "← " + m.Text
	case proto.KindSystem:
		return stamp + "· " + m.Text
	case proto.KindAction:
		return stamp + "* " + m.From + " " + m.Text
	default:
		return stamp + m.From + " › " + m.Text
	}
}

func askName() (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("who are you? pass --name <your-name> (or set GROKBOX_NAME)")
	}
	fmt.Fprint(os.Stderr, "your name: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no name given")
	}
	return proto.CleanName(line)
}

// joinHint turns the server's refusals into advice.
func joinHint(err error) error {
	var ae *client.APIError
	if !errors.As(err, &ae) {
		return err
	}
	switch ae.Code {
	case 403:
		return fmt.Errorf("%s — check the invite code or the key with whoever runs the room", ae.Msg)
	case 409:
		return fmt.Errorf("%s — pick another name with --name", ae.Msg)
	case 503:
		return fmt.Errorf("%s — try again once someone leaves", ae.Msg)
	}
	return err
}

// patience is a --wait value. It takes a duration, and also a bare number,
// because "--wait 25" is what everyone types.
type patience time.Duration

func (p *patience) String() string {
	if *p == 0 {
		return "0s"
	}
	return time.Duration(*p).String()
}

func (p *patience) Set(v string) error {
	if d, err := time.ParseDuration(v); err == nil {
		*p = patience(d)
		return nil
	}
	secs, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fmt.Errorf("%q is not a duration: try 25s", v)
	}
	*p = patience(time.Duration(secs * float64(time.Second)))
	return nil
}

// clamped keeps the wait inside what the server will honour.
func (p *patience) clamped() time.Duration {
	d := time.Duration(*p)
	if d < 0 {
		return 0
	}
	if d > 60*time.Second {
		return 60 * time.Second
	}
	return d
}

func usageFor(fs *flag.FlagSet, name, blurb string) func() {
	return func() {
		fmt.Fprintf(os.Stderr, "usage: grokbox %s [invite] [flags]\n\n%s\n\nflags:\n", name, blurb)
		fs.PrintDefaults()
	}
}

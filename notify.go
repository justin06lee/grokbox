package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/justin06lee/grokbox/internal/desktop"
	"github.com/justin06lee/grokbox/internal/proto"
)

// Hooks let an agent hear its name. This is the same thing for the person:
// a process that sits in the room and puts a notification on screen when
// somebody says yours, so a room full of agents is usable by a human who is
// not watching it.
//
// It deliberately shares the session every other command on this machine
// uses, rather than joining twice under one name — two members cannot hold
// the same name, and a notifier that fought the chat window for it would be
// worse than no notifier.

const notifyUsage = `usage: grokbox notify [invite] --name NAME [flags]

sit in the room and raise a notification when somebody says your name.

  grokbox notify --name justin

Runs until stopped. It shares this machine's session, so a chat window open
at the same time is the same member, not a second one — and stopping the
notifier does not take you out of the room.
`

func cmdNotify(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("notify", flag.ContinueOnError)
	var aliases aliasList
	all := fs.Bool("all", false, "notify on every message, not only the ones naming you")
	silent := fs.Bool("silent", false, "no sound")
	printOnly := fs.Bool("print", false, "print instead of notifying, to see what would be raised")
	fs.Var(&aliases, "alias", "another name that counts as you (repeatable)")
	fs.Usage = func() { fmt.Fprint(os.Stderr, notifyUsage, "\nflags:\n"); fs.PrintDefaults() }

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
	s.save()

	names := append([]string{s.cl.Name}, aliases...)
	fmt.Fprintf(os.Stderr, "watching %s as %s — you will be told when somebody says %s\n",
		s.cl.Room, s.cl.Name, "@"+strings.Join(names, " or @"))
	if !*all {
		fmt.Fprintln(os.Stderr, "(only mentions; --all for every message)")
	}

	// Follow re-joins when a session expires, which mints a new token. Write
	// it back so the next `grokbox read` or chat window resumes the same
	// membership instead of being refused the name this process is holding.
	go func() {
		last := s.cl.Token()
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if now := s.cl.Token(); now != "" && now != last {
					last = now
					s.save()
				}
			}
		}
	}()

	n := &notifier{room: s.cl.Room, name: s.cl.Name, silent: *silent, printOnly: *printOnly}
	err = s.cl.Follow(ctx, func(m proto.Message) error {
		if m.From == s.cl.Name {
			return nil // your own words
		}
		if m.Kind != proto.KindChat && m.Kind != proto.KindAction {
			return nil // joins, leaves and server notices are not news
		}
		if !*all && !proto.Mentions(m.Text, s.cl.Name, aliases) {
			return nil
		}
		n.raise(m)
		return nil
	}, func(note string) {
		fmt.Fprintln(os.Stderr, "· "+note)
	})

	// Not leaving on the way out is deliberate: a chat window may be sharing
	// this session, and stopping the notifier must not close it.
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// ------------------------------------------------------------- the notifier

type notifier struct {
	room      string
	name      string // for the "open the room" action, which joins as you
	silent    bool
	printOnly bool
}

func (n *notifier) raise(m proto.Message) {
	body := m.Text
	if m.Kind == proto.KindAction {
		body = m.From + " " + m.Text
	}
	if len([]rune(body)) > 200 {
		body = string([]rune(body)[:197]) + "…"
	}
	line := fmt.Sprintf("%s in %s: %s", m.From, n.room, body)

	if n.printOnly {
		fmt.Println(line)
		return
	}
	note := desktop.Notification{
		Title:    "grokbox",
		Subtitle: m.From + " in " + n.room,
		Body:     body,
		Group:    "grokbox-" + n.room,
		Silent:   n.silent,
	}
	// A notification you can click to open the room is worth more than one
	// you cannot; terminal-notifier is the only thing here that can do it.
	if self, err := os.Executable(); err == nil && n.name != "" {
		note.Exec = []string{self, "window", "--name", n.name}
	}
	if err := desktop.Show(note); err != nil {
		// A desktop that will not show a notification is not a reason to stop
		// watching the room; say it once on the terminal instead.
		fmt.Fprintln(os.Stderr, line)
	}
}

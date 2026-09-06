package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

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
	if err := n.show(m.From+" in "+n.room, body); err != nil {
		// A desktop that will not show a notification is not a reason to stop
		// watching the room; say it once on the terminal instead.
		fmt.Fprintln(os.Stderr, line)
	}
}

func (n *notifier) show(subtitle, body string) error {
	switch runtime.GOOS {
	case "darwin":
		return n.showMac(subtitle, body)
	case "windows":
		return n.showWindows(subtitle, body)
	default:
		return n.showUnix(subtitle, body)
	}
}

// showMac prefers terminal-notifier when it is installed, because a
// notification you can click to open the room is worth more than one you
// cannot. Otherwise osascript, which every Mac has.
func (n *notifier) showMac(subtitle, body string) error {
	if tn, err := exec.LookPath("terminal-notifier"); err == nil {
		args := []string{"-title", "grokbox", "-subtitle", subtitle, "-message", body, "-group", "grokbox-" + n.room}
		if self, err := os.Executable(); err == nil && n.name != "" {
			args = append(args, "-execute", shellJoin([]string{self, "window", "--name", n.name}))
		}
		if !n.silent {
			args = append(args, "-sound", "Ping")
		}
		return exec.Command(tn, args...).Run()
	}

	sound := ""
	if !n.silent {
		sound = " sound name \"Ping\""
	}
	osa := fmt.Sprintf("display notification %s with title \"grokbox\" subtitle %s%s",
		appleString(body), appleString(subtitle), sound)
	return exec.Command("osascript", "-e", osa).Run()
}

func (n *notifier) showUnix(subtitle, body string) error {
	send, err := exec.LookPath("notify-send")
	if err != nil {
		return err
	}
	return exec.Command(send, "-a", "grokbox", subtitle, body).Run()
}

func (n *notifier) showWindows(subtitle, body string) error {
	ps, err := exec.LookPath("powershell")
	if err != nil {
		return err
	}
	// The toast APIs need a module nobody has by default; a balloon from the
	// notification area needs nothing and shows up in the same place.
	script := `[reflection.assembly]::LoadWithPartialName("System.Windows.Forms") > $null
$n = New-Object System.Windows.Forms.NotifyIcon
$n.Icon = [System.Drawing.SystemIcons]::Information
$n.BalloonTipTitle = ` + psQuote(subtitle) + `
$n.BalloonTipText = ` + psQuote(body) + `
$n.Visible = $true
$n.ShowBalloonTip(6000)
Start-Sleep -Seconds 7`
	return exec.Command(ps, "-NoProfile", "-Command", script).Run()
}

func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

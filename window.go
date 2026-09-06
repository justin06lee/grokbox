package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/justin06lee/grokbox/internal/desktop"
)

// A room an agent is in is invisible to the person it works for. `window`
// opens the ordinary chat window on their desktop — a real terminal, filling
// the screen — and returns straight away, so an agent can offer somebody a
// view of the room without `join` swallowing its turn.
//
// It is the one command here that is for an agent to run *on the user's
// machine* and nowhere else. A bot's own cloud box has no desktop to put a
// window on.

const windowUsage = `usage: grokbox window --name THEIR-NAME [invite] [flags]

open the chat window on this machine's desktop, full screen, and return.

The window is for a person, so it needs their name and not yours — it joins
as a separate member. Nothing about your own session is touched.

  grokbox window --name maya

An agent should run this on the user's machine. It needs a desktop; a cloud
box does not have one, and there it will tell you so rather than hang.
`

func cmdWindow(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("window", flag.ContinueOnError)
	home := fs.String("home", "", "GROKBOX_HOME for the window (default: the desktop user's own)")
	termProg := fs.String("term", "", "terminal program to open, instead of guessing")
	dryRun := fs.Bool("print", false, "print the command that would be run, and open nothing")
	fs.Usage = func() { fmt.Fprint(os.Stderr, windowUsage, "\nflags:\n"); fs.PrintDefaults() }

	t, err := parseTarget(fs, args)
	if err != nil {
		return err
	}
	// Deliberately not falling back to the saved name. A bot running this
	// without thinking would otherwise open a window as itself, and the two
	// would fight over one seat in the room.
	if t.name == "" {
		return errors.New("who is the window for? pass --name <their-name>.\nIt joins as its own member, so give the person's name, not the one you use")
	}

	s, err := t.resolve(true)
	if err != nil {
		return err
	}
	// No s.save(): opening somebody a window must not rewrite whose session
	// this machine remembers.
	if s.prof != nil && strings.EqualFold(s.prof.Name, s.cl.Name) {
		fmt.Fprintf(os.Stderr, "grokbox: warning — %q is the name already saved on this machine; the window and whatever saved it will collide in the room\n", s.cl.Name)
	}

	self, err := os.Executable()
	if err != nil || self == "" {
		self = "grokbox" // fall back to whatever the desktop's PATH finds
	}
	argv := []string{self, "join"}
	env := [][2]string{
		{"GROKBOX_INVITE", s.cl.Invite().Encode()},
		{"GROKBOX_NAME", s.cl.Name},
	}

	if *dryRun {
		for _, kv := range env {
			fmt.Printf("%s=%s\n", kv[0], desktop.ShellQuote(kv[1]))
		}
		fmt.Println(desktop.ShellJoin(argv))
		return nil
	}

	if err := openWindow(argv, env, *home, *termProg); err != nil {
		manual := desktop.ShellJoin([]string{self, "join", s.cl.Invite().Encode(), "--name", s.cl.Name})
		return fmt.Errorf("%w\n\nrun this yourself in a terminal instead:\n  %s", err, manual)
	}
	fmt.Printf("opened the room in a window as %s\n", s.cl.Name)
	return nil
}

// openWindow starts a terminal running argv and returns without waiting for
// it. Everything below is best-effort by nature: there is no portable way to
// ask for a terminal, only a list of the ones people have.
func openWindow(argv []string, env [][2]string, home, termProg string) error {
	script, err := launcher(argv, env, home)
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		return openMac(script, termProg)
	case "windows":
		return openWindows(script, argv, termProg)
	default:
		return openUnix(script, termProg)
	}
}

// launcher writes the command out as a shell script, so no invite code, name
// or path is ever pasted through AppleScript quoting or a terminal's own
// argument parsing. It removes itself before starting the chat.
func launcher(argv []string, env [][2]string, home string) (string, error) {
	f, err := os.CreateTemp("", "grokbox-window-*.sh")
	if err != nil {
		return "", err
	}
	defer f.Close()

	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("rm -f \"$0\"\n")
	// Out of the temp directory, so the window is not named after it.
	b.WriteString("cd \"$HOME\" 2>/dev/null || true\n")
	for _, kv := range env {
		fmt.Fprintf(&b, "%s=%s\nexport %s\n", kv[0], desktop.ShellQuote(kv[1]), kv[0])
	}
	if home != "" {
		fmt.Fprintf(&b, "GROKBOX_HOME=%s\nexport GROKBOX_HOME\n", desktop.ShellQuote(home))
	} else {
		// The window belongs to the person at the keyboard, not to whatever
		// agent opened it, so it must not inherit an agent's state directory.
		b.WriteString("unset GROKBOX_HOME\n")
	}
	fmt.Fprintf(&b, "exec %s\n", desktop.ShellJoin(argv))

	if _, err := f.WriteString(b.String()); err != nil {
		return "", err
	}
	if err := os.Chmod(f.Name(), 0o700); err != nil {
		return "", err
	}
	return f.Name(), nil
}

// openMac drives Terminal through AppleScript, then stretches the window it
// just made to the size of the desktop.
func openMac(script, termProg string) error {
	app := termProg
	if app == "" {
		app = "Terminal"
	}
	osa := fmt.Sprintf(`
set deskBounds to {0, 0, 1440, 900}
try
	tell application "Finder" to set deskBounds to bounds of window of desktop
end try
tell application %s
	activate
	do script %s
	delay 0.4
	try
		set bounds of front window to deskBounds
	end try
end tell`, desktop.AppleString(app), desktop.AppleString(script))

	cmd := exec.Command("osascript", "-e", osa)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("could not open %s: %s", app, msg)
	}
	return nil
}

// unixTerminals are tried in order. The flag that fills the screen differs on
// every one of them, which is why they are listed rather than detected.
var unixTerminals = []struct {
	bin  string
	args func(script string) []string
}{
	{"gnome-terminal", func(s string) []string { return []string{"--maximize", "--", s} }},
	{"konsole", func(s string) []string { return []string{"--fullscreen", "-e", s} }},
	{"xfce4-terminal", func(s string) []string { return []string{"--maximize", "-x", s} }},
	{"mate-terminal", func(s string) []string { return []string{"--maximize", "--", s} }},
	{"alacritty", func(s string) []string { return []string{"-o", "window.startup_mode=\"Maximized\"", "-e", s} }},
	{"kitty", func(s string) []string { return []string{"--start-as=maximized", s} }},
	{"x-terminal-emulator", func(s string) []string { return []string{"-e", s} }},
	{"xterm", func(s string) []string { return []string{"-maximized", "-e", s} }},
}

func openUnix(script, termProg string) error {
	try := unixTerminals
	if termProg != "" {
		try = []struct {
			bin  string
			args func(string) []string
		}{{termProg, func(s string) []string { return []string{"-e", s} }}}
	}
	for _, t := range try {
		path, err := exec.LookPath(t.bin)
		if err != nil {
			continue
		}
		return spawn(path, t.args(script))
	}
	return errors.New("no terminal program found — this machine has no desktop to open a window on")
}

func openWindows(script string, argv []string, termProg string) error {
	// No shell script: Windows terminals take the command directly.
	if termProg == "" {
		termProg = "wt"
	}
	if path, err := exec.LookPath(termProg); err == nil {
		return spawn(path, append([]string{"--maximized"}, argv...))
	}
	_ = script
	return spawn("cmd", append([]string{"/c", "start", "", "/max"}, argv...))
}

// spawn starts a process and lets go of it, so the window outlives the
// command that opened it — which is the entire point of this being separate
// from `join`.
func spawn(path string, args []string) error {
	if setsid, err := exec.LookPath("setsid"); err == nil && runtime.GOOS != "windows" {
		args = append([]string{path}, args...)
		path = setsid
	}
	cmd := exec.Command(path, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not open %s: %w", filepath.Base(path), err)
	}
	return cmd.Process.Release()
}

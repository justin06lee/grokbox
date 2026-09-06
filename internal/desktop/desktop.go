// Package desktop is the machine's own surface: raising a notification,
// and quoting a command line for the shell or for AppleScript.
//
// It exists because grokbox has two front ends — the CLI and the desktop
// app — and both need to put a notification on screen. The rules for doing
// that are per-platform and fiddly enough that a second copy would drift.
package desktop

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Notification is one thing to tell the person, in the terms every platform
// agrees on. Anything richer belongs to the app, which has a bundle and can
// use the real notification APIs.
type Notification struct {
	Title    string   // app name, shown in bold
	Subtitle string   // who and where, on macOS and Linux
	Body     string   // what was said
	Group    string   // replaces the previous notification with the same group
	Silent   bool     // no sound
	Exec     []string // run this when clicked, where the platform can
}

// Show puts the notification on screen. It reports an error when the desktop
// has no way to show one — the caller decides whether that is worth stopping
// for; in practice it means printing the line instead.
func Show(n Notification) error {
	if n.Title == "" {
		n.Title = "grokbox"
	}
	switch runtime.GOOS {
	case "darwin":
		return showMac(n)
	case "windows":
		return showWindows(n)
	default:
		return showUnix(n)
	}
}

// showMac prefers terminal-notifier when it is installed, because a
// notification you can click to open the room is worth more than one you
// cannot. Otherwise osascript, which every Mac has.
func showMac(n Notification) error {
	if tn, err := exec.LookPath("terminal-notifier"); err == nil {
		args := []string{"-title", n.Title, "-subtitle", n.Subtitle, "-message", n.Body}
		if n.Group != "" {
			args = append(args, "-group", n.Group)
		}
		if len(n.Exec) > 0 {
			args = append(args, "-execute", ShellJoin(n.Exec))
		}
		if !n.Silent {
			args = append(args, "-sound", "Ping")
		}
		return exec.Command(tn, args...).Run()
	}

	sound := ""
	if !n.Silent {
		sound = " sound name \"Ping\""
	}
	osa := fmt.Sprintf("display notification %s with title %s subtitle %s%s",
		AppleString(n.Body), AppleString(n.Title), AppleString(n.Subtitle), sound)
	return exec.Command("osascript", "-e", osa).Run()
}

func showUnix(n Notification) error {
	send, err := exec.LookPath("notify-send")
	if err != nil {
		return err
	}
	return exec.Command(send, "-a", n.Title, n.Subtitle, n.Body).Run()
}

func showWindows(n Notification) error {
	ps, err := exec.LookPath("powershell")
	if err != nil {
		return err
	}
	// The toast APIs need a module nobody has by default; a balloon from the
	// notification area needs nothing and shows up in the same place.
	script := `[reflection.assembly]::LoadWithPartialName("System.Windows.Forms") > $null
$n = New-Object System.Windows.Forms.NotifyIcon
$n.Icon = [System.Drawing.SystemIcons]::Information
$n.BalloonTipTitle = ` + psQuote(n.Subtitle) + `
$n.BalloonTipText = ` + psQuote(n.Body) + `
$n.Visible = $true
$n.ShowBalloonTip(6000)
Start-Sleep -Seconds 7`
	return exec.Command(ps, "-NoProfile", "-Command", script).Run()
}

func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// ------------------------------------------------------------------ quoting

// ShellQuote wraps a string so a POSIX shell reads it back unchanged.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ShellJoin renders an argv as one shell command line.
func ShellJoin(argv []string) string {
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = ShellQuote(a)
	}
	return strings.Join(out, " ")
}

// AppleString renders an AppleScript string literal.
func AppleString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

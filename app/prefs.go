package main

// The app's own state — the things the window remembers that the protocol has
// no opinion about: what a room's avatar looks like, what you have renamed it
// to on this machine, whether it may interrupt you, and your own picture.
//
// It is a separate file from client.json on purpose. client.json holds room
// keys and session tokens and is shared with the CLI; a colour picker has no
// business writing to it, and a corrupt app.json must not cost you a room.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/justin06lee/grokbox/internal/client"
)

// RoomPrefs is how one room looks and behaves in this window.
type RoomPrefs struct {
	Shape    string `json:"shape,omitempty"`    // one of avatarShapes; empty means derive it from the name
	Color    string `json:"color,omitempty"`    // one of avatarColors; empty means derive it
	Photo    string `json:"photo,omitempty"`    // a file in the config dir, used instead of the shape
	Nickname string `json:"nickname,omitempty"` // what to call it here, when two rooms share a name
	Hidden   bool   `json:"hidden,omitempty"`   // out of the sidebar, still connected
	// Quiet is the notifications toggle inverted, so that the zero value — a
	// room nobody has touched the settings of — is one that may interrupt you.
	Quiet bool `json:"quiet,omitempty"`
}

// ProfilePrefs is you.
type ProfilePrefs struct {
	// Login is the GitHub account whose picture this is. Grok Bot signs in
	// through GitHub and keeps the picture's URL inside an encrypted store, so
	// rather than prising that open we fetch the same public avatar the same
	// account has on GitHub.
	Login string `json:"login,omitempty"`
	Photo string `json:"photo,omitempty"` // a file in the config dir
	Shape string `json:"shape,omitempty"`
	Color string `json:"color,omitempty"`
}

// Prefs is the whole of app.json.
type Prefs struct {
	Profile ProfilePrefs         `json:"profile"`
	Rooms   map[string]RoomPrefs `json:"rooms,omitempty"`
}

func prefsPath() string { return filepath.Join(client.UserDir(), "app.json") }

// mediaDir is where pictures the window shows are kept: fetched avatars and
// anything you picked off disk. Copying the file in rather than pointing at
// where you found it means the picture survives you tidying up your Downloads.
func mediaDir() string { return filepath.Join(client.UserDir(), "media") }

func loadPrefs() Prefs {
	var p Prefs
	b, err := os.ReadFile(prefsPath())
	if err == nil {
		// A broken app.json is worth ignoring, not reporting: it costs you a
		// colour, and the alternative is an app that will not start.
		_ = json.Unmarshal(b, &p)
	}
	if p.Rooms == nil {
		p.Rooms = map[string]RoomPrefs{}
	}
	return p
}

func (p Prefs) save() error {
	if err := os.MkdirAll(client.UserDir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := prefsPath() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, prefsPath())
}

// ------------------------------------------------------------- the manager

// prefsFor reads one room's preferences.
func (m *Manager) prefsFor(id string) RoomPrefs {
	m.prefsMu.Lock()
	defer m.prefsMu.Unlock()
	return m.prefs.Rooms[id]
}

// editRoom applies a change to one room's preferences and writes the file.
func (m *Manager) editRoom(id string, edit func(*RoomPrefs)) error {
	m.prefsMu.Lock()
	if m.prefs.Rooms == nil {
		m.prefs.Rooms = map[string]RoomPrefs{}
	}
	rp := m.prefs.Rooms[id]
	edit(&rp)
	if rp == (RoomPrefs{}) {
		delete(m.prefs.Rooms, id)
	} else {
		m.prefs.Rooms[id] = rp
	}
	err := m.prefs.save()
	m.prefsMu.Unlock()
	if m.app != nil {
		m.app.Event.Emit("grokbox:rooms", m.List())
	}
	return err
}

// forgetPrefs drops a room's preferences when you leave it, so re-joining
// later does not silently inherit a nickname you no longer remember setting.
func (m *Manager) forgetPrefs(id string) {
	m.prefsMu.Lock()
	delete(m.prefs.Rooms, id)
	_ = m.prefs.save()
	m.prefsMu.Unlock()
}

// ------------------------------------------------------------------ you

// ProfileView is you, as the window draws you: a name, a picture if there is
// one, and the shape and colour to fall back on if there is not.
type ProfileView struct {
	Login string `json:"login"`
	Photo string `json:"photo"` // a data URL, or empty
	Shape string `json:"shape"`
	Color string `json:"color"`
}

func (m *Manager) profileView() ProfileView {
	m.prefsMu.Lock()
	p := m.prefs.Profile
	m.prefsMu.Unlock()
	return ProfileView{Login: p.Login, Photo: dataURL(p.Photo), Shape: p.Shape, Color: p.Color}
}

// dataURL reads a picture out of the media directory and inlines it. The
// window is served from an embedded file system with no route into the config
// directory, and an app that opened one would be an app that could be talked
// into serving any file on the disk.
func dataURL(name string) string {
	if name == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(mediaDir(), filepath.Base(name)))
	if err != nil || len(b) == 0 {
		return ""
	}
	mime := "image/png"
	switch {
	case len(b) > 3 && b[0] == 0xff && b[1] == 0xd8:
		mime = "image/jpeg"
	case len(b) > 12 && string(b[8:12]) == "WEBP":
		mime = "image/webp"
	case len(b) > 4 && string(b[:4]) == "GIF8":
		mime = "image/gif"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
}

// adoptPhoto copies a picture into the media directory under a stable name and
// returns that name.
func adoptPhoto(src, as string) (string, error) {
	b, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	return keepPhoto(b, as+filepath.Ext(src))
}

func keepPhoto(b []byte, name string) (string, error) {
	if len(b) == 0 {
		return "", fmt.Errorf("that file is empty")
	}
	if len(b) > 8<<20 {
		return "", fmt.Errorf("that picture is over 8MB — try a smaller one")
	}
	if err := os.MkdirAll(mediaDir(), 0o700); err != nil {
		return "", err
	}
	name = filepath.Base(name)
	if err := os.WriteFile(filepath.Join(mediaDir(), name), b, 0o600); err != nil {
		return "", err
	}
	return name, nil
}

// gitHubLogin is the account whose picture to use. You can say who you are,
// and if you have not, the machine usually already knows: `gh` is signed in,
// or git has a github.user set. Either beats making you type it.
func gitHubLogin() string {
	if out, err := run("gh", "api", "user", "--jq", ".login"); err == nil && out != "" {
		return out
	}
	if out, err := run("git", "config", "--get", "github.user"); err == nil && out != "" {
		return out
	}
	return ""
}

// run finds a command the way a shell would and reads one line out of it. An
// app launched from the Finder inherits almost no PATH, so the two places
// Homebrew puts things are searched by hand.
func run(name string, args ...string) (string, error) {
	bin, err := exec.LookPath(name)
	if err != nil {
		for _, dir := range []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin"} {
			candidate := filepath.Join(dir, name)
			if _, statErr := os.Stat(candidate); statErr == nil {
				bin, err = candidate, nil
				break
			}
		}
	}
	if err != nil {
		return "", err
	}
	out, err := exec.Command(bin, args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

var photoOnce sync.Once

// findPhoto gets your picture once at startup, without holding up the window:
// whoever this machine is signed in to GitHub as, that account's avatar. It is
// the same picture Grok Bot shows, because Grok Bot signs in through GitHub —
// its own copy of the URL is sealed in an encrypted store, and reaching into
// that would mean handling session tokens to fetch a public image.
func (m *Manager) findPhoto() {
	photoOnce.Do(func() { go m.fetchPhoto("") })
}

// fetchPhoto resolves a login and downloads its avatar. An empty login means
// "work out who I am"; it gives up quietly, since a missing picture falls back
// to a coloured disc and is not worth an error in the window.
func (m *Manager) fetchPhoto(login string) error {
	m.prefsMu.Lock()
	if login == "" {
		login = m.prefs.Profile.Login
	}
	had := m.prefs.Profile.Photo
	m.prefsMu.Unlock()

	if login == "" {
		if login = gitHubLogin(); login == "" {
			return fmt.Errorf("no GitHub account on this machine — say which one to use")
		}
	}

	cl := &http.Client{Timeout: 15 * time.Second}
	resp, err := cl.Get("https://github.com/" + url(login) + ".png?size=256")
	if err != nil {
		return fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub has no picture for %q", login)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	name, err := keepPhoto(b, "profile.png")
	if err != nil {
		return err
	}

	m.prefsMu.Lock()
	m.prefs.Profile.Login = login
	m.prefs.Profile.Photo = name
	err = m.prefs.save()
	m.prefsMu.Unlock()
	if err != nil {
		return err
	}
	if m.app != nil && (had != name || had == "") {
		m.app.Event.Emit("grokbox:profile", m.profileView())
	}
	return nil
}

// url keeps a login to the characters GitHub allows in one, so nothing that
// arrives from the window can steer the request somewhere else.
func url(login string) string {
	var b strings.Builder
	for _, r := range login {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// editProfile applies a change to your own appearance and tells the window.
func (m *Manager) editProfile(edit func(*ProfilePrefs)) error {
	m.prefsMu.Lock()
	edit(&m.prefs.Profile)
	err := m.prefs.save()
	m.prefsMu.Unlock()
	if m.app != nil {
		m.app.Event.Emit("grokbox:profile", m.profileView())
	}
	return err
}

// setProfilePhoto points you at a picture already in the media directory.
func (m *Manager) setProfilePhoto(name string) error {
	return m.editProfile(func(p *ProfilePrefs) {
		p.Photo, p.Shape, p.Color = name, "", ""
	})
}

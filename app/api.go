package main

import (
	"errors"
	"strings"

	"github.com/justin06lee/grokbox/internal/proto"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// API is everything the window is allowed to ask the room manager to do.
//
// Wails binds the exported methods of this one struct, so this file is the
// whole surface between the HTML and the rest of grokbox. Anything not here
// cannot be reached from the page.
type API struct {
	m   *Manager
	app *application.App
}

// ServiceName names the service in Wails' own logs.
func (a *API) ServiceName() string { return "grokbox" }

// Rooms lists every room this machine belongs to, for the sidebar.
func (a *API) Rooms() []RoomView { return a.m.List() }

// Open switches to a room and returns its backlog. Opening a room is what
// marks it read.
func (a *API) Open(id string) (RoomView, error) { return a.m.Open(id) }

// Send says something. A line starting with "/me " is sent as an action, the
// same as in the terminal.
func (a *API) Send(id string, text string) error { return a.m.Send(id, text) }

// Join adds a room from an invite code. The code carries the server, the
// room, the key and the hash of the server's certificate — it is one string
// because it is one thing, and taking it apart would drop the pinning.
func (a *API) Join(invite string, name string) (RoomView, error) { return a.m.Join(invite, name) }

// Leave ends the membership and drops the room from this machine.
func (a *API) Leave(id string) error { return a.m.Leave(id) }

// Version is the app's build version, shown in the sidebar footer.
func (a *API) Version() string { return version }

// MarkUnread puts a room back in the state it was in before you opened it.
func (a *API) MarkUnread(id string) error { return a.m.MarkUnread(id) }

// Invite is the room's invite code, for handing to somebody else.
func (a *API) Invite(id string) (string, error) { return a.m.InviteCode(id) }

// Fingerprint is the hash of the certificate the room is pinned to, shown
// under the invite so it can be read out over a different channel.
func (a *API) Fingerprint(id string) (string, error) {
	code, err := a.m.InviteCode(id)
	if err != nil {
		return "", err
	}
	in, err := proto.ParseInvite(code)
	if err != nil {
		return "", err
	}
	return in.Fingerprint, nil
}

// Copy puts text on the system clipboard. The page could ask the webview to
// do this itself, but only during a gesture it can prove; going through here
// works from a menu item that has already closed.
func (a *API) Copy(text string) error {
	if a.app == nil || !a.app.Clipboard.SetText(text) {
		return errors.New("could not reach the clipboard")
	}
	return nil
}

// ------------------------------------------------------------- appearance

// SetAvatar dresses a room: one of the eight shapes and one of the eleven
// colours. Empty for both puts it back to the pair derived from its name.
func (a *API) SetAvatar(id, shape, color string) error {
	return a.m.editRoom(id, func(p *RoomPrefs) {
		p.Shape, p.Color = shape, color
		if shape != "" || color != "" {
			p.Photo = "" // a picture and a shape cannot both be the avatar
		}
	})
}

// SetNickname is what to call a room on this machine — the way out when two
// people both call their room "lounge". Empty restores the real name.
func (a *API) SetNickname(id, nickname string) error {
	nickname = strings.TrimSpace(nickname)
	if len([]rune(nickname)) > 40 {
		return errors.New("that name is too long")
	}
	return a.m.editRoom(id, func(p *RoomPrefs) { p.Nickname = nickname })
}

// SetQuiet turns off being told when you are named in one room, without
// touching the others.
func (a *API) SetQuiet(id string, quiet bool) error {
	return a.m.editRoom(id, func(p *RoomPrefs) { p.Quiet = quiet })
}

// SetHidden takes a room out of the sidebar. It stays joined and stays
// connected — hiding is about the list, not the membership.
func (a *API) SetHidden(id string, hidden bool) error {
	return a.m.editRoom(id, func(p *RoomPrefs) { p.Hidden = hidden })
}

// PickPhoto opens a file dialog and uses what you choose as the avatar. An
// empty id means your own picture. It answers with the room again, or with
// your profile, so the window can redraw without asking twice.
func (a *API) PickPhoto(id string) error {
	if a.app == nil {
		return errors.New("no window to hang the dialog on")
	}
	path, err := a.app.Dialog.OpenFile().
		SetTitle("Pick a picture").
		CanChooseFiles(true).
		AddFilter("Images", "*.png;*.jpg;*.jpeg;*.gif;*.webp").
		PromptForSingleSelection()
	if err != nil || path == "" {
		return nil // a cancelled dialog is not a failure
	}
	if id == "" {
		name, err := adoptPhoto(path, "profile")
		if err != nil {
			return err
		}
		return a.m.setProfilePhoto(name)
	}
	name, err := adoptPhoto(path, "room-"+safeName(id))
	if err != nil {
		return err
	}
	return a.m.editRoom(id, func(p *RoomPrefs) {
		p.Photo, p.Shape, p.Color = name, "", ""
	})
}

// ------------------------------------------------------------------- you

// Profile is your name, your picture and the disc to fall back on.
func (a *API) Profile() ProfileView { return a.m.profileView() }

// SetProfileAvatar dresses you the way SetAvatar dresses a room, and drops
// any picture, since only one of the two can be showing.
func (a *API) SetProfileAvatar(shape, color string) error {
	return a.m.editProfile(func(p *ProfilePrefs) {
		p.Shape, p.Color = shape, color
		if shape != "" || color != "" {
			p.Photo = ""
		}
	})
}

// SetGitHub says which GitHub account's picture is yours and fetches it. An
// empty login means "work it out" — `gh` is usually already signed in.
func (a *API) SetGitHub(login string) error {
	return a.m.fetchPhoto(strings.TrimSpace(login))
}

// ClearProfile puts you back to a plain coloured disc with your initial on it.
func (a *API) ClearProfile() error {
	return a.m.editProfile(func(p *ProfilePrefs) { *p = ProfilePrefs{} })
}

// safeName turns a room id into something that can be a file name.
func safeName(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

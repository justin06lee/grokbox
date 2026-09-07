package main

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/justin06lee/grokbox/internal/client"
	"github.com/justin06lee/grokbox/internal/desktop"
	"github.com/justin06lee/grokbox/internal/proto"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/dock"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// historyCap is how much of a room the app keeps in memory. The server keeps
// its own transcript; this is only what the window can scroll through.
const historyCap = 2000

// Room is one membership, with its stream running.
//
// Every room this machine has ever joined gets one of these at startup and
// keeps it for the life of the app: a room you are not looking at still
// collects messages, still counts unread, and still says your name out loud.
// That is the whole reason the app exists rather than a terminal.
type Room struct {
	ID     string // server + "/" + room, stable across restarts
	Server string
	Room   string
	Name   string // who you are in it

	cl     *client.Client
	cancel context.CancelFunc

	mu        sync.Mutex
	history   []proto.Message
	members   []proto.Member
	unread    int
	mentioned bool // an unread message named you
	connected bool
	note      string // last thing the connection said about itself
}

func (r *Room) snapshot(withHistory bool) RoomView {
	r.mu.Lock()
	defer r.mu.Unlock()
	v := RoomView{
		ID:        r.ID,
		Server:    r.Server,
		Room:      r.Room,
		Name:      r.Name,
		Unread:    r.unread,
		Mentioned: r.mentioned,
		Connected: r.connected,
		Note:      r.note,
		Members:   append([]proto.Member(nil), r.members...),
	}
	if n := len(r.history); n > 0 {
		last := r.history[n-1]
		v.Last = &last
	}
	if withHistory {
		v.History = make([]MessageView, 0, len(r.history))
		for _, m := range r.history {
			v.History = append(v.History, r.view(m))
		}
	}
	return v
}

// RoomView is a room as the window sees it.
type RoomView struct {
	ID        string         `json:"id"`
	Server    string         `json:"server"`
	Room      string         `json:"room"`
	Name      string         `json:"name"`
	Unread    int            `json:"unread"`
	Mentioned bool           `json:"mentioned"`
	Connected bool           `json:"connected"`
	Note      string         `json:"note"`
	Last      *proto.Message `json:"last"`
	History   []MessageView  `json:"history"`
	Members   []proto.Member `json:"members"`

	// Everything below is app.json's, not the protocol's: how the room looks
	// in this window and whether it may interrupt you. Title is Room unless
	// you have renamed it here.
	Title    string `json:"title"`
	Shape    string `json:"shape"`
	Color    string `json:"color"`
	Photo    string `json:"photo"`
	Nickname string `json:"nickname"`
	Hidden   bool   `json:"hidden"`
	Quiet    bool   `json:"quiet"`
}

// MessageView is a message with the two things the window would otherwise
// have to work out for itself: whether you said it, and whether it says your
// name. The second one is proto.Mentions — the same test the server uses to
// wake an agent — so what the window highlights is exactly what would have
// interrupted a bot standing in your place.
type MessageView struct {
	proto.Message
	Mine    bool `json:"mine"`
	Mention bool `json:"mention"`
}

// view annotates a message for the window. The caller holds no lock on the
// room: Name is fixed for the life of the membership.
func (r *Room) view(m proto.Message) MessageView {
	return MessageView{
		Message: m,
		Mine:    m.From == r.Name,
		Mention: m.From != r.Name && proto.Mentions(m.Text, r.Name, nil),
	}
}

// Manager owns every room, the config file, and the two ways the app
// interrupts you: a notification and a number on the dock icon.
type Manager struct {
	app   *application.App
	notes *notifications.NotificationService
	dock  *dock.DockService
	tray  *application.SystemTray

	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	rooms   []*Room
	active  string // the room the window is showing
	focused bool   // is the window in front

	cfgMu sync.Mutex // one writer at a time for client.json

	prefsMu sync.Mutex // and one for app.json
	prefs   Prefs

	// notesOK is whether the OS has actually granted this build the right to
	// notify. It has to be asked, not assumed: an ad-hoc signed bundle is
	// refused, and SendNotification then swallows the message rather than
	// failing, so a notifier that trusted it would go quietly silent.
	notesOK atomic.Bool
}

// AllowNotifications records the answer to the authorization request.
func (m *Manager) AllowNotifications(ok bool) { m.notesOK.Store(ok) }

func NewManager() *Manager {
	m := &Manager{prefs: loadPrefs()}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	return m
}

// dress puts app.json's opinions onto a room the protocol just described.
func (m *Manager) dress(v RoomView) RoomView {
	rp := m.prefsFor(v.ID)
	v.Shape, v.Color, v.Nickname = rp.Shape, rp.Color, rp.Nickname
	v.Photo, v.Hidden, v.Quiet = dataURL(rp.Photo), rp.Hidden, rp.Quiet
	v.Title = v.Room
	if rp.Nickname != "" {
		v.Title = rp.Nickname
	}
	return v
}

func (m *Manager) attach(app *application.App, notes *notifications.NotificationService, dk *dock.DockService) {
	m.app = app
	m.notes = notes
	m.dock = dk
}

// ------------------------------------------------------------------ loading

// Load brings up every room this machine has joined before. It is the same
// list `grokbox rooms` prints, from the same file, so a room joined in the
// terminal is in the app the next time it starts.
func (m *Manager) Load() error {
	cfg, err := client.LoadConfig()
	if err != nil {
		return err
	}
	for i := range cfg.Profiles {
		p := cfg.Profiles[i]
		if p.Name == "" {
			continue // a room we have an invite for but never joined as anyone
		}
		m.adopt(p, nil, nil)
	}
	return nil
}

// adopt starts a room from a saved profile, or returns the one already running
// for it. A caller that has already joined — the paste-an-invite path, which
// joins to find out whether the code works — hands over that client and its
// backlog rather than making the room join a second time.
func (m *Manager) adopt(p client.Profile, cl *client.Client, joined *proto.JoinResponse) *Room {
	id := roomID(p.Server, p.Room)
	m.mu.Lock()
	for _, r := range m.rooms {
		if r.ID == id {
			m.mu.Unlock()
			return r
		}
	}
	if cl == nil {
		cl = client.New(p.Invite(), p.Name)
		if p.Token != "" {
			cl.SetToken(p.Token)
			cl.SetSeq(p.Seq)
		}
	}
	r := &Room{ID: id, Server: strings.TrimRight(p.Server, "/"), Room: p.Room, Name: p.Name, cl: cl}
	m.rooms = append(m.rooms, r)
	m.mu.Unlock()

	ctx, cancel := context.WithCancel(m.ctx)
	r.cancel = cancel
	go m.run(ctx, r, joined)
	return r
}

func roomID(server, room string) string {
	return strings.TrimRight(server, "/") + "/" + room
}

// ------------------------------------------------------------- the stream

// run keeps one room connected for as long as the app is open. resp, when
// given, is a join the caller already did.
func (m *Manager) run(ctx context.Context, r *Room, resp *proto.JoinResponse) {
	// Join rather than Resume: presenting the saved token reclaims the same
	// membership, and the response carries the backlog, which Resume skips
	// when the old session is still good. A window with no history in it is
	// not worth the reconnect it saves.
	var err error
	if resp == nil {
		resp, err = r.cl.Join(ctx)
	}
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		r.setNote(false, friendly(err))
		m.publish(r)
	} else {
		r.mu.Lock()
		r.history = trim(resp.History)
		r.members = resp.Members
		r.connected = true
		r.note = ""
		r.mu.Unlock()
		m.save(r)
		m.publish(r)
	}

	// Follow re-joins when a session expires, which mints a new token. Write
	// it back so the CLI resumes the same membership instead of being refused
	// the name this process is holding.
	go m.trackToken(ctx, r)

	err = r.cl.Follow(ctx, func(msg proto.Message) error {
		m.arrived(r, msg)
		return nil
	}, func(note string) {
		up := strings.HasPrefix(note, "connected")
		r.setNote(up, note)
		if up {
			m.refreshMembers(ctx, r)
		}
		m.publish(r)
	})
	if err != nil && ctx.Err() == nil {
		r.setNote(false, friendly(err))
		m.publish(r)
	}
}

// arrived files one message: into the room's history, out to the window, and
// — if it names you and you are not looking — onto the desktop.
func (m *Manager) arrived(r *Room, msg proto.Message) {
	m.mu.Lock()
	watching := m.active == r.ID && m.focused
	m.mu.Unlock()

	news := msg.Kind == proto.KindChat || msg.Kind == proto.KindAction
	mine := msg.From == r.Name
	mention := news && !mine && proto.Mentions(msg.Text, r.Name, nil)

	r.mu.Lock()
	r.history = trim(append(r.history, msg))
	r.connected = true
	if !watching && news && !mine {
		r.unread++
		if mention {
			r.mentioned = true
		}
	}
	r.mu.Unlock()

	switch msg.Kind {
	case proto.KindJoin, proto.KindLeave, proto.KindSystem:
		m.refreshMembers(m.ctx, r)
	}
	m.app.Event.Emit("grokbox:message", map[string]any{"room": r.ID, "message": r.view(msg)})
	m.publish(r)
	m.save(r)

	if mention && !watching && !m.prefsFor(r.ID).Quiet {
		m.notify(r, msg)
	}
}

func (m *Manager) refreshMembers(ctx context.Context, r *Room) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	members, err := r.cl.Members(ctx)
	if err != nil {
		return
	}
	r.mu.Lock()
	r.members = members
	r.mu.Unlock()
}

func (m *Manager) trackToken(ctx context.Context, r *Room) {
	last := r.cl.Token()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if now := r.cl.Token(); now != "" && now != last {
				last = now
				m.save(r)
			}
		}
	}
}

func trim(h []proto.Message) []proto.Message {
	if len(h) <= historyCap {
		return h
	}
	return h[len(h)-historyCap:]
}

// ------------------------------------------------------- telling the window

// publish pushes the whole room list to the window. It is small enough that
// diffing it would cost more than sending it.
func (m *Manager) publish(r *Room) {
	if m.app == nil {
		return
	}
	m.app.Event.Emit("grokbox:rooms", m.List())
	m.badge()
	_ = r
}

// List is every room, most recently spoken in first.
func (m *Manager) List() []RoomView {
	m.mu.Lock()
	rooms := append([]*Room(nil), m.rooms...)
	m.mu.Unlock()

	out := make([]RoomView, 0, len(rooms))
	for _, r := range rooms {
		out = append(out, m.dress(r.snapshot(false)))
	}
	return out
}

// badge puts the number of unread messages on the dock icon and next to the
// menu bar icon — the only parts of the app you can see when it is behind
// everything else.
func (m *Manager) badge() {
	total := 0
	m.mu.Lock()
	for _, r := range m.rooms {
		r.mu.Lock()
		total += r.unread
		r.mu.Unlock()
	}
	m.mu.Unlock()

	label := ""
	if total > 0 {
		label = strconv.Itoa(total)
	}
	if m.tray != nil {
		m.tray.SetLabel(label)
	}
	if m.dock == nil {
		return
	}
	if label == "" {
		_ = m.dock.RemoveBadge()
		return
	}
	_ = m.dock.SetBadge(label)
}

// notify raises a real notification for a message that named you.
//
// The bundled notification service is the good one — right icon, click to
// come back — but macOS only allows it for a properly signed bundle. When it
// is refused, this falls back to the same path `grokbox notify` uses, so an
// unsigned build still tells you when you are named.
func (m *Manager) notify(r *Room, msg proto.Message) {
	body := msg.Text
	if msg.Kind == proto.KindAction {
		body = msg.From + " " + msg.Text
	}
	if len([]rune(body)) > 200 {
		body = string([]rune(body)[:197]) + "…"
	}
	if m.notes != nil && m.notesOK.Load() {
		err := m.notes.SendNotification(notifications.NotificationOptions{
			ID:       r.ID + "/" + strconv.FormatInt(msg.Seq, 10),
			Title:    msg.From + " in " + r.Room,
			Body:     body,
			ThreadID: r.ID,
			Data:     map[string]any{"room": r.ID},
		})
		if err == nil {
			return
		}
	}
	_ = desktop.Show(desktop.Notification{
		Title:    "grokbox",
		Subtitle: msg.From + " in " + r.Room,
		Body:     body,
		Group:    "grokbox-" + r.Room,
		// terminal-notifier is the only one of these that can act on a click;
		// where it is installed, the notification brings the app back.
		Exec: []string{"open", "-b", bundleID},
	})
}

// ------------------------------------------------------------------ actions

func (m *Manager) find(id string) *Room {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.rooms {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// Open marks a room as the one being read and hands back its backlog.
func (m *Manager) Open(id string) (RoomView, error) {
	r := m.find(id)
	if r == nil {
		return RoomView{}, errors.New("no such room")
	}
	m.mu.Lock()
	m.active = id
	m.mu.Unlock()
	r.mu.Lock()
	r.unread, r.mentioned = 0, false
	r.mu.Unlock()
	m.badge()
	m.app.Event.Emit("grokbox:rooms", m.List())
	return m.dress(r.snapshot(true)), nil
}

// Send says something in a room.
func (m *Manager) Send(id, text string) error {
	r := m.find(id)
	if r == nil {
		return errors.New("no such room")
	}
	ctx, cancel := context.WithTimeout(m.ctx, 20*time.Second)
	defer cancel()

	body, action := strings.TrimSpace(text), false
	if rest, ok := strings.CutPrefix(body, "/me "); ok {
		body, action = strings.TrimSpace(rest), true
	}
	var err error
	if action {
		_, err = r.cl.Act(ctx, body)
	} else {
		_, err = r.cl.Say(ctx, body)
	}
	if err != nil {
		return errors.New(friendly(err))
	}
	return nil
}

// Join takes an invite code and a name and adds the room to the list.
func (m *Manager) Join(invite, name string) (RoomView, error) {
	in, err := proto.ParseInvite(strings.TrimSpace(invite))
	if err != nil {
		return RoomView{}, err
	}
	if in.Room, err = proto.CleanRoom(in.Room); err != nil {
		return RoomView{}, err
	}
	if name, err = proto.CleanName(name); err != nil {
		return RoomView{}, err
	}
	in.Server = withScheme(in.Server)

	// Prove the invite works before writing it down: a room in the sidebar
	// that never connects is worse than an error on the button.
	cl := client.New(in, name)
	ctx, cancel := context.WithTimeout(m.ctx, 20*time.Second)
	defer cancel()
	resp, err := cl.Join(ctx)
	if err != nil {
		return RoomView{}, errors.New(friendly(err))
	}

	p := client.Profile{
		Server: strings.TrimRight(in.Server, "/"), Room: in.Room, Key: in.Key,
		Fingerprint: in.Fingerprint, Name: name, Token: cl.Token(), Seq: cl.Seq(),
	}
	m.persist(p)
	// The membership just proved to work is the one the room keeps: joining a
	// second time would show everyone a join, a leave and a join.
	r := m.adopt(p, cl, resp)
	m.app.Event.Emit("grokbox:rooms", m.List())
	return m.dress(r.snapshot(false)), nil
}

// Leave ends the membership and forgets the room.
//
// The room goes from this machine first and the server is told afterwards, in
// the background. Telling it is a courtesy — it puts a leave line in the room
// straight away instead of waiting for the idle timeout — and a courtesy must
// not be able to hold the button down: leaving is most wanted exactly when the
// server is unreachable, and a server that accepts the connection but never
// answers would otherwise freeze the window until the timeout ran out.
func (m *Manager) Leave(id string) error {
	r := m.find(id)
	if r == nil {
		return errors.New("no such room")
	}
	if r.cancel != nil {
		r.cancel()
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = r.cl.Leave(ctx)
	}()

	m.mu.Lock()
	kept := m.rooms[:0]
	for _, other := range m.rooms {
		if other.ID != id {
			kept = append(kept, other)
		}
	}
	m.rooms = kept
	if m.active == id {
		m.active = ""
	}
	m.mu.Unlock()

	m.forget(r)
	m.forgetPrefs(id)
	m.app.Event.Emit("grokbox:rooms", m.List())
	m.badge()
	return nil
}

// MarkUnread puts a room back in the state it was in before you read it, so
// you come back to it. The count is one because the point is the mark, not an
// accurate replay of how much you had missed.
func (m *Manager) MarkUnread(id string) error {
	r := m.find(id)
	if r == nil {
		return errors.New("no such room")
	}
	m.mu.Lock()
	if m.active == id {
		m.active = ""
	}
	m.mu.Unlock()
	r.mu.Lock()
	if r.unread == 0 {
		r.unread = 1
	}
	r.mu.Unlock()
	m.badge()
	m.app.Event.Emit("grokbox:rooms", m.List())
	return nil
}

// InviteCode is the string that gets somebody else into this room. It is
// rebuilt from the live client rather than read back off disk, so it carries
// whatever certificate the room is actually pinned to right now.
func (m *Manager) InviteCode(id string) (string, error) {
	r := m.find(id)
	if r == nil {
		return "", errors.New("no such room")
	}
	return r.cl.Invite().String(), nil
}

// SetFocus records whether the window is in front. A message that arrives
// while you are looking at its room is not something to interrupt you with.
func (m *Manager) SetFocus(focused bool) {
	m.mu.Lock()
	m.focused = focused
	active := m.active
	m.mu.Unlock()
	if !focused || active == "" {
		return
	}
	if r := m.find(active); r != nil {
		r.mu.Lock()
		r.unread, r.mentioned = 0, false
		r.mu.Unlock()
		m.badge()
		m.app.Event.Emit("grokbox:rooms", m.List())
	}
}

func (m *Manager) Shutdown() { m.cancel() }

// --------------------------------------------------------------- the config

func (m *Manager) save(r *Room) {
	inv := r.cl.Invite()
	m.persist(client.Profile{
		Server: inv.Server, Room: inv.Room, Key: inv.Key, Fingerprint: inv.Fingerprint,
		Name: r.cl.Name, Token: r.cl.Token(), Seq: r.cl.Seq(),
	})
}

// persist writes one profile back, re-reading the file first so a `grokbox`
// command running in a terminal at the same time does not lose its place.
func (m *Manager) persist(p client.Profile) {
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()
	cfg, err := client.LoadConfig()
	if err != nil {
		return
	}
	cfg.Remember(p)
	_ = cfg.Save()
}

func (m *Manager) forget(r *Room) {
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()
	cfg, err := client.LoadConfig()
	if err != nil {
		return
	}
	kept := cfg.Profiles[:0]
	for _, p := range cfg.Profiles {
		if roomID(p.Server, p.Room) != r.ID {
			kept = append(kept, p)
		}
	}
	cfg.Profiles = kept
	_ = cfg.Save()
}

// ---------------------------------------------------------------- odds/ends

func (r *Room) setNote(connected bool, note string) {
	r.mu.Lock()
	r.connected, r.note = connected, note
	r.mu.Unlock()
}

func withScheme(server string) string {
	server = strings.TrimRight(strings.TrimSpace(server), "/")
	if server == "" || strings.Contains(server, "://") {
		return server
	}
	return "http://" + server
}

// friendly keeps the client's own error wording, which already explains the
// common failures in terms of what to do about them.
func friendly(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

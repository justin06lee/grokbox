package main

// API is everything the window is allowed to ask the room manager to do.
//
// Wails binds the exported methods of this one struct, so this file is the
// whole surface between the HTML and the rest of grokbox. Anything not here
// cannot be reached from the page.
type API struct{ m *Manager }

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

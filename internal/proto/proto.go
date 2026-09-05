// Package proto defines the grokbox wire format: the message shape every
// participant sees, the request/response bodies of the HTTP API, and the
// invite code that packs an address, a room and a key into one string.
package proto

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"
)

// Version is the protocol version. A client refuses to talk to a server that
// speaks a different major version.
const Version = 1

// Limits enforced by both ends.
const (
	MaxNameLen    = 32
	MaxRoomLen    = 48
	MaxKeyLen     = 128
	MaxTextLen    = 4000
	MaxRoomMember = 64
)

// Message kinds.
const (
	KindChat   = "chat"   // someone said something
	KindAction = "action" // someone did something (/me)
	KindJoin   = "join"   // someone entered the room
	KindLeave  = "leave"  // someone left the room
	KindSystem = "system" // the server said something
)

// Message is one line of room history. Seq is a per-room monotonic counter
// starting at 1; clients use it as a cursor.
type Message struct {
	Seq  int64     `json:"seq"`
	Room string    `json:"room"`
	Kind string    `json:"kind"`
	From string    `json:"from"`
	Text string    `json:"text"`
	Time time.Time `json:"time"`
}

// Member is a participant currently in a room.
type Member struct {
	Name   string    `json:"name"`
	Joined time.Time `json:"joined"`
}

// JoinRequest is the body of POST /v1/join. Token is optional: presenting a
// previous session token for the same name reclaims it, so a client that was
// cut off can come straight back instead of waiting out the idle timeout.
type JoinRequest struct {
	Room    string `json:"room"`
	Key     string `json:"key"`
	Name    string `json:"name"`
	Token   string `json:"token,omitempty"`
	Version int    `json:"version"`
}

// JoinResponse is returned on a successful join.
type JoinResponse struct {
	Token   string    `json:"token"`
	Name    string    `json:"name"`
	Room    string    `json:"room"`
	Seq     int64     `json:"seq"`     // latest sequence number at join time
	History []Message `json:"history"` // recent backlog, oldest first
	Members []Member  `json:"members"`
	Server  string    `json:"server"`  // server version string
	Version int       `json:"version"` // protocol version
}

// SendRequest is the body of POST /v1/send.
type SendRequest struct {
	Text string `json:"text"`
	Kind string `json:"kind,omitempty"` // "chat" (default) or "action"
}

// SendResponse acknowledges a sent message.
type SendResponse struct {
	Seq int64 `json:"seq"`
}

// MessagesResponse is returned by GET /v1/messages.
type MessagesResponse struct {
	Messages []Message `json:"messages"`
	Seq      int64     `json:"seq"` // latest sequence number in the room
	Members  []Member  `json:"members"`
}

// Error is the body of every non-2xx response.
type Error struct {
	Error string `json:"error"`
}

// Invite packs everything a newcomer needs into one shareable string.
//
// Fingerprint is what makes a room on a bare IP safe to join. A server with no
// certificate authority behind it signs its own, and the invite carries the
// hash of it — so the client knows which certificate to expect before it ever
// connects, which is a stronger position than the usual one of trusting
// whatever a stranger presents on first contact.
type Invite struct {
	Server      string `json:"s"` // base URL, e.g. https://chat.example.com
	Room        string `json:"r"`
	Key         string `json:"k"`
	Fingerprint string `json:"f,omitempty"`
}

// Fingerprint hashes a certificate in its DER encoding.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// SameFingerprint compares two fingerprints without leaking where they differ.
func SameFingerprint(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

const invitePrefix = "grokbox1-"

// Encode renders the invite as a single token safe to paste into a chat
// message or read aloud over a table.
func (in Invite) Encode() string {
	b, err := json.Marshal(in)
	if err != nil { // impossible for a struct of strings
		panic(err)
	}
	return invitePrefix + base64.RawURLEncoding.EncodeToString(b)
}

func (in Invite) String() string { return in.Encode() }

// ParseInvite reads a code produced by Encode.
func ParseInvite(code string) (Invite, error) {
	var in Invite
	code = strings.TrimSpace(code)
	if !strings.HasPrefix(code, invitePrefix) {
		return in, fmt.Errorf("not a grokbox invite code (want a %s… string)", invitePrefix)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(code, invitePrefix))
	if err != nil {
		return in, errors.New("invite code is corrupt")
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return in, errors.New("invite code is corrupt")
	}
	if in.Server == "" || in.Room == "" {
		return in, errors.New("invite code is missing a server or room")
	}
	if _, err := url.Parse(in.Server); err != nil {
		return in, fmt.Errorf("invite code has a bad server address: %w", err)
	}
	return in, nil
}

// CleanName validates a display name. Names are single-line, printable, and
// free of the whitespace that would let one member impersonate another.
func CleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("name is empty")
	}
	if len([]rune(name)) > MaxNameLen {
		return "", fmt.Errorf("name is longer than %d characters", MaxNameLen)
	}
	for _, r := range name {
		if unicode.IsSpace(r) && r != ' ' {
			return "", errors.New("name cannot contain tabs or newlines")
		}
		if unicode.IsControl(r) {
			return "", errors.New("name cannot contain control characters")
		}
	}
	if strings.Contains(name, "  ") {
		return "", errors.New("name cannot contain double spaces")
	}
	return name, nil
}

// CleanRoom validates a room name: lowercase-friendly, no spaces, URL-safe.
func CleanRoom(room string) (string, error) {
	room = strings.TrimSpace(strings.ToLower(room))
	if room == "" {
		return "", errors.New("room name is empty")
	}
	if len(room) > MaxRoomLen {
		return "", fmt.Errorf("room name is longer than %d characters", MaxRoomLen)
	}
	if strings.HasPrefix(room, ".") {
		return "", errors.New("room name cannot start with a dot")
	}
	for _, r := range room {
		ok := r == '-' || r == '_' || r == '.' ||
			(r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if !ok {
			return "", errors.New("room name may only contain a-z, 0-9, dot, dash and underscore")
		}
	}
	return room, nil
}

// CleanText validates a chat line.
func CleanText(text string) (string, error) {
	text = strings.TrimRight(text, " \t\r\n")
	if strings.TrimSpace(text) == "" {
		return "", errors.New("message is empty")
	}
	if len([]rune(text)) > MaxTextLen {
		return "", fmt.Errorf("message is longer than %d characters", MaxTextLen)
	}
	var b strings.Builder
	for _, r := range text {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			continue // strip escape sequences so nobody can repaint another terminal
		}
		b.WriteRune(r)
	}
	return b.String(), nil
}

// NewKey mints a room key that is short enough to read out loud but too large
// to guess: 4 groups of 4 characters from an unambiguous alphabet.
func NewKey() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789" // no i/l/o/0/1
	buf := make([]byte, 16)
	mustRand(buf)
	out := make([]byte, 0, 19)
	for i, b := range buf {
		if i > 0 && i%4 == 0 {
			out = append(out, '-')
		}
		out = append(out, alphabet[int(b)%len(alphabet)])
	}
	return string(out)
}

// NewToken mints an opaque session token.
func NewToken() string {
	buf := make([]byte, 24)
	mustRand(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

func mustRand(b []byte) {
	if _, err := rand.Read(b); err != nil {
		panic("grokbox: no system randomness: " + err.Error())
	}
}

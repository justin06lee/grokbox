// Package proto defines the grokbox wire format: the message shape every
// participant sees, the request/response bodies of the HTTP API, and the
// invite code that packs an address, a room, a key and the hash of the
// server's certificate into one shareable string.
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
	"unicode/utf8"
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
	MaxRoomHook   = 32
	MaxHookURLLen = 512
	MaxHookKeyLen = 512
	MaxHookAlias  = 8
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

// Hook is a standing request to be woken. A member registers the address of
// something that starts an agent — a Grok Bot routine's webhook, a CI job, a
// script — and the server calls it whenever that member is mentioned in the
// room. It is the difference between an agent that has to be told to look and
// one that hears its name.
//
// The bearer token is never returned by the API: it is a credential that
// starts a run, so it goes to the server and stays there.
type Hook struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`              // whose mentions wake it
	Aliases []string  `json:"aliases,omitempty"` // other @names it answers to
	URL     string    `json:"url"`
	Added   time.Time `json:"added"`
	Woken   int64     `json:"woken"`            // deliveries made
	Failed  int       `json:"failed,omitempty"` // consecutive failures
	Broken  bool      `json:"broken,omitempty"` // gave up after too many
}

// HookAddRequest is the body of POST /v1/hooks. The hook wakes on mentions of
// whoever registers it, so there is no name to give.
type HookAddRequest struct {
	URL     string   `json:"url"`
	Key     string   `json:"key,omitempty"` // bearer token for the call
	Aliases []string `json:"aliases,omitempty"`
}

// HookResponse carries one hook.
type HookResponse struct {
	Hook Hook `json:"hook"`
}

// HooksResponse lists the hooks in a room.
type HooksResponse struct {
	Hooks []Hook `json:"hooks"`
}

// Names that wake everybody at once.
var everyoneAliases = []string{"all", "everyone", "room", "channel", "here"}

// Mentions reports whether text addresses name, or one of its aliases, or the
// whole room.
//
// A mention is the name after an "@", bounded on both sides: "@navi" in
// "@navi are you there?" counts, "@navigator" does not, and neither does the
// "@" in an email address. Names may contain spaces — a bot called
// "Alex's navi" is reached as "@Alex's navi" — which is why this matches
// whole candidates rather than splitting the line into words.
func Mentions(text, name string, aliases []string) bool {
	if strings.IndexByte(text, '@') < 0 {
		return false
	}
	low := strings.ToLower(text)
	if mentionsOne(low, strings.ToLower(strings.TrimSpace(name))) {
		return true
	}
	for _, a := range aliases {
		if mentionsOne(low, strings.ToLower(strings.TrimSpace(a))) {
			return true
		}
	}
	for _, a := range everyoneAliases {
		if mentionsOne(low, a) {
			return true
		}
	}
	return false
}

// mentionsOne looks for "@candidate" in an already-lowercased line.
func mentionsOne(low, candidate string) bool {
	if candidate == "" {
		return false
	}
	want := "@" + candidate
	for i := 0; ; {
		j := strings.Index(low[i:], want)
		if j < 0 {
			return false
		}
		at := i + j
		end := at + len(want)
		if boundaryBefore(low, at) && boundaryAfter(low, end) {
			return true
		}
		i = at + 1
	}
}

// boundaryBefore rejects an "@" glued to the end of a word, which is what an
// email address looks like.
func boundaryBefore(s string, at int) bool {
	if at == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(s[:at])
	return !isNameRune(r)
}

// boundaryAfter rejects a longer name that merely starts with a shorter one,
// so "@navigator" does not wake "navi".
func boundaryAfter(s string, end int) bool {
	if end >= len(s) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(s[end:])
	return !isNameRune(r)
}

func isNameRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
}

// CleanHookURL checks an address the server is being asked to call. It must be
// absolute and, unless the operator has said otherwise, HTTPS: the bearer
// token travels with every call.
func CleanHookURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("a webhook URL is required")
	}
	if len(raw) > MaxHookURLLen {
		return "", fmt.Errorf("webhook URL is longer than %d characters", MaxHookURLLen)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("that is not a URL: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", errors.New("webhook URL must be http or https")
	}
	if u.Host == "" {
		return "", errors.New("webhook URL has no host")
	}
	return u.String(), nil
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

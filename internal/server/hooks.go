package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/justin06lee/grokbox/internal/proto"
)

// Hooks turn the room from something an agent has to check into something
// that reaches out. A member registers an address that starts a run — a Grok
// Bot routine's webhook, a CI job, any URL — and the server calls it the
// moment somebody says that member's name.
//
// Only mentions fire a hook, and that is the whole safety story. A room full
// of agents that all woke on every line would answer each other's answers,
// and each wake costs a real run; requiring a name means a bot speaks when it
// is spoken to and is otherwise silent. "@all" is the deliberate exception.

const (
	// hookTimeout bounds one delivery. A webhook that starts an agent
	// answers quickly with an acknowledgement; it does not hold the
	// connection open for the run.
	hookTimeout = 15 * time.Second

	// hookFailLimit is how many consecutive failures a hook gets before the
	// server stops calling it. A webhook whose automation was deleted would
	// otherwise be retried until the end of time.
	hookFailLimit = 10

	// hookBatch caps how many mentions one delivery carries. Past this the
	// oldest are dropped: an agent that has fallen this far behind should
	// read the room, not the payload.
	hookBatch = 20

	// hookBodyMax caps the rendered payload.
	hookBodyMax = 4000
)

// hook is one registered wake-up, plus the state of its delivery.
type hook struct {
	id      string
	room    string
	name    string
	aliases []string
	url     string
	key     string
	added   time.Time

	// Delivery state, guarded by hooks.mu.
	pending []proto.Message
	firing  bool
	nextOK  time.Time
	woken   int64
	failed  int
	broken  bool
	gone    bool
}

func (h *hook) wire() proto.Hook {
	return proto.Hook{
		ID:      h.id,
		Name:    h.name,
		Aliases: h.aliases,
		URL:     h.url,
		Added:   h.added,
		Woken:   h.woken,
		Failed:  h.failed,
		Broken:  h.broken,
	}
}

// matches reports whether a message should wake this hook. A hook never wakes
// on its owner's own words: an agent that answered itself would never stop.
func (h *hook) matches(m proto.Message) bool {
	if m.Kind != proto.KindChat && m.Kind != proto.KindAction {
		return false
	}
	if strings.EqualFold(m.From, h.name) {
		return false
	}
	return proto.Mentions(m.Text, h.name, h.aliases)
}

// hooks is the set of every hook the server holds, and the machinery that
// delivers to them.
type hooks struct {
	ctx      context.Context
	cooldown time.Duration
	store    *store
	logf     func(string, ...any)
	http     *http.Client

	mu     sync.Mutex
	byRoom map[string][]*hook
}

func newHooks(ctx context.Context, cooldown time.Duration, allowPrivate bool, st *store, logf func(string, ...any)) *hooks {
	return &hooks{
		ctx:      ctx,
		cooldown: cooldown,
		store:    st,
		logf:     logf,
		http:     hookClient(allowPrivate),
		byRoom:   map[string][]*hook{},
	}
}

// hookClient is the client the server calls out with. Two things about it
// matter more than the timeout.
//
// It does not follow redirects, because a redirect would carry somebody's
// bearer token to an address they never registered. And it refuses to connect
// to a private address unless told otherwise: anyone holding a room key can
// register a URL, so without that check a room on a public box is a way to
// make it knock on the doors of its own network. The check runs at dial time,
// on the address actually resolved, so a name that answers publicly once and
// privately the next time does not get through either.
func hookClient(allowPrivate bool) *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if !allowPrivate {
		d.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("cannot read the address %q", address)
			}
			if !isGloballyRoutable(ip) {
				return fmt.Errorf("refusing to call %s: it is not a public address (pass --hook-private to allow it)", ip)
			}
			return nil
		}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = d.DialContext
	return &http.Client{
		Transport: tr,
		Timeout:   hookTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// ------------------------------------------------------------- the register

var (
	errNoSuchHook = errors.New("no such hook in this room")
	errNotYours   = errors.New("that hook belongs to somebody else")
	errTooMany    = errors.New("this room already holds as many hooks as it will")
)

// add registers a hook for name, replacing any this member already had. One
// member gets one hook: registering again is how a bot whose webhook was
// regenerated corrects itself, and it should not have to remember an id to do
// that.
func (h *hooks) add(room, name, rawURL, key string, aliases []string) (proto.Hook, error) {
	url, err := proto.CleanHookURL(rawURL)
	if err != nil {
		return proto.Hook{}, err
	}
	if len(key) > proto.MaxHookKeyLen {
		return proto.Hook{}, fmt.Errorf("webhook key is longer than %d characters", proto.MaxHookKeyLen)
	}
	clean, err := cleanAliases(aliases)
	if err != nil {
		return proto.Hook{}, err
	}

	h.mu.Lock()
	list := h.byRoom[room]
	replaced := false
	for _, old := range list {
		if strings.EqualFold(old.name, name) {
			old.gone = true
			replaced = true
		}
	}
	if !replaced && len(list) >= proto.MaxRoomHook {
		h.mu.Unlock()
		return proto.Hook{}, errTooMany
	}
	kept := list[:0]
	for _, old := range list {
		if !old.gone {
			kept = append(kept, old)
		}
	}
	hk := &hook{
		id:      newHookID(),
		room:    room,
		name:    name,
		aliases: clean,
		url:     url,
		key:     key,
		added:   time.Now().UTC(),
	}
	h.byRoom[room] = append(kept, hk)
	out := hk.wire()
	h.mu.Unlock()

	h.persist()
	return out, nil
}

// remove drops a hook. Only the member it wakes may take it away.
func (h *hooks) remove(room, id, by string) error {
	h.mu.Lock()
	list := h.byRoom[room]
	idx := -1
	for i, hk := range list {
		if hk.id == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		h.mu.Unlock()
		return errNoSuchHook
	}
	if !strings.EqualFold(list[idx].name, by) {
		h.mu.Unlock()
		return errNotYours
	}
	list[idx].gone = true
	h.byRoom[room] = append(list[:idx], list[idx+1:]...)
	h.mu.Unlock()

	h.persist()
	return nil
}

// list reports a room's hooks, without their keys.
func (h *hooks) list(room string) []proto.Hook {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]proto.Hook, 0, len(h.byRoom[room]))
	for _, hk := range h.byRoom[room] {
		out = append(out, hk.wire())
	}
	return out
}

// fire calls one hook now, past its cooldown, and reports what happened. It
// is how somebody checks the wiring without waiting to be mentioned, and it
// clears a hook the server had given up on.
func (h *hooks) fire(room, id, by, text string) error {
	h.mu.Lock()
	var hk *hook
	for _, c := range h.byRoom[room] {
		if c.id == id {
			hk = c
			break
		}
	}
	if hk == nil {
		h.mu.Unlock()
		return errNoSuchHook
	}
	if !strings.EqualFold(hk.name, by) {
		h.mu.Unlock()
		return errNotYours
	}
	url, key, name := hk.url, hk.key, hk.name
	h.mu.Unlock()

	err := h.deliverTo(url, key, fmt.Sprintf(
		"grokbox: a test wake for %q in room %q. Nothing was said — this is somebody checking that the hook reaches you. No reply is needed.\n\n%s",
		name, room, text))

	h.mu.Lock()
	if err == nil {
		hk.woken++
		hk.failed, hk.broken = 0, false
	} else {
		hk.failed++
	}
	h.mu.Unlock()
	return err
}

// ---------------------------------------------------------------- delivery

// deliver is called for every message a room takes. It is on the hot path of
// posting, so it does no I/O: it files the message with whichever hooks it
// mentions and lets their own goroutine do the calling.
func (h *hooks) deliver(room string, m proto.Message) {
	h.mu.Lock()
	var start []*hook
	for _, hk := range h.byRoom[room] {
		if hk.broken || !hk.matches(m) {
			continue
		}
		hk.pending = append(hk.pending, m)
		if len(hk.pending) > hookBatch {
			hk.pending = hk.pending[len(hk.pending)-hookBatch:]
		}
		if !hk.firing {
			hk.firing = true
			start = append(start, hk)
		}
	}
	h.mu.Unlock()

	for _, hk := range start {
		go h.run(hk)
	}
}

// run drains one hook's pending mentions, never more often than the cooldown.
//
// Mentions that arrive while a call is in flight are not dropped and do not
// queue up a second call: they collect, and the next delivery carries them
// all. An agent woken once with three lines is cheaper and more use than one
// woken three times with one line each.
func (h *hooks) run(hk *hook) {
	for {
		h.mu.Lock()
		if hk.gone {
			hk.firing = false
			h.mu.Unlock()
			return
		}
		wait := time.Until(hk.nextOK)
		h.mu.Unlock()

		if wait > 0 {
			t := time.NewTimer(wait)
			select {
			case <-t.C:
			case <-h.ctx.Done():
				t.Stop()
				return
			}
		}

		h.mu.Lock()
		if hk.gone || hk.broken || len(hk.pending) == 0 {
			hk.firing = false
			h.mu.Unlock()
			return
		}
		batch := hk.pending
		hk.pending = nil
		hk.nextOK = time.Now().Add(h.cooldown)
		url, key, name, room := hk.url, hk.key, hk.name, hk.room
		h.mu.Unlock()

		err := h.deliverTo(url, key, hookBody(name, room, batch))

		h.mu.Lock()
		if err == nil {
			hk.woken++
			hk.failed = 0
		} else {
			hk.failed++
			if hk.failed >= hookFailLimit {
				hk.broken = true
			}
		}
		failed, broken := hk.failed, hk.broken
		h.mu.Unlock()

		switch {
		case err == nil:
			h.logf("wake  %s in %s (%d mention(s))", name, room, len(batch))
		case broken:
			h.logf("wake  %s in %s failed %d times, giving up on that hook: %v", name, room, failed, err)
		default:
			h.logf("warn  waking %s in %s failed: %v", name, room, err)
		}
	}
}

// deliverTo makes the call. One retry, because the common failure is a
// connection that was not there for a moment, and a second attempt costs
// nothing an agent will notice.
func (h *hooks) deliverTo(url, key, context string) error {
	body, err := json.Marshal(map[string]string{"context": context})
	if err != nil {
		return err
	}
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			t := time.NewTimer(time.Second)
			select {
			case <-t.C:
			case <-h.ctx.Done():
				t.Stop()
				return last
			}
		}
		last = h.attempt(url, key, body)
		if last == nil {
			return nil
		}
		if !worthRetrying(last) {
			return last
		}
	}
	return last
}

// httpStatus is a non-2xx answer from a webhook.
type httpStatus struct {
	code int
	body string
}

func (e *httpStatus) Error() string {
	if e.body == "" {
		return fmt.Sprintf("the webhook answered %d", e.code)
	}
	return fmt.Sprintf("the webhook answered %d: %s", e.code, e.body)
}

// worthRetrying is true for a hiccup and false for an answer. A 401 means the
// key is wrong and will still be wrong in a second.
func worthRetrying(err error) bool {
	var st *httpStatus
	if errors.As(err, &st) {
		return st.code >= 500 || st.code == http.StatusTooManyRequests
	}
	return true // a transport error: the connection, not the answer
}

func (h *hooks) attempt(url, key string, body []byte) error {
	ctx, cancel := context.WithTimeout(h.ctx, hookTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "grokbox/"+Build)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := h.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		buf := make([]byte, 200)
		n, _ := resp.Body.Read(buf)
		return &httpStatus{code: resp.StatusCode, body: strings.TrimSpace(string(buf[:n]))}
	}
	return nil
}

// hookBody renders what the woken agent is handed. It says who was called,
// where, and exactly what was said — enough to answer a one-line question
// without reading anything else, and enough to know it is worth reading the
// room when it is not.
func hookBody(name, room string, msgs []proto.Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "grokbox: you were mentioned in room %q, as %q.\n\n", room, name)
	for _, m := range msgs {
		line := m.Text
		if m.Kind == proto.KindAction {
			line = "* " + m.From + " " + m.Text
			fmt.Fprintf(&b, "[%s] %s\n", m.Time.Format("15:04:05"), line)
			continue
		}
		fmt.Fprintf(&b, "[%s] %s: %s\n", m.Time.Format("15:04:05"), m.From, line)
	}
	b.WriteString("\nYou are already a member of this room. Read what you have missed with\n")
	b.WriteString("`grokbox read --json`, and answer with `grokbox send --text \"...\"`.\n")
	b.WriteString("If nothing here needs you, say nothing and stop — an empty room is fine.\n")

	out := b.String()
	if len(out) > hookBodyMax {
		out = out[:hookBodyMax-3] + "..."
	}
	return out
}

// ------------------------------------------------------------- persistence

// load restores the hooks a previous run registered, so a server restart does
// not quietly leave every agent deaf.
func (h *hooks) load() error {
	if h.store == nil {
		return nil
	}
	recs, err := h.store.loadHooks()
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range recs {
		h.byRoom[r.Room] = append(h.byRoom[r.Room], &hook{
			id:      r.ID,
			room:    r.Room,
			name:    r.Name,
			aliases: r.Aliases,
			url:     r.URL,
			key:     r.Key,
			added:   r.Added,
		})
	}
	return nil
}

func (h *hooks) persist() {
	if h.store == nil {
		return
	}
	h.mu.Lock()
	recs := make([]hookRecord, 0, 8)
	for room, list := range h.byRoom {
		for _, hk := range list {
			recs = append(recs, hookRecord{
				Room:    room,
				ID:      hk.id,
				Name:    hk.name,
				Aliases: hk.aliases,
				URL:     hk.url,
				Key:     hk.key,
				Added:   hk.added,
			})
		}
	}
	h.mu.Unlock()

	if err := h.store.saveHooks(recs); err != nil {
		h.logf("warn  cannot save hooks: %v", err)
	}
}

// ------------------------------------------------------------------ helpers

func cleanAliases(in []string) ([]string, error) {
	if len(in) > proto.MaxHookAlias {
		return nil, fmt.Errorf("a hook may answer to at most %d extra names", proto.MaxHookAlias)
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, a := range in {
		name, err := proto.CleanName(a)
		if err != nil {
			return nil, fmt.Errorf("alias %q: %w", a, err)
		}
		if low := strings.ToLower(name); !seen[low] {
			seen[low] = true
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// newHookID mints something short enough to retype from a listing.
func newHookID() string {
	return strings.ToLower(proto.NewToken()[:8])
}

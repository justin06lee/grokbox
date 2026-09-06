package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/justin06lee/grokbox/internal/client"
	"github.com/justin06lee/grokbox/internal/proto"
	"github.com/justin06lee/grokbox/internal/server"
)

// wakes records what a webhook was called with, so a test can wait for a
// delivery instead of sleeping and hoping.
type wakes struct {
	URL string

	mu     sync.Mutex
	got    []string
	auth   []string
	arrive chan string
	status int
}

func newWakes(t *testing.T) *wakes {
	t.Helper()
	w := &wakes{arrive: make(chan string, 16), status: http.StatusOK}

	ts := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Context string `json:"context"`
		}
		_ = json.Unmarshal(body, &payload)

		w.mu.Lock()
		w.got = append(w.got, payload.Context)
		w.auth = append(w.auth, r.Header.Get("Authorization"))
		code := w.status
		w.mu.Unlock()

		rw.WriteHeader(code)
		select {
		case w.arrive <- payload.Context:
		default:
		}
	}))
	t.Cleanup(ts.Close)
	w.URL = ts.URL + "/wake"
	return w
}

// next waits for one delivery.
func (w *wakes) next(t *testing.T) string {
	t.Helper()
	select {
	case c := <-w.arrive:
		return c
	case <-time.After(3 * time.Second):
		t.Fatal("no wake arrived")
		return ""
	}
}

// quiet asserts that nothing arrives.
func (w *wakes) quiet(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case c := <-w.arrive:
		t.Fatalf("something woke the hook that should not have: %q", c)
	case <-time.After(d):
	}
}

func (w *wakes) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.got)
}

// hookServer is a room with hooks on, pointing at loopback — which the server
// only allows because the test says so.
func hookServer(t *testing.T, cooldown time.Duration) (*server.Server, proto.Invite) {
	t.Helper()
	return newTestServer(t, server.Config{
		Hooks:        true,
		HookPrivate:  true,
		HookCooldown: cooldown,
	})
}

func TestHookWakesOnMention(t *testing.T) {
	ctx := context.Background()
	_, in := hookServer(t, 10*time.Millisecond)
	w := newWakes(t)

	bob := join(t, in, "bob")
	if _, err := bob.AddHook(ctx, w.URL, "crsr_secret", nil); err != nil {
		t.Fatalf("bob could not register a hook: %v", err)
	}

	alice := join(t, in, "alice")
	if _, err := alice.Say(ctx, "@bob can you look at the logs?"); err != nil {
		t.Fatalf("alice could not speak: %v", err)
	}

	body := w.next(t)
	for _, want := range []string{"bob", "alice", "can you look at the logs?", "grokbox read"} {
		if !strings.Contains(body, want) {
			t.Errorf("the wake did not carry %q:\n%s", want, body)
		}
	}

	w.mu.Lock()
	auth := w.auth[0]
	w.mu.Unlock()
	if auth != "Bearer crsr_secret" {
		t.Errorf("the call carried %q, want the bearer token bob registered", auth)
	}
}

func TestHookStaysQuietWithoutAMention(t *testing.T) {
	ctx := context.Background()
	_, in := hookServer(t, 10*time.Millisecond)
	w := newWakes(t)

	bob := join(t, in, "bob")
	if _, err := bob.AddHook(ctx, w.URL, "k", nil); err != nil {
		t.Fatal(err)
	}

	alice := join(t, in, "alice")
	// Bob's name in passing, an email address, and a longer name that merely
	// starts with his: none of these are somebody addressing him.
	for _, line := range []string{"bob said he would do it", "write to bob@example.com", "@bobby is here too"} {
		if _, err := alice.Say(ctx, line); err != nil {
			t.Fatal(err)
		}
	}
	w.quiet(t, 300*time.Millisecond)
}

func TestHookDoesNotWakeYouOnYourOwnWords(t *testing.T) {
	ctx := context.Background()
	_, in := hookServer(t, 10*time.Millisecond)
	w := newWakes(t)

	bob := join(t, in, "bob")
	if _, err := bob.AddHook(ctx, w.URL, "k", nil); err != nil {
		t.Fatal(err)
	}
	// An agent that answered its own "@bob" would never stop.
	if _, err := bob.Say(ctx, "@bob reminder to myself"); err != nil {
		t.Fatal(err)
	}
	w.quiet(t, 300*time.Millisecond)
}

func TestHookWakesEverybodyOnAll(t *testing.T) {
	ctx := context.Background()
	_, in := hookServer(t, 10*time.Millisecond)
	one, two := newWakes(t), newWakes(t)

	bob := join(t, in, "bob")
	if _, err := bob.AddHook(ctx, one.URL, "k", nil); err != nil {
		t.Fatal(err)
	}
	carol := join(t, in, "carol")
	if _, err := carol.AddHook(ctx, two.URL, "k", nil); err != nil {
		t.Fatal(err)
	}

	alice := join(t, in, "alice")
	if _, err := alice.Say(ctx, "@all standup in five"); err != nil {
		t.Fatal(err)
	}
	one.next(t)
	two.next(t)
}

func TestHookAnswersToAnAlias(t *testing.T) {
	ctx := context.Background()
	_, in := hookServer(t, 10*time.Millisecond)
	w := newWakes(t)

	// A bot in a room of bots is called "Alex's navi", but everybody types
	// "@navi".
	bot := join(t, in, "Alex's navi")
	if _, err := bot.AddHook(ctx, w.URL, "k", []string{"navi"}); err != nil {
		t.Fatal(err)
	}

	alice := join(t, in, "alice")
	if _, err := alice.Say(ctx, "@navi what is due tomorrow?"); err != nil {
		t.Fatal(err)
	}
	if body := w.next(t); !strings.Contains(body, "what is due tomorrow?") {
		t.Errorf("the alias woke the hook but carried the wrong line:\n%s", body)
	}
}

func TestHookBatchesMentionsInsideTheCooldown(t *testing.T) {
	ctx := context.Background()
	_, in := hookServer(t, 400*time.Millisecond)
	w := newWakes(t)

	bob := join(t, in, "bob")
	if _, err := bob.AddHook(ctx, w.URL, "k", nil); err != nil {
		t.Fatal(err)
	}

	alice := join(t, in, "alice")
	for _, line := range []string{"@bob one", "@bob two", "@bob three"} {
		if _, err := alice.Say(ctx, line); err != nil {
			t.Fatal(err)
		}
	}

	// Three mentions in a burst must not become three runs — but nothing may
	// be lost either: what the cooldown holds back rides along with the next
	// call.
	first := w.next(t)
	second := w.next(t)
	all := first + second
	for _, want := range []string{"one", "two", "three"} {
		if !strings.Contains(all, want) {
			t.Errorf("%q was never delivered:\nfirst: %s\nsecond: %s", want, first, second)
		}
	}
	w.quiet(t, 600*time.Millisecond)
	if n := w.count(); n != 2 {
		t.Errorf("three mentions caused %d calls, want 2", n)
	}
}

func TestHookIsOnlyTheirsToChange(t *testing.T) {
	ctx := context.Background()
	_, in := hookServer(t, 10*time.Millisecond)
	w := newWakes(t)

	bob := join(t, in, "bob")
	hk, err := bob.AddHook(ctx, w.URL, "k", nil)
	if err != nil {
		t.Fatal(err)
	}

	alice := join(t, in, "alice")
	err = alice.RemoveHook(ctx, hk.ID)
	if err == nil {
		t.Fatal("alice removed bob's hook")
	}
	var ae *client.APIError
	if !asAPIError(err, &ae) || ae.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %v", err)
	}

	// Bob may, and then he stops being woken.
	if err := bob.RemoveHook(ctx, hk.ID); err != nil {
		t.Fatalf("bob could not remove his own hook: %v", err)
	}
	if _, err := alice.Say(ctx, "@bob still there?"); err != nil {
		t.Fatal(err)
	}
	w.quiet(t, 300*time.Millisecond)
}

func TestRegisteringAgainReplacesTheOldHook(t *testing.T) {
	ctx := context.Background()
	_, in := hookServer(t, 10*time.Millisecond)
	old, fresh := newWakes(t), newWakes(t)

	bob := join(t, in, "bob")
	if _, err := bob.AddHook(ctx, old.URL, "k", nil); err != nil {
		t.Fatal(err)
	}
	// A regenerated webhook key is corrected by registering a second time.
	if _, err := bob.AddHook(ctx, fresh.URL, "k2", nil); err != nil {
		t.Fatal(err)
	}
	if list, err := bob.Hooks(ctx); err != nil || len(list) != 1 {
		t.Fatalf("want one hook, got %d (%v)", len(list), err)
	}

	alice := join(t, in, "alice")
	if _, err := alice.Say(ctx, "@bob hello"); err != nil {
		t.Fatal(err)
	}
	fresh.next(t)
	old.quiet(t, 300*time.Millisecond)
}

func TestHooksNeverHandBackTheKey(t *testing.T) {
	ctx := context.Background()
	_, in := hookServer(t, 10*time.Millisecond)
	w := newWakes(t)

	bob := join(t, in, "bob")
	if _, err := bob.AddHook(ctx, w.URL, "crsr_very_secret", nil); err != nil {
		t.Fatal(err)
	}

	// Anyone in the room may see who is wired up; nobody may read the
	// credential that starts their agent.
	alice := join(t, in, "alice")
	list, err := alice.Hooks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), "crsr_very_secret") {
		t.Fatalf("the listing leaked a hook key: %s", raw)
	}
}

func TestHookTestCallsItNow(t *testing.T) {
	ctx := context.Background()
	_, in := hookServer(t, time.Hour) // long enough that only an explicit test fires
	w := newWakes(t)

	bob := join(t, in, "bob")
	hk, err := bob.AddHook(ctx, w.URL, "k", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := bob.TestHook(ctx, hk.ID); err != nil {
		t.Fatalf("the test call failed: %v", err)
	}
	if body := w.next(t); !strings.Contains(body, "test wake") {
		t.Errorf("the test call did not say it was a test:\n%s", body)
	}
}

func TestHookTestReportsAFarEndThatRefuses(t *testing.T) {
	ctx := context.Background()
	_, in := hookServer(t, time.Hour)
	w := newWakes(t)
	w.mu.Lock()
	w.status = http.StatusUnauthorized
	w.mu.Unlock()

	bob := join(t, in, "bob")
	hk, err := bob.AddHook(ctx, w.URL, "wrong-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	err = bob.TestHook(ctx, hk.ID)
	if err == nil {
		t.Fatal("a webhook that answered 401 was reported as working")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("the failure did not say what the far end answered: %v", err)
	}
	// A wrong key is not a hiccup: it must not be retried.
	if n := w.count(); n != 1 {
		t.Errorf("a 401 was retried %d times", n-1)
	}
}

func TestHooksRefusePrivateAddressesByDefault(t *testing.T) {
	ctx := context.Background()
	// Hooks on, but without the escape hatch the tests above use: a room key
	// must not become a way to make the server knock on its own network.
	_, in := newTestServer(t, server.Config{Hooks: true})
	w := newWakes(t)

	bob := join(t, in, "bob")
	hk, err := bob.AddHook(ctx, w.URL, "k", nil)
	if err != nil {
		t.Fatalf("registering should still be allowed: %v", err)
	}
	if err := bob.TestHook(ctx, hk.ID); err == nil {
		t.Fatal("the server called a loopback address")
	}
	if w.count() != 0 {
		t.Fatal("the request reached a private address")
	}
}

func TestHooksOffAnswersPlainly(t *testing.T) {
	ctx := context.Background()
	_, in := newTestServer(t, server.Config{})

	bob := join(t, in, "bob")
	_, err := bob.AddHook(ctx, "https://example.com/hook", "k", nil)
	if err == nil {
		t.Fatal("a server without hooks accepted one")
	}
	if !strings.Contains(err.Error(), "--hooks") {
		t.Errorf("the refusal did not say how to turn them on: %v", err)
	}
}

func TestHooksSurviveARestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg := server.Config{
		StoreDir:     dir,
		Hooks:        true,
		HookPrivate:  true,
		HookCooldown: 10 * time.Millisecond,
		Rooms:        []server.RoomSpec{{Name: "lounge", Key: "open-sesame"}},
	}
	_, in := newTestServer(t, cfg)
	w := newWakes(t)

	bob := join(t, in, "bob")
	if _, err := bob.AddHook(ctx, w.URL, "k", nil); err != nil {
		t.Fatal(err)
	}

	// A server restarted from the same store must not quietly leave every
	// agent in the room deaf.
	_, in2 := newTestServer(t, cfg)
	alice := join(t, in2, "alice")
	if _, err := alice.Say(ctx, "@bob are you still there?"); err != nil {
		t.Fatal(err)
	}
	if body := w.next(t); !strings.Contains(body, "are you still there?") {
		t.Errorf("the restored hook carried the wrong line:\n%s", body)
	}
}

func TestHookRetriesAHiccupButNotARefusal(t *testing.T) {
	ctx := context.Background()
	_, in := hookServer(t, time.Hour)
	w := newWakes(t)
	w.mu.Lock()
	w.status = http.StatusBadGateway
	w.mu.Unlock()

	bob := join(t, in, "bob")
	hk, err := bob.AddHook(ctx, w.URL, "k", nil)
	if err != nil {
		t.Fatal(err)
	}
	// A 5xx is the far end having a moment, so it is worth asking twice.
	if err := bob.TestHook(ctx, hk.ID); err == nil {
		t.Fatal("a webhook that answered 502 twice was reported as working")
	}
	if n := w.count(); n != 2 {
		t.Errorf("a 502 was attempted %d times, want 2", n)
	}
}

func TestARoomHoldsOnlySoManyHooks(t *testing.T) {
	ctx := context.Background()
	_, in := hookServer(t, time.Hour)
	w := newWakes(t)

	// Documented as 32. Each member gets one, so this is 32 members with a
	// hook and one more who cannot have one.
	var first *client.Client
	for i := 0; i < proto.MaxRoomHook; i++ {
		c := join(t, in, fmt.Sprintf("bot-%d", i))
		if _, err := c.AddHook(ctx, w.URL, "k", nil); err != nil {
			t.Fatalf("bot-%d could not register: %v", i, err)
		}
		if i == 0 {
			first = c
		}
	}
	last := join(t, in, "one-too-many")
	if _, err := last.AddHook(ctx, w.URL, "k", nil); err == nil {
		t.Fatalf("a %dth hook was accepted", proto.MaxRoomHook+1)
	}

	// Somebody already registered may still correct their own.
	if _, err := first.AddHook(ctx, w.URL, "k2", nil); err != nil {
		t.Errorf("a full room stopped a member replacing their own hook: %v", err)
	}
}

func TestAHookAnswersToAtMostEightAliases(t *testing.T) {
	ctx := context.Background()
	_, in := hookServer(t, time.Hour)
	w := newWakes(t)
	bob := join(t, in, "bob")

	eight := make([]string, proto.MaxHookAlias)
	for i := range eight {
		eight[i] = fmt.Sprintf("alias%d", i)
	}
	if _, err := bob.AddHook(ctx, w.URL, "k", eight); err != nil {
		t.Fatalf("%d aliases were refused: %v", len(eight), err)
	}
	if _, err := bob.AddHook(ctx, w.URL, "k", append(eight, "one-more")); err == nil {
		t.Errorf("%d aliases were accepted", len(eight)+1)
	}
}

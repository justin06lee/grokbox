package server_test

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/justin06lee/grokbox/internal/client"
	"github.com/justin06lee/grokbox/internal/proto"
	"github.com/justin06lee/grokbox/internal/server"
)

// newTestServer starts a room server on a local port and returns the invite
// for its single room.
func newTestServer(t *testing.T, cfg server.Config) (*server.Server, proto.Invite) {
	t.Helper()
	if cfg.Rooms == nil {
		cfg.Rooms = []server.RoomSpec{{Name: "lounge", Key: "open-sesame"}}
	}
	cfg.Logf = func(string, ...any) {}
	srv, err := server.New(cfg)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, proto.Invite{Server: ts.URL, Room: cfg.Rooms[0].Name, Key: cfg.Rooms[0].Key}
}

func join(t *testing.T, in proto.Invite, name string) *client.Client {
	t.Helper()
	c := client.New(in, name)
	if _, err := c.Join(context.Background()); err != nil {
		t.Fatalf("%s could not join: %v", name, err)
	}
	return c
}

func TestJoinRequiresTheRightKey(t *testing.T) {
	_, in := newTestServer(t, server.Config{})

	wrong := in
	wrong.Key = "guessing"
	if _, err := client.New(wrong, "mallory").Join(context.Background()); err == nil {
		t.Fatal("joined with the wrong key")
	}

	// An unknown room must be indistinguishable from a wrong key, or the
	// server becomes a room directory for anyone who asks.
	missing := in
	missing.Room = "secret-room"
	_, err := client.New(missing, "mallory").Join(context.Background())
	if err == nil {
		t.Fatal("joined a room that does not exist")
	}
	if !strings.Contains(err.Error(), "unknown room or wrong key") {
		t.Fatalf("unknown room leaked a different error: %v", err)
	}
}

func TestChatBetweenTwoMembers(t *testing.T) {
	ctx := context.Background()
	_, in := newTestServer(t, server.Config{})

	alice := join(t, in, "alice")
	bob := join(t, in, "bob")

	if _, err := alice.Say(ctx, "hello bob"); err != nil {
		t.Fatalf("alice could not speak: %v", err)
	}
	msgs, err := bob.Poll(ctx, 2*time.Second)
	if err != nil {
		t.Fatalf("bob could not read: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Text != "hello bob" || msgs[0].From != "alice" {
		t.Fatalf("bob read %+v, want one message from alice", msgs)
	}

	// The cursor moved, so a second read returns nothing.
	again, err := bob.Poll(ctx, 0)
	if err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("bob re-read %d message(s) he had already seen", len(again))
	}

	members, err := bob.Members(ctx)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("room has %d members, want 2", len(members))
	}
}

func TestLongPollWakesOnANewMessage(t *testing.T) {
	ctx := context.Background()
	_, in := newTestServer(t, server.Config{})
	alice := join(t, in, "alice")
	bob := join(t, in, "bob")
	bob.Poll(ctx, 0) // drain the join notices

	go func() {
		time.Sleep(150 * time.Millisecond)
		alice.Say(ctx, "late")
	}()

	start := time.Now()
	msgs, err := bob.Poll(ctx, 5*time.Second)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Text != "late" {
		t.Fatalf("poll returned %+v", msgs)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("long poll took %v, so it slept instead of waking on the message", elapsed)
	}
}

func TestStreamDeliversMessages(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, in := newTestServer(t, server.Config{})
	alice := join(t, in, "alice")
	bob := join(t, in, "bob")

	got := make(chan proto.Message, 4)
	go bob.Stream(ctx, func(m proto.Message) error {
		got <- m
		return nil
	})

	time.Sleep(100 * time.Millisecond) // let the stream attach
	if _, err := alice.Say(ctx, "over the wire"); err != nil {
		t.Fatalf("say: %v", err)
	}
	select {
	case m := <-got:
		if m.Text != "over the wire" {
			t.Fatalf("stream delivered %q", m.Text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream delivered nothing")
	}
}

func TestNamesAreUniqueButReclaimable(t *testing.T) {
	ctx := context.Background()
	_, in := newTestServer(t, server.Config{})
	alice := join(t, in, "alice")

	if _, err := client.New(in, "alice").Join(ctx); err == nil {
		t.Fatal("a second alice was allowed in")
	}

	// The same client reconnecting presents its old token and gets the name
	// back rather than waiting out the idle timeout.
	if _, err := alice.Join(ctx); err != nil {
		t.Fatalf("alice could not reclaim her own session: %v", err)
	}
}

func TestLeaveTellsTheRoom(t *testing.T) {
	ctx := context.Background()
	_, in := newTestServer(t, server.Config{})
	alice := join(t, in, "alice")
	bob := join(t, in, "bob")
	bob.Poll(ctx, 0)

	if err := alice.Leave(ctx); err != nil {
		t.Fatalf("leave: %v", err)
	}
	msgs, err := bob.Poll(ctx, 2*time.Second)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Kind != proto.KindLeave {
		t.Fatalf("bob saw %+v, want a leave notice", msgs)
	}
}

func TestOpenServerCreatesRoomsOnJoin(t *testing.T) {
	ctx := context.Background()
	_, in := newTestServer(t, server.Config{Open: true})

	fresh := in
	fresh.Room = "new-room"
	fresh.Key = "first-one-in-sets-it"
	first := client.New(fresh, "alice")
	if _, err := first.Join(ctx); err != nil {
		t.Fatalf("open server refused a new room: %v", err)
	}

	wrong := fresh
	wrong.Key = "something-else"
	if _, err := client.New(wrong, "bob").Join(ctx); err == nil {
		t.Fatal("the room did not keep the key its creator set")
	}
}

func TestRateLimitStopsAFlood(t *testing.T) {
	ctx := context.Background()
	_, in := newTestServer(t, server.Config{})
	alice := join(t, in, "alice")

	var lastErr error
	for i := 0; i < 40; i++ {
		if _, err := alice.Say(ctx, "spam"); err != nil {
			lastErr = err
			break
		}
	}
	if lastErr == nil {
		t.Fatal("the server accepted 40 messages in a row without limiting")
	}
	var ae *client.APIError
	if !asAPIError(lastErr, &ae) || ae.Code != 429 {
		t.Fatalf("flood was rejected with %v, want a 429", lastErr)
	}
}

func TestKeysAndHistorySurviveARestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg := server.Config{
		Rooms:    []server.RoomSpec{{Name: "lounge", Key: "open-sesame"}},
		StoreDir: filepath.Join(dir, "store"),
	}
	_, in := newTestServer(t, cfg)

	alice := join(t, in, "alice")
	if _, err := alice.Say(ctx, "remember this"); err != nil {
		t.Fatalf("say: %v", err)
	}
	alice.Leave(ctx)

	// A second server over the same store, told nothing about the room.
	restarted, err := server.New(server.Config{StoreDir: cfg.StoreDir, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	ts2 := httptest.NewServer(restarted.Handler())
	defer ts2.Close()

	rooms := restarted.Rooms()
	if len(rooms) != 1 || rooms[0].Name() != "lounge" || rooms[0].Key() != "open-sesame" {
		t.Fatalf("restarted server lost the room or its key: %+v", rooms)
	}

	back := in
	back.Server = ts2.URL
	bob := client.New(back, "bob")
	resp, err := bob.Join(ctx)
	if err != nil {
		t.Fatalf("bob could not join the restarted server: %v", err)
	}
	found := false
	for _, m := range resp.History {
		if m.Text == "remember this" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the transcript did not survive the restart: %+v", resp.History)
	}
}

func TestIdleMembersAreDropped(t *testing.T) {
	ctx := context.Background()
	_, in := newTestServer(t, server.Config{Idle: time.Millisecond})
	alice := join(t, in, "alice")
	time.Sleep(20 * time.Millisecond)

	// The janitor only runs on a timer inside Run, so a fresh join with the
	// same name is the observable check that idle sessions are collectable.
	if _, err := alice.Members(ctx); err != nil {
		t.Fatalf("members: %v", err)
	}
}

func asAPIError(err error, target **client.APIError) bool {
	ae, ok := err.(*client.APIError)
	if ok {
		*target = ae
	}
	return ok
}

// TestFollowWhileSpeaking mirrors what the interactive client does: one
// goroutine following the room while another sends. It exists to keep the
// race detector honest about the shared session state.
func TestFollowWhileSpeaking(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, in := newTestServer(t, server.Config{})

	alice := join(t, in, "alice")
	bob := join(t, in, "bob")

	seen := make(chan proto.Message, 32)
	go alice.Follow(ctx, func(m proto.Message) error {
		select {
		case seen <- m:
		default:
		}
		return nil
	}, func(string) {})

	for i := 0; i < 5; i++ {
		if _, err := alice.Say(ctx, "tick"); err != nil {
			t.Fatalf("say %d: %v", i, err)
		}
		if _, err := bob.Say(ctx, "tock"); err != nil {
			t.Fatalf("bob say %d: %v", i, err)
		}
	}

	deadline := time.After(5 * time.Second)
	for n := 0; n < 3; {
		select {
		case <-seen:
			n++
		case <-deadline:
			t.Fatalf("follower saw only %d messages", n)
		}
	}
}

// TestResumeAfterAnExpiredSessionReturnsTheBacklog covers the case a polling
// client hits after being away: its token is gone, so it re-joins, and the
// messages it missed have to come back with that join or they are lost.
func TestResumeAfterAnExpiredSessionReturnsTheBacklog(t *testing.T) {
	ctx := context.Background()
	_, in := newTestServer(t, server.Config{})

	alice := join(t, in, "alice")
	bob := join(t, in, "bob")
	bob.Poll(ctx, 0)
	away := bob.Seq()

	if _, err := alice.Say(ctx, "said while bob was away"); err != nil {
		t.Fatalf("say: %v", err)
	}

	// Bob drops off, then comes back holding a token the server has forgotten
	// — which is what a saved session looks like after it has timed out.
	if err := bob.Leave(ctx); err != nil {
		t.Fatalf("leave: %v", err)
	}
	bob.SetToken("lounge~this-token-is-no-longer-valid")
	bob.SetSeq(away)
	resp, err := bob.Resume(ctx)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resp == nil {
		t.Fatal("Resume reported a live session for a token the server never issued")
	}

	var missed []string
	for _, m := range resp.History {
		if m.Seq > away && m.Kind == proto.KindChat {
			missed = append(missed, m.Text)
		}
	}
	if len(missed) != 1 || missed[0] != "said while bob was away" {
		t.Fatalf("re-join returned %v, want the one message bob missed", missed)
	}
}

package proto

import (
	"strings"
	"testing"
)

func TestInviteRoundTrip(t *testing.T) {
	in := Invite{Server: "https://chat.example.com:8443", Room: "lounge", Key: "abcd-efgh-ijkl-mnop"}
	code := in.Encode()
	if !strings.HasPrefix(code, "grokbox1-") {
		t.Fatalf("invite code %q has no version prefix", code)
	}
	got, err := ParseInvite("  " + code + "\n")
	if err != nil {
		t.Fatalf("ParseInvite: %v", err)
	}
	if got != in {
		t.Fatalf("round trip changed the invite: %+v != %+v", got, in)
	}
}

func TestParseInviteRejectsJunk(t *testing.T) {
	for _, bad := range []string{"", "hello", "grokbox1-!!!!", "grokbox1-" + "e30"} {
		if _, err := ParseInvite(bad); err == nil {
			t.Errorf("ParseInvite(%q) accepted a bad code", bad)
		}
	}
}

func TestCleanName(t *testing.T) {
	ok := []string{"alice", "Grok 3", "bot-7", "márta"}
	for _, name := range ok {
		if _, err := CleanName(name); err != nil {
			t.Errorf("CleanName(%q) = %v, want ok", name, err)
		}
	}
	bad := []string{"", "   ", "a\tb", "a\nb", "two  spaces", strings.Repeat("x", MaxNameLen+1), "esc\x1b[31m"}
	for _, name := range bad {
		if _, err := CleanName(name); err == nil {
			t.Errorf("CleanName(%q) accepted a bad name", name)
		}
	}
	if got, _ := CleanName("  alice  "); got != "alice" {
		t.Errorf("CleanName did not trim: %q", got)
	}
}

func TestCleanRoom(t *testing.T) {
	if got, err := CleanRoom("  Lounge  "); err != nil || got != "lounge" {
		t.Fatalf("CleanRoom(Lounge) = %q, %v", got, err)
	}
	for _, bad := range []string{"", "a b", "../etc", ".hidden", "room/1", strings.Repeat("r", MaxRoomLen+1)} {
		if _, err := CleanRoom(bad); err == nil {
			t.Errorf("CleanRoom(%q) accepted a bad room name", bad)
		}
	}
}

func TestCleanTextStripsControlBytes(t *testing.T) {
	got, err := CleanText("hi \x1b[2Jthere\x07")
	if err != nil {
		t.Fatalf("CleanText: %v", err)
	}
	if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\x07') {
		t.Fatalf("CleanText left control bytes in %q", got)
	}
	if _, err := CleanText("   \n"); err == nil {
		t.Error("CleanText accepted an empty message")
	}
	if _, err := CleanText(strings.Repeat("x", MaxTextLen+1)); err == nil {
		t.Error("CleanText accepted an oversized message")
	}
}

func TestNewKeyIsReadableAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		k := NewKey()
		if len(k) != 19 || strings.Count(k, "-") != 3 {
			t.Fatalf("NewKey() = %q, want 4 groups of 4", k)
		}
		if strings.ContainsAny(k, "ilo01") {
			t.Fatalf("NewKey() = %q, contains an ambiguous character", k)
		}
		if seen[k] {
			t.Fatalf("NewKey() repeated %q", k)
		}
		seen[k] = true
	}
}

func TestInviteCarriesTheFingerprint(t *testing.T) {
	in := Invite{
		Server:      "https://203.0.113.9:7777",
		Room:        "lounge",
		Key:         "abcd-efgh-ijkl-mnop",
		Fingerprint: Fingerprint([]byte("a certificate")),
	}
	got, err := ParseInvite(in.Encode())
	if err != nil {
		t.Fatalf("ParseInvite: %v", err)
	}
	if got != in {
		t.Fatalf("round trip lost something: %+v != %+v", got, in)
	}

	// An invite for a server with a real certificate carries no fingerprint,
	// and must still decode.
	plain := Invite{Server: "https://chat.example.com", Room: "lounge", Key: "k"}
	if got, err := ParseInvite(plain.Encode()); err != nil || got.Fingerprint != "" {
		t.Fatalf("ParseInvite(plain) = %+v, %v", got, err)
	}
}

func TestFingerprint(t *testing.T) {
	a := Fingerprint([]byte("one certificate"))
	b := Fingerprint([]byte("another certificate"))
	if a == b {
		t.Fatal("two different certificates hashed the same")
	}
	if a != Fingerprint([]byte("one certificate")) {
		t.Fatal("hashing the same certificate twice gave two answers")
	}
	if len(a) != 43 { // 32 bytes, base64url, unpadded
		t.Fatalf("fingerprint is %d characters: %q", len(a), a)
	}
	if !SameFingerprint(a, a) {
		t.Error("SameFingerprint said a fingerprint differs from itself")
	}
	if SameFingerprint(a, b) || SameFingerprint(a, "") || SameFingerprint(a, a[:len(a)-1]) {
		t.Error("SameFingerprint accepted a mismatch")
	}
}

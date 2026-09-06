package ui

import (
	"strings"
	"testing"
)

const (
	red   = "\x1b[38;5;203m"
	reset = "\x1b[0m"
)

func TestVisibleLenIgnoresColour(t *testing.T) {
	if got := visibleLen(red + "maya" + reset); got != 4 {
		t.Errorf("visibleLen counted escape bytes: got %d, want 4", got)
	}
	if got := visibleLen("plain"); got != 5 {
		t.Errorf("visibleLen(plain) = %d, want 5", got)
	}
}

func TestTruncateNeverCutsAnEscape(t *testing.T) {
	// Cutting through "\x1b[38;5;203m" would leave the rest of the line, and
	// every line under it, painted whatever colour the terminal made of the
	// fragment.
	got := truncate(red+"abcdef"+reset, 3)
	if visibleLen(got) != 3 {
		t.Errorf("truncate kept %d visible columns, want 3: %q", visibleLen(got), got)
	}
	if !strings.HasPrefix(got, red) {
		t.Errorf("truncate dropped the colour it started with: %q", got)
	}
	if strings.Count(got, "\x1b") != 1 {
		t.Errorf("truncate left a torn escape sequence: %q", got)
	}
}

func TestWrapBreaksOnWords(t *testing.T) {
	got := wrap("the quick brown fox jumps", 12)
	if len(got) < 2 {
		t.Fatalf("nothing wrapped: %q", got)
	}
	for i, l := range got {
		if visibleLen(l) > 12 {
			t.Errorf("line %d is %d columns wide, want <= 12: %q", i, visibleLen(l), l)
		}
	}
	// Words stay whole, and the joined text is what went in.
	flat := strings.Join(got, " ")
	for _, word := range []string{"quick", "brown", "jumps"} {
		if !strings.Contains(flat, word) {
			t.Errorf("wrap split %q apart: %q", word, got)
		}
	}
}

func TestWrapIndentsContinuations(t *testing.T) {
	got := wrap("alpha beta gamma delta epsilon", 12)
	if len(got) < 2 {
		t.Fatal("expected more than one line")
	}
	if strings.HasPrefix(got[0], " ") {
		t.Errorf("the first line was indented: %q", got[0])
	}
	for _, l := range got[1:] {
		if !strings.HasPrefix(l, "  ") {
			t.Errorf("a continuation was not indented, so it reads as a new speaker: %q", l)
		}
	}
}

func TestWrapKeepsColour(t *testing.T) {
	got := wrap(red+"alpha beta gamma delta"+reset, 12)
	joined := strings.Join(got, "")
	if !strings.Contains(joined, red) || !strings.Contains(joined, reset) {
		t.Errorf("wrap lost the colour codes: %q", got)
	}
}

func TestWrapHandlesAnUnbreakableRun(t *testing.T) {
	// A pasted URL has no spaces to break at; it must still be cut to width
	// rather than overflowing into the row below and shifting everything.
	long := strings.Repeat("x", 40)
	got := wrap(long, 12)
	if len(got) < 2 {
		t.Fatalf("an over-long run was not broken: %q", got)
	}
	for i, l := range got {
		if visibleLen(l) > 12 {
			t.Errorf("line %d is %d wide: %q", i, visibleLen(l), l)
		}
	}
	if n := strings.Count(strings.Join(got, ""), "x"); n != 40 {
		t.Errorf("wrap lost characters: kept %d of 40", n)
	}
}

func TestWrapLeavesShortLinesAlone(t *testing.T) {
	in := red + "hi" + reset
	got := wrap(in, 40)
	if len(got) != 1 || got[0] != in {
		t.Errorf("a line that fits was rewritten: %q", got)
	}
}

func TestMentionsMatchesWhatWakesSomebody(t *testing.T) {
	// The bold in the transcript has to mean the same thing as the server's
	// decision to wake a hook, or it puts weight on lines that reached nobody.
	if !mentions("@maya can you confirm", "maya") {
		t.Error("a real mention was not highlighted")
	}
	if mentions("nice. maya, you seeing this?", "maya") {
		t.Error("a bare name was highlighted as though it had reached her")
	}
}

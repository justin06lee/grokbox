// Package ui is the interactive terminal chat: a full-screen window with a
// header pinned to the top, a transcript that fills the middle, and an input
// line pinned to the bottom.
//
// It paints on the alternate screen, the way a pager or an editor does. That
// costs the terminal's own scrollback — so the transcript is kept here and
// scrolled from the keyboard — and buys two things worth more. The window is
// blank before the first line lands, whatever the shell printed on the way in,
// which matters because a room is often opened for somebody to glance at. And
// quitting puts back exactly what was on screen before.
package ui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/justin06lee/grokbox/internal/client"
	"github.com/justin06lee/grokbox/internal/proto"
)

// Options tune the chat window.
type Options struct {
	Color bool // ANSI colour
	Clock bool // show timestamps
	Quiet bool // skip the header
}

type chat struct {
	cl   *client.Client
	out  *os.File
	in   io.Reader
	opts Options
	raw  bool

	mu      sync.Mutex
	buf     []rune
	cur     int
	history [][]rune
	hidx    int

	// lines is the transcript. The alternate screen has no scrollback of its
	// own, so this is the only copy there is.
	lines  []string
	scroll int // rows held above the live bottom; 0 follows new messages
	w, h   int // size at the last paint, to notice a resize
	alt    bool

	sending sync.WaitGroup
	stop    context.CancelFunc
}

// Run drives an interactive session on an already-joined client and returns
// when the user quits or the context is cancelled. backlog is replayed first.
func Run(ctx context.Context, cl *client.Client, backlog []proto.Message, opts Options) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	t := &chat{cl: cl, out: os.Stdout, in: os.Stdin, opts: opts, stop: cancel}
	t.raw = term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	if !t.raw {
		t.opts.Color = false
	}

	if t.raw {
		state, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			t.raw = false
		} else {
			defer term.Restore(int(os.Stdin.Fd()), state)
		}
	}

	if t.raw {
		// Alternate screen, then clear it: nothing the shell printed on the
		// way in is on this canvas.
		fmt.Fprint(t.out, "\x1b[?1049h\x1b[2J\x1b[H")
		// Push the old window title, then say what this window is. A room
		// opened for somebody to glance at should be named in the title bar,
		// not by whatever command line happened to start it.
		fmt.Fprintf(t.out, "\x1b[22;0t\x1b]0;grokbox — %s\x07", cl.Room)
		t.alt = true
		defer t.teardown()
	}

	for _, m := range backlog {
		t.append(t.format(m))
	}
	t.repaint()

	msgs := make(chan proto.Message, 64)
	notes := make(chan string, 16)
	go func() {
		err := cl.Follow(ctx, func(m proto.Message) error {
			select {
			case msgs <- m:
			case <-ctx.Done():
				return client.ErrStop
			}
			return nil
		}, func(note string) {
			select {
			case notes <- note:
			default:
			}
		})
		if err != nil && ctx.Err() == nil {
			notes <- "disconnected: " + err.Error()
		}
	}()

	keys := make(chan key, 32)
	go t.readKeys(keys)

	// There is no portable signal for a resized window, so the size is simply
	// looked at now and then; repainting only happens when it actually moved.
	resize := time.NewTicker(400 * time.Millisecond)
	defer resize.Stop()

	for {
		select {
		case <-ctx.Done():
			t.finish()
			t.teardown()
			fmt.Fprintln(t.out, "left "+t.cl.Room)
			return nil
		case <-resize.C:
			t.mu.Lock()
			w, h := t.size()
			changed := w != t.w || h != t.h
			t.mu.Unlock()
			if changed {
				t.repaint()
			}
		case m := <-msgs:
			t.print(t.format(m))
		case n := <-notes:
			t.print(t.dim("· " + n))
		case k, ok := <-keys:
			if !ok {
				t.finish()
				t.teardown()
				return nil
			}
			if quit := t.onKey(ctx, k); quit {
				t.finish()
				t.teardown()
				fmt.Fprintln(t.out, "left "+t.cl.Room)
				return nil
			}
		}
	}
}

func (t *chat) finish() {
	// A piped run reaches EOF the instant it has fed us every line, so wait
	// for those sends to land before tearing the session down.
	t.sending.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = t.cl.Leave(ctx)
}

// teardown puts the terminal back the way it was found. Safe to call twice.
func (t *chat) teardown() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.alt {
		fmt.Fprint(t.out, "\x1b[23;0t\x1b[?1049l")
		t.alt = false
	}
}

// ------------------------------------------------------------------- input

type key struct {
	r    rune
	code int // one of the k* constants; 0 means "a literal rune"
}

const (
	kEnter = iota + 1
	kBackspace
	kLeft
	kRight
	kUp
	kDown
	kHome
	kEnd
	kKillLine
	kKillWord
	kClear
	kInterrupt
	kEOF
	kPageUp
	kPageDown
	kRecallBack
	kRecallFwd
)

// readKeys decodes stdin into key events. In raw mode it reads byte by byte;
// otherwise it reads whole lines, which is what happens when input is piped.
func (t *chat) readKeys(out chan<- key) {
	defer close(out)
	br := bufio.NewReader(t.in)

	if !t.raw {
		for {
			line, err := br.ReadString('\n')
			line = strings.TrimRight(line, "\r\n")
			if line != "" {
				for _, r := range line {
					out <- key{r: r}
				}
				out <- key{code: kEnter}
			}
			if err != nil {
				return
			}
		}
	}

	for {
		r, _, err := br.ReadRune()
		if err != nil {
			return
		}
		switch r {
		case '\r', '\n':
			out <- key{code: kEnter}
		case 0x7f, 0x08:
			out <- key{code: kBackspace}
		case 0x03:
			out <- key{code: kInterrupt}
		case 0x04:
			out <- key{code: kEOF}
		case 0x01:
			out <- key{code: kHome}
		case 0x05:
			out <- key{code: kEnd}
		case 0x02:
			out <- key{code: kLeft}
		case 0x06:
			out <- key{code: kRight}
		case 0x10:
			out <- key{code: kRecallBack}
		case 0x0e:
			out <- key{code: kRecallFwd}
		case 0x0b: // ^K — treat as kill-line, like ^U at the start
			out <- key{code: kKillLine}
		case 0x0c:
			out <- key{code: kClear}
		case 0x15:
			out <- key{code: kKillLine}
		case 0x17:
			out <- key{code: kKillWord}
		case 0x1b:
			// An escape sequence arrives in one burst; a lone ESC does not.
			if br.Buffered() == 0 {
				continue
			}
			b1, err := br.ReadByte()
			if err != nil {
				return
			}
			if b1 != '[' && b1 != 'O' {
				continue
			}
			// Read to the final byte, keeping the parameters: page keys are
			// "\x1b[5~" and "\x1b[6~", which a one-byte lookahead would miss.
			var params []byte
			var final byte
			for {
				c, err := br.ReadByte()
				if err != nil {
					return
				}
				if c >= '@' && c <= '~' {
					final = c
					break
				}
				params = append(params, c)
				if len(params) > 16 {
					break
				}
			}
			switch final {
			case 'A':
				out <- key{code: kUp}
			case 'B':
				out <- key{code: kDown}
			case 'C':
				out <- key{code: kRight}
			case 'D':
				out <- key{code: kLeft}
			case 'H':
				out <- key{code: kHome}
			case 'F':
				out <- key{code: kEnd}
			case '~':
				switch string(params) {
				case "1", "7":
					out <- key{code: kHome}
				case "4", "8":
					out <- key{code: kEnd}
				case "5":
					out <- key{code: kPageUp}
				case "6":
					out <- key{code: kPageDown}
				}
			}
		default:
			if r >= 0x20 {
				out <- key{r: r}
			}
		}
	}
}

// onKey applies one key event and reports whether the session should end.
func (t *chat) onKey(ctx context.Context, k key) bool {
	t.mu.Lock()

	switch k.code {
	case 0:
		t.buf = append(t.buf, 0)
		copy(t.buf[t.cur+1:], t.buf[t.cur:])
		t.buf[t.cur] = k.r
		t.cur++
	case kBackspace:
		if t.cur > 0 {
			t.buf = append(t.buf[:t.cur-1], t.buf[t.cur:]...)
			t.cur--
		}
	case kLeft:
		if t.cur > 0 {
			t.cur--
		}
	case kRight:
		if t.cur < len(t.buf) {
			t.cur++
		}
	case kHome:
		t.cur = 0
	case kEnd:
		t.cur = len(t.buf)
	case kKillLine:
		t.buf, t.cur = nil, 0
	case kKillWord:
		i := t.cur
		for i > 0 && t.buf[i-1] == ' ' {
			i--
		}
		for i > 0 && t.buf[i-1] != ' ' {
			i--
		}
		t.buf = append(t.buf[:i], t.buf[t.cur:]...)
		t.cur = i
	case kUp, kDown:
		if len(t.buf) == 0 {
			// Nothing half-typed: you are reading, so move the transcript.
			// Terminals send these when the wheel turns on the alternate
			// screen, and scrolling is what that should do.
			if k.code == kUp {
				t.scroll++
			} else if t.scroll > 0 {
				t.scroll--
			}
			break
		}
		t.recall(k.code == kUp)
	case kRecallBack, kRecallFwd:
		t.recall(k.code == kRecallBack)
	case kClear:
		fmt.Fprint(t.out, "\x1b[2J")
	case kPageUp, kPageDown:
		_, h := t.size()
		step := h - 4
		if step < 1 {
			step = 1
		}
		if k.code == kPageDown {
			step = -step
		}
		t.scroll += step
		if t.scroll < 0 {
			t.scroll = 0
		}
	case kInterrupt:
		t.mu.Unlock()
		return true
	case kEOF:
		if len(t.buf) == 0 {
			t.mu.Unlock()
			return true
		}
	case kEnter:
		t.scroll = 0 // saying something puts you back at the bottom
		line := strings.TrimSpace(string(t.buf))
		if len(t.buf) > 0 {
			t.history = append(t.history, append([]rune(nil), t.buf...))
		}
		t.hidx = len(t.history)
		t.buf, t.cur = nil, 0
		t.repaintLocked()
		t.mu.Unlock()
		if line == "" {
			return false
		}
		return t.submit(ctx, line)
	}

	t.repaintLocked()
	t.mu.Unlock()
	return false
}

func (t *chat) recall(back bool) {
	if len(t.history) == 0 {
		return
	}
	if back {
		if t.hidx > 0 {
			t.hidx--
		}
	} else {
		if t.hidx < len(t.history) {
			t.hidx++
		}
	}
	if t.hidx >= len(t.history) {
		t.buf, t.cur = nil, 0
		return
	}
	t.buf = append([]rune(nil), t.history[t.hidx]...)
	t.cur = len(t.buf)
}

// submit handles one entered line and reports whether to quit.
func (t *chat) submit(ctx context.Context, line string) bool {
	if !strings.HasPrefix(line, "/") {
		t.send(ctx, line, false)
		return false
	}

	cmd, rest, _ := strings.Cut(strings.TrimPrefix(line, "/"), " ")
	rest = strings.TrimSpace(rest)
	switch strings.ToLower(cmd) {
	case "quit", "exit", "q", "part":
		return true
	case "me":
		if rest == "" {
			t.print(t.dim("usage: /me <what you are doing>"))
			return false
		}
		t.send(ctx, rest, true)
	case "who", "members", "names":
		t.sending.Add(1)
		go func() {
			defer t.sending.Done()
			t.showMembers(ctx)
		}()
	case "invite":
		t.print(t.bold("invite  ") + t.cl.Invite().Encode())
		t.print(t.dim("anyone with that code and grokbox can join this room"))
	case "name", "whoami":
		t.print(t.dim("you are " + t.cl.Name + " in " + t.cl.Room))
	case "clear":
		t.mu.Lock()
		t.lines, t.scroll = nil, 0
		t.repaintLocked()
		t.mu.Unlock()
	case "help", "?":
		for _, l := range []string{
			"/me <text>    say something in the third person",
			"/who          list who is in the room",
			"/invite       print the invite code to share",
			"/clear        empty the transcript",
			"/quit         leave the room",
			"",
			"page up / page down   move through what has been said",
			"↑ / ↓                 the same, while the input line is empty",
			"                      — once you are typing they recall what you sent",
			"^p / ^n               recall, whatever the arrows are doing",
			"",
			"a line that does not start with / is sent to the room.",
			"say @someone to reach them: a bot is only woken by its name.",
		} {
			t.print(t.dim(l))
		}
	default:
		t.print(t.dim("no such command: /" + cmd + " — try /help"))
	}
	return false
}

func (t *chat) send(ctx context.Context, text string, action bool) {
	post := func() {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		var err error
		if action {
			_, err = t.cl.Act(ctx, text)
		} else {
			_, err = t.cl.Say(ctx, text)
		}
		if err != nil && ctx.Err() == nil {
			t.print(t.warn("not sent: " + err.Error()))
		}
	}
	if !t.raw {
		post() // piped input: keep the lines in the order they were written
		return
	}
	t.sending.Add(1)
	go func() {
		defer t.sending.Done()
		post()
	}()
}

func (t *chat) showMembers(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	members, err := t.cl.Members(ctx)
	if err != nil {
		t.print(t.warn("cannot list members: " + err.Error()))
		return
	}
	names := make([]string, 0, len(members))
	for _, m := range members {
		if m.Name == t.cl.Name {
			names = append(names, m.Name+" (you)")
			continue
		}
		names = append(names, m.Name)
	}
	sort.Strings(names)
	t.print(t.dim(fmt.Sprintf("in %s: %s", t.cl.Room, strings.Join(names, ", "))))
}

// ------------------------------------------------------------------ output

// append adds a line to the transcript. A reader who has scrolled up is left
// where they are rather than being yanked to the bottom by somebody else
// typing.
func (t *chat) append(s string) {
	t.mu.Lock()
	t.lines = append(t.lines, s)
	if n := len(t.lines); n > maxScrollback {
		t.lines = append(t.lines[:0], t.lines[n-maxScrollback:]...)
	}
	if t.scroll > 0 {
		t.scroll++ // hold position: the new line pushes the view up by one
	}
	t.mu.Unlock()
}

// print adds a line and shows it.
func (t *chat) print(s string) {
	if !t.raw {
		t.mu.Lock()
		fmt.Fprintln(t.out, s)
		t.mu.Unlock()
		return
	}
	t.append(s)
	t.repaint()
}

// maxScrollback bounds what is kept. A room replays at most a few hundred
// messages, and past this nobody is scrolling back by hand anyway.
const maxScrollback = 2000

func (t *chat) size() (w, h int) {
	w, h, err := term.GetSize(int(t.out.Fd()))
	if err != nil || w < 20 || h < 4 {
		return 80, 24
	}
	return w, h
}

func (t *chat) repaint() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.repaintLocked()
}

// repaintLocked draws the whole window in one write, so nothing is ever seen
// half-drawn.
func (t *chat) repaintLocked() {
	if !t.raw {
		return
	}
	w, h := t.size()
	t.w, t.h = w, h

	head := t.headerLines(w)
	body := h - len(head) - 1 // one row for the input line
	if body < 1 {
		body = 1
	}

	// Wrap everything to the current width, so a resize reflows rather than
	// leaving the transcript cut off at the old one.
	rows := make([]string, 0, body+16)
	for _, l := range t.lines {
		rows = append(rows, wrap(l, w)...)
	}

	// Clamp the scroll: it is measured in rows above the live bottom, and the
	// window may have grown since it was set.
	maxScroll := len(rows) - body
	if maxScroll < 0 {
		maxScroll = 0
	}
	if t.scroll > maxScroll {
		t.scroll = maxScroll
	}
	end := len(rows) - t.scroll
	start := end - body
	if start < 0 {
		start = 0
	}
	view := rows[start:end]

	var b strings.Builder
	b.WriteString("\x1b[H")
	for _, l := range head {
		b.WriteString("\x1b[2K" + l + "\r\n")
	}
	// The transcript sits on the bottom of its region, the way every chat
	// does: the newest line is the one just above where you type.
	for i := 0; i < body-len(view); i++ {
		b.WriteString("\x1b[2K\r\n")
	}
	for _, l := range view {
		b.WriteString("\x1b[2K" + l + "\r\n")
	}

	prompt, col := t.inputLocked(w)
	b.WriteString("\x1b[2K" + prompt)
	b.WriteString(fmt.Sprintf("\x1b[%d;%dH", h, col))
	fmt.Fprint(t.out, b.String())
}

// headerLines is the chrome pinned to the top: who and where, and how far up
// the transcript is being held.
func (t *chat) headerLines(w int) []string {
	if t.opts.Quiet {
		return nil
	}
	inv := t.cl.Invite()
	first := t.bold("grokbox") + t.dim("  "+inv.Room+"  ·  "+inv.Server)
	second := t.dim("you are " + t.cl.Name + "  ·  /help for commands  ·  ctrl-c to leave")

	third := ""
	if t.scroll > 0 {
		note := fmt.Sprintf("↑ %d more below — page down to catch up", t.scroll)
		if pad := w - visibleLen(note); pad > 0 {
			note = strings.Repeat(" ", pad) + note
		}
		third = t.dim(note)
	}
	return []string{truncate(first, w), truncate(second, w), third}
}

// inputLocked renders the input line and says which column the cursor belongs
// in. The buffer scrolls sideways so a long line never wraps into the
// transcript.
func (t *chat) inputLocked(w int) (string, int) {
	prompt := "› "
	avail := w - len([]rune(prompt)) - 1
	if avail < 8 {
		avail = 8
	}
	start := 0
	if t.cur > avail {
		start = t.cur - avail
	}
	end := start + avail
	if end > len(t.buf) {
		end = len(t.buf)
	}
	return t.dim(prompt) + string(t.buf[start:end]), len([]rune(prompt)) + (t.cur - start) + 1
}

// scrollBy moves the view, in rows. Positive is backwards through the
// transcript.
func (t *chat) scrollBy(rows int) {
	t.mu.Lock()
	t.scroll += rows
	if t.scroll < 0 {
		t.scroll = 0
	}
	t.mu.Unlock()
	t.repaint()
}

// ------------------------------------------------------------- line fitting

// tok is one printable rune, or one escape sequence that takes no space.
type tok struct {
	s string
	w int
}

// tokens splits a coloured string so it can be measured and cut without ever
// slicing through an escape sequence.
func tokens(s string) []tok {
	out := make([]tok, 0, len(s))
	rs := []rune(s)
	for i := 0; i < len(rs); {
		if rs[i] == 0x1b {
			j := i + 1
			if j < len(rs) && (rs[j] == '[' || rs[j] == ']') {
				j++
				for j < len(rs) && !(rs[j] >= '@' && rs[j] <= '~') {
					j++
				}
				if j < len(rs) {
					j++
				}
			}
			out = append(out, tok{s: string(rs[i:j])})
			i = j
			continue
		}
		out = append(out, tok{s: string(rs[i]), w: 1})
		i++
	}
	return out
}

func visibleLen(s string) int {
	n := 0
	for _, t := range tokens(s) {
		n += t.w
	}
	return n
}

// truncate cuts a coloured string to a visible width.
func truncate(s string, w int) string {
	if visibleLen(s) <= w {
		return s
	}
	var b strings.Builder
	n := 0
	for _, t := range tokens(s) {
		if n+t.w > w {
			break
		}
		b.WriteString(t.s)
		n += t.w
	}
	return b.String()
}

// wrap breaks a coloured line to the window width, at a space where there is
// one. Continuations are indented, so a wrapped message reads as one thing
// rather than as a new speaker.
func wrap(s string, w int) []string {
	if w < 12 {
		w = 12
	}
	toks := tokens(s)
	if visibleLen(s) <= w {
		return []string{s}
	}

	const indent = "  "
	var out []string
	var cur []tok
	width, lastSpace := 0, -1
	limit := w

	flush := func(upto int) {
		var b strings.Builder
		for _, t := range cur[:upto] {
			b.WriteString(t.s)
		}
		line := strings.TrimRight(b.String(), " ")
		if len(out) > 0 {
			line = indent + line
		}
		out = append(out, line)

		rest := cur[upto:]
		for len(rest) > 0 && rest[0].s == " " {
			rest = rest[1:]
		}
		cur = append([]tok(nil), rest...)
		width, lastSpace = 0, -1
		for i, t := range cur {
			width += t.w
			if t.s == " " {
				lastSpace = i
			}
		}
		limit = w - len(indent)
	}

	for _, t := range toks {
		if width+t.w > limit && len(cur) > 0 {
			at := len(cur)
			if lastSpace > 0 {
				at = lastSpace
			}
			flush(at)
		}
		cur = append(cur, t)
		width += t.w
		if t.s == " " {
			lastSpace = len(cur) - 1
		}
	}
	if len(cur) > 0 {
		var b strings.Builder
		for _, t := range cur {
			b.WriteString(t.s)
		}
		line := b.String()
		if len(out) > 0 {
			line = indent + line
		}
		out = append(out, line)
	}
	return out
}

// format renders one message for the transcript.
func (t *chat) format(m proto.Message) string {
	var b strings.Builder
	if t.opts.Clock {
		b.WriteString(t.dim(m.Time.Local().Format("15:04 ")))
	}
	mine := m.From == t.cl.Name

	switch m.Kind {
	case proto.KindJoin:
		return b.String() + t.dim("→ "+m.Text)
	case proto.KindLeave:
		return b.String() + t.dim("← "+m.Text)
	case proto.KindSystem:
		return b.String() + t.dim("· "+m.Text)
	case proto.KindAction:
		return b.String() + t.color(m.From, "* "+m.From+" "+m.Text)
	}

	name := t.color(m.From, m.From)
	if mine {
		name = t.dim(m.From)
	}
	text := m.Text
	if !mine && mentions(text, t.cl.Name) {
		text = t.bold(text)
	}
	b.WriteString(name + t.dim(" › ") + text)
	return b.String()
}

// mentions is the same test the server uses to decide whether to wake
// somebody. Bolding anything that merely contained your name would put weight
// on lines that reached nobody, which is exactly the confusion to avoid now
// that an "@" is what starts somebody's agent.
func mentions(text, name string) bool {
	return name != "" && proto.Mentions(text, name, nil)
}

// ------------------------------------------------------------------- colour

// palette holds 256-colour codes that stay legible on both dark and light
// terminals, spread across hues so two names in the same room rarely land on
// the same colour family.
var palette = []int{39, 45, 78, 81, 111, 141, 147, 150, 170, 173, 179, 203, 208, 210, 213, 220}

func (t *chat) color(seed, s string) string {
	if !t.opts.Color {
		return s
	}
	var h uint32 = 2166136261
	for _, r := range seed {
		h ^= uint32(r)
		h *= 16777619
	}
	return fmt.Sprintf("\x1b[38;5;%dm%s\x1b[0m", palette[int(h)%len(palette)], s)
}

func (t *chat) dim(s string) string {
	if !t.opts.Color || s == "" {
		return s
	}
	return "\x1b[2m" + s + "\x1b[0m"
}

func (t *chat) bold(s string) string {
	if !t.opts.Color {
		return s
	}
	return "\x1b[1m" + s + "\x1b[0m"
}

func (t *chat) warn(s string) string {
	if !t.opts.Color {
		return s
	}
	return "\x1b[33m" + s + "\x1b[0m"
}

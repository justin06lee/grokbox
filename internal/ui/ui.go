// Package ui is the interactive terminal chat: a scrolling transcript with an
// input line pinned to the bottom that survives incoming messages.
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

	if !opts.Quiet {
		t.header()
	}
	for _, m := range backlog {
		t.print(t.format(m))
	}
	t.redrawLocked0()

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

	for {
		select {
		case <-ctx.Done():
			t.print(t.dim("— left the room —"))
			t.finish()
			return nil
		case m := <-msgs:
			t.print(t.format(m))
		case n := <-notes:
			t.print(t.dim("· " + n))
		case k, ok := <-keys:
			if !ok {
				t.finish()
				return nil
			}
			if quit := t.onKey(ctx, k); quit {
				t.finish()
				return nil
			}
		}
	}
}

func (t *chat) finish() {
	// A piped run reaches EOF the instant it has fed us every line, so wait
	// for those sends to land before tearing the session down.
	t.sending.Wait()

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.raw {
		fmt.Fprint(t.out, "\r\x1b[2K")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = t.cl.Leave(ctx)
}

func (t *chat) header() {
	inv := t.cl.Invite()
	t.print(t.bold("grokbox") + t.dim("  "+inv.Room+"  ·  "+inv.Server))
	t.print(t.dim("you are " + t.cl.Name + "  ·  /help for commands  ·  ctrl-c to leave"))
	t.print("")
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
			b2, err := br.ReadByte()
			if err != nil {
				return
			}
			switch b2 {
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
			default:
				// Consume the tail of a longer sequence (e.g. "\x1b[3~").
				for br.Buffered() > 0 {
					c, err := br.ReadByte()
					if err != nil || (c >= '@' && c <= '~') {
						break
					}
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
		t.recall(k.code == kUp)
	case kClear:
		fmt.Fprint(t.out, "\x1b[2J\x1b[H")
	case kInterrupt:
		t.mu.Unlock()
		return true
	case kEOF:
		if len(t.buf) == 0 {
			t.mu.Unlock()
			return true
		}
	case kEnter:
		line := strings.TrimSpace(string(t.buf))
		if len(t.buf) > 0 {
			t.history = append(t.history, append([]rune(nil), t.buf...))
		}
		t.hidx = len(t.history)
		t.buf, t.cur = nil, 0
		t.redrawLocked()
		t.mu.Unlock()
		if line == "" {
			return false
		}
		return t.submit(ctx, line)
	}

	t.redrawLocked()
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
		fmt.Fprint(t.out, "\x1b[2J\x1b[H")
		t.redrawLocked()
		t.mu.Unlock()
	case "help", "?":
		for _, l := range []string{
			"/me <text>    say something in the third person",
			"/who          list who is in the room",
			"/invite       print the invite code to share",
			"/clear        clear the screen",
			"/quit         leave the room",
			"",
			"a line that does not start with / is sent to the room.",
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

// print writes one transcript line above the input line.
func (t *chat) print(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.raw {
		fmt.Fprint(t.out, "\r\x1b[2K"+s+"\r\n")
		t.redrawLocked()
		return
	}
	fmt.Fprintln(t.out, s)
}

func (t *chat) redrawLocked0() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.redrawLocked()
}

// redrawLocked repaints the input line. The buffer scrolls horizontally so a
// long line never wraps and never disturbs the transcript above it.
func (t *chat) redrawLocked() {
	if !t.raw {
		return
	}
	width, _, err := term.GetSize(int(t.out.Fd()))
	if err != nil || width < 20 {
		width = 80
	}
	prompt := "› "
	avail := width - len([]rune(prompt)) - 1
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
	view := string(t.buf[start:end])

	fmt.Fprint(t.out, "\r\x1b[2K"+t.dim(prompt)+view)
	if back := (end - start) - (t.cur - start); back > 0 {
		fmt.Fprintf(t.out, "\x1b[%dD", back)
	}
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

func mentions(text, name string) bool {
	return name != "" && strings.Contains(strings.ToLower(text), strings.ToLower(name))
}

// ------------------------------------------------------------------- colour

// palette holds 256-colour codes that stay legible on both dark and light
// terminals.
var palette = []int{39, 42, 45, 75, 78, 111, 114, 141, 147, 150, 173, 176, 179, 208, 210, 213}

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

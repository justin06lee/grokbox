// Package client talks to a grokbox server: join a room with a key, send
// lines, and follow everything anyone else says.
//
// When the invite names a certificate, the client pins it and will talk to
// nothing else — which is what lets a room on a bare IP be reached privately,
// with no certificate authority in the picture.
package client

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/justin06lee/grokbox/internal/proto"
)

// UserAgent identifies the client to the server; the CLI stamps its version.
var UserAgent = "grokbox/dev"

// APIError is a non-2xx response from the server.
type APIError struct {
	Code int
	Msg  string
}

func (e *APIError) Error() string { return e.Msg }

// Expired reports whether the server rejected our session token.
func (e *APIError) Expired() bool { return e.Code == http.StatusUnauthorized }

// Client is one membership of one room. A reader goroutine (Stream, Follow)
// and a writer goroutine (Say, Poll) may share one client; the session token
// and cursor are guarded. Name is set before the first join and not written
// afterwards.
type Client struct {
	Server      string // base URL
	Room        string
	Key         string
	Fingerprint string // pinned server certificate, when self-signed
	Name        string

	HTTP *http.Client

	mu    sync.Mutex
	token string
	seq   int64
}

// New builds a client for an invite.
func New(in proto.Invite, name string) *Client {
	return &Client{
		Server:      strings.TrimRight(in.Server, "/"),
		Room:        in.Room,
		Key:         in.Key,
		Fingerprint: in.Fingerprint,
		Name:        name,
		// No client-wide timeout: /v1/stream is meant to stay open. Every
		// request that should time out carries its own context deadline.
		HTTP: &http.Client{Transport: transportFor(in.Fingerprint)},
	}
}

// transportFor pins the server's certificate when the invite named one.
//
// Skipping the usual verification and checking a hash instead is not a
// weakening here, it is the opposite: the fingerprint arrived with the invite,
// out of band, before any connection was made. There is no first contact to be
// impersonated on, and no authority to be mis-issued by.
func transportFor(fingerprint string) http.RoundTripper {
	if fingerprint == "" {
		return http.DefaultTransport
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // replaced by the check below, not dropped
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("the server presented no certificate")
			}
			if !proto.SameFingerprint(proto.Fingerprint(rawCerts[0]), fingerprint) {
				return errors.New("the server's certificate does not match the invite — either the invite is stale, or this is not the server it names")
			}
			return nil
		},
	}
	return t
}

// Invite renders the invite this client was built from, so a member can pass
// it on to somebody else.
func (c *Client) Invite() proto.Invite {
	return proto.Invite{
		Server:      c.Server,
		Room:        c.Room,
		Key:         c.Key,
		Fingerprint: c.Fingerprint,
	}
}

// Seq is the sequence number of the last message this client has seen.
func (c *Client) Seq() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seq
}

// SetSeq rewinds or fast-forwards the cursor.
func (c *Client) SetSeq(n int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq = n
}

// advance moves the cursor forward only, and reports whether it moved.
func (c *Client) advance(n int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n <= c.seq {
		return false
	}
	c.seq = n
	return true
}

// Joined reports whether we currently hold a session token.
func (c *Client) Joined() bool { return c.Token() != "" }

// Token exposes the session token so a one-shot command can save it and reuse
// the same membership on its next run instead of re-joining every time.
func (c *Client) Token() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

// SetToken restores a session token saved by an earlier run.
func (c *Client) SetToken(tok string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = tok
}

// Resume makes sure there is a usable session, reusing a restored token when
// the server still honours it and joining afresh when it does not. It returns
// the join response when it had to re-join — the caller owns the backlog that
// comes with it — and nil when the old session was still good.
func (c *Client) Resume(ctx context.Context) (*proto.JoinResponse, error) {
	if c.Joined() {
		if _, err := c.Members(ctx); err == nil {
			return nil, nil
		}
	}
	return c.Join(ctx)
}

// Join authenticates with the room key and claims the display name. The
// returned backlog is whatever the server still remembers.
func (c *Client) Join(ctx context.Context) (*proto.JoinResponse, error) {
	var out proto.JoinResponse
	err := c.do(ctx, http.MethodPost, "/v1/join", proto.JoinRequest{
		Room:    c.Room,
		Key:     c.Key,
		Name:    c.Name,
		Token:   c.Token(), // reclaim a session we were cut off from
		Version: proto.Version,
	}, &out, false)
	if err != nil {
		return nil, err
	}
	seq := out.Seq
	if n := len(out.History); n > 0 {
		seq = out.History[n-1].Seq
	}
	c.mu.Lock()
	c.token, c.seq = out.Token, seq
	c.mu.Unlock()
	return &out, nil
}

// Say posts a chat line.
func (c *Client) Say(ctx context.Context, text string) (int64, error) {
	return c.say(ctx, proto.KindChat, text)
}

// Act posts a third-person line ("/me waves").
func (c *Client) Act(ctx context.Context, text string) (int64, error) {
	return c.say(ctx, proto.KindAction, text)
}

func (c *Client) say(ctx context.Context, kind, text string) (int64, error) {
	text, err := proto.CleanText(text)
	if err != nil {
		return 0, err
	}
	var out proto.SendResponse
	if err := c.do(ctx, http.MethodPost, "/v1/send", proto.SendRequest{Text: text, Kind: kind}, &out, true); err != nil {
		return 0, err
	}
	return out.Seq, nil
}

// Poll returns everything said since the client's cursor. A non-zero wait
// makes the server hold the request open until something is said, which is
// how a polling reader stays responsive without hammering the server.
func (c *Client) Poll(ctx context.Context, wait time.Duration) ([]proto.Message, error) {
	path := fmt.Sprintf("/v1/messages?since=%d&wait=%d", c.Seq(), int(wait.Seconds()))
	var out proto.MessagesResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &out, true); err != nil {
		return nil, err
	}
	// A room whose latest sequence number sits behind our cursor has been
	// restarted without its transcript, so the cursor points at nothing that
	// will ever exist. Rewind rather than going silent forever.
	if out.Seq < c.Seq() {
		c.SetSeq(0)
	}
	for _, m := range out.Messages {
		c.advance(m.Seq)
	}
	return out.Messages, nil
}

// Members lists who is in the room right now.
func (c *Client) Members(ctx context.Context) ([]proto.Member, error) {
	var out struct {
		Members []proto.Member `json:"members"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/members", nil, &out, true); err != nil {
		return nil, err
	}
	return out.Members, nil
}

// Leave ends the session politely so the room sees a leave line immediately
// instead of waiting for the idle timeout.
func (c *Client) Leave(ctx context.Context) error {
	if !c.Joined() {
		return nil
	}
	err := c.do(ctx, http.MethodPost, "/v1/leave", struct{}{}, nil, true)
	c.SetToken("")
	return err
}

// Health pings the server without joining anything.
func (c *Client) Health(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	if err := c.do(ctx, http.MethodGet, "/v1/health", nil, &out, false); err != nil {
		return nil, err
	}
	return out, nil
}

// Stream opens a server-sent-events connection and calls fn for every message
// until the context ends or the connection drops.
func (c *Client) Stream(ctx context.Context, fn func(proto.Message) error) error {
	req, err := c.request(ctx, http.MethodGet, fmt.Sprintf("/v1/stream?since=%d", c.Seq()), nil, true)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return apiError(resp)
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var data strings.Builder
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, ":"): // comment / keepalive
			continue
		case strings.HasPrefix(line, "data:"):
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		case line == "":
			payload := data.String()
			data.Reset()
			if payload == "" {
				continue
			}
			var m proto.Message
			if err := json.Unmarshal([]byte(payload), &m); err != nil {
				continue
			}
			if !c.advance(m.Seq) {
				continue
			}
			if err := fn(m); err != nil {
				return err
			}
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return io.EOF // the server closed the stream
}

// Follow keeps a stream open forever: it reconnects with backoff when the
// connection drops and re-joins if the session expires. It returns only when
// the context ends or fn asks it to stop.
func (c *Client) Follow(ctx context.Context, fn func(proto.Message) error, onNotice func(string)) error {
	backoff := time.Second
	for {
		if !c.Joined() {
			if _, err := c.Join(ctx); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				var ae *APIError
				if errors.As(err, &ae) && ae.Code == http.StatusConflict {
					// Our old session is still holding the name; it will time
					// out shortly, so wait rather than mangling the name.
					if onNotice != nil {
						onNotice("waiting for the room to release " + c.Name + "…")
					}
				} else if onNotice != nil {
					onNotice("reconnecting: " + err.Error())
				}
				if !sleep(ctx, backoff) {
					return ctx.Err()
				}
				backoff = nextBackoff(backoff)
				continue
			}
			if onNotice != nil {
				onNotice("connected to " + c.Room)
			}
			backoff = time.Second
		}

		err := c.Stream(ctx, fn)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil && !errors.Is(err, io.EOF) {
			var ae *APIError
			if errors.As(err, &ae) && ae.Expired() {
				c.SetToken("") // re-join on the next pass
			} else if !isStop(err) {
				if onNotice != nil {
					onNotice("connection lost: " + err.Error())
				}
			} else {
				return err // fn asked to stop
			}
		}
		if !sleep(ctx, backoff) {
			return ctx.Err()
		}
		backoff = nextBackoff(backoff)
	}
}

// ErrStop can be returned by a Follow callback to end the loop.
var ErrStop = errors.New("stop following")

func isStop(err error) bool { return errors.Is(err, ErrStop) }

func nextBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > 15*time.Second {
		d = 15 * time.Second
	}
	return d
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// ------------------------------------------------------------------ plumbing

func (c *Client) request(ctx context.Context, method, path string, body any, auth bool) (*http.Request, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Server+path, rdr)
	if err != nil {
		return nil, fmt.Errorf("bad server address %q: %w", c.Server, err)
	}
	req.Header.Set("User-Agent", UserAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		tok := c.Token()
		if tok == "" {
			return nil, errors.New("not joined: call join first")
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return req, nil
}

func (c *Client) do(ctx context.Context, method, path string, body, out any, auth bool) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	req, err := c.request(ctx, method, path, body, auth)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return friendly(c.Server, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return apiError(resp)
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func apiError(resp *http.Response) error {
	var e proto.Error
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	_ = json.Unmarshal(body, &e)
	if e.Error == "" {
		e.Error = strings.TrimSpace(string(body))
	}
	if e.Error == "" {
		e.Error = resp.Status
	}
	return &APIError{Code: resp.StatusCode, Msg: e.Error}
}

// friendly turns a raw dial failure into something a person can act on.
func friendly(server string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return fmt.Errorf("nothing is listening at %s — is the server running?", server)
	case strings.Contains(msg, "no such host"):
		return fmt.Errorf("cannot resolve the host in %s", server)
	case strings.Contains(msg, "i/o timeout"):
		return fmt.Errorf("%s did not answer — check the address and any firewall in between", server)
	case strings.Contains(msg, "does not match the invite"):
		return errors.New(msg)
	case strings.Contains(msg, "certificate"):
		return fmt.Errorf("%s presented a certificate this client will not accept: %w", server, err)
	}
	return err
}

// Package probnode reads the real-time stream `/v2/live` from Go: connect with an account's API key,
// subscribe markets and channels, and read messages - JSON objects, or Probe records (pnb) when the
// connection asked for `format: probe`.
//
//	c, err := probnode.Dial(ctx, "wss://live.probnode.com/v2/live", key, probnode.Probe)
//	// or, over dropped connections: probnode.DialWith(ctx, url, key, probnode.Options{Format: probnode.Probe, Reconnect: true})
//	err = c.Subscribe(ctx, []string{"btc-updown-15m-1795366800"}, []string{"book+", "trades+"}, nil)
//	for {
//		m, err := c.Next(ctx)
//		...
//		for _, r := range m.Records { if p, ok := r.(*pnb.ProbnodeExt); ok { ... } }
//	}
package probnode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/probnode/probnode-go/pnb"
)

// Format of a connection.
type Format string

const (
	JSON  Format = "json"
	Probe Format = "probe"
)

// Message is one frame: a JSON object (JSON set) or the Probe records of a binary frame.
type Message struct {
	JSON    map[string]any
	Records []pnb.Record
	// Unknown counts records of types this build does not know (skipped by their length).
	Unknown int
}

// Options of a connection.
type Options struct {
	Format Format
	// Reconnect reopens a dropped connection (backing off 0.5 s to 30 s) and sends every active
	// subscription again; each starts with a fresh snapshot.
	Reconnect bool
	// OnGap, when set, gets the wall-clock stretch nothing was received, after a reconnect.
	OnGap func(from, to time.Time)
	// Compress offers permessage-deflate: less bandwidth, more CPU on both ends.
	Compress bool
}

// ErrRefused: the server answered the connection with HTTP 401, the key is refused. Reconnecting
// would only repeat it, so Next returns it (wrapped) instead of retrying.
var ErrRefused = errors.New("probnode: the key is refused (HTTP 401)")

// Client is one connection.
type Client struct {
	url, key string
	opt      Options
	format   Format

	mu     sync.Mutex
	conn   *websocket.Conn
	subs   map[string]map[string]any // market -> its sub message
	closed bool
}

// Dial connects; key is the account's API key ("" = Free).
func Dial(ctx context.Context, url, key string, format Format) (*Client, error) {
	return DialWith(ctx, url, key, Options{Format: format})
}

// DialWith connects with options.
func DialWith(ctx context.Context, url, key string, opt Options) (*Client, error) {
	if opt.Format != JSON && opt.Format != Probe {
		return nil, errors.New("client: format is json or probe")
	}
	c := &Client{url: url, key: key, opt: opt, format: opt.Format, subs: map[string]map[string]any{}}
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	c.conn = conn
	return c, nil
}

func (c *Client) dial(ctx context.Context) (*websocket.Conn, error) {
	h := http.Header{}
	if c.key != "" {
		h.Set("Authorization", "Bearer "+c.key)
	}
	mode := websocket.CompressionDisabled
	if c.opt.Compress {
		mode = websocket.CompressionNoContextTakeover
	}
	conn, resp, err := websocket.Dial(ctx, c.url, &websocket.DialOptions{HTTPHeader: h, CompressionMode: mode})
	if err != nil {
		// Decided by the HTTP status, never by the error's text: a transport error names the
		// connection's addresses, and a port like 40122 contains "401".
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("%w: %v", ErrRefused, err)
		}
		return nil, err
	}
	conn.SetReadLimit(64 << 20)
	return conn, nil
}

func (c *Client) send(ctx context.Context, m map[string]any) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	return conn.Write(ctx, websocket.MessageText, b)
}

// Subscribe watches markets on channels; blocks picks the probnode blocks (nil = all).
func (c *Client) Subscribe(ctx context.Context, markets, channels, blocks []string) error {
	m := map[string]any{"op": "sub", "markets": markets, "channels": channels, "format": string(c.format)}
	if blocks != nil {
		m["blocks"] = blocks
	}
	if err := c.send(ctx, m); err != nil {
		return err
	}
	c.mu.Lock()
	for _, mk := range markets {
		one := map[string]any{"op": "sub", "markets": []string{mk}, "channels": channels, "format": string(c.format)}
		if blocks != nil {
			one["blocks"] = blocks
		}
		c.subs[mk] = one
	}
	c.mu.Unlock()
	return nil
}

// Unsubscribe stops watching markets (every channel of them).
func (c *Client) Unsubscribe(ctx context.Context, markets []string) error {
	if err := c.send(ctx, map[string]any{"op": "unsub", "markets": markets}); err != nil {
		return err
	}
	c.mu.Lock()
	for _, mk := range markets {
		delete(c.subs, mk)
	}
	c.mu.Unlock()
	return nil
}

// resume reopens after a drop and resubscribes: one message per distinct channel set.
func (c *Client) resume(ctx context.Context, lost time.Time) error {
	delay := 500 * time.Millisecond
	for {
		conn, err := c.dial(ctx)
		if err == nil {
			c.mu.Lock()
			c.conn = conn
			c.mu.Unlock()
			break
		}
		if errors.Is(err, ErrRefused) { // the key is refused: retrying repeats it
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		delay = min(delay*2, 30*time.Second)
	}
	c.mu.Lock()
	groups := map[string]map[string]any{}
	for mk, m := range c.subs {
		k, _ := json.Marshal(map[string]any{"channels": m["channels"], "blocks": m["blocks"]})
		g := groups[string(k)]
		if g == nil {
			g = map[string]any{}
			for x, v := range m {
				g[x] = v
			}
			g["markets"] = []string{}
			groups[string(k)] = g
		}
		g["markets"] = append(g["markets"].([]string), mk)
	}
	c.mu.Unlock()
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		g := groups[k]
		ms := g["markets"].([]string)
		sort.Strings(ms)
		if err := c.send(ctx, g); err != nil {
			return err
		}
	}
	if c.opt.OnGap != nil {
		c.opt.OnGap(lost, time.Now())
	}
	return nil
}

// Next reads the next frame. With Reconnect, a dropped connection is reopened and resubscribed
// here and reading goes on; Close (or ctx) ends it.
func (c *Client) Next(ctx context.Context) (Message, error) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	typ, b, err := conn.Read(ctx)
	for err != nil {
		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if closed || !c.opt.Reconnect || ctx.Err() != nil || isClientClose(err) {
			return Message{}, err
		}
		if rerr := c.resume(ctx, time.Now()); rerr != nil {
			return Message{}, rerr
		}
		c.mu.Lock()
		conn = c.conn
		c.mu.Unlock()
		typ, b, err = conn.Read(ctx)
	}
	if typ == websocket.MessageText {
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return Message{}, err
		}
		return Message{JSON: m}, nil
	}
	var out Message
	for len(b) > 0 {
		r, n, err := pnb.Decode(b)
		switch {
		case errors.Is(err, pnb.ErrUnknown):
			out.Unknown++
		case err != nil:
			return out, err
		default:
			out.Records = append(out.Records, r)
		}
		b = b[n:]
	}
	return out, nil
}

// Close ends the connection; no reconnect follows.
func (c *Client) Close() error {
	c.mu.Lock()
	c.closed = true
	conn := c.conn
	c.mu.Unlock()
	return conn.Close(websocket.StatusNormalClosure, "")
}

// isClientClose: the server refused us on purpose (policy / a bad request, or the key at dial),
// so reconnecting would only repeat it. Anything else on a live connection - a reset, an EOF,
// a cut frame - is a drop. Decided by the close status and ErrRefused, never by the error's text.
func isClientClose(err error) bool {
	s := websocket.CloseStatus(err)
	return s == websocket.StatusPolicyViolation || s == websocket.StatusUnsupportedData || errors.Is(err, ErrRefused)
}

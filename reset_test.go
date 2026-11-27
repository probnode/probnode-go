package probnode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// A reset is a transport failure, not a refusal: its text names the addresses of the connection,
// and an address can contain "401" (the CI run of 2026-11-26 failed on "127.0.0.1:40122").
func TestATransportErrorNamingA401PortIsNotARefusal(t *testing.T) {
	err := errors.New("failed to get reader: failed to read frame header: read tcp 127.0.0.1:40122->127.0.0.1:45649: read: connection reset by peer")
	if isClientClose(err) {
		t.Fatal("a TCP reset whose address contains 401 is taken for a refused key")
	}
}

type connKey struct{}

// resetServer listens on a port whose number contains "401", so every transport error the client
// sees names it. Its first connection is reset (RST, linger 0) at the given point; later ones send
// a header and then each message they are asked for. A status other than 0 answers every
// connection after the first with that HTTP status instead.
type resetServer struct {
	srv    *httptest.Server
	mu     sync.Mutex
	got    [][]map[string]any
	dials  atomic.Int32
	status int // the HTTP answer to connections after the first; 0 = accept
}

func listen401(t *testing.T) net.Listener {
	for p := 40100; p < 40200; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			return ln
		}
	}
	t.Skip("no free port 40100-40199")
	return nil
}

func reset(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetLinger(0)
	}
	_ = c.Close()
}

func newResetServer(t *testing.T, at string, status int) *resetServer {
	s := &resetServer{status: status}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(s.dials.Add(1)) - 1
		if n > 0 && s.status != 0 {
			w.WriteHeader(s.status)
			return
		}
		raw := r.Context().Value(connKey{}).(net.Conn)
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		s.mu.Lock()
		s.got = append(s.got, nil)
		s.mu.Unlock()
		ctx := r.Context()
		for {
			_, b, err := c.Read(ctx)
			if err != nil {
				return
			}
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			s.mu.Lock()
			s.got[n] = append(s.got[n], m)
			s.mu.Unlock()
			if m["op"] != "sub" {
				continue
			}
			if n == 0 {
				switch at {
				case "before":
					reset(raw)
					return
				case "mid":
					for i := 0; i < 3; i++ {
						_ = c.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"type":"trade","conn":0,"i":%d}`, i)))
					}
					reset(raw)
					return
				case "frame":
					_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"header","conn":0}`))
					_, _ = raw.Write([]byte{0x81, 0x7e, 0x01}) // a text frame's header, cut inside its length
					reset(raw)
					return
				}
			}
			_ = c.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"type":"header","conn":%d}`, n)))
		}
	})
	s.srv = httptest.NewUnstartedServer(h)
	s.srv.Listener.Close()
	s.srv.Listener = listen401(t)
	s.srv.Config.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
		return context.WithValue(ctx, connKey{}, c)
	}
	s.srv.Start()
	t.Cleanup(s.srv.Close)
	return s
}

func (s *resetServer) url() string { return "ws" + strings.TrimPrefix(s.srv.URL, "http") }

// A connection reset by the server (or a proxy) before the header, in the middle of the stream, or
// inside a frame is a drop: with Reconnect the client reopens, subscribes again, reports the gap
// and reads on; it never hands the reset to the caller. Without Reconnect the reset is the error.
func TestAResetConnectionIsReopenedWherever(t *testing.T) {
	for _, at := range []string{"before", "mid", "frame"} {
		t.Run(at, func(t *testing.T) {
			s := newResetServer(t, at, 0)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var gaps atomic.Int32
			c, err := DialWith(ctx, s.url(), "", Options{Format: JSON, Reconnect: true,
				OnGap: func(from, to time.Time) { gaps.Add(1) }})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if err := c.Subscribe(ctx, []string{"m1"}, []string{"book+"}, nil); err != nil {
				t.Fatal(err)
			}
			for {
				m, err := c.Next(ctx)
				if err != nil {
					t.Fatalf("a reset (%s) came back as an error: %v", at, err)
				}
				if m.JSON["type"] == "header" && m.JSON["conn"] == float64(1) {
					break
				}
			}
			if gaps.Load() < 1 {
				t.Fatal("the drop was not reported through OnGap")
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if len(s.got) < 2 || len(s.got[1]) != 1 || s.got[1][0]["op"] != "sub" {
				t.Fatalf("not subscribed again: %v", s.got)
			}
		})
		t.Run(at+"/no reconnect", func(t *testing.T) {
			s := newResetServer(t, at, 0)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			c, err := DialWith(ctx, s.url(), "", Options{Format: JSON})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if err := c.Subscribe(ctx, []string{"m1"}, []string{"book+"}, nil); err != nil {
				t.Fatal(err)
			}
			for {
				if _, err := c.Next(ctx); err != nil {
					if ctx.Err() != nil {
						t.Fatal("no error before the deadline")
					}
					break
				}
			}
			if d := s.dials.Load(); d != 1 {
				t.Fatalf("redialled %d times without Reconnect", d-1)
			}
		})
	}
}

// A key the server refuses on the way back (HTTP 401) ends the reconnect: retrying would only
// repeat it. The test serves the 401 on a port that also contains 401, so only the status decides.
func TestAReconnectRefusedWith401Stops(t *testing.T) {
	s := newResetServer(t, "mid", http.StatusUnauthorized)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := DialWith(ctx, s.url(), "k", Options{Format: JSON, Reconnect: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Subscribe(ctx, []string{"m1"}, []string{"book+"}, nil); err != nil {
		t.Fatal(err)
	}
	for {
		_, err := c.Next(ctx)
		if err == nil {
			continue
		}
		if !errors.Is(err, ErrRefused) {
			t.Fatalf("want ErrRefused, got %v", err)
		}
		break
	}
	if d := s.dials.Load(); d != 2 {
		t.Fatalf("dials %d, want 2 (the first and one refused retry)", d)
	}
}

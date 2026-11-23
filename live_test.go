package probnode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fake is a stand-in for /v2/live: it records each sub it receives and answers it with a header.
// When drop is set it closes the first connection after its first answer.
func fake(t *testing.T, drop bool) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var subs atomic.Int32
	var conns atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer k" {
			t.Errorf("Authorization %q", got)
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		n := conns.Add(1)
		ctx := r.Context()
		for {
			_, b, err := c.Read(ctx)
			if err != nil {
				return
			}
			var m map[string]any
			if json.Unmarshal(b, &m) != nil || m["op"] != "sub" {
				continue
			}
			subs.Add(1)
			_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"header","market":"m1"}`))
			if drop && n == 1 {
				c.CloseNow()
				return
			}
		}
	}))
	return srv, &subs
}

func wsURL(s *httptest.Server) string { return "ws" + strings.TrimPrefix(s.URL, "http") }

func TestSubscribeAndRead(t *testing.T) {
	srv, subs := fake(t, false)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, wsURL(srv), "k", JSON)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Subscribe(ctx, []string{"m1"}, []string{"book+"}, nil); err != nil {
		t.Fatal(err)
	}
	m, err := c.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if m.JSON["type"] != "header" || subs.Load() != 1 {
		t.Fatalf("got %v after %d subs", m.JSON, subs.Load())
	}
}

func TestReconnectResubscribesAndReportsTheGap(t *testing.T) {
	srv, subs := fake(t, true)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var gaps atomic.Int32
	c, err := DialWith(ctx, wsURL(srv), "k", Options{Format: JSON, Reconnect: true, OnGap: func(from, to time.Time) {
		if to.Before(from) {
			t.Errorf("gap ends before it starts")
		}
		gaps.Add(1)
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Subscribe(ctx, []string{"m1"}, []string{"book+"}, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // the first header, then the one after the reconnect
		if _, err := c.Next(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if subs.Load() != 2 || gaps.Load() != 1 {
		t.Fatalf("subs %d, gaps %d: want the market sent again once and one gap", subs.Load(), gaps.Load())
	}
}

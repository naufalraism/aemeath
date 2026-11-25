package securityaudit2

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/ws"
)

func wsServer(t *testing.T, open *atomic.Int32, cfg ...ws.Config) string {
	t.Helper()
	app := aemeath.New()
	app.Handle("GET", "/ws", func(c *aemeath.Ctx) error {
		conn, err := ws.Upgrade(c, cfg...)
		if err != nil {
			return nil
		}
		open.Add(1)
		defer open.Add(-1)
		defer func() { _ = conn.Close() }()
		for {
			kind, msg, err := conn.ReadMessage()
			if err != nil {
				return nil
			}
			if err := conn.WriteMessage(kind, msg); err != nil {
				return nil
			}
		}
	})
	srv := app.NewServer("127.0.0.1:0")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return "ws://" + ln.Addr().String() + "/ws"
}

func waitFor(cond func() bool, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// net/http's timeouts stop at Hijack: an idle WebSocket used to keep its
// connection and goroutine for as long as the client wanted.
func TestWebSocket_IdleConnectionIsClosed(t *testing.T) {
	const idle = 300 * time.Millisecond
	var open atomic.Int32
	url := wsServer(t, &open, ws.Config{IdleTimeout: idle})

	// A client that does not answer pings (like an attacker's raw socket).
	silent, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = silent.Close() }()
	silent.SetPingHandler(func(string) error { return nil }) // swallow pings, never pong

	// A normal client: it only listens, but its library answers pings.
	alive, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = alive.Close() }()
	go func() {
		for {
			if _, _, err := alive.ReadMessage(); err != nil {
				return
			}
		}
	}()
	go func() { // the silent client must read for its ping handler to run
		for {
			if _, _, err := silent.ReadMessage(); err != nil {
				return
			}
		}
	}()

	if !waitFor(func() bool { return open.Load() == 2 }, time.Second) {
		t.Fatalf("connections open: %d", open.Load())
	}
	closed := waitFor(func() bool { return open.Load() == 1 }, 3*time.Second)
	time.Sleep(4 * idle) // the live client must survive several idle periods
	t.Logf("idle timeout %v: silent client closed by the server: %v; connections still open after %v more: %d (the one that answers pings)",
		idle, closed, 4*idle, open.Load())
	if !closed {
		t.Error("a WebSocket that never answers is never closed by the server")
	}
	if open.Load() != 1 {
		t.Errorf("open connections %d, want 1: a quiet client that answers pings must stay connected", open.Load())
	}
	if err := alive.WriteMessage(websocket.TextMessage, []byte("still here")); err != nil {
		t.Errorf("the live client was disconnected: %v", err)
	}
}

func TestWebSocket_IdleTimeoutCanBeTurnedOff(t *testing.T) {
	var open atomic.Int32
	conn, _, err := websocket.DefaultDialer.Dial(wsServer(t, &open, ws.Config{IdleTimeout: -1}), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	time.Sleep(500 * time.Millisecond)
	if open.Load() != 1 {
		t.Errorf("with IdleTimeout -1 the connection was closed (open %d)", open.Load())
	}
}

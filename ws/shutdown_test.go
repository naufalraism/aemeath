//go:build !windows

package ws_test

import (
	"context"
	"net"
	"net/http"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/ws"
)

// With one SSE client and one WebSocket client connected, a shutdown used to
// take the full 10 second grace period (the stream's handler was never told),
// and the WebSocket handler kept running after Listen had returned, its
// client cut off without a close frame.
func TestShutdownEndsStreamsAndWebSockets(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	app := aemeath.New()
	sseEnded, wsEnded := make(chan struct{}), make(chan struct{})
	app.Handle("GET", "/sse", func(c *aemeath.Ctx) error {
		defer close(sseEnded)
		s, err := c.SSE()
		if err != nil {
			return err
		}
		for {
			select {
			case <-c.Context().Done():
				return nil
			case <-time.After(50 * time.Millisecond):
				_ = s.Comment("ping")
			}
		}
	})
	app.Handle("GET", "/ws", func(c *aemeath.Ctx) error {
		defer close(wsEnded)
		conn, err := ws.Upgrade(c)
		if err != nil {
			return nil
		}
		defer func() { _ = conn.CloseNow() }()
		for {
			if _, _, err := conn.Read(c.Context()); err != nil {
				return nil
			}
		}
	})
	done := make(chan error, 1)
	go func() { done <- app.Listen(addr) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if c, err := net.Dial("tcp", addr); err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the server did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}

	resp, err := http.Get("http://" + addr + "/sse")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	client, _, err := websocket.Dial(t.Context(), "ws://"+addr+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.CloseNow() }()
	time.Sleep(200 * time.Millisecond)

	start := time.Now()
	p, _ := os.FindProcess(os.Getpid())
	if err := p.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Listen did not return")
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("shutdown took %v with a stream connected; the stream was not told to stop", took)
	}
	for name, ch := range map[string]chan struct{}{"SSE": sseEnded, "WebSocket": wsEnded} {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Errorf("the %s handler is still running after Listen returned", name)
		}
	}
	// The WebSocket client is told why: close code 1001 (going away).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, rerr := client.Read(ctx)
	if websocket.CloseStatus(rerr) != websocket.StatusGoingAway {
		t.Errorf("WebSocket client: %v, want close 1001 (going away)", rerr)
	}
}

package ws_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/ws"
)

func echoServer(t *testing.T) *httptest.Server {
	t.Helper()
	app := aemeath.New()
	app.Use(aemeath.Logger()) // the wrapped ResponseWriter must still be hijackable
	app.Handle("GET", "/ws", func(c *aemeath.Ctx) error {
		conn, err := ws.Upgrade(c)
		if err != nil {
			return nil
		}
		defer func() { _ = conn.Close() }()
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return nil
			}
			if err := conn.WriteMessage(mt, msg); err != nil {
				return nil
			}
		}
	})
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return srv
}

func wsURL(srv *httptest.Server) string { return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws" }

func TestEchoThroughTheMiddlewareChain(t *testing.T) {
	srv := echoServer(t)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL(srv), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil || string(msg) != "ping" {
		t.Fatalf("echo = %q, err = %v", msg, err)
	}
}

func TestCrossOriginIsRejectedByDefault(t *testing.T) {
	srv := echoServer(t)
	_, resp, err := websocket.DefaultDialer.Dial(wsURL(srv), http.Header{"Origin": {"https://evil.example"}})
	if err == nil {
		t.Fatal("a cross-origin upgrade must be refused")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("response = %v, want 403", resp)
	}
}

func TestSameOriginIsAccepted(t *testing.T) {
	srv := echoServer(t)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL(srv), http.Header{"Origin": {srv.URL}})
	if err != nil {
		t.Fatalf("same-origin upgrade failed: %v", err)
	}
	_ = conn.Close()
}

func TestCheckOriginCanBeRelaxed(t *testing.T) {
	old := ws.Upgrader.CheckOrigin
	ws.Upgrader.CheckOrigin = func(r *http.Request) bool { return r.Header.Get("Origin") == "https://app.example" }
	defer func() { ws.Upgrader.CheckOrigin = old }()

	srv := echoServer(t)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL(srv), http.Header{"Origin": {"https://app.example"}})
	if err != nil {
		t.Fatalf("an allowed origin was refused: %v", err)
	}
	_ = conn.Close()
}

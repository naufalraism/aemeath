package ws_test

import (
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/metrics"
	"github.com/naufalraism/aemeath/session"
	"github.com/naufalraism/aemeath/tracing"
	"github.com/naufalraism/aemeath/ws"
)

// The real middlewares that wrap the ResponseWriter must not break upgrades.
func TestUpgradeWithSessionMetricsAndTracing(t *testing.T) {
	tr := tracing.New(tracing.Config{ServiceName: "ws-test", Endpoint: "http://127.0.0.1:1/v1/traces"})
	app := aemeath.New()
	app.Use(
		aemeath.Logger(),
		metrics.New().Middleware(),
		tr.Middleware(),
		session.Middleware(session.Config{Store: session.NewMemoryStore(), Insecure: true}),
	)
	app.Handle("GET", "/ws", func(c *aemeath.Ctx) error {
		conn, err := ws.Upgrade(c)
		if err != nil {
			return nil
		}
		defer func() { _ = conn.Close() }()
		return conn.WriteMessage(websocket.TextMessage, []byte("hi"))
	})
	srv := httptest.NewServer(app)
	defer srv.Close()

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL(srv), nil)
	if err != nil {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		t.Fatalf("dial failed (status %d): %v", code, err)
	}
	defer func() { _ = conn.Close() }()
	if _, msg, err := conn.ReadMessage(); err != nil || string(msg) != "hi" {
		t.Fatalf("got %q, %v", msg, err)
	}
}

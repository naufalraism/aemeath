package securityaudit

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/ws"
)

func wsServer(t *testing.T, got chan int) *httptest.Server {
	app := aemeath.New()
	app.Handle("GET", "/ws", func(c *aemeath.Ctx) error {
		conn, err := ws.Upgrade(c)
		if err != nil {
			return nil
		}
		defer func() { _ = conn.Close() }()
		_, msg, err := conn.ReadMessage()
		if err != nil {
			got <- -1
			return nil
		}
		got <- len(msg)
		return nil
	})
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return srv
}

// gorilla/websocket buffers a whole message in ReadMessage. Without a read
// limit one client can make the server allocate any amount of memory.
func TestWebSocket_NoDefaultReadLimit(t *testing.T) {
	got := make(chan int, 1)
	srv := wsServer(t, got)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	const size = 64 << 20 // 64 MiB
	_ = conn.WriteMessage(websocket.BinaryMessage, bytes.Repeat([]byte("A"), size))
	select {
	case n := <-got:
		t.Logf("server ReadMessage returned %d bytes", n)
		if n == size {
			t.Errorf("WEAKNESS: the server accepted a single %d MiB message into memory (no default read limit)", size>>20)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("timeout")
	}
}

// Cross-site WebSocket hijacking: a foreign Origin must be refused.
func TestWebSocket_CrossOriginRefused(t *testing.T) {
	srv := wsServer(t, make(chan int, 1))
	_, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws",
		map[string][]string{"Origin": {"https://evil.example"}})
	code := 0
	if resp != nil {
		code = resp.StatusCode
	}
	t.Logf("Origin https://evil.example -> status %d, err %v", code, err)
	if err == nil {
		t.Error("WEAKNESS: cross-origin WebSocket accepted")
	}
}

package ws_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/ws"
)

// readServer upgrades with cfg and reports the error of the first read.
func readServer(t *testing.T, cfg ws.Config) (*httptest.Server, chan error) {
	t.Helper()
	got := make(chan error, 1)
	app := aemeath.New()
	app.Handle("GET", "/ws", func(c *aemeath.Ctx) error {
		conn, err := ws.Upgrade(c, cfg)
		if err != nil {
			return nil
		}
		defer func() { _ = conn.Close() }()
		_, _, err = conn.ReadMessage()
		got <- err
		return nil
	})
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return srv, got
}

func sendOne(t *testing.T, srv *httptest.Server, got chan error, size int) error {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL(srv), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.WriteMessage(websocket.BinaryMessage, make([]byte, size))
	return <-got
}

// The settings were package variables: one app could not have a different
// read limit from another in the same process, and changing one while a
// server ran was a data race. They are given to Upgrade now.
func TestConfigIsPerUpgrade(t *testing.T) {
	small, gotSmall := readServer(t, ws.Config{ReadLimit: 10})
	normal, gotNormal := readServer(t, ws.Config{})
	unlimited, gotUnlimited := readServer(t, ws.Config{ReadLimit: -1})

	if err := sendOne(t, small, gotSmall, 20); !errors.Is(err, websocket.ErrReadLimit) {
		t.Errorf("ReadLimit 10, 20 bytes: %v, want ErrReadLimit", err)
	}
	if err := sendOne(t, normal, gotNormal, 20); err != nil {
		t.Errorf("default limit, 20 bytes: %v", err)
	}
	if err := sendOne(t, normal, gotNormal, ws.DefaultReadLimit+1); !errors.Is(err, websocket.ErrReadLimit) {
		t.Errorf("default limit, %d bytes: %v, want ErrReadLimit", ws.DefaultReadLimit+1, err)
	}
	if err := sendOne(t, unlimited, gotUnlimited, 2<<20); err != nil {
		t.Errorf("ReadLimit -1 (no limit), 2 MiB: %v", err)
	}
}

func TestTrustedOrigins(t *testing.T) {
	srv, _ := readServer(t, ws.Config{TrustedOrigins: []string{"https://app.example"}})
	for origin, ok := range map[string]bool{
		"https://app.example":  true,
		srv.URL:                true, // the server's own origin stays allowed
		"https://evil.example": false,
		"http://app.example":   false, // another scheme is another origin
	} {
		conn, resp, err := websocket.DefaultDialer.Dial(wsURL(srv), http.Header{"Origin": {origin}})
		if ok && err != nil {
			t.Errorf("origin %s refused: %v", origin, err)
		}
		if !ok && (err == nil || resp == nil || resp.StatusCode != http.StatusForbidden) {
			t.Errorf("origin %s: err %v, want 403", origin, err)
		}
		if conn != nil {
			_ = conn.Close()
		}
	}
}

func TestUpgradeTakesOneConfig(t *testing.T) {
	app := aemeath.New()
	var msg string
	app.Handle("GET", "/ws", func(c *aemeath.Ctx) error {
		defer func() {
			if r := recover(); r != nil {
				msg, _ = r.(string)
			}
		}()
		_, _ = ws.Upgrade(c, ws.Config{}, ws.Config{})
		return nil
	})
	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/ws", nil))
	if !strings.Contains(msg, "one") {
		t.Errorf("two configs: panic %q, want a message saying Upgrade takes one", msg)
	}
}

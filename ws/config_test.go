package ws_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

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
		defer func() { _ = conn.CloseNow() }()
		_, _, err = conn.Read(c.Context())
		got <- err
		return nil
	})
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return srv, got
}

func sendOne(t *testing.T, srv *httptest.Server, got chan error, size int) error {
	t.Helper()
	conn, _, err := dial(t, wsURL(srv), "")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Write(t.Context(), websocket.MessageBinary, make([]byte, size))
	return <-got
}

// The settings were package variables: one app could not have a different
// read limit from another in the same process, and changing one while a
// server ran was a data race. They are given to Upgrade now.
func TestConfigIsPerUpgrade(t *testing.T) {
	small, gotSmall := readServer(t, ws.Config{ReadLimit: 10})
	normal, gotNormal := readServer(t, ws.Config{})
	unlimited, gotUnlimited := readServer(t, ws.Config{ReadLimit: -1})

	if err := sendOne(t, small, gotSmall, 20); !errors.Is(err, websocket.ErrMessageTooBig) {
		t.Errorf("ReadLimit 10, 20 bytes: %v, want ErrMessageTooBig", err)
	}
	if err := sendOne(t, normal, gotNormal, 20); err != nil {
		t.Errorf("default limit, 20 bytes: %v", err)
	}
	if err := sendOne(t, normal, gotNormal, ws.DefaultReadLimit+1); !errors.Is(err, websocket.ErrMessageTooBig) {
		t.Errorf("default limit, %d bytes: %v, want ErrMessageTooBig", ws.DefaultReadLimit+1, err)
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
		_, resp, err := dial(t, wsURL(srv), origin)
		if ok && err != nil {
			t.Errorf("origin %s refused: %v", origin, err)
		}
		if !ok && (err == nil || resp == nil || resp.StatusCode != http.StatusForbidden) {
			t.Errorf("origin %s: err %v, want 403", origin, err)
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

// A handler that only writes calls CloseRead, as the package documentation
// says: pings are still answered, so the connection outlives several idle
// periods, and the handler learns when the client leaves.
func TestAWriteOnlyHandlerWithCloseReadStaysConnected(t *testing.T) {
	const idle = 200 * time.Millisecond
	ended := make(chan struct{})
	app := aemeath.New()
	app.Handle("GET", "/ws", func(c *aemeath.Ctx) error {
		defer close(ended)
		conn, err := ws.Upgrade(c, ws.Config{IdleTimeout: idle})
		if err != nil {
			return nil
		}
		defer func() { _ = conn.CloseNow() }()
		ctx := conn.CloseRead(c.Context())
		for i := 0; ; i++ {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(20 * time.Millisecond):
				if conn.Write(ctx, websocket.MessageText, []byte("tick")) != nil {
					return nil
				}
			}
		}
	})
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	conn, _, err := dial(t, wsURL(srv), "")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * idle)
	n := 0
	for time.Now().Before(deadline) {
		if _, _, err := conn.Read(t.Context()); err != nil {
			t.Fatalf("after %d messages (%v): %v", n, time.Until(deadline), err)
		}
		n++
	}
	_ = conn.Close(websocket.StatusNormalClosure, "bye")
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Error("the handler did not notice that the client left")
	}
}

// Package ws upgrades HTTP connections to WebSockets.
package ws

import (
	"bufio"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/naufalraism/aemeath"
)

// DefaultReadLimit is the largest message (in bytes) a connection accepts
// when Config.ReadLimit is 0: 1 MiB. ReadMessage keeps a whole message in
// memory, so without a limit one client could make the server allocate any
// amount. A larger message closes the connection (close code 1009, "message
// too big").
const DefaultReadLimit = 1 << 20

// DefaultIdleTimeout closes a connection whose client has not answered a ping
// for this long, when Config.IdleTimeout is 0: 60 seconds. The server pings
// every half of it, and browsers and WebSocket libraries answer pings by
// themselves, so a client that only listens stays connected; one that has
// gone away, or never answers, is closed instead of holding a connection and
// a goroutine forever (net/http's timeouts end when the connection is taken
// over). ReadMessage then returns an error, as for any closed connection.
//
// If you set your own PongHandler, extend the read deadline in it, as this
// one does.
const DefaultIdleTimeout = 60 * time.Second

// Config adjusts one Upgrade. The zero value is the safe default.
type Config struct {
	// TrustedOrigins lists the origins, besides the server's own, whose pages
	// may connect: "https://app.example.com" (scheme and host, exactly). By
	// default only same-origin browser connections are accepted, which
	// protects against cross-site WebSocket hijacking; clients that send no
	// Origin header (not browsers) are accepted.
	TrustedOrigins []string
	// ReadLimit is the largest message in bytes: 0 means DefaultReadLimit,
	// a negative value no limit.
	ReadLimit int64
	// IdleTimeout: 0 means DefaultIdleTimeout, a negative value turns the
	// pings off and leaves timeouts to you.
	IdleTimeout time.Duration
}

// allowed reports whether the page that opened r may connect.
func (cfg Config) allowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	for _, o := range cfg.TrustedOrigins {
		if strings.EqualFold(o, origin) {
			return true
		}
	}
	return false
}

// Upgrade turns the HTTP connection of c into a WebSocket connection. When it
// fails, the upgrader has already written the error response: return nil.
//
// Middlewares often wrap the ResponseWriter (sessions, metrics, tracing, ...).
// The connection is taken over through http.ResponseController, which follows
// their Unwrap methods, so the upgrade works no matter how many wrappers there are.
func Upgrade(c *aemeath.Ctx, config ...Config) (*websocket.Conn, error) {
	if len(config) > 1 {
		panic("aemeath/ws: Upgrade takes one Config, not several")
	}
	var cfg Config
	if len(config) == 1 {
		cfg = config[0]
	}
	up := websocket.Upgrader{CheckOrigin: cfg.allowed}
	conn, err := up.Upgrade(hijackable{c.W}, c.R, nil)
	if err != nil {
		return nil, err
	}
	switch limit := cfg.ReadLimit; {
	case limit == 0:
		conn.SetReadLimit(DefaultReadLimit)
	case limit > 0:
		conn.SetReadLimit(limit)
	}
	// net/http's timeouts end at the takeover: without a deadline, a client
	// that goes silent holds the connection and the handler forever.
	var pinger *time.Ticker
	var ping <-chan time.Time
	idle := cfg.IdleTimeout
	if idle == 0 {
		idle = DefaultIdleTimeout
	}
	if idle > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(idle))
		conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(idle)) })
		pinger = time.NewTicker(idle / 2)
		ping = pinger.C
	}
	// A hijacked connection is invisible to http.Server.Shutdown: without
	// this, the handler kept running after Listen returned and the client was
	// cut off without a close frame when the process exited.
	stopping, done := c.Stopping(), c.Context().Done()
	go func() {
		if pinger != nil {
			defer pinger.Stop()
		}
		for {
			select {
			case <-stopping:
				msg := websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down")
				_ = conn.WriteControl(websocket.CloseMessage, msg, time.Now().Add(time.Second))
				_ = conn.Close() // unblocks the handler's ReadMessage
				return
			case <-done: // the handler returned
				return
			case <-ping:
				// WriteControl may run alongside the handler's own writes.
				if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)) != nil {
					return // the connection is gone; the handler's read fails too
				}
			}
		}
	}()
	return conn, nil
}

// hijackable exposes http.Hijacker for any ResponseWriter whose chain of
// Unwrap methods eventually reaches one that can be hijacked.
type hijackable struct{ http.ResponseWriter }

func (h hijackable) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(h.ResponseWriter).Hijack()
}

func (h hijackable) Unwrap() http.ResponseWriter { return h.ResponseWriter }

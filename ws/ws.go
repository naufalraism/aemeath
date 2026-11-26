// Package ws upgrades HTTP connections to WebSockets, with
// github.com/coder/websocket.
//
//	app.Handle("GET", "/ws", func(c *aemeath.Ctx) error {
//		conn, err := ws.Upgrade(c)
//		if err != nil {
//			return nil // Upgrade has written the error response
//		}
//		defer conn.CloseNow()
//		for {
//			kind, msg, err := conn.Read(c.Context())
//			if err != nil {
//				return nil // the client left, or the server is stopping
//			}
//			if err := conn.Write(c.Context(), kind, msg); err != nil {
//				return nil
//			}
//		}
//	})
//
// A handler that only writes must still let the connection read, so that
// pings, pongs and the close handshake are answered: call
// ctx := conn.CloseRead(c.Context()) once, and stop writing when ctx is done.
package ws

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/naufalraism/aemeath"
)

// DefaultReadLimit is the largest message (in bytes) a connection accepts
// when Config.ReadLimit is 0: 1 MiB. Read keeps a whole message in memory, so
// without a limit one client could make the server allocate any amount. A
// larger message closes the connection (close code 1009, "message too big")
// and Read returns an error wrapping websocket.ErrMessageTooBig.
const DefaultReadLimit = 1 << 20

// DefaultIdleTimeout closes a connection whose client has not answered a ping
// for this long, when Config.IdleTimeout is 0: 60 seconds. The server pings
// every half of it, and browsers and WebSocket libraries answer pings by
// themselves while the connection is read, so a client that only listens
// stays connected; one that has gone away, or never answers, is closed
// instead of holding a connection and a goroutine forever (net/http's
// timeouts end when the connection is taken over). Read then returns an
// error, as for any closed connection.
const DefaultIdleTimeout = 60 * time.Second

// ErrOriginNotAllowed is returned by Upgrade when the page that opened the
// connection is from an origin that may not connect; Upgrade has answered
// 403.
var ErrOriginNotAllowed = errors.New("aemeath/ws: origin not allowed")

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
// fails it has already written the error response: return nil.
//
// Middlewares often wrap the ResponseWriter (sessions, metrics, tracing, ...).
// The connection is taken over through http.ResponseController, which follows
// their Unwrap methods, so the upgrade works no matter how many wrappers there are.
//
// While the handler runs, the server pings the client (see IdleTimeout), and
// when the app stops (App.Stopping) it closes the connection with code 1001,
// "going away", so a Read in the handler returns and the handler can end.
func Upgrade(c *aemeath.Ctx, config ...Config) (*websocket.Conn, error) {
	if len(config) > 1 {
		panic("aemeath/ws: Upgrade takes one Config, not several")
	}
	var cfg Config
	if len(config) == 1 {
		cfg = config[0]
	}
	if !cfg.allowed(c.R) {
		http.Error(c.W, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return nil, ErrOriginNotAllowed
	}
	hj := &hijackable{ResponseWriter: c.W}
	conn, err := websocket.Accept(hj, c.R, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // the origin was checked above, with TrustedOrigins
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return nil, err
	}
	switch limit := cfg.ReadLimit; {
	case limit == 0:
		conn.SetReadLimit(DefaultReadLimit)
	case limit > 0:
		conn.SetReadLimit(limit)
	default:
		conn.SetReadLimit(-1)
	}

	idle := cfg.IdleTimeout
	if idle == 0 {
		idle = DefaultIdleTimeout
	}
	// A hijacked connection is invisible to http.Server.Shutdown: without
	// this, the handler kept running after Listen returned and the client was
	// cut off without a close frame when the process exited.
	stopping, done := c.Stopping(), c.Context().Done()
	go func() {
		var ping <-chan time.Time
		if idle > 0 {
			t := time.NewTicker(idle / 2)
			defer t.Stop()
			ping = t.C
		}
		for {
			select {
			case <-stopping:
				// Close sends the close frame at once, then waits up to 5
				// seconds for the client's answer, which a client that does not
				// read never sends. After a second the network connection is
				// closed under it, which ends the handler's Read too.
				closed := make(chan struct{})
				go func() {
					defer close(closed)
					_ = conn.Close(websocket.StatusGoingAway, "server shutting down")
				}()
				select {
				case <-closed:
				case <-time.After(time.Second):
					_ = hj.conn.Close()
				}
				return
			case <-done: // the handler returned
				return
			case <-ping:
				// The pong is read by the handler's Read (or CloseRead); a client
				// that does not answer within half the idle time is gone.
				ctx, cancel := context.WithTimeout(context.Background(), idle/2)
				err := conn.Ping(ctx)
				cancel()
				if err != nil {
					_ = conn.CloseNow()
					return
				}
			}
		}
	}()
	return conn, nil
}

// hijackable exposes http.Hijacker for any ResponseWriter whose chain of
// Unwrap methods eventually reaches one that can be hijacked, and keeps the
// network connection it hands over, so a shutdown can close it.
type hijackable struct {
	http.ResponseWriter
	conn net.Conn // set by Hijack, during Accept
}

func (h *hijackable) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	nc, brw, err := http.NewResponseController(h.ResponseWriter).Hijack()
	h.conn = nc
	return nc, brw, err
}

func (h *hijackable) Unwrap() http.ResponseWriter { return h.ResponseWriter }

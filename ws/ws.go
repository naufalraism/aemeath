// Package ws upgrades HTTP connections to WebSockets.
package ws

import (
	"bufio"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/naufalraism/aemeath"
)

// Upgrader is used by Upgrade.
//
// By default only same-origin browser connections are accepted, which
// protects against cross-site WebSocket hijacking. To allow other origins,
// set CheckOrigin explicitly, for example:
//
//	ws.Upgrader.CheckOrigin = func(r *http.Request) bool {
//		return r.Header.Get("Origin") == "https://app.example.com"
//	}
var Upgrader = websocket.Upgrader{}

// DefaultReadLimit is the largest message (in bytes) a connection returned by
// Upgrade accepts: 1 MiB. ReadMessage keeps a whole message in memory, so
// without a limit one client could make the server allocate any amount. A
// larger message closes the connection (close code 1009, "message too big").
// Change it before the server starts, or call conn.SetReadLimit per
// connection; 0 or less disables the limit.
var DefaultReadLimit int64 = 1 << 20

// Upgrade turns the HTTP connection of c into a WebSocket connection. When it
// fails, the upgrader has already written the error response: return nil.
//
// Middlewares often wrap the ResponseWriter (sessions, metrics, tracing, ...).
// The connection is taken over through http.ResponseController, which follows
// their Unwrap methods, so the upgrade works no matter how many wrappers there are.
func Upgrade(c *aemeath.Ctx) (*websocket.Conn, error) {
	conn, err := Upgrader.Upgrade(hijackable{c.W}, c.R, nil)
	if err != nil {
		return nil, err
	}
	if DefaultReadLimit > 0 {
		conn.SetReadLimit(DefaultReadLimit)
	}
	// A hijacked connection is invisible to http.Server.Shutdown: without
	// this, the handler kept running after Listen returned and the client was
	// cut off without a close frame when the process exited.
	stopping, done := c.Stopping(), c.Context().Done()
	go func() {
		select {
		case <-stopping:
			msg := websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down")
			_ = conn.WriteControl(websocket.CloseMessage, msg, time.Now().Add(time.Second))
			_ = conn.Close() // unblocks the handler's ReadMessage
		case <-done: // the handler returned
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

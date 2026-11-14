// Package ws upgrades HTTP connections to WebSockets.
package ws

import (
	"bufio"
	"net"
	"net/http"

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

// Upgrade turns the HTTP connection of c into a WebSocket connection. When it
// fails, the upgrader has already written the error response: return nil.
//
// Middlewares often wrap the ResponseWriter (sessions, metrics, tracing, ...).
// The connection is taken over through http.ResponseController, which follows
// their Unwrap methods, so the upgrade works no matter how many wrappers there are.
func Upgrade(c *aemeath.Ctx) (*websocket.Conn, error) {
	return Upgrader.Upgrade(hijackable{c.W}, c.R, nil)
}

// hijackable exposes http.Hijacker for any ResponseWriter whose chain of
// Unwrap methods eventually reaches one that can be hijacked.
type hijackable struct{ http.ResponseWriter }

func (h hijackable) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(h.ResponseWriter).Hijack()
}

func (h hijackable) Unwrap() http.ResponseWriter { return h.ResponseWriter }

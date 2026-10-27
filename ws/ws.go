// Package ws upgrades HTTP connections to WebSockets.
package ws

import (
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
func Upgrade(c *aemeath.Ctx) (*websocket.Conn, error) {
	return Upgrader.Upgrade(c.W, c.R, nil)
}

package aemeath

import (
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true }, // Izinkan semua origin untuk development
}

// Upgrade mengubah koneksi HTTP biasa menjadi WebSocket.
func (c *Ctx) Upgrade() (*websocket.Conn, error) {
	return upgrader.Upgrade(c.W, c.R, nil)
}

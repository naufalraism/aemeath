package aemeath

import (
	"encoding/json"
	"log"
	"net/http"
)

// Ctx membungkus request dan response dengan state yang bisa dibagikan antar middleware.
type Ctx struct {
	W     http.ResponseWriter
	R     *http.Request
	State map[string]any
}

// Param mengambil nilai path parameter berdasarkan nama.
func (c *Ctx) Param(name string) string { return c.R.PathValue(name) }

// Query mengambil nilai query string berdasarkan nama.
func (c *Ctx) Query(name string) string { return c.R.URL.Query().Get(name) }

// JSON mengirim respons dalam format JSON beserta HTTP status code.
func (c *Ctx) JSON(code int, v any) error {
	writeJSON(c.W, code, v)
	return nil
}

// writeJSON adalah helper internal untuk mengirim response JSON.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if v != nil {
		if err := json.NewEncoder(w).Encode(v); err != nil {
			log.Printf("aemeath: gagal encode JSON: %v", err)
		}
	}
}

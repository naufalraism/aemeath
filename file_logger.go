package aemeath

import (
	"errors"
	"fmt"
	"log"
	"os"
	"time"
)

// FileLoggerPlugin adalah plugin untuk mencatat log HTTP ke dalam file.
type FileLoggerPlugin struct {
	File   *os.File
	Logger *log.Logger
}

// NewFileLogger membuat instance FileLogger baru.
// Contoh: aemeath.NewFileLogger("access.log")
func NewFileLogger(path string) (*FileLoggerPlugin, error) {
	// Buka atau buat file log (append mode)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return nil, fmt.Errorf("aemeath: gagal membuka file log: %w", err)
	}

	return &FileLoggerPlugin{
		File:   file,
		Logger: log.New(file, "", 0), // Kita format manual di bawah
	}, nil
}

// Name memenuhi antarmuka Plugin.
func (p *FileLoggerPlugin) Name() string { return "file_logger" }

// Apply menyuntikkan middleware logging ke dalam aplikasi.
func (p *FileLoggerPlugin) Apply(app *App) error {
	app.Use(func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) (err error) {
			start := time.Now()

			// Bungkus ResponseWriter untuk menangkap status code
			sw := &statusWriter{ResponseWriter: c.W, status: 200}
			c.W = sw

			// Jalankan handler
			err = next(c)

			// Tentukan status code akhir
			status := sw.status
			if err != nil {
				var he *HTTPError
				if errors.As(err, &he) {
					status = he.Code
				} else {
					status = 500
				}
			}

			// Tulis ke file log
			p.Logger.Printf("[%s] %s %s %d %v | IP: %s",
				time.Now().Format("2006-01-02 15:04:05"),
				c.R.Method,
				c.R.URL.Path,
				status,
				time.Since(start),
				c.R.RemoteAddr,
			)

			return err
		}
	})
	return nil
}

// Close menutup file log saat server dimatikan.
func (p *FileLoggerPlugin) Close() error {
	return p.File.Close()
}

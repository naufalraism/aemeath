package aemeath

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

// FileLoggerPlugin writes HTTP access logs to a file.
type FileLoggerPlugin struct {
	File   *os.File
	Logger *log.Logger
}

// NewFileLogger creates a FileLogger that appends to the file at path.
// Example: aemeath.NewFileLogger("access.log")
func NewFileLogger(path string) (*FileLoggerPlugin, error) {
	// Open or create the log file in append mode.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, fmt.Errorf("aemeath: failed to open log file: %w", err)
	}

	return &FileLoggerPlugin{
		File:   file,
		Logger: log.New(file, "", 0), // the line is formatted manually below
	}, nil
}

// Name implements Plugin.
func (p *FileLoggerPlugin) Name() string { return "file_logger" }

// Apply registers the logging middleware.
func (p *FileLoggerPlugin) Apply(app *App) error {
	app.Use(func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) (err error) {
			start := time.Now()

			// Wrap the ResponseWriter to capture the status code.
			sw := &statusWriter{ResponseWriter: c.W, status: http.StatusOK}
			c.W = sw

			err = next(c)

			// Work out the final status code.
			status := sw.status
			if err != nil {
				var he *HTTPError
				if errors.As(err, &he) {
					status = he.Code
				} else {
					status = http.StatusInternalServerError
				}
			}

			p.Logger.Printf("[%s] %s %s %d %v | IP: %s",
				time.Now().Format("2006-01-02 15:04:05"),
				c.R.Method,
				c.R.URL.EscapedPath(),
				status,
				time.Since(start),
				c.R.RemoteAddr,
			)

			return err
		}
	})
	return nil
}

// Close closes the log file; call it when the server shuts down.
func (p *FileLoggerPlugin) Close() error {
	return p.File.Close()
}

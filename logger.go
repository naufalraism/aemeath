package aemeath

import "log/slog"

// SetLogger sets the logger for the framework's own messages: the cause of a
// 500, a panic, a failed readiness check, the server starting and stopping,
// warnings at startup, and the messages of plugins that log through the app
// (session, db, tracing). Default: slog.Default(), which writes through the
// standard log package unless the program changed it.
//
//	app.SetLogger(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
//
// Call it before registering routes, so warnings at registration use it too.
func (a *App) SetLogger(l *slog.Logger) { a.logger = l }

// Logger returns the logger the app uses (see SetLogger).
func (a *App) Logger() *slog.Logger {
	if a != nil && a.logger != nil {
		return a.logger
	}
	return slog.Default()
}

// Logger returns the app's logger with the request's method, path and, when
// RequestID is installed, request_id, so a handler's own messages can be
// matched with the request and with the framework's messages about it.
func (c *Ctx) Logger() *slog.Logger {
	l := c.app.Logger()
	if c.R == nil {
		return l
	}
	args := []any{"method", c.R.Method, "path", c.R.URL.Path}
	if c.requestID != "" {
		args = append(args, "request_id", c.requestID)
	}
	return l.With(args...)
}

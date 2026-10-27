// Package mailer sends email over SMTP.
package mailer

import (
	"fmt"

	"gopkg.in/gomail.v2"

	"github.com/naufalraism/aemeath"
)

// Plugin sends email through one SMTP server.
type Plugin struct {
	Dialer *gomail.Dialer
	From   string
}

// New creates a Plugin.
// Example: mailer.New("smtp.gmail.com", 587, "email@gmail.com", "password", "Admin <admin@example.com>")
func New(host string, port int, username, password, from string) *Plugin {
	return &Plugin{Dialer: gomail.NewDialer(host, port, username, password), From: from}
}

// Name implements aemeath.Plugin.
func (p *Plugin) Name() string { return "mailer" }

// Apply provides the *Plugin to handlers.
func (p *Plugin) Apply(app *aemeath.App) error {
	aemeath.Provide(app, p)
	return nil
}

// From returns the mailer of the application handling c.
func From(c *aemeath.Ctx) *Plugin { return aemeath.MustGet[*Plugin](c) }

// Send sends an HTML email to the recipient. It blocks until the SMTP
// exchange finishes, so call it from a goroutine for slow servers.
func (p *Plugin) Send(to, subject, body string) error {
	msg := gomail.NewMessage()
	msg.SetHeader("From", p.From)
	msg.SetHeader("To", to)
	msg.SetHeader("Subject", subject)
	msg.SetBody("text/html", body)

	if err := p.Dialer.DialAndSend(msg); err != nil {
		return fmt.Errorf("aemeath/mailer: failed to send email: %w", err)
	}
	return nil
}

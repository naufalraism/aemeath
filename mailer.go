package aemeath

import (
	"fmt"

	"gopkg.in/gomail.v2"
)

// MailerPlugin adalah plugin untuk mengirim email menggunakan SMTP.
type MailerPlugin struct {
	Dialer *gomail.Dialer
	From   string
}

// NewMailer membuat instance Mailer baru.
// Contoh: aemeath.NewMailer("smtp.gmail.com", 587, "email@gmail.com", "password", "Admin <admin@aemeath.dev>")
func NewMailer(host string, port int, username, password, from string) *MailerPlugin {
	d := gomail.NewDialer(host, port, username, password)
	return &MailerPlugin{
		Dialer: d,
		From:   from,
	}
}

// Name memenuhi antarmuka Plugin.
func (m *MailerPlugin) Name() string { return "mailer" }

// Apply menyuntikkan Mailer ke dalam Context Aemeath.
func (m *MailerPlugin) Apply(app *App) error {
	app.Use(func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) error {
			c.State["mailer"] = m
			return next(c)
		}
	})
	return nil
}

// Send mengirim email HTML ke penerima.
func (m *MailerPlugin) Send(to, subject, body string) error {
	msg := gomail.NewMessage()
	msg.SetHeader("From", m.From)
	msg.SetHeader("To", to)
	msg.SetHeader("Subject", subject)
	msg.SetBody("text/html", body)

	if err := m.Dialer.DialAndSend(msg); err != nil {
		return fmt.Errorf("aemeath: gagal kirim email: %w", err)
	}
	return nil
}

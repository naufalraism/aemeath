// Package mailer sends email over SMTP.
package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/gomail.v2"

	"github.com/naufalraism/aemeath"
)

// Plugin sends email through one SMTP server.
type Plugin struct {
	Dialer *gomail.Dialer
	From   string
	// Timeout limits one Send, from connecting to the server's last answer.
	// Zero means DefaultTimeout.
	Timeout time.Duration
}

// New creates a Plugin.
// Example: mailer.New("smtp.gmail.com", 587, "email@gmail.com", "password", "Admin <admin@example.com>")
func New(host string, port int, username, password, from string) *Plugin {
	return &Plugin{Dialer: gomail.NewDialer(host, port, username, password), From: from}
}

// Apply provides the *Plugin to handlers.
func (p *Plugin) Apply(app *aemeath.App) error {
	aemeath.Provide(app, p)
	return nil
}

// From returns the mailer of the application handling c.
func From(c *aemeath.Ctx) *Plugin { return aemeath.MustGet[*Plugin](c) }

// DefaultTimeout is the time one Send may take when Plugin.Timeout is zero.
const DefaultTimeout = 30 * time.Second

// Send sends an HTML email to the recipient. It blocks until the SMTP
// exchange finishes or Timeout passes (DefaultTimeout when unset).
func (p *Plugin) Send(to, subject, body string) error {
	return p.SendContext(context.Background(), to, subject, body)
}

// SendContext is Send with a context: the exchange is abandoned when ctx is
// cancelled (a job's time limit, a request that ended) or Timeout passes,
// whichever comes first. A server that accepts the connection and then stays
// silent can therefore no longer block the caller for ever.
func (p *Plugin) SendContext(ctx context.Context, to, subject, body string) error {
	msg := gomail.NewMessage()
	msg.SetHeader("From", p.From)
	msg.SetHeader("To", to)
	msg.SetHeader("Subject", subject)
	msg.SetBody("text/html", body)

	timeout := p.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := p.deliver(ctx, msg); err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("%w (%v)", ctx.Err(), err)
		}
		return fmt.Errorf("aemeath/mailer: failed to send email: %w", err)
	}
	return nil
}

// deliver runs the SMTP exchange on a connection that is closed when ctx
// ends. It follows gomail's Dialer (implicit TLS when Dialer.SSL is set,
// STARTTLS when offered, then CRAM-MD5, LOGIN or PLAIN), which has no way to
// set a deadline itself.
func (p *Plugin) deliver(ctx context.Context, msg *gomail.Message) error {
	d := p.Dialer
	var nd net.Dialer
	conn, err := nd.DialContext(ctx, "tcp", net.JoinHostPort(d.Host, strconv.Itoa(d.Port)))
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() }) // unblocks any read or write
	defer stop()
	defer func() { _ = conn.Close() }()

	tlsConfig := d.TLSConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{ServerName: d.Host, MinVersion: tls.VersionTLS12}
	}
	if d.SSL {
		conn = tls.Client(conn, tlsConfig)
	}
	c, err := smtp.NewClient(conn, d.Host)
	if err != nil {
		return err
	}
	if d.LocalName != "" {
		if err := c.Hello(d.LocalName); err != nil {
			return err
		}
	}
	if !d.SSL {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsConfig); err != nil {
				return err
			}
		}
	}
	auth := d.Auth
	if auth == nil && d.Username != "" {
		if ok, mechanisms := c.Extension("AUTH"); ok {
			switch {
			case strings.Contains(mechanisms, "CRAM-MD5"):
				auth = smtp.CRAMMD5Auth(d.Username, d.Password)
			case strings.Contains(mechanisms, "LOGIN") && !strings.Contains(mechanisms, "PLAIN"):
				auth = &loginAuth{d.Username, d.Password, d.Host}
			default:
				auth = smtp.PlainAuth("", d.Username, d.Password, d.Host)
			}
		}
	}
	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := gomail.Send(smtpSender{c}, msg); err != nil {
		return err
	}
	return c.Quit()
}

// smtpSender adapts an smtp.Client to gomail.Sender.
type smtpSender struct{ c *smtp.Client }

func (s smtpSender) Send(from string, to []string, msg io.WriterTo) error {
	if err := s.c.Mail(from); err != nil {
		return err
	}
	for _, addr := range to {
		if err := s.c.Rcpt(addr); err != nil {
			return err
		}
	}
	w, err := s.c.Data()
	if err != nil {
		return err
	}
	if _, err := msg.WriteTo(w); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

// loginAuth is the LOGIN mechanism, for servers that do not offer PLAIN. Like
// smtp.PlainAuth, it only sends the password over TLS or to localhost.
type loginAuth struct{ username, password, host string }

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	local := server.Name == "localhost" || server.Name == "127.0.0.1" || server.Name == "::1"
	if !server.TLS && !local {
		return "", nil, errors.New("unencrypted connection")
	}
	if server.Name != a.host {
		return "", nil, errors.New("wrong host name")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSuffix(string(fromServer), ":")) {
	case "username":
		return []byte(a.username), nil
	case "password":
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("unexpected server challenge %q", fromServer)
}

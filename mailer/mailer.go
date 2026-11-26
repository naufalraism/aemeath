// Package mailer sends email over SMTP.
package mailer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/naufalraism/aemeath"
)

// Config describes one SMTP server and the sender.
type Config struct {
	Host     string // e.g. "smtp.gmail.com"
	Port     int    // usually 587 (STARTTLS) or 465 (TLS from the start)
	Username string // empty: no authentication
	Password string
	From     string // e.g. "Admin <admin@example.com>"

	// SSL speaks TLS from the first byte (port 465 always does). Otherwise
	// the connection switches to TLS with STARTTLS, and Send refuses a server
	// that does not offer it (see AllowPlaintext).
	SSL bool
	// AllowPlaintext lets Send go on without TLS when the server does not
	// offer STARTTLS. Without TLS, anyone on the network path can read the
	// messages and the login, and can force that by deleting the server's
	// STARTTLS offer. Leave it off, except for a relay on a network you
	// trust, such as a MailHog container. A server on localhost or a
	// loopback address needs no opt-in.
	AllowPlaintext bool
	// TLSConfig overrides the TLS settings (server name, roots, minimum
	// version). Nil: verify Host's certificate, TLS 1.2 or later.
	TLSConfig *tls.Config
	// Timeout limits one Send, from connecting to the server's last answer.
	// Zero means DefaultTimeout.
	Timeout time.Duration
}

// Plugin sends email through one SMTP server.
type Plugin struct {
	cfg Config
}

// New creates a Plugin. The connection is made on each Send.
//
//	mailer.New(mailer.Config{Host: "smtp.gmail.com", Port: 587,
//		Username: "email@gmail.com", Password: os.Getenv("SMTP_PASSWORD"),
//		From: "Admin <admin@example.com>"})
func New(cfg Config) *Plugin {
	if cfg.Port == 465 {
		cfg.SSL = true
	}
	return &Plugin{cfg: cfg}
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
	msg, err := newMessage(p.cfg.From, to, subject, body, time.Now())
	if err != nil {
		return fmt.Errorf("aemeath/mailer: %w", err)
	}

	timeout := p.cfg.Timeout
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
// ends: implicit TLS when Config.SSL is set, STARTTLS when offered, then
// CRAM-MD5, LOGIN or PLAIN.
func (p *Plugin) deliver(ctx context.Context, msg *message) error {
	d := p.cfg
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
	if !d.SSL {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsConfig); err != nil {
				return err
			}
		} else if !d.AllowPlaintext && !isLoopback(d.Host) {
			return fmt.Errorf("%s does not offer STARTTLS, so the login and the message would cross the network unencrypted; use SSL on port 465 if the server supports it, or set AllowPlaintext for a relay you trust", d.Host)
		}
	}
	var auth smtp.Auth
	if d.Username != "" {
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
	if err := c.Mail(msg.from); err != nil {
		return err
	}
	if err := c.Rcpt(msg.to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg.data); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// isLoopback reports whether host is this machine, where plain TCP crosses no
// network.
func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// message is one email ready for SMTP: the envelope addresses and the bytes
// sent after DATA.
type message struct {
	from, to string
	data     []byte
}

// newMessage builds an HTML email for one recipient. from and to are parsed
// as RFC 5322 addresses ("Name <user@example.com>" or a bare address), so a
// line break in them is an error rather than a new header. The subject and
// display names are encoded (RFC 2047) when they hold anything but printable
// ASCII, which also keeps a line break in the subject from starting a
// header. The body is quoted-printable, so no line exceeds SMTP's limit.
func newMessage(from, to, subject, html string, now time.Time) (*message, error) {
	f, err := mail.ParseAddress(from)
	if err != nil {
		return nil, fmt.Errorf("From %q is not an email address: %w", from, err)
	}
	r, err := mail.ParseAddress(to)
	if err != nil {
		return nil, fmt.Errorf("recipient %q is not one email address: %w", to, err)
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	domain := f.Address[strings.LastIndexByte(f.Address, '@')+1:]

	var b bytes.Buffer
	for _, h := range [][2]string{
		{"From", f.String()},
		{"To", r.String()},
		{"Subject", foldEncoded(mime.QEncoding.Encode("UTF-8", subject))},
		{"Date", now.Format(time.RFC1123Z)},
		{"Message-ID", "<" + hex.EncodeToString(id) + "@" + domain + ">"},
		{"MIME-Version", "1.0"},
		{"Content-Type", "text/html; charset=UTF-8"},
		{"Content-Transfer-Encoding", "quoted-printable"},
	} {
		b.WriteString(h[0] + ": " + h[1] + "\r\n")
	}
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&b)
	if _, err := qp.Write([]byte(html)); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return &message{from: f.Address, to: r.Address, data: b.Bytes()}, nil
}

// foldEncoded puts each encoded word of a long encoded header on a line of
// its own, so the header stays under SMTP's line limit.
func foldEncoded(v string) string {
	return strings.ReplaceAll(v, "?= =?", "?=\r\n =?")
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

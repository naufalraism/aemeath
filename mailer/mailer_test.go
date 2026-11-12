package mailer_test

import (
	"bufio"
	"encoding/base64"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http/httptest"
	"net/mail"
	"strings"
	"sync"
	"testing"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/mailer"
)

type captured struct {
	from string
	to   []string
	data string
}

// smtpServer is a small SMTP server for tests: it speaks the real protocol (EHLO,
// optional AUTH PLAIN, MAIL, RCPT, DATA, QUIT) and keeps the messages it accepts.
type smtpServer struct {
	ln       net.Listener
	user     string // when set, AUTH PLAIN with these credentials is required
	pass     string
	mu       sync.Mutex
	messages []captured
}

func startSMTP(t *testing.T, user, pass string) (*smtpServer, string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &smtpServer{ln: ln, user: user, pass: pass}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	return s, host, atoi(portStr)
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func (s *smtpServer) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	say := func(line string) { _, _ = io.WriteString(conn, line+"\r\n") }
	say("220 test ESMTP")
	authed := s.user == ""
	var cur captured
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			if s.user != "" {
				say("250-localhost")
				say("250 AUTH PLAIN")
			} else {
				say("250 localhost")
			}
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(line[len("AUTH PLAIN"):]))
			parts := strings.Split(string(raw), "\x00")
			if len(parts) == 3 && parts[1] == s.user && parts[2] == s.pass {
				authed = true
				say("235 authenticated")
			} else {
				say("535 bad credentials")
			}
		case strings.HasPrefix(upper, "MAIL FROM:"):
			if !authed {
				say("530 authentication required")
				continue
			}
			cur = captured{from: strings.Trim(line[len("MAIL FROM:"):], " <>")}
			say("250 ok")
		case strings.HasPrefix(upper, "RCPT TO:"):
			cur.to = append(cur.to, strings.Trim(line[len("RCPT TO:"):], " <>"))
			say("250 ok")
		case upper == "DATA":
			say("354 go ahead")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			cur.data = b.String()
			s.mu.Lock()
			s.messages = append(s.messages, cur)
			s.mu.Unlock()
			say("250 queued")
		case upper == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok") // RSET, NOOP
		}
	}
}

func (s *smtpServer) received() []captured {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]captured(nil), s.messages...)
}

// parse splits a captured message into headers and the decoded body.
func parse(t *testing.T, raw string) (*mail.Message, string) {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("the server received something that is not a mail message: %v\n%s", err, raw)
	}
	body := msg.Body
	if strings.EqualFold(msg.Header.Get("Content-Transfer-Encoding"), "quoted-printable") {
		body = quotedprintable.NewReader(msg.Body)
	}
	b, _ := io.ReadAll(body)
	return msg, string(b)
}

func decodeHeader(t *testing.T, v string) string {
	t.Helper()
	out, err := new(mime.WordDecoder).DecodeHeader(v)
	if err != nil {
		t.Fatalf("decoding header %q: %v", v, err)
	}
	return out
}

func TestSendDeliversAnHTMLMessage(t *testing.T) {
	srv, host, port := startSMTP(t, "", "")
	m := mailer.New(host, port, "", "", "App <noreply@example.com>")

	if err := m.Send("ana@example.com", "Welcome", "<h1>Hello</h1><p>caf\u00e9 =)</p>"); err != nil {
		t.Fatal(err)
	}
	got := srv.received()
	if len(got) != 1 {
		t.Fatalf("the server received %d messages, want 1", len(got))
	}
	if got[0].from != "noreply@example.com" || len(got[0].to) != 1 || got[0].to[0] != "ana@example.com" {
		t.Fatalf("envelope = from %q to %v", got[0].from, got[0].to)
	}
	msg, body := parse(t, got[0].data)
	if decodeHeader(t, msg.Header.Get("Subject")) != "Welcome" {
		t.Errorf("Subject = %q", msg.Header.Get("Subject"))
	}
	if !strings.Contains(msg.Header.Get("From"), "noreply@example.com") || !strings.Contains(msg.Header.Get("To"), "ana@example.com") {
		t.Errorf("From = %q, To = %q", msg.Header.Get("From"), msg.Header.Get("To"))
	}
	if !strings.HasPrefix(msg.Header.Get("Content-Type"), "text/html") {
		t.Errorf("Content-Type = %q, want text/html", msg.Header.Get("Content-Type"))
	}
	if !strings.Contains(body, "<h1>Hello</h1>") || !strings.Contains(body, "caf\u00e9 =)") {
		t.Errorf("body was altered on the way (characters and '=' must survive): %q", body)
	}
}

func TestSendEncodesNonASCIISubjects(t *testing.T) {
	srv, host, port := startSMTP(t, "", "")
	m := mailer.New(host, port, "", "", "noreply@example.com")
	if err := m.Send("ana@example.com", "Selamat datang \u2615", "<p>hi</p>"); err != nil {
		t.Fatal(err)
	}
	msg, _ := parse(t, srv.received()[0].data)
	if got := decodeHeader(t, msg.Header.Get("Subject")); got != "Selamat datang \u2615" {
		t.Errorf("Subject = %q", got)
	}
}

// A subject or an address that contains a line break must never add a header
// (for example a hidden Bcc) or a recipient.
func TestSendResistsHeaderInjection(t *testing.T) {
	srv, host, port := startSMTP(t, "", "")
	m := mailer.New(host, port, "", "", "noreply@example.com")

	errSubject := m.Send("ana@example.com", "Hi\r\nBcc: evil@example.com", "<p>x</p>")
	errAddress := m.Send("ana@example.com\r\nBcc: evil@example.com", "Hi", "<p>x</p>")
	errBody := m.Send("ana@example.com", "Hi", "<p>x</p>\r\n.\r\nMAIL FROM:<evil@example.com>\r\nRCPT TO:<evil@example.com>\r\n")

	// Each attempt must either be rejected or neutralised, and the test must have
	// seen at least one of them delivered, otherwise it proves nothing.
	got := srv.received()
	delivered := 3
	for _, err := range []error{errSubject, errAddress, errBody} {
		if err != nil {
			delivered--
		}
	}
	if len(got) != delivered {
		t.Fatalf("%d messages reached the server but %d sends reported success: a message was split or lost", len(got), delivered)
	}
	if errSubject != nil || errBody != nil {
		t.Fatalf("a subject or body that merely contains line breaks must still be sent safely: %v / %v", errSubject, errBody)
	}
	if errAddress == nil {
		t.Errorf("an address containing a line break should be rejected")
	}

	for _, c := range got {
		if c.from != "noreply@example.com" {
			t.Errorf("the envelope sender was hijacked: %q", c.from)
		}
		if len(c.to) != 1 || c.to[0] != "ana@example.com" {
			t.Errorf("the attacker became a recipient: %v", c.to)
		}
		msg, _ := parse(t, c.data)
		for key := range msg.Header {
			if strings.EqualFold(key, "Bcc") {
				t.Errorf("a Bcc header was injected")
			}
		}
	}
}

func TestSendAuthenticates(t *testing.T) {
	srv, host, port := startSMTP(t, "mailer", "correct-password")

	if err := mailer.New(host, port, "mailer", "correct-password", "noreply@example.com").Send("ana@example.com", "Hi", "<p>x</p>"); err != nil {
		t.Fatalf("valid credentials: %v", err)
	}
	if len(srv.received()) != 1 {
		t.Fatalf("the message was not delivered")
	}

	err := mailer.New(host, port, "mailer", "wrong-password", "noreply@example.com").Send("ana@example.com", "Hi", "<p>x</p>")
	if err == nil || !strings.Contains(err.Error(), "aemeath/mailer") {
		t.Fatalf("a wrong password must be an aemeath/mailer error, got %v", err)
	}
	if strings.Contains(err.Error(), "wrong-password") {
		t.Errorf("the password must never appear in an error: %v", err)
	}
	if len(srv.received()) != 1 {
		t.Errorf("a message was delivered with the wrong password")
	}
}

func TestSendReportsAnUnreachableServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close() // nothing listens on this port any more
	err = mailer.New("127.0.0.1", atoi(portStr), "", "", "noreply@example.com").Send("ana@example.com", "Hi", "<p>x</p>")
	if err == nil || !strings.Contains(err.Error(), "aemeath/mailer: failed to send email") {
		t.Fatalf("err = %v", err)
	}
}

func TestPluginProvidesTheMailerToHandlers(t *testing.T) {
	srv, host, port := startSMTP(t, "", "")
	app := aemeath.New()
	if err := app.RegisterPlugin(mailer.New(host, port, "", "", "noreply@example.com")); err != nil {
		t.Fatal(err)
	}
	app.Handle("POST", "/welcome", func(c *aemeath.Ctx) error {
		if err := mailer.From(c).Send("ana@example.com", "Welcome", "<p>hi</p>"); err != nil {
			return err
		}
		return c.JSON(202, "sent")
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("POST", "/welcome", nil))
	if rec.Code != 202 || len(srv.received()) != 1 {
		t.Fatalf("status = %d, delivered = %d", rec.Code, len(srv.received()))
	}
}

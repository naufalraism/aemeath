package mailer_test

import (
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/naufalraism/aemeath/mailer"
)

// What a receiving server and a mail client rely on, written down before the
// message is built without gomail, so both builders answer to the same tests.
func TestMessageIsWellFormed(t *testing.T) {
	srv, host, port := startSMTP(t, "", "")
	m := mailer.New(mailer.Config{Host: host, Port: port, From: "Café Team <noreply@example.com>"})
	long := strings.Repeat("a", 200) + " =" + strings.Repeat("é", 50)
	body := "<p>" + long + "</p>\n.\n<p>end</p>"
	if err := m.Send("Ana Lima <ana@example.com>", "Hi \u2615", body); err != nil {
		t.Fatal(err)
	}
	c := srv.received()[0]
	if len(c.to) != 1 || c.to[0] != "ana@example.com" || c.from != "noreply@example.com" {
		t.Fatalf("envelope = from %q to %v, want the bare addresses", c.from, c.to)
	}
	for _, line := range strings.Split(c.data, "\r\n") {
		if len(line) > 998 {
			t.Fatalf("a line of %d bytes; SMTP allows 998", len(line))
		}
	}
	// Without the SMTPUTF8 extension, headers must be ASCII: names and
	// subjects outside it are encoded (RFC 2047).
	head, _, _ := strings.Cut(c.data, "\r\n\r\n")
	for i := 0; i < len(head); i++ {
		if head[i] >= 0x80 {
			t.Fatalf("raw non-ASCII byte in the headers:\n%s", head)
		}
	}
	msg, got := parse(t, c.data)
	// Mail uses CRLF line breaks, and the test server keeps the dot that SMTP
	// adds before a line starting with "." (dot-stuffing).
	got = strings.TrimSuffix(strings.ReplaceAll(strings.ReplaceAll(got, "\r\n", "\n"), "\n..", "\n."), "\n")
	if got != body {
		t.Errorf("body changed on the way:\n got %q\nwant %q", got, body)
	}
	from, err := mail.ParseAddress(decodeHeader(t, msg.Header.Get("From")))
	if err != nil || from.Name != "Café Team" || from.Address != "noreply@example.com" {
		t.Errorf("From = %q (%v), want the name and address", msg.Header.Get("From"), err)
	}
	if !strings.Contains(msg.Header.Get("To"), "ana@example.com") {
		t.Errorf("To = %q", msg.Header.Get("To"))
	}
	if msg.Header.Get("MIME-Version") != "1.0" {
		t.Errorf("MIME-Version = %q", msg.Header.Get("MIME-Version"))
	}
	if ct := strings.ToLower(msg.Header.Get("Content-Type")); !strings.Contains(ct, "text/html") || !strings.Contains(ct, "utf-8") {
		t.Errorf("Content-Type = %q, want text/html in UTF-8", msg.Header.Get("Content-Type"))
	}
	if d, err := msg.Header.Date(); err != nil || time.Since(d) > time.Minute {
		t.Errorf("Date = %q (%v)", msg.Header.Get("Date"), err)
	}
}

func TestSendRejectsABadFromOrTo(t *testing.T) {
	srv, host, port := startSMTP(t, "", "")
	for _, tc := range []struct{ from, to string }{
		{"not an address", "ana@example.com"},
		{"noreply@example.com", "ana@example.com, eve@example.com"},
	} {
		if err := mailer.New(mailer.Config{Host: host, Port: port, From: tc.from}).Send(tc.to, "Hi", "<p>x</p>"); err == nil {
			t.Errorf("from %q to %q was accepted", tc.from, tc.to)
		}
	}
	if n := len(srv.received()); n != 0 {
		t.Fatalf("%d message(s) delivered", n)
	}
}

// An encoded subject is a run of encoded words; on one line a long subject
// passes SMTP's 998-byte limit and the server refuses the message.
func TestLongSubjectStaysWithinTheLineLimit(t *testing.T) {
	srv, host, port := startSMTP(t, "", "")
	subject := strings.Repeat("Pesanan ☕ ", 120)
	if err := mailer.New(mailer.Config{Host: host, Port: port, From: "noreply@example.com"}).Send("ana@example.com", subject, "<p>x</p>"); err != nil {
		t.Fatal(err)
	}
	c := srv.received()[0]
	for _, line := range strings.Split(c.data, "\r\n") {
		if len(line) > 998 {
			t.Fatalf("a line of %d bytes; SMTP allows 998", len(line))
		}
	}
	msg, _ := parse(t, c.data)
	if got := decodeHeader(t, msg.Header.Get("Subject")); got != subject {
		t.Fatalf("Subject came back as %q", got)
	}
}

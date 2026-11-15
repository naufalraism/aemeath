package securityaudit

import (
	"bufio"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/naufalraism/aemeath/mailer"
)

// fakeSMTP records the recipients and the message of every mail it receives.
type fakeSMTP struct {
	mu    sync.Mutex
	rcpts []string
	data  string
	addr  string
}

func startSMTP(t *testing.T) *fakeSMTP {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	f := &fakeSMTP{addr: l.Addr().String()}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	r := bufio.NewReader(c)
	say := func(s string) { _, _ = c.Write([]byte(s + "\r\n")) }
	say("220 fake")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250 fake")
		case strings.HasPrefix(cmd, "MAIL FROM"):
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO"):
			f.mu.Lock()
			f.rcpts = append(f.rcpts, strings.TrimSpace(line[8:]))
			f.mu.Unlock()
			say("250 ok")
		case cmd == "DATA":
			say("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.data = b.String()
			f.mu.Unlock()
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func (f *fakeSMTP) mailer(t *testing.T) *mailer.Plugin {
	host, port, _ := net.SplitHostPort(f.addr)
	p, _ := strconv.Atoi(port)
	return mailer.New(host, p, "", "", "noreply@app.example")
}

// A signup or "email me a copy" form passes the user's input to Send.
func TestMailer_HeaderInjectionInSubject(t *testing.T) {
	f := startSMTP(t)
	err := f.mailer(t).Send("user@example.com", "Hello\r\nBcc: victim@evil.example", "<p>hi</p>")
	t.Logf("Send with CRLF in the subject: err=%v", err)
	f.mu.Lock()
	defer f.mu.Unlock()
	t.Logf("recipients the server received: %v", f.rcpts)
	hdr, _, _ := strings.Cut(f.data, "\r\n\r\n")
	t.Logf("headers:\n%s", hdr)
	if strings.Contains(strings.ToLower(hdr), "\r\nbcc: victim") || len(f.rcpts) > 1 {
		t.Error("WEAKNESS: a CRLF in the subject injected a header")
	}
}

func TestMailer_RecipientListInjection(t *testing.T) {
	f := startSMTP(t)
	err := f.mailer(t).Send("user@example.com, a@victim.example, b@victim.example", "Welcome", "<p>hi</p>")
	t.Logf("Send(to = list of 3 addresses): err=%v", err)
	f.mu.Lock()
	defer f.mu.Unlock()
	t.Logf("recipients the server received: %v", f.rcpts)
	if len(f.rcpts) > 1 {
		t.Errorf("WEAKNESS: one 'to' value from a form became %d recipients (your server sends mail to anyone the visitor lists)", len(f.rcpts))
	}
}

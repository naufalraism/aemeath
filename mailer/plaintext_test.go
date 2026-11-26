package mailer_test

import (
	"net"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath/mailer"
)

// nonLoopbackIP returns an address of this machine that is not loopback, so a
// test server can stand in for a remote SMTP server.
func nonLoopbackIP(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Skipf("no interface addresses: %v", err)
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			if l, err := net.Listen("tcp", net.JoinHostPort(n.IP.String(), "0")); err == nil {
				_ = l.Close()
				return n.IP.String()
			}
		}
	}
	t.Skip("this machine has no usable non-loopback IPv4 address")
	return ""
}

// Without TLS, anyone on the path reads the message (a password-reset link)
// and the login, and a man in the middle gets there by deleting the STARTTLS
// offer. So a remote server that does not offer STARTTLS is refused before
// any login or message is sent.
func TestRemoteServerWithoutSTARTTLSIsRefused(t *testing.T) {
	ip := nonLoopbackIP(t)
	for name, o := range map[string]fakeOpts{
		"no login":        {listen: ip},
		"CRAM-MD5 login":  {listen: ip, cramOnly: true},
		"AUTH LOGIN only": {listen: ip, loginOnly: true},
	} {
		t.Run(name, func(t *testing.T) {
			f, cfg := startFake(t, o)
			if o.cramOnly || o.loginOnly {
				cfg.Username, cfg.Password = "app", "hunter2"
			}
			err := mailer.New(cfg).Send("user@example.com", "Reset", "<a href=\"https://app.example/reset?t=1\">reset</a>")
			if err == nil || !strings.Contains(err.Error(), "STARTTLS") || !strings.Contains(err.Error(), "AllowPlaintext") {
				t.Fatalf("Send = %v, want an error naming STARTTLS and AllowPlaintext", err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.authTried || f.delivered != 0 {
				t.Fatalf("the server got a login=%v and %d message(s) over plain TCP", f.authTried, f.delivered)
			}
		})
	}
}

func TestAllowPlaintextSendsToATrustedRelay(t *testing.T) {
	ip := nonLoopbackIP(t)
	f, cfg := startFake(t, fakeOpts{listen: ip})
	cfg.AllowPlaintext = true
	if err := mailer.New(cfg).Send("user@example.com", "hi", "<p>hi</p>"); err != nil {
		t.Fatal(err)
	}
	if n, onTLS, _, _ := f.state(); n != 1 || onTLS {
		t.Fatalf("delivered=%d tls=%v, want 1 over plain TCP", n, onTLS)
	}
}

func TestRemoteServerWithSTARTTLSStillWorks(t *testing.T) {
	ip := nonLoopbackIP(t)
	f, cfg := startFake(t, fakeOpts{listen: ip, startTLS: true})
	if err := mailer.New(cfg).Send("user@example.com", "hi", "<p>hi</p>"); err != nil {
		t.Fatal(err)
	}
	if n, onTLS, _, _ := f.state(); n != 1 || !onTLS {
		t.Fatalf("delivered=%d tls=%v, want 1 over TLS", n, onTLS)
	}
}

// A server on this machine is reached without crossing a network, so plain
// TCP needs no opt-in (a local relay, a test server).
func TestLocalServerWithoutSTARTTLSIsAllowed(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "localhost"} {
		f, cfg := startFake(t, fakeOpts{})
		cfg.Host = host
		if err := mailer.New(cfg).Send("user@example.com", "hi", "<p>hi</p>"); err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		if n, _, _, _ := f.state(); n != 1 {
			t.Fatalf("%s: delivered=%d", host, n)
		}
	}
}

package mailer_test

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/naufalraism/aemeath/mailer"
)

type fakeOpts struct {
	silent      bool // accept the connection and never answer
	implicitTLS bool // TLS from the first byte (port 465 style)
	startTLS    bool // offer STARTTLS
	loginOnly   bool // offer AUTH LOGIN only
}

type fakeServer struct {
	mu        sync.Mutex
	delivered int
	sawTLS    bool
	user      string
	pass      string
}

func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func startFake(t *testing.T, o fakeOpts) (*fakeServer, *mailer.Plugin) {
	t.Helper()
	cert := selfSigned(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	f := &fakeServer{}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go f.serve(conn, o, cert)
		}
	}()
	host, port, _ := net.SplitHostPort(l.Addr().String())
	n, _ := strconv.Atoi(port)
	p := mailer.New(host, n, "", "", "noreply@app.example")
	p.Dialer.TLSConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test server with a self-signed certificate
	p.Dialer.SSL = o.implicitTLS
	return f, p
}

func (f *fakeServer) serve(conn net.Conn, o fakeOpts, cert tls.Certificate) {
	defer func() { _ = conn.Close() }()
	if o.silent {
		_, _ = conn.Read(make([]byte, 1)) // hold the connection, say nothing
		return
	}
	onTLS := false
	if o.implicitTLS {
		conn, onTLS = tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}}), true
	}
	r := bufio.NewReader(conn)
	say := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	say("220 fake")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"):
			say("250-fake")
			if o.startTLS && !onTLS {
				say("250-STARTTLS")
			}
			if o.loginOnly {
				say("250 AUTH LOGIN")
			} else {
				say("250 OK")
			}
		case upper == "STARTTLS":
			say("220 go ahead")
			conn, onTLS = tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}}), true
			r = bufio.NewReader(conn)
		case upper == "AUTH LOGIN":
			say("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
			u, _ := r.ReadString('\n')
			say("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
			pw, _ := r.ReadString('\n')
			ub, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(u))
			pb, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(pw))
			f.mu.Lock()
			f.user, f.pass = string(ub), string(pb)
			f.mu.Unlock()
			say("235 ok")
		case strings.HasPrefix(upper, "MAIL FROM"), strings.HasPrefix(upper, "RCPT TO"):
			say("250 ok")
		case upper == "DATA":
			say("354 go")
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
			}
			f.mu.Lock()
			f.delivered++
			f.sawTLS = onTLS
			f.mu.Unlock()
			say("250 queued")
		case upper == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func (f *fakeServer) state() (int, bool, string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.delivered, f.sawTLS, f.user, f.pass
}

// A server that accepts the connection and then stays silent used to block
// Send for ever: gomail sets no deadline and takes no context.
func TestSendGivesUpOnASilentServer(t *testing.T) {
	_, p := startFake(t, fakeOpts{silent: true})
	p.Timeout = 300 * time.Millisecond
	start := time.Now()
	err := p.Send("user@example.com", "hi", "<p>hi</p>")
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want a deadline error", err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("Send took %v with a 300ms timeout", took)
	}
}

func TestSendContextStopsWhenTheContextIsCancelled(t *testing.T) {
	_, p := startFake(t, fakeOpts{silent: true})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	err := p.SendContext(ctx, "user@example.com", "hi", "<p>hi</p>")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("SendContext returned after %v", took)
	}
}

// The delivery code replaced gomail's dialer; it must still speak the same
// TLS modes and the LOGIN mechanism.
func TestDeliveryModes(t *testing.T) {
	for name, o := range map[string]fakeOpts{
		"plain":        {},
		"implicit TLS": {implicitTLS: true},
		"STARTTLS":     {startTLS: true},
	} {
		t.Run(name, func(t *testing.T) {
			f, p := startFake(t, o)
			if err := p.Send("user@example.com", "hi", "<p>hi</p>"); err != nil {
				t.Fatal(err)
			}
			n, onTLS, _, _ := f.state()
			if n != 1 || onTLS != (o.implicitTLS || o.startTLS) {
				t.Fatalf("delivered=%d tls=%v", n, onTLS)
			}
		})
	}
	t.Run("AUTH LOGIN", func(t *testing.T) {
		f, p := startFake(t, fakeOpts{loginOnly: true})
		p.Dialer.Username, p.Dialer.Password = "ann", "s3cret"
		if err := p.Send("user@example.com", "hi", "<p>hi</p>"); err != nil {
			t.Fatal(err)
		}
		if n, _, u, pw := f.state(); n != 1 || u != "ann" || pw != "s3cret" {
			t.Fatalf("delivered=%d user=%q pass=%q", n, u, pw)
		}
	})
}

// Concurrent sends share the Plugin; gomail's dialer wrote to itself on each
// dial, which the race detector flags.
func TestConcurrentSends(t *testing.T) {
	f, p := startFake(t, fakeOpts{loginOnly: true})
	p.Dialer.Username, p.Dialer.Password = "ann", "s3cret"
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.Send("user@example.com", "hi", "<p>hi</p>"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n, _, _, _ := f.state(); n != 8 {
		t.Fatalf("delivered %d of 8", n)
	}
}

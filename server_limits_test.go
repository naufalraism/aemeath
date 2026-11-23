package aemeath_test

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
)

// The limits NewServer promises in its documentation, and that Listen,
// ListenTLS and the generated projects rely on.
func TestNewServerDefaults(t *testing.T) {
	srv := aemeath.New().NewServer(":8080")
	for _, c := range []struct {
		name      string
		got, want any
	}{
		{"Addr", srv.Addr, ":8080"},
		{"ReadHeaderTimeout", srv.ReadHeaderTimeout, 5 * time.Second},
		{"ReadTimeout", srv.ReadTimeout, 15 * time.Second},
		{"WriteTimeout", srv.WriteTimeout, 15 * time.Second},
		{"IdleTimeout", srv.IdleTimeout, 60 * time.Second},
		{"MaxHeaderBytes", srv.MaxHeaderBytes, aemeath.DefaultMaxHeaderBytes},
		{"DefaultMaxHeaderBytes", aemeath.DefaultMaxHeaderBytes, 64 << 10},
	} {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if srv.Handler == nil {
		t.Error("Handler is nil")
	}
}

// serveNewServer runs app.NewServer on a real local port.
func serveNewServer(t *testing.T) string {
	t.Helper()
	app := aemeath.New()
	app.Handle("GET", "/ping", func(c *aemeath.Ctx) error { return c.Text(http.StatusOK, "pong") })
	srv := app.NewServer("")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

func TestNewServerAnswers431ToOversizedHeaders(t *testing.T) {
	addr := serveNewServer(t)
	get := func(headerKiB int) int {
		req, _ := http.NewRequest("GET", "http://"+addr+"/ping", nil)
		req.Header.Set("X-Big", strings.Repeat("a", headerKiB<<10))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%d KiB header: %v", headerKiB, err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if code := get(60); code != http.StatusOK {
		t.Errorf("60 KiB header -> %d, want 200", code)
	}
	// net/http allows 4 KiB on top of MaxHeaderBytes, so go well past it.
	if code := get(80); code != http.StatusRequestHeaderFieldsTooLarge {
		t.Errorf("80 KiB header -> %d, want 431", code)
	}
}

// A client that sends its headers a byte at a time (slowloris) is cut off
// by ReadHeaderTimeout instead of holding the connection open.
func TestNewServerDropsClientsThatSendHeadersSlowly(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the 5s ReadHeaderTimeout")
	}
	addr := serveNewServer(t)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	start := time.Now()
	closed := make(chan time.Duration, 1)
	go func() {
		_, _ = io.Copy(io.Discard, conn) // returns when the server closes the connection
		closed <- time.Since(start)
	}()
	if _, err := io.WriteString(conn, "GET /ping HTTP/1.1\r\nHost: x\r\n"); err != nil {
		t.Fatal(err)
	}
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case took := <-closed:
			if took < 4*time.Second {
				t.Errorf("connection closed after %v, before the 5s ReadHeaderTimeout", took)
			}
			return
		case <-tick.C:
			if time.Since(start) > 9*time.Second {
				t.Fatal("still connected after 9s of a header sent a byte at a time")
			}
			_, _ = io.WriteString(conn, "X") // keep dribbling a header that never ends
		}
	}
}

func TestListenTLSServesHTTPSAndStops(t *testing.T) {
	certFile, pool := writeTestCertificate(t)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	c := startChild(t, 5*time.Second, certFile, client)

	resp, err := client.Get("https://" + c.addr + "/ping")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "pong" || resp.TLS == nil {
		t.Errorf("https GET /ping -> %d %q (TLS %v), want 200 pong over TLS", resp.StatusCode, body, resp.TLS != nil)
	}

	// Plain HTTP to the HTTPS port is refused, not served.
	conn, err := net.Dial("tcp", c.addr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(conn, "GET /ping HTTP/1.1\r\nHost: x\r\n\r\n")
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, _ := bufio.NewReader(conn).ReadString('\n')
	_ = conn.Close()
	if !strings.Contains(line, "400") {
		t.Errorf("plain HTTP to the TLS port -> %q, want a 400", line)
	}

	sendStopSignal(t, c.cmd)
	c.waitExit(t, 10*time.Second)
	if !strings.Contains(c.stdout.String(), "Listen returned: <nil>") {
		t.Errorf("ListenTLS did not return nil after the signal:\n%s\n%s", c.stdout, c.stderr)
	}
}

func TestListenTLSReturnsAnErrorForAMissingCertificate(t *testing.T) {
	dir := t.TempDir()
	done := make(chan error, 1)
	go func() {
		done <- aemeath.New().ListenTLS("127.0.0.1:"+freeLocalPort(t), filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "cert.pem") {
			t.Errorf("ListenTLS with no certificate returned %v, want an error naming cert.pem", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ListenTLS with no certificate did not return")
	}
}

// writeTestCertificate writes a self-signed certificate for 127.0.0.1 to
// cert.pem and key.pem in a temporary folder.
func writeTestCertificate(t *testing.T) (certFile string, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "aemeath test"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	write := func(name, typ string, b []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: b}), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("cert.pem", "CERTIFICATE", der)
	write("key.pem", "PRIVATE KEY", keyDER)
	cert, _ := x509.ParseCertificate(der)
	pool = x509.NewCertPool()
	pool.AddCert(cert)
	return certFile, pool
}

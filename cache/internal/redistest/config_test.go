package redistest

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/naufalraism/aemeath/cache"
)

// tlsRedis starts a Redis that only speaks TLS, with a self-signed
// certificate for 127.0.0.1, and returns a client config that trusts it.
func tlsRedis(t *testing.T) (*miniredis.Miniredis, *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	mr, err := miniredis.RunTLS(&tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mr.Close)
	return mr, &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
}

func roundTrip(p *cache.Plugin) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := p.Client.Set(ctx, "k", "v", time.Minute).Err(); err != nil {
		return err
	}
	_, err := p.Client.Get(ctx, "k").Result()
	return err
}

// Managed Redis services (Upstash, Redis Cloud, ElastiCache with in-transit
// encryption) only accept TLS. cache.New(addr, password, db) had no way to
// turn it on.
func TestNewConnectsOverTLS(t *testing.T) {
	mr, clientTLS := tlsRedis(t)

	p := cache.New(cache.Config{Addr: mr.Addr(), TLS: clientTLS})
	t.Cleanup(func() { _ = p.Close() })
	if err := roundTrip(p); err != nil {
		t.Fatalf("over TLS: %v", err)
	}

	plain := cache.New(cache.Config{Addr: mr.Addr()})
	t.Cleanup(func() { _ = plain.Close() })
	if err := roundTrip(plain); err == nil {
		t.Fatal("a plain connection to a TLS-only server succeeded")
	}
}

// The URL form is what managed services hand out: rediss:// means TLS.
func TestNewFromURL(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.RequireAuth("s3cret")
	p := cache.New(cache.Config{URL: "redis://:s3cret@" + mr.Addr() + "/2"})
	t.Cleanup(func() { _ = p.Close() })
	if err := roundTrip(p); err != nil {
		t.Fatalf("redis:// URL with a password and a DB: %v", err)
	}
	if got := p.Client.Options().DB; got != 2 {
		t.Errorf("DB = %d, want 2 from the URL", got)
	}

	tmr, clientTLS := tlsRedis(t)
	tp := cache.New(cache.Config{URL: "rediss://" + tmr.Addr(), TLS: clientTLS})
	t.Cleanup(func() { _ = tp.Close() })
	if err := roundTrip(tp); err != nil {
		t.Fatalf("rediss:// URL: %v", err)
	}
	if tp.Client.Options().TLSConfig == nil {
		t.Error("rediss:// did not turn TLS on")
	}
}

func TestNewPanicsOnABadURL(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil || !strings.Contains(r.(string), "cache: Config.URL") {
			t.Errorf("recover() = %v, want a panic naming Config.URL", r)
		}
	}()
	cache.New(cache.Config{URL: "http://not-redis"})
}

package jwt_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/naufalraism/aemeath/jwt"
)

type slowProvider struct {
	srv   *httptest.Server
	hits  atomic.Int32
	delay atomic.Int64 // nanoseconds
	down  atomic.Bool
	body  []byte
}

func newSlowProvider(t *testing.T) *slowProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	p := &slowProvider{}
	p.body, _ = json.Marshal(map[string]any{"keys": []any{map[string]any{
		"kty": "RSA", "kid": "k1", "use": "sig",
		"n": enc(key.N.Bytes()), "e": enc(big.NewInt(int64(key.E)).Bytes()),
	}}})
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.hits.Add(1)
		time.Sleep(time.Duration(p.delay.Load()))
		if p.down.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(p.body)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func concurrently(n int, fn func()) time.Duration {
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); fn() }()
	}
	wg.Wait()
	return time.Since(start)
}

// A provider that is down at startup used to be fetched once per incoming
// token, each fetch holding the lock, so requests waited in a single line.
func TestJWKSProviderDownIsFetchedOncePerMinRefresh(t *testing.T) {
	p := newSlowProvider(t)
	p.down.Store(true)
	p.delay.Store(int64(300 * time.Millisecond))
	set, _ := jwt.NewJWKS(p.srv.URL)

	took := concurrently(10, func() {
		if _, _, err := set.Key(context.Background(), "k1"); err == nil {
			t.Error("a key was returned while the provider is down")
		}
	})
	if hits := p.hits.Load(); hits != 1 {
		t.Fatalf("%d fetches for 10 concurrent lookups, want 1", hits)
	}
	if took > time.Second {
		t.Fatalf("10 concurrent lookups took %v: they waited in line", took)
	}
	// Within minRefresh, later lookups fail fast without contacting the provider.
	if _, _, err := set.Key(context.Background(), "k1"); err == nil {
		t.Fatal("expected an error")
	}
	if hits := p.hits.Load(); hits != 1 {
		t.Fatalf("a lookup within minRefresh fetched again (%d fetches)", hits)
	}
}

// A slow refresh (triggered by an unknown kid) must not block lookups of keys
// that are already cached.
func TestJWKSCachedKeysAreNotBlockedByARefresh(t *testing.T) {
	p := newSlowProvider(t)
	set, _ := jwt.NewJWKS(p.srv.URL, jwt.WithJWKSMinRefresh(0))
	if _, _, err := set.Key(context.Background(), "k1"); err != nil {
		t.Fatal(err)
	}
	p.delay.Store(int64(time.Second))

	go func() { _, _, _ = set.Key(context.Background(), "unknown") }() // slow refresh
	time.Sleep(50 * time.Millisecond)

	start := time.Now()
	if _, _, err := set.Key(context.Background(), "k1"); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 200*time.Millisecond {
		t.Fatalf("a cached key took %v while a refresh was running", took)
	}
}

// Concurrent lookups of a new key share one fetch and all get the key.
func TestJWKSConcurrentLookupsShareOneFetch(t *testing.T) {
	p := newSlowProvider(t)
	p.delay.Store(int64(200 * time.Millisecond))
	set, _ := jwt.NewJWKS(p.srv.URL)
	var ok atomic.Int32
	concurrently(20, func() {
		if _, _, err := set.Key(context.Background(), "k1"); err == nil {
			ok.Add(1)
		}
	})
	if ok.Load() != 20 || p.hits.Load() != 1 {
		t.Fatalf("%d/20 lookups succeeded with %d fetches, want 20 with 1", ok.Load(), p.hits.Load())
	}
}

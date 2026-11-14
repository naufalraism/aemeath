// Package session provides server-side sessions with an opaque random id in a cookie.
//
// The cookie carries only the id; data never leaves the server. Unknown ids are
// never adopted (no session fixation), and Renew rotates the id at login.
package session

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/naufalraism/aemeath"
)

// Store persists session data by id.
type Store interface {
	Load(ctx context.Context, id string) (map[string]any, bool, error)
	Save(ctx context.Context, id string, data map[string]any, ttl time.Duration) error
	Delete(ctx context.Context, id string) error
}

// Config configures Middleware.
type Config struct {
	Store      Store
	CookieName string        // default "aemeath_session"
	TTL        time.Duration // default 24h
	Sliding    bool          // extend the TTL on every request
	Insecure   bool          // allow the cookie over plain HTTP (development only)
	SameSite   http.SameSite // default Lax
	Path       string        // default "/"
	Domain     string
}

// Session is the per-request session.
type Session struct {
	cfg     Config
	id      string
	data    map[string]any
	loaded  bool // a stored session was found for the request's id
	dirty   bool
	destroy bool
	renewed bool
	mu      sync.Mutex
}

const stateKey = "aemeath.session"

func newID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("session: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b) // 43 chars
}

func validID(s string) bool { return len(s) == 43 }

// Middleware loads the session before the handler and persists it afterwards.
func Middleware(cfg Config) aemeath.Middleware {
	if cfg.Store == nil {
		panic("session: Config.Store is required")
	}
	if cfg.CookieName == "" {
		cfg.CookieName = "aemeath_session"
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 24 * time.Hour
	}
	if cfg.SameSite == 0 {
		cfg.SameSite = http.SameSiteLaxMode
	}
	if cfg.Path == "" {
		cfg.Path = "/"
	}
	return func(next aemeath.HandlerFunc) aemeath.HandlerFunc {
		return func(c *aemeath.Ctx) error {
			s := &Session{cfg: cfg, data: map[string]any{}}
			if ck, err := c.R.Cookie(cfg.CookieName); err == nil && validID(ck.Value) {
				data, ok, err := cfg.Store.Load(c.Context(), ck.Value)
				if err != nil {
					return err
				}
				if ok {
					s.id, s.data, s.loaded = ck.Value, data, true
				}
			}
			// An id we did not issue is never adopted: s.id stays empty.
			c.State[stateKey] = s
			c.W = &commitWriter{ResponseWriter: c.W, commit: func() { s.commit(c) }}
			err := next(c)
			if cw, ok := c.W.(*commitWriter); ok {
				cw.finish()
			}
			return err
		}
	}
}

// commitWriter persists the session just before headers are sent, so the cookie can be set.
type commitWriter struct {
	http.ResponseWriter
	commit func()
	done   bool
}

func (w *commitWriter) finish() {
	if !w.done {
		w.done = true
		w.commit()
	}
}
func (w *commitWriter) WriteHeader(code int) { w.finish(); w.ResponseWriter.WriteHeader(code) }
func (w *commitWriter) Write(b []byte) (int, error) {
	w.finish()
	return w.ResponseWriter.Write(b)
}
func (w *commitWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *commitWriter) Flush() {
	w.finish()
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Session) cookie(value string, maxAge int) *http.Cookie {
	// #nosec G124 -- Secure is on unless the developer sets Config.Insecure for plain-http localhost.
	return &http.Cookie{
		Name: s.cfg.CookieName, Value: value, Path: s.cfg.Path, Domain: s.cfg.Domain,
		MaxAge: maxAge, HttpOnly: true, Secure: !s.cfg.Insecure, SameSite: s.cfg.SameSite,
	}
}

func (s *Session) commit(c *aemeath.Ctx) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx := context.WithoutCancel(c.Context())
	if s.destroy {
		if s.id != "" {
			_ = s.cfg.Store.Delete(ctx, s.id)
		}
		http.SetCookie(c.W, s.cookie("", -1))
		return
	}
	if !s.dirty && (!s.loaded || !s.cfg.Sliding) {
		return
	}
	if s.id == "" {
		s.id = newID()
	}
	if err := s.cfg.Store.Save(ctx, s.id, s.data, s.cfg.TTL); err != nil {
		return
	}
	http.SetCookie(c.W, s.cookie(s.id, int(s.cfg.TTL.Seconds())))
}

// From returns the request's session; it panics if Middleware is not installed.
func From(c *aemeath.Ctx) *Session {
	s, ok := c.State[stateKey].(*Session)
	if !ok {
		panic("session: Middleware is not installed")
	}
	return s
}

// ID returns the current session id ("" if none has been issued yet).
func (s *Session) ID() string { s.mu.Lock(); defer s.mu.Unlock(); return s.id }

// Set stores a value.
func (s *Session) Set(key string, v any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = v
	s.dirty = true
}

// Get returns a value.
func (s *Session) Get(key string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[key]
	return v, ok
}

// String returns a string value, or "".
func (s *Session) String(key string) string { v, _ := s.Get(key); str, _ := v.(string); return str }

// Int returns an int value, or 0 (also accepts float64 as produced by JSON stores).
func (s *Session) Int(key string) int {
	v, _ := s.Get(key)
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
}

// Delete removes a key.
func (s *Session) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	s.dirty = true
}

// Renew issues a fresh id and deletes the old session, keeping the data.
// Call it at login to defeat session fixation.
func (s *Session) Renew(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.id != "" {
		if err := s.cfg.Store.Delete(ctx, s.id); err != nil {
			return err
		}
	}
	s.id = newID()
	s.dirty, s.renewed = true, true
	return nil
}

// Destroy deletes the session and clears the cookie (logout).
func (s *Session) Destroy() { s.mu.Lock(); defer s.mu.Unlock(); s.destroy = true }

// ErrStoreFull is returned by MemoryStore.Save when the store holds MaxSessions
// live sessions and the id is new. The middleware then sends no cookie, so the
// visitor simply gets no session until older ones expire.
var ErrStoreFull = errors.New("session: memory store is full")

const (
	// DefaultMaxSessions is the live-session cap of a MemoryStore.
	DefaultMaxSessions = 100_000
	sweepEvery         = time.Minute
	fullSweepEvery     = time.Second
)

// MemoryStore is an in-process Store, suitable for development, tests and a
// single small instance. Expired sessions are swept in the background of Save
// (no goroutine) and the number of live sessions is capped, so a flood of
// one-time visitors cannot grow memory without bound. Use a shared store such as
// cache.NewSessionStore (Redis) when you run more than one instance.
type MemoryStore struct {
	mu        sync.Mutex
	m         map[string]entry
	now       func() time.Time
	max       int
	lastSweep time.Time
}

type entry struct {
	data    map[string]any
	expires time.Time
}

// NewMemoryStore creates a MemoryStore capped at DefaultMaxSessions live sessions.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{m: map[string]entry{}, now: time.Now, max: DefaultMaxSessions}
}

// WithClock replaces the clock (tests).
func (s *MemoryStore) WithClock(f func() time.Time) *MemoryStore { s.now = f; return s }

// WithMaxSessions sets the cap on live sessions; n <= 0 keeps DefaultMaxSessions.
func (s *MemoryStore) WithMaxSessions(n int) *MemoryStore {
	if n <= 0 {
		n = DefaultMaxSessions
	}
	s.mu.Lock()
	s.max = n
	s.mu.Unlock()
	return s
}

// Len returns the number of live sessions.
func (s *MemoryStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.m {
		if e.expires.After(s.now()) {
			n++
		}
	}
	return n
}

func copyMap(m map[string]any) map[string]any {
	o := make(map[string]any, len(m))
	for k, v := range m {
		o[k] = v
	}
	return o
}

// sweep drops every expired entry. The caller holds s.mu.
func (s *MemoryStore) sweep(now time.Time) {
	for id, e := range s.m {
		if !e.expires.After(now) {
			delete(s.m, id)
		}
	}
	s.lastSweep = now
}

func (s *MemoryStore) Load(_ context.Context, id string) (map[string]any, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[id]
	if !ok || !e.expires.After(s.now()) {
		delete(s.m, id)
		return nil, false, nil
	}
	return copyMap(e.data), true, nil
}

func (s *MemoryStore) Save(_ context.Context, id string, d map[string]any, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	_, exists := s.m[id]
	// Periodic sweep; when full, sweep again (at most once a second) to make room.
	since := now.Sub(s.lastSweep)
	if since >= sweepEvery || (!exists && len(s.m) >= s.max && since >= fullSweepEvery) {
		s.sweep(now)
	}
	if !exists && len(s.m) >= s.max {
		return ErrStoreFull
	}
	s.m[id] = entry{data: copyMap(d), expires: now.Add(ttl)}
	return nil
}

func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
	return nil
}

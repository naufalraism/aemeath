// Package session provides server-side sessions with an opaque random id in a cookie.
//
// The cookie carries only the id; data never leaves the server. Unknown ids are
// never adopted (no session fixation), and Renew rotates the id at login.
package session

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"reflect"
	"strings"
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
	Store Store
	// CookieName defaults to "__Host-aemeath_session" when the cookie can
	// carry that prefix (Secure, Path "/", no Domain) and to
	// "aemeath_session" otherwise (Insecure, a Domain or another Path). A
	// browser refuses a __Host- cookie set by another subdomain or over plain
	// http, so nobody can plant a session id in it (session fixation).
	CookieName string
	TTL        time.Duration // default 24h
	Sliding    bool          // extend the TTL on every request
	Insecure   bool          // allow the cookie over plain HTTP (development only)
	SameSite   http.SameSite // default Lax
	Path       string        // default "/"
	Domain     string

	// TrustedOrigins are other origins allowed to send POST, PUT, PATCH and
	// DELETE requests with the session cookie, such as a front end on
	// another subdomain: "https://app.example.com". The middleware refuses
	// other cross-origin writes with 403 (see Middleware).
	TrustedOrigins []string
	// AllowCrossOrigin turns that check off. Only for an application that
	// protects every write in another way.
	AllowCrossOrigin bool
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
	ttl     time.Duration // ExpireIn: the lifetime of this save, instead of Config.TTL
	full    *fullLog      // shared by the requests of one Middleware
	// changes are the keys this request set or deleted. A loaded session is
	// saved as these changes applied to what the store holds at the end of
	// the request, so overlapping requests of one visitor do not erase each
	// other's keys.
	changes map[string]change
	mu      sync.Mutex
}

// change is one key set (value) or deleted (deleted) by the request.
type change struct {
	value   any
	deleted bool
}

func (s *Session) record(key string, c change) {
	if s.changes == nil {
		s.changes = map[string]change{}
	}
	s.changes[key] = c
}

const stateKey = "aemeath.session"

// internalPrefix marks session keys owned by the framework (csrf, oidc).
const internalPrefix = "aemeath."

func newID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("session: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b) // 43 chars
}

func validID(s string) bool { return len(s) == 43 }

// Middleware loads the session before the handler and persists it afterwards.
//
// Cookies are sent with requests that a hostile page triggers, so the
// middleware refuses cross-origin writes (POST, PUT, PATCH, DELETE) with
// 403, using net/http.CrossOriginProtection: the browser's Sec-Fetch-Site
// header, or the Origin header on browsers that do not send it. Pages of the
// same origin, reads, and clients that are not browsers (curl, mobile apps,
// webhooks) are not affected. List a front end on another origin in
// Config.TrustedOrigins. csrf.Middleware adds a token check on top.
func Middleware(cfg Config) aemeath.Middleware {
	if cfg.Store == nil {
		panic("session: Config.Store is required")
	}
	crossOrigin := http.NewCrossOriginProtection()
	for _, o := range cfg.TrustedOrigins {
		if err := crossOrigin.AddTrustedOrigin(o); err != nil {
			panic(fmt.Sprintf("session: Config.TrustedOrigins: %v", err))
		}
	}
	full := &fullLog{}
	hostOK := !cfg.Insecure && cfg.Domain == "" && (cfg.Path == "" || cfg.Path == "/")
	switch {
	case cfg.CookieName == "" && hostOK:
		cfg.CookieName = "__Host-aemeath_session"
	case cfg.CookieName == "":
		cfg.CookieName = "aemeath_session"
	case strings.HasPrefix(cfg.CookieName, "__Host-") && !hostOK:
		panic("session: Config.CookieName " + cfg.CookieName + ": a __Host- cookie must be Secure (no Insecure), with Path \"/\" and no Domain, or browsers drop it")
	case strings.HasPrefix(cfg.CookieName, "__Secure-") && cfg.Insecure:
		panic("session: Config.CookieName " + cfg.CookieName + ": a __Secure- cookie must be Secure (no Insecure), or browsers drop it")
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
			if !cfg.AllowCrossOrigin {
				if err := crossOrigin.Check(c.R); err != nil {
					return aemeath.Forbidden("cross-origin request refused; a front end on another origin must be listed in session.Config.TrustedOrigins")
				}
			}
			s := &Session{cfg: cfg, data: map[string]any{}, full: full}
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
			c.Set(stateKey, s)
			cw := &commitWriter{ResponseWriter: c.W, commit: func() error { return s.commit(c) }}
			c.W = cw
			err := next(c)
			if serr := cw.finish(); serr != nil && err == nil && !cw.started {
				// Nothing was sent yet: report the failure instead of answering
				// as if the session had been saved.
				return aemeath.NewError(http.StatusInternalServerError, "internal server error")
			}
			return err
		}
	}
}

// commitWriter persists the session just before headers are sent, so the
// cookie can be set. When saving fails, the response the handler meant to
// send (for example "logged in") is replaced by a 500, because the session it
// relies on does not exist.
type commitWriter struct {
	http.ResponseWriter
	commit  func() error
	done    bool
	err     error
	started bool // the response headers have been sent
	failed  bool // the 500 replaced the handler's response; drop its writes
}

func (w *commitWriter) finish() error {
	if !w.done {
		w.done = true
		w.err = w.commit()
	}
	return w.err
}

func (w *commitWriter) WriteHeader(code int) {
	if w.started {
		return
	}
	w.started = true
	if err := w.finish(); err != nil {
		w.failed = true
		h := w.Header()
		h.Del("Content-Length")
		h.Del("Content-Encoding")
		h.Set("Content-Type", "application/json")
		w.ResponseWriter.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w.ResponseWriter).Encode(map[string]string{"error": "internal server error"})
		return
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *commitWriter) Write(b []byte) (int, error) {
	if !w.started {
		w.WriteHeader(http.StatusOK)
	}
	if w.failed {
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}

func (w *commitWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *commitWriter) Flush() {
	if !w.started {
		w.WriteHeader(http.StatusOK)
	}
	if w.failed {
		return
	}
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

func (s *Session) commit(c *aemeath.Ctx) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx := context.WithoutCancel(c.Context())
	if s.destroy {
		if s.id != "" {
			if err := s.cfg.Store.Delete(ctx, s.id); err != nil {
				c.Logger().Error("aemeath/session: deleting the session failed", "error", err)
				return err
			}
		}
		http.SetCookie(c.W, s.cookie("", -1))
		return nil
	}
	if !s.dirty && (!s.loaded || !s.cfg.Sliding) {
		return nil
	}
	if s.loaded && !s.renewed {
		// Save on top of what is stored now, not of what this request loaded:
		// another request may have changed other keys meanwhile, or ended the
		// session (logout, Renew, expiry), which must not be undone.
		current, ok, err := s.cfg.Store.Load(ctx, s.id)
		if err != nil {
			c.Logger().Error("aemeath/session: reloading the session before saving failed", "error", err)
			return err
		}
		if !ok {
			return nil
		}
		merged := make(map[string]any, len(current)+len(s.changes))
		for k, v := range current {
			merged[k] = v
		}
		for k, c := range s.changes {
			if c.deleted {
				delete(merged, k)
			} else {
				merged[k] = c.value
			}
		}
		s.data = merged
	}
	if s.id == "" {
		s.id = newID()
	}
	ttl := s.cfg.TTL
	if s.ttl > 0 {
		ttl = s.ttl
	}
	if err := s.cfg.Store.Save(ctx, s.id, s.data, ttl); err != nil {
		if errors.Is(err, ErrStoreFull) {
			// A capacity limit, not a failure: the visitor gets no session
			// and the response stays as the handler wrote it. (Answering 500
			// made clients retry requests whose work was already done.)
			s.id = ""
			s.full.note(c.Logger())
			return nil
		}
		c.Logger().Error("aemeath/session: saving the session failed", "error", err)
		return err
	}
	http.SetCookie(c.W, s.cookie(s.id, int(ttl.Seconds())))
	return nil
}

// fullLog counts the visitors refused a session by a full store, so that a
// flood of them logs one line a minute instead of one per request.
type fullLog struct {
	mu      sync.Mutex
	last    time.Time
	dropped int
}

func (f *fullLog) note(logger *slog.Logger) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dropped++
	if !f.last.IsZero() && time.Since(f.last) < time.Minute {
		return
	}
	logger.Warn("aemeath/session: the memory store is full: new visitors get no session (raise WithMaxSessions, or use a shared store such as Redis)", "dropped_since_last_message", f.dropped)
	f.last, f.dropped = time.Now(), 0
}

// From returns the request's session; it panics if Middleware is not installed.
func From(c *aemeath.Ctx) *Session {
	s, ok := c.Value(stateKey).(*Session)
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
	s.record(key, change{value: v})
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

// Int returns an integer value as an int, or 0 when the key is missing or
// does not hold a whole number that fits. Every store gives numbers back as
// float64 on later requests (they are kept as JSON), so prefer Int to a type
// assertion such as s.Get("n").(int), which only works in the request that
// set the value. It accepts every Go integer type and a whole float64.
func (s *Session) Int(key string) int {
	v, _ := s.Get(key)
	switch n := v.(type) {
	case int:
		return n
	case int8, int16, int32, int64:
		i := reflect.ValueOf(n).Int()
		if i < math.MinInt || i > math.MaxInt {
			return 0
		}
		return int(i)
	case uint, uint8, uint16, uint32, uint64, uintptr:
		u := reflect.ValueOf(n).Uint()
		if u > math.MaxInt {
			return 0
		}
		return int(u)
	case float64:
		// float64(math.MaxInt) rounds up to 2^63, which does not fit.
		if n != math.Trunc(n) || n < math.MinInt || n >= math.MaxInt {
			return 0
		}
		return int(n)
	}
	return 0
}

// Delete removes a key.
func (s *Session) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	s.record(key, change{deleted: true})
	s.dirty = true
}

// Renew issues a fresh id and deletes the old session, keeping your data.
// Call it at login to defeat session fixation. Entries the framework keeps
// in the session (keys starting with "aemeath.", such as the CSRF token) are
// dropped, so a token an attacker learned before login stops working.
func (s *Session) Renew(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.id != "" {
		if err := s.cfg.Store.Delete(ctx, s.id); err != nil {
			return err
		}
	}
	for k := range s.data {
		if strings.HasPrefix(k, internalPrefix) {
			delete(s.data, k)
		}
	}
	s.id = newID()
	s.dirty, s.renewed = true, true
	return nil
}

// Login starts a logged-in session: it renews the id (see Renew) and sets
// key to value, for example s.Login(c.Context(), "user_id", user.ID). Use it
// where a login succeeds instead of Set alone: keeping the id lets an
// attacker who planted that id in the victim's browser share the logged-in
// session (session fixation).
func (s *Session) Login(ctx context.Context, key string, value any) error {
	if err := s.Renew(ctx); err != nil {
		return err
	}
	s.Set(key, value)
	return nil
}

// IsNew reports whether the request came without a stored session: the
// session exists only in this request until something is saved in it.
func (s *Session) IsNew() bool { s.mu.Lock(); defer s.mu.Unlock(); return !s.loaded && s.id == "" }

// ExpireIn makes this request save the session with a lifetime of d instead
// of Config.TTL (store entry and cookie). The next request that changes the
// session saves it with Config.TTL again. oidc.Provider.Begin uses it so a
// session that only holds a login in progress lasts as long as the login may
// take, not 24 hours: a crawler hitting /login cannot keep the store full.
func (s *Session) ExpireIn(d time.Duration) { s.mu.Lock(); defer s.mu.Unlock(); s.ttl = d }

// Destroy deletes the session and clears the cookie (logout).
func (s *Session) Destroy() { s.mu.Lock(); defer s.mu.Unlock(); s.destroy = true }

// ErrStoreFull is returned by MemoryStore.Save when the store holds MaxSessions
// live sessions and the id is new. The middleware then sends no cookie, so the
// visitor simply gets no session until older ones expire; the response is
// the handler's, and a line is logged at most once a minute. Existing
// sessions keep working (a Renew during that time loses its session).
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

// entry keeps a session encoded as JSON, as a shared store such as Redis
// does: values come back as encoding/json decodes them (numbers as float64),
// a value JSON cannot encode is refused when saving, and every Load returns
// fresh maps and slices, so two requests of one session never share one.
type entry struct {
	data    []byte
	expires time.Time
}

// NewMemoryStore creates a MemoryStore capped at DefaultMaxSessions live sessions.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{m: map[string]entry{}, now: time.Now, max: DefaultMaxSessions}
}

// WithClock replaces the clock the store uses to expire sessions. It is
// meant for tests, yours included: a test can move the clock past the TTL
// to check that an expired session logs the user out, without waiting.
// Call it before the store is used.
//
//	now := time.Now()
//	store := session.NewMemoryStore().WithClock(func() time.Time { return now })
//	// ... log in ...
//	now = now.Add(25 * time.Hour) // past the TTL: the session is gone
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
	var data map[string]any
	if err := json.Unmarshal(e.data, &data); err != nil {
		return nil, false, nil // cannot happen: Save stored what Marshal wrote
	}
	return data, true, nil
}

func (s *MemoryStore) Save(_ context.Context, id string, d map[string]any, ttl time.Duration) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("session: a value cannot be stored: %w", err)
	}
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
	s.m[id] = entry{data: raw, expires: now.Add(ttl)}
	return nil
}

func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
	return nil
}

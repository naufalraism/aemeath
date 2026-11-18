//go:build !windows

// Package loadtest sends thousands of parallel requests through a real
// server (TCP, app.Serve, SIGTERM) with sessions, a rate limiter and a job
// runner, and checks the results, the goroutines and the heap. Run it with
// -race. AEMEATH_LOAD=full makes it larger.
package loadtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/jobs"
	"github.com/naufalraism/aemeath/session"
)

type workOut struct {
	Count int `json:"count"`
}

type counterJob struct{ done *atomic.Int64 }

func (j counterJob) Run(ctx context.Context) error {
	select {
	case <-time.After(200 * time.Microsecond): // a little work
	case <-ctx.Done():
		return ctx.Err()
	}
	j.done.Add(1)
	return nil
}

type stats struct {
	ok, limited, queueFull, other atomic.Int64
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func heapInUse() uint64 {
	var ms runtime.MemStats
	for i := 0; i < 3; i++ {
		runtime.GC()
	}
	runtime.ReadMemStats(&ms)
	return ms.HeapInuse
}

// settledGoroutines waits until the number of goroutines stops falling.
func settledGoroutines(limit int, d time.Duration) int {
	deadline := time.Now().Add(d)
	n := runtime.NumGoroutine()
	for time.Now().Before(deadline) {
		if n <= limit {
			return n
		}
		time.Sleep(50 * time.Millisecond)
		n = runtime.NumGoroutine()
	}
	return n
}

func aemeathStacks() string {
	var buf bytes.Buffer
	_ = pprof.Lookup("goroutine").WriteTo(&buf, 1)
	var out []string
	for _, g := range strings.Split(buf.String(), "\n\n") {
		if strings.Contains(g, "naufalraism/aemeath") && !strings.Contains(g, "loadtest.Test") {
			out = append(out, g)
		}
	}
	return strings.Join(out, "\n\n")
}

func TestLoad(t *testing.T) {
	users, perUser, rounds := 100, 20, 4
	if os.Getenv("AEMEATH_LOAD") == "full" {
		users, perUser, rounds = 300, 40, 8
	}
	if testing.Short() {
		t.Skip("load test")
	}

	baseline := runtime.NumGoroutine()
	var jobsDone atomic.Int64

	sessionCap := users * (rounds + 2)
	store := session.NewMemoryStore().WithMaxSessions(sessionCap)
	runner := jobs.New(jobs.WithWorkers(8), jobs.WithQueueSize(256))
	app := aemeath.New()
	if err := app.RegisterPlugin(runner); err != nil {
		t.Fatal(err)
	}
	// Each virtual user sends its own address; RealIP trusts loopback, so the
	// global limiter keeps one entry per user, as behind a proxy.
	app.Use(aemeath.RealIP("127.0.0.1/32"), aemeath.RateLimiter(100000, 2*time.Second),
		session.Middleware(session.Config{Store: store, Insecure: true}))
	app.EnableHealth()
	aemeath.POST(app, "/work", func(c *aemeath.Ctx, _ struct{}) (workOut, error) {
		if err := jobs.From(c).Enqueue(counterJob{&jobsDone}); err != nil {
			if errors.Is(err, jobs.ErrQueueFull) {
				return workOut{}, aemeath.NewError(http.StatusServiceUnavailable, "queue full")
			}
			return workOut{}, err
		}
		s := session.From(c)
		n := s.Int("n") + 1
		s.Set("n", n)
		return workOut{Count: n}, nil
	})
	limited := app.Group("/limited", aemeath.RateLimiter(5, time.Minute))
	limited.Handle("GET", "/", func(c *aemeath.Ctx) error { return c.Text(200, "ok") })

	addr := freeAddr(t)
	srv := app.NewServer(addr)
	// Our own Notify keeps SIGTERM from killing the test process even before
	// app.Serve has registered its handler.
	keep := make(chan os.Signal, 1)
	signal.Notify(keep, syscall.SIGTERM)
	defer signal.Stop(keep)
	served := make(chan error, 1)
	go func() { served <- app.Serve(srv) }()

	base := "http://" + addr
	transport := &http.Transport{MaxIdleConns: users * 2, MaxIdleConnsPerHost: users * 2, IdleConnTimeout: 30 * time.Second}
	plain := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	for i := 0; ; i++ {
		if r, err := plain.Get(base + "/healthz"); err == nil {
			_ = r.Body.Close()
			break
		}
		if i > 200 {
			t.Fatal("the server did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}

	type user struct {
		ip     string
		client *http.Client
		ok     int // successful /work requests in its session
	}
	newUsers := func(round int) []*user {
		us := make([]*user, users)
		for i := range us {
			jar, _ := cookiejar.New(nil)
			us[i] = &user{ip: fmt.Sprintf("10.%d.%d.%d", round, i/250, i%250+1), client: &http.Client{Transport: transport, Jar: jar, Timeout: 30 * time.Second}}
		}
		return us
	}
	quiet := false
	do := func(u *user, method, path string, st *stats) (int, []byte) {
		req, _ := http.NewRequest(method, base+path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", u.ip)
		resp, err := u.client.Do(req)
		if err != nil {
			st.other.Add(1)
			t.Errorf("%s %s: %v", method, path, err)
			return 0, nil
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		switch resp.StatusCode {
		case 200:
			st.ok.Add(1)
		case 429:
			st.limited.Add(1)
		case 503:
			st.queueFull.Add(1)
		default:
			st.other.Add(1)
			if !quiet {
				t.Errorf("%s %s: %d %s", method, path, resp.StatusCode, body)
			}
		}
		return resp.StatusCode, body
	}

	// round runs every user in parallel; each user sends its requests one
	// after the other (a browser tab), so its session counter must be exact.
	round := func(us []*user) (*stats, time.Duration) {
		st := &stats{}
		var wg sync.WaitGroup
		start := time.Now()
		for _, u := range us {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for k := 0; k < perUser; k++ {
					code, body := do(u, "POST", "/work", st)
					if code == 200 {
						var out workOut
						_ = json.Unmarshal(body, &out)
						u.ok++
						if out.Count != u.ok {
							t.Errorf("user %s: session counter %d after %d successful requests", u.ip, out.Count, u.ok)
						}
					}
				}
				for k := 0; k < 8; k++ {
					do(u, "GET", "/limited", st)
				}
			}()
		}
		wg.Wait()
		return st, time.Since(start)
	}

	type sample struct {
		heap       uint64
		goroutines int
		sessions   int
	}
	var samples []sample
	same := newUsers(0)
	var enqueued int64
	for r := 0; r < rounds; r++ {
		us := same
		if r >= rounds/2 {
			us = newUsers(r) // second half: only new visitors (new sessions, new limiter keys)
		}
		st, took := round(us)
		enqueued += st.ok.Load() - int64(len(us)*5) // 200s from /work (each user also got 5 from /limited)
		transport.CloseIdleConnections()
		time.Sleep(2100 * time.Millisecond) // past the global limiter's window
		s := sample{heap: heapInUse(), goroutines: runtime.NumGoroutine(), sessions: store.Len()}
		samples = append(samples, s)
		n := len(us) * (perUser + 8)
		t.Logf("round %d (%s users): %d requests in %v (%.0f/s): 200=%d 429=%d 503=%d other=%d | heap %.1f MB, goroutines %d, sessions %d, jobs done %d",
			r+1, map[bool]string{true: "same", false: "new"}[r < rounds/2], n, took.Round(time.Millisecond), float64(n)/took.Seconds(),
			st.ok.Load(), st.limited.Load(), st.queueFull.Load(), st.other.Load(), float64(s.heap)/(1<<20), s.goroutines, s.sessions, jobsDone.Load())
		// /limited allows 5 requests per user per minute. New users: 5 of 8
		// pass. The same users again within the minute: none pass.
		want := int64(len(us) * 3)
		if r > 0 && r < rounds/2 {
			want = int64(len(us) * 8)
		}
		if st.limited.Load() != want {
			t.Errorf("round %d: %d responses 429 from /limited, want %d", r+1, st.limited.Load(), want)
		}
		if r > 0 && r < rounds/2 {
			enqueued += int64(len(us) * 5) // none of /limited passed this round
		}
	}

	// Memory with the same users must be flat once warm; with new visitors it
	// may grow only up to the session cap.
	if half := rounds / 2; half >= 2 {
		warm, last := samples[1].heap, samples[half-1].heap
		if last > warm+4<<20 {
			t.Errorf("same users: heap grew from %.1f MB to %.1f MB between round 2 and %d", float64(warm)/(1<<20), float64(last)/(1<<20), half)
		}
	}
	for r := 1; r < len(samples); r++ {
		if d := samples[r].goroutines - samples[0].goroutines; d > 2 || d < -2 {
			t.Errorf("round %d: %d goroutines while idle, round 1 had %d", r+1, samples[r].goroutines, samples[0].goroutines)
		}
	}

	// A flood of one-time visitors (no cookie kept), 20 times the session cap:
	// the store must stop at its cap and the heap with it.
	before := heapInUse()
	flood := sessionCap * 20
	quiet = true
	var fst stats
	var fwg sync.WaitGroup
	sem := make(chan struct{}, users)
	start := time.Now()
	for i := 0; i < flood; i++ {
		fwg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; fwg.Done() }()
			do(&user{ip: fmt.Sprintf("172.16.%d.%d", i/250%250, i%250+1), client: plain}, "POST", "/work", &fst)
		}()
	}
	fwg.Wait()
	took := time.Since(start)
	transport.CloseIdleConnections()
	time.Sleep(2100 * time.Millisecond)
	after := heapInUse()
	enqueued += fst.ok.Load()
	t.Logf("flood: %d one-time visitors in %v (%.0f/s): 200=%d 503=%d other=%d | sessions %d (cap %d), heap %.1f MB -> %.1f MB, goroutines %d",
		flood, took.Round(time.Millisecond), float64(flood)/took.Seconds(), fst.ok.Load(), fst.queueFull.Load(), fst.other.Load(),
		store.Len(), sessionCap, float64(before)/(1<<20), float64(after)/(1<<20), runtime.NumGoroutine())
	quiet = false
	if store.Len() > sessionCap {
		t.Errorf("the session store holds %d sessions, above its cap of %d", store.Len(), sessionCap)
	}
	if fst.other.Load() > 0 {
		t.Errorf("%d of %d one-time visitors got another answer than 200 or 503 once the session store was full (the store's cap is the documented limit, not an error)", fst.other.Load(), flood)
	}
	// Visitors who already have a session are not affected by the flood.
	var est stats
	for _, u := range same[:10] {
		if code, _ := do(u, "POST", "/work", &est); code == 200 {
			u.ok++
			enqueued++
		}
	}
	t.Logf("after the flood, 10 existing users: 200=%d other=%d", est.ok.Load(), est.other.Load()+est.queueFull.Load())
	if est.ok.Load() != 10 {
		t.Errorf("existing sessions were affected by the flood: %d of 10 requests succeeded", est.ok.Load())
	}
	if after > before+16<<20 {
		t.Errorf("the flood grew the heap by %.1f MB", float64(after-before)/(1<<20))
	}

	// Graceful shutdown: SIGTERM, as Docker or Kubernetes would send.
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the server did not stop within 20 s of SIGTERM")
	}
	st := runner.Stats()
	t.Logf("after shutdown: jobs %+v, run to the end %d, accepted by /work %d", st, jobsDone.Load(), enqueued)
	if st.Enqueued != enqueued || st.Succeeded != jobsDone.Load() || st.Succeeded+st.Failed != st.Enqueued {
		t.Errorf("jobs: %d accepted by /work, runner says %+v, %d ran to the end", enqueued, st, jobsDone.Load())
	}

	transport.CloseIdleConnections()
	left := settledGoroutines(baseline+2, 10*time.Second)
	t.Logf("goroutines: %d before the server, %d after shutdown", baseline, left)
	if left > baseline+2 {
		t.Errorf("%d goroutines left after shutdown (baseline %d):\n%s", left, baseline, aemeathStacks())
	}
}

type slowJob struct{ done *atomic.Int64 }

func (j slowJob) Run(ctx context.Context) error {
	select {
	case <-time.After(20 * time.Millisecond):
		j.done.Add(1)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// A small queue and slow jobs: requests get 503 when the queue is full, the
// app shuts down with jobs still queued, and nothing is left running.
func TestLoadQueueFullAndShutdown(t *testing.T) {
	if testing.Short() {
		t.Skip("load test")
	}
	baseline := runtime.NumGoroutine()
	var done atomic.Int64
	runner := jobs.New(jobs.WithWorkers(2), jobs.WithQueueSize(16))
	app := aemeath.New()
	if err := app.RegisterPlugin(runner); err != nil {
		t.Fatal(err)
	}
	app.EnableHealth()
	aemeath.POST(app, "/slow", func(c *aemeath.Ctx, _ struct{}) (struct{}, error) {
		if err := jobs.From(c).Enqueue(slowJob{&done}); err != nil {
			return struct{}{}, aemeath.NewError(http.StatusServiceUnavailable, err.Error())
		}
		return struct{}{}, nil
	})
	addr := freeAddr(t)
	keep := make(chan os.Signal, 1)
	signal.Notify(keep, syscall.SIGTERM)
	defer signal.Stop(keep)
	served := make(chan error, 1)
	go func() { served <- app.Serve(app.NewServer(addr)) }()
	transport := &http.Transport{MaxIdleConnsPerHost: 64}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	for i := 0; ; i++ {
		if r, err := client.Get("http://" + addr + "/healthz"); err == nil {
			_ = r.Body.Close()
			break
		}
		if i > 200 {
			t.Fatal("the server did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var ok, full, other atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 30; k++ {
				resp, err := client.Post("http://"+addr+"/slow", "application/json", strings.NewReader("{}"))
				if err != nil {
					other.Add(1)
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				switch resp.StatusCode {
				case 200:
					ok.Add(1)
				case 503:
					full.Add(1)
				default:
					other.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	queued := runner.Stats().Queued
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the server did not stop within 20 s of SIGTERM")
	}
	st := runner.Stats()
	t.Logf("1920 requests: 200=%d 503=%d other=%d | queued at SIGTERM %d | after shutdown %+v, ran %d", ok.Load(), full.Load(), other.Load(), queued, st, done.Load())
	if full.Load() == 0 {
		t.Error("the queue never filled up: the test did not reach ErrQueueFull")
	}
	if other.Load() != 0 || st.Enqueued != ok.Load() || st.Succeeded != done.Load() || st.Succeeded+st.Failed != st.Enqueued {
		t.Errorf("accepted %d, runner %+v, ran %d, other responses %d", ok.Load(), st, done.Load(), other.Load())
	}
	transport.CloseIdleConnections()
	left := settledGoroutines(baseline+2, 10*time.Second)
	t.Logf("goroutines: %d before, %d after shutdown", baseline, left)
	if left > baseline+2 {
		t.Errorf("%d goroutines left after shutdown (baseline %d):\n%s", left, baseline, aemeathStacks())
	}
}

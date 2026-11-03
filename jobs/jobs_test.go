package jobs_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/jobs"
)

func quietLogger() (*slog.Logger, *lockedBuf) {
	b := &lockedBuf{}
	return slog.New(slog.NewTextHandler(b, nil)), b
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

func fast() jobs.Option { return jobs.WithBackoff(func(int) time.Duration { return time.Millisecond }) }

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func shutdown(t *testing.T, r *jobs.Runner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestRunsJobsOnAPoolOfWorkers(t *testing.T) {
	l, _ := quietLogger()
	r := jobs.New(jobs.WithWorkers(3), jobs.WithLogger(l))
	r.Start()

	var running, peak, done atomic.Int32
	release := make(chan struct{})
	for i := 0; i < 6; i++ {
		if err := r.Enqueue(jobs.Func(func(context.Context) error {
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			<-release
			running.Add(-1)
			done.Add(1)
			return nil
		})); err != nil {
			t.Fatal(err)
		}
	}
	waitUntil(t, "three jobs to run at once", func() bool { return running.Load() == 3 })
	close(release)
	waitUntil(t, "all six jobs", func() bool { return done.Load() == 6 })
	if peak.Load() != 3 {
		t.Fatalf("peak concurrency = %d, want 3 (the number of workers)", peak.Load())
	}
	shutdown(t, r)
	if s := r.Stats(); s.Enqueued != 6 || s.Succeeded != 6 || s.Failed != 0 {
		t.Fatalf("stats = %+v", s)
	}
}

func TestRetriesAFailingJobUntilItSucceeds(t *testing.T) {
	l, logs := quietLogger()
	r := jobs.New(jobs.WithWorkers(1), jobs.WithRetries(3), fast(), jobs.WithLogger(l))
	r.Start()
	var attempts atomic.Int32
	_ = r.Enqueue(jobs.Func(func(context.Context) error {
		if attempts.Add(1) < 3 {
			return errors.New("temporary")
		}
		return nil
	}))
	waitUntil(t, "the job to succeed", func() bool { return r.Stats().Succeeded == 1 })
	shutdown(t, r)
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
	if s := r.Stats(); s.Retried != 2 || s.Failed != 0 {
		t.Fatalf("stats = %+v", s)
	}
	if !strings.Contains(logs.String(), "will retry") {
		t.Fatalf("the retries were not logged: %s", logs.String())
	}
}

func TestGivesUpAfterTheLastAttemptAndLogsIt(t *testing.T) {
	l, logs := quietLogger()
	r := jobs.New(jobs.WithWorkers(1), jobs.WithRetries(2), fast(), jobs.WithLogger(l))
	r.Start()
	var attempts atomic.Int32
	_ = r.Enqueue(jobs.Func(func(context.Context) error { attempts.Add(1); return errors.New("always broken") }))
	waitUntil(t, "the job to be given up on", func() bool { return r.Stats().Failed == 1 })
	shutdown(t, r)
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3 (1 + 2 retries)", attempts.Load())
	}
	if !strings.Contains(logs.String(), "job failed") || !strings.Contains(logs.String(), "always broken") {
		t.Fatalf("the final failure was not logged: %s", logs.String())
	}
}

func TestNoRetriesWhenDisabled(t *testing.T) {
	l, _ := quietLogger()
	r := jobs.New(jobs.WithRetries(0), jobs.WithLogger(l))
	r.Start()
	var attempts atomic.Int32
	_ = r.Enqueue(jobs.Func(func(context.Context) error { attempts.Add(1); return errors.New("x") }))
	waitUntil(t, "the failure", func() bool { return r.Stats().Failed == 1 })
	shutdown(t, r)
	if attempts.Load() != 1 {
		t.Fatalf("attempts = %d, want 1", attempts.Load())
	}
}

func TestAPanickingJobDoesNotKillTheWorker(t *testing.T) {
	l, logs := quietLogger()
	r := jobs.New(jobs.WithWorkers(1), jobs.WithRetries(0), jobs.WithLogger(l))
	r.Start()
	_ = r.Enqueue(jobs.Func(func(context.Context) error { panic("kaboom") }))
	var ran atomic.Bool
	_ = r.Enqueue(jobs.Func(func(context.Context) error { ran.Store(true); return nil }))
	waitUntil(t, "the job after the panic to run", ran.Load)
	shutdown(t, r)
	if !strings.Contains(logs.String(), "kaboom") {
		t.Fatalf("the panic was not logged: %s", logs.String())
	}
}

func TestEnqueueDoesNotBlockWhenTheQueueIsFull(t *testing.T) {
	l, _ := quietLogger()
	r := jobs.New(jobs.WithWorkers(1), jobs.WithQueueSize(1), jobs.WithLogger(l))
	r.Start()
	started, release := make(chan struct{}), make(chan struct{})
	_ = r.Enqueue(jobs.Func(func(context.Context) error { close(started); <-release; return nil }))
	<-started // the only worker is busy
	if err := r.Enqueue(jobs.Func(func(context.Context) error { return nil })); err != nil {
		t.Fatalf("the first waiting job must fit: %v", err)
	}
	begin := time.Now()
	err := r.Enqueue(jobs.Func(func(context.Context) error { return nil }))
	if !errors.Is(err, jobs.ErrQueueFull) || time.Since(begin) > time.Second {
		t.Fatalf("err = %v after %v, want an immediate ErrQueueFull", err, time.Since(begin))
	}
	close(release)
	shutdown(t, r)
}

func TestShutdownFinishesRunningAndQueuedJobsThenRefusesNewOnes(t *testing.T) {
	l, _ := quietLogger()
	r := jobs.New(jobs.WithWorkers(1), jobs.WithLogger(l))
	r.Start()
	var done atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	_ = r.Enqueue(jobs.Func(func(context.Context) error { close(started); <-release; done.Add(1); return nil }))
	<-started
	for i := 0; i < 3; i++ {
		_ = r.Enqueue(jobs.Func(func(context.Context) error { done.Add(1); return nil }))
	}

	finished := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		finished <- r.Shutdown(ctx)
	}()
	waitUntil(t, "Shutdown to refuse new jobs", func() bool {
		return errors.Is(r.Enqueue(jobs.Func(func(context.Context) error { return nil })), jobs.ErrClosed)
	})
	select {
	case <-finished:
		t.Fatal("Shutdown returned while a job was still running")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if done.Load() != 4 {
		t.Fatalf("finished %d jobs, want all 4 (running + queued)", done.Load())
	}
	shutdown(t, r) // calling it again is harmless
}

func TestShutdownDeadlineCancelsJobsThatIgnoreTheDeadlineButHonorTheirContext(t *testing.T) {
	l, _ := quietLogger()
	r := jobs.New(jobs.WithWorkers(1), jobs.WithRetries(0), jobs.WithLogger(l))
	r.Start()
	started, cancelled := make(chan struct{}), make(chan struct{})
	_ = r.Enqueue(jobs.Func(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return ctx.Err()
	}))
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := r.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("the job's context was not cancelled when the deadline passed")
	}
}

func TestShutdownDropsAJobThatIsWaitingToRetry(t *testing.T) {
	l, logs := quietLogger()
	r := jobs.New(jobs.WithWorkers(1), jobs.WithRetries(5), jobs.WithBackoff(func(int) time.Duration { return time.Hour }), jobs.WithLogger(l))
	r.Start()
	_ = r.Enqueue(jobs.Func(func(context.Context) error { return errors.New("nope") }))
	waitUntil(t, "the first failure", func() bool { return strings.Contains(logs.String(), "will retry") })

	begin := time.Now()
	shutdown(t, r) // must not wait an hour
	if time.Since(begin) > 2*time.Second {
		t.Fatal("Shutdown waited for a retry that was an hour away")
	}
	if !strings.Contains(logs.String(), "dropped at shutdown") || r.Stats().Failed != 1 {
		t.Fatalf("the dropped job was not reported: %s %+v", logs.String(), r.Stats())
	}
}

func TestEveryRunsPeriodicallyAndStopsAtShutdown(t *testing.T) {
	l, _ := quietLogger()
	r := jobs.New(jobs.WithLogger(l))
	r.Start()
	var runs atomic.Int32
	if err := r.Every(5*time.Millisecond, jobs.Func(func(context.Context) error { runs.Add(1); return nil })); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "three periodic runs", func() bool { return runs.Load() >= 3 })
	shutdown(t, r)
	after := runs.Load()
	time.Sleep(40 * time.Millisecond)
	if runs.Load() != after {
		t.Fatal("the periodic job kept running after Shutdown")
	}
	if err := r.Every(time.Second, jobs.Func(func(context.Context) error { return nil })); !errors.Is(err, jobs.ErrClosed) {
		t.Fatalf("Every after Shutdown: err = %v, want ErrClosed", err)
	}
}

func TestEveryDoesNotPileUpBehindASlowJob(t *testing.T) {
	l, _ := quietLogger()
	r := jobs.New(jobs.WithWorkers(4), jobs.WithQueueSize(50), jobs.WithLogger(l))
	r.Start()
	var started atomic.Int32
	release := make(chan struct{})
	_ = r.Every(2*time.Millisecond, jobs.Func(func(context.Context) error { started.Add(1); <-release; return nil }))
	time.Sleep(60 * time.Millisecond) // ~30 ticks while the first run is stuck
	if started.Load() != 1 || r.Stats().Queued != 0 {
		t.Fatalf("started = %d, queued = %d: ticks must be skipped while the previous run is unfinished", started.Load(), r.Stats().Queued)
	}
	close(release)
	shutdown(t, r)
}

func TestInvalidOptionsPanic(t *testing.T) {
	for name, f := range map[string]func(){
		"zero workers":   func() { jobs.New(jobs.WithWorkers(0)) },
		"zero queue":     func() { jobs.New(jobs.WithQueueSize(0)) },
		"negative retry": func() { jobs.New(jobs.WithRetries(-1)) },
		"zero interval":  func() { _ = jobs.New().Every(0, jobs.Func(func(context.Context) error { return nil })) },
		"nil backoff":    func() { jobs.New(jobs.WithBackoff(nil)) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: expected a panic", name)
				}
			}()
			f()
		}()
	}
	if err := jobs.New().Enqueue(nil); err == nil {
		t.Error("a nil job must be rejected")
	}
}

func TestExponentialBackoffIsCapped(t *testing.T) {
	b := jobs.Exponential(time.Second)
	for attempt, want := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 7: time.Minute, 40: time.Minute, 0: time.Second} {
		if got := b(attempt); got != want {
			t.Errorf("attempt %d: %v, want %v", attempt, got, want)
		}
	}
}

func TestRunnerAsAPlugin(t *testing.T) {
	l, _ := quietLogger()
	r := jobs.New(jobs.WithLogger(l))
	app := aemeath.New()
	if err := app.RegisterPlugin(r); err != nil {
		t.Fatal(err)
	}
	ran := make(chan struct{})
	app.Handle("POST", "/work", func(c *aemeath.Ctx) error {
		if err := jobs.From(c).Enqueue(jobs.Func(func(context.Context) error { close(ran); return nil })); err != nil {
			return err
		}
		return c.JSON(202, "queued")
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("POST", "/work", nil))
	if rec.Code != 202 {
		t.Fatalf("status = %d", rec.Code)
	}
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("the job enqueued from a handler never ran (the plugin must start the workers)")
	}
	shutdown(t, r)
}

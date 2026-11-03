// Package jobs runs background work inside the application process: a bounded
// queue, a pool of workers, retries with backoff, and periodic jobs.
//
// It is meant for fire-and-forget work such as sending an email or resizing an
// image. The queue lives in memory, so queued jobs are lost if the process
// crashes: use a durable queue when a job must never be lost.
//
//	runner := jobs.New(jobs.WithWorkers(4))
//	app.RegisterPlugin(runner)                       // starts the workers, stops them on shutdown
//
//	// in a handler:
//	err := jobs.From(c).Enqueue(&SendWelcomeEmail{To: user.Email})
//
//	// somewhere at startup:
//	runner.Every(time.Hour, &CleanupExpiredSessions{})
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/naufalraism/aemeath"
)

// Job is a unit of background work. ctx is cancelled when the runner is forced
// to stop, so long jobs should return when it is done.
type Job interface {
	Run(ctx context.Context) error
}

// Func adapts a function to a Job.
type Func func(ctx context.Context) error

// Run implements Job.
func (f Func) Run(ctx context.Context) error { return f(ctx) }

var (
	// ErrQueueFull is returned by Enqueue when the queue has no room left.
	ErrQueueFull = errors.New("jobs: the queue is full")
	// ErrClosed is returned after Shutdown.
	ErrClosed = errors.New("jobs: the runner is shut down")
)

// Stats are counters since the runner was created.
type Stats struct {
	Enqueued  int64 // accepted by Enqueue (and by Every ticks)
	Succeeded int64
	Failed    int64 // gave up after the last attempt, or dropped at shutdown
	Retried   int64 // attempts after the first
	Queued    int   // waiting right now
}

// Option configures a Runner.
type Option func(*Runner)

// WithWorkers sets how many jobs run at the same time (default 4).
func WithWorkers(n int) Option { return func(r *Runner) { r.workers = n } }

// WithQueueSize sets how many jobs can wait (default 256).
func WithQueueSize(n int) Option { return func(r *Runner) { r.queueSize = n } }

// WithRetries sets how many extra attempts a failing job gets (default 2, so 3 attempts in total).
func WithRetries(n int) Option { return func(r *Runner) { r.retries = n } }

// WithBackoff sets the wait before retry number attempt (1 for the first retry).
func WithBackoff(fn func(attempt int) time.Duration) Option {
	return func(r *Runner) { r.backoff = fn }
}

// WithLogger sets the logger (default slog.Default()).
func WithLogger(l *slog.Logger) Option { return func(r *Runner) { r.logger = l } }

// Exponential waits base, 2*base, 4*base, ... up to one minute.
func Exponential(base time.Duration) func(attempt int) time.Duration {
	return func(attempt int) time.Duration {
		if attempt < 1 {
			attempt = 1
		}
		if attempt > 20 {
			return time.Minute
		}
		d := base << (attempt - 1)
		if d <= 0 || d > time.Minute {
			return time.Minute
		}
		return d
	}
}

type task struct{ job Job }

// Runner is a queue with a pool of workers. Use New.
type Runner struct {
	workers   int
	queueSize int
	retries   int
	backoff   func(attempt int) time.Duration
	logger    *slog.Logger

	queue   chan *task
	baseCtx context.Context
	cancel  context.CancelFunc
	stopCh  chan struct{} // closed by Shutdown: stops tickers and aborts retries that are waiting

	mu           sync.RWMutex // guards closed, and sends on queue
	closed       bool
	startOnce    sync.Once
	shutdownOnce sync.Once
	workersWG    sync.WaitGroup
	tickersWG    sync.WaitGroup

	enqueued, succeeded, failed, retried atomic.Int64
}

// New creates a Runner. Call Start (or register it as a plugin) to run jobs.
func New(opts ...Option) *Runner {
	r := &Runner{workers: 4, queueSize: 256, retries: 2, backoff: Exponential(time.Second)}
	for _, o := range opts {
		o(r)
	}
	if r.workers < 1 || r.queueSize < 1 || r.retries < 0 || r.backoff == nil {
		panic("aemeath/jobs: WithWorkers and WithQueueSize need a value of at least 1, WithRetries cannot be negative, and WithBackoff cannot be nil")
	}
	if r.logger == nil {
		r.logger = slog.Default()
	}
	r.queue = make(chan *task, r.queueSize)
	r.stopCh = make(chan struct{})
	r.baseCtx, r.cancel = context.WithCancel(context.Background())
	return r
}

// Start launches the workers. It is safe to call more than once.
func (r *Runner) Start() {
	r.startOnce.Do(func() {
		for i := 0; i < r.workers; i++ {
			r.workersWG.Add(1)
			go r.worker()
		}
	})
}

// Enqueue adds a job without blocking. It returns ErrQueueFull when there is
// no room, and ErrClosed after Shutdown.
func (r *Runner) Enqueue(j Job) error {
	if j == nil {
		return errors.New("jobs: cannot enqueue a nil job")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return ErrClosed
	}
	select {
	case r.queue <- &task{job: j}:
		r.enqueued.Add(1)
		return nil
	default:
		return ErrQueueFull
	}
}

// scheduled makes sure a periodic job never piles up behind itself.
type scheduled struct {
	job  Job
	busy *atomic.Bool
}

func (s *scheduled) Run(ctx context.Context) error {
	defer s.busy.Store(false)
	return s.job.Run(ctx)
}

// Every enqueues j once per interval, starting one interval from now. If the
// previous run has not finished when the next tick arrives, that tick is skipped.
func (r *Runner) Every(interval time.Duration, j Job) error {
	if interval <= 0 || j == nil {
		panic("aemeath/jobs: Every needs a positive interval and a job")
	}
	r.mu.RLock()
	if r.closed {
		r.mu.RUnlock()
		return ErrClosed
	}
	r.tickersWG.Add(1)
	r.mu.RUnlock()

	busy := new(atomic.Bool)
	go func() {
		defer r.tickersWG.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if !busy.CompareAndSwap(false, true) {
					continue
				}
				if err := r.Enqueue(&scheduled{job: j, busy: busy}); err != nil {
					busy.Store(false)
					r.logger.Warn("periodic job skipped", "job", fmt.Sprintf("%T", j), "error", err)
				}
			case <-r.stopCh:
				return
			}
		}
	}()
	return nil
}

func (r *Runner) worker() {
	defer r.workersWG.Done()
	for t := range r.queue {
		r.execute(t)
	}
}

func (r *Runner) execute(t *task) {
	name := fmt.Sprintf("%T", t.job)
	attempts := r.retries + 1
	for attempt := 1; ; attempt++ {
		err := r.runSafely(t.job)
		if err == nil {
			r.succeeded.Add(1)
			return
		}
		if attempt >= attempts {
			r.failed.Add(1)
			r.logger.Error("job failed", "job", name, "attempts", attempt, "error", err)
			return
		}
		delay := r.backoff(attempt)
		r.logger.Warn("job failed, will retry", "job", name, "attempt", attempt, "retry_in", delay, "error", err)

		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
			r.retried.Add(1)
		case <-r.stopCh:
			timer.Stop()
			r.failed.Add(1)
			r.logger.Error("job dropped at shutdown while waiting to retry", "job", name, "attempts", attempt, "error", err)
			return
		case <-r.baseCtx.Done():
			timer.Stop()
			r.failed.Add(1)
			r.logger.Error("job dropped at shutdown while waiting to retry", "job", name, "attempts", attempt, "error", err)
			return
		}
	}
}

// runSafely turns a panic into an error so one bad job cannot kill a worker.
func (r *Runner) runSafely(j Job) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return j.Run(r.baseCtx)
}

// Shutdown stops accepting jobs and periodic ticks, lets the running and
// already queued jobs finish, and returns when they have, or when ctx ends. If
// ctx ends first, the jobs' context is cancelled and ctx.Err() is returned
// without waiting for jobs that ignore it. A job that is waiting to retry is dropped.
//
// Jobs that were queued before Start are dropped if Start was never called.
func (r *Runner) Shutdown(ctx context.Context) error {
	r.shutdownOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		close(r.stopCh)
		r.mu.Unlock()

		r.tickersWG.Wait()

		r.mu.Lock()
		close(r.queue) // nobody can send any more: Enqueue sees closed first
		r.mu.Unlock()
	})

	done := make(chan struct{})
	go func() {
		r.workersWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		r.cancel()
		return nil
	case <-ctx.Done():
		r.cancel()
		return ctx.Err()
	}
}

// Stats returns the counters.
func (r *Runner) Stats() Stats {
	return Stats{
		Enqueued:  r.enqueued.Load(),
		Succeeded: r.succeeded.Load(),
		Failed:    r.failed.Load(),
		Retried:   r.retried.Load(),
		Queued:    len(r.queue),
	}
}

// Name implements aemeath.Plugin.
func (r *Runner) Name() string { return "jobs" }

// Apply provides the runner to handlers, starts the workers, and stops them
// when the server shuts down.
func (r *Runner) Apply(app *aemeath.App) error {
	aemeath.Provide(app, r)
	app.OnShutdown(r.Shutdown)
	r.Start()
	return nil
}

// From returns the runner of the application handling c.
func From(c *aemeath.Ctx) *Runner { return aemeath.MustGet[*Runner](c) }

# Background jobs

`aemeath/jobs` runs work inside your process: a bounded queue, a pool of workers, retries with backoff, and periodic jobs.

```go
import "github.com/naufalraism/aemeath/jobs"

type SendWelcomeEmail struct{ To string }

func (j *SendWelcomeEmail) Run(ctx context.Context) error {
	// send the email; return an error to have it retried
	return nil
}

runner := jobs.New(jobs.WithWorkers(4))
if err := app.RegisterPlugin(runner); err != nil { // starts the workers, stops them at shutdown
	log.Fatal(err)
}
```

## Enqueue from a handler

```go
aemeath.POST(app, "/signup", func(c *aemeath.Ctx, in SignupIn) (User, error) {
	user := createUser(in)
	if err := jobs.From(c).Enqueue(&SendWelcomeEmail{To: user.Email}); err != nil {
		return User{}, err // jobs.ErrQueueFull when the queue has no room
	}
	return user, nil
}, aemeath.Status(201))
```

`Enqueue` never blocks. Use `jobs.Func(func(ctx context.Context) error { ... })` for a one-off closure.

## Retries and failures

A job that returns an error, or panics, is retried. The defaults are 2 extra attempts with exponential backoff (1 s, 2 s, ...), capped at one minute. The final failure is logged with `slog` and counted in `runner.Stats()`.

```go
jobs.New(
	jobs.WithWorkers(8),
	jobs.WithQueueSize(1000),
	jobs.WithRetries(5),
	jobs.WithBackoff(jobs.Exponential(500*time.Millisecond)),
	jobs.WithLogger(myLogger),
)
```

## Periodic jobs

```go
runner.Every(time.Hour, &CleanupExpiredSessions{})
```

The first run is one interval from now. If the previous run has not finished when the next tick arrives, that tick is skipped, so a slow job never piles up behind itself.

## Shutdown

When the server stops, the runner stops accepting jobs, finishes the running and the already queued ones, and returns within the 10 second shutdown limit. If the limit passes, the context of the running jobs is cancelled, so long jobs should return when `ctx.Done()` fires. A job that is waiting to retry is dropped.

## What it is not

The queue lives in memory. If the process crashes, the jobs that are waiting are lost, and a job can run again after a crash if you enqueue it again. Use a durable queue (a database or a message broker) for work that must not be lost.

## Shutdown hooks for your own code

```go
app.OnShutdown(func(ctx context.Context) error {
	return mySink.Flush(ctx) // runs after in-flight requests, in reverse order of registration
})
```

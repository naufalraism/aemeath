package aemeath_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/jobs"
)

// The other shutdown tests signal their own process. These start the test
// binary again as a real server, stop it the way Docker, Kubernetes or a
// terminal does (SIGTERM on Unix, Ctrl+Break on Windows), and check what
// the outside world sees: in-flight requests finish, the hooks run and the
// process exits on its own with status 0.

const shutdownChildEnv = "AEMEATH_TEST_SHUTDOWN_CHILD"

// runShutdownChild serves until stopped, then reports and exits. mode is
// "addr|grace" (grace in milliseconds), or "addr|grace|certFile|keyFile"
// to serve with ListenTLS.
func runShutdownChild(mode string) {
	parts := strings.Split(mode, "|")
	addr := parts[0]
	grace, _ := strconv.Atoi(parts[1])
	log.SetOutput(os.Stderr)

	app := aemeath.New()
	app.SetShutdownTimeout(time.Duration(grace) * time.Millisecond)
	app.Handle("GET", "/ping", func(c *aemeath.Ctx) error { return c.Text(http.StatusOK, "pong") })
	app.Handle("GET", "/slow", func(c *aemeath.Ctx) error {
		ms, _ := strconv.Atoi(c.Query("ms"))
		fmt.Println("slow request started")
		time.Sleep(time.Duration(ms) * time.Millisecond)
		return c.Text(http.StatusOK, "finished")
	})
	app.Handle("GET", "/events", func(c *aemeath.Ctx) error {
		stream, err := c.SSE()
		if err != nil {
			return err
		}
		fmt.Println("stream started")
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-c.Context().Done():
				fmt.Println("stream ended by the server")
				return nil
			case <-tick.C:
				if err := stream.Send("tick", "x"); err != nil {
					return nil
				}
			}
		}
	})
	runner := jobs.New()
	if err := app.RegisterPlugin(runner); err != nil {
		panic(err)
	}
	app.Handle("POST", "/job", func(c *aemeath.Ctx) error {
		ms, _ := strconv.Atoi(c.Query("ms"))
		err := jobs.From(c).Enqueue(jobs.Func(func(context.Context) error {
			fmt.Println("job started")
			time.Sleep(time.Duration(ms) * time.Millisecond) // a job that does not watch its context
			fmt.Println("job finished")
			return nil
		}))
		if err != nil {
			return err
		}
		return c.Text(http.StatusAccepted, "queued")
	})
	app.OnShutdown(func(context.Context) error {
		fmt.Println("shutdown hook ran")
		return nil
	})
	var err error
	if len(parts) == 4 {
		err = app.ListenTLS(addr, parts[2], parts[3])
	} else {
		err = app.Listen(addr)
	}
	fmt.Printf("Listen returned: %v\n", err)
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

type shutdownChild struct {
	cmd  *exec.Cmd
	addr string
	// http opens a new connection per request. A keep-alive client may dial a
	// spare connection it never uses, and net/http's Shutdown waits up to 5s
	// for such a connection, which made the timings below flaky.
	http           *http.Client
	stdout, stderr *syncBuffer
	exited         chan struct{} // closed when the process has exited
	waitErr        error         // valid once exited is closed
}

func startShutdownChild(t *testing.T, grace time.Duration) *shutdownChild {
	t.Helper()
	return startChild(t, grace, "", nil)
}

// startChild starts the server process; with certFile set it serves HTTPS
// and client is used to wait for it.
func startChild(t *testing.T, grace time.Duration, certFile string, client *http.Client) *shutdownChild {
	t.Helper()
	if testing.Short() {
		t.Skip("starts a separate server process")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := &shutdownChild{addr: "127.0.0.1:" + freeLocalPort(t), stdout: &syncBuffer{}, stderr: &syncBuffer{}, exited: make(chan struct{})}
	c.cmd = exec.Command(exe, "-test.run=^$")
	mode := fmt.Sprintf("%s|%d", c.addr, grace.Milliseconds())
	scheme := "http://"
	if certFile != "" {
		mode += "|" + certFile + "|" + strings.TrimSuffix(certFile, "cert.pem") + "key.pem"
		scheme = "https://"
	}
	var clientTLS *tls.Config
	if client != nil {
		if tr, ok := client.Transport.(*http.Transport); ok {
			clientTLS = tr.TLSClientConfig
		}
	}
	c.http = &http.Client{Transport: &http.Transport{DisableKeepAlives: true, TLSClientConfig: clientTLS}}
	c.cmd.Env = append(os.Environ(), shutdownChildEnv+"="+mode)
	c.cmd.Stdout, c.cmd.Stderr = c.stdout, c.stderr
	prepareForSignal(c.cmd)
	if err := c.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { c.waitErr = c.cmd.Wait(); close(c.exited) }()
	t.Cleanup(func() {
		select {
		case <-c.exited:
		default:
			_ = c.cmd.Process.Kill()
			<-c.exited
		}
	})

	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := c.http.Get(scheme + c.addr + "/ping")
		if err == nil {
			_ = resp.Body.Close()
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not start: %v\nstderr:\n%s", err, c.stderr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// slowRequest starts GET /slow and returns once the handler is running.
func (c *shutdownChild) slowRequest(t *testing.T, d time.Duration) <-chan string {
	t.Helper()
	result := make(chan string, 1)
	go func() {
		resp, err := c.http.Get(fmt.Sprintf("http://%s/slow?ms=%d", c.addr, d.Milliseconds()))
		if err != nil {
			result <- "error: " + err.Error()
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		result <- fmt.Sprintf("%d %s", resp.StatusCode, body)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(c.stdout.String(), "slow request started") {
		if time.Now().After(deadline) {
			t.Fatal("the slow request never reached the handler")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return result
}

func (c *shutdownChild) waitExit(t *testing.T, within time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	select {
	case <-c.exited:
		if c.waitErr != nil {
			t.Errorf("process exited with %v, want status 0\nstdout:\n%s\nstderr:\n%s", c.waitErr, c.stdout, c.stderr)
		}
		return time.Since(start)
	case <-time.After(within):
		t.Fatalf("process still running %v after the signal\nstderr:\n%s", within, c.stderr)
		return 0
	}
}

func TestShutdownOfARealProcessLetsInFlightRequestsFinish(t *testing.T) {
	c := startShutdownChild(t, 10*time.Second)
	inFlight := c.slowRequest(t, 700*time.Millisecond)

	sendStopSignal(t, c.cmd)
	c.waitExit(t, 10*time.Second)

	if got := <-inFlight; got != "200 finished" {
		t.Errorf("in-flight request got %q, want \"200 finished\"", got)
	}
	out := c.stdout.String()
	for _, want := range []string{"shutdown hook ran", "Listen returned: <nil>"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(c.stderr.String(), "server stopped gracefully") {
		t.Errorf("stderr lacks \"server stopped gracefully\":\n%s", c.stderr)
	}
	if conn, err := net.DialTimeout("tcp", c.addr, time.Second); err == nil {
		_ = conn.Close()
		t.Error("the port still accepts connections after the process exited")
	}
}

func TestShutdownOfARealProcessGivesUpAfterTheGraceTime(t *testing.T) {
	c := startShutdownChild(t, 300*time.Millisecond)
	inFlight := c.slowRequest(t, 30*time.Second)

	sendStopSignal(t, c.cmd)
	took := c.waitExit(t, 10*time.Second)

	if took > 5*time.Second {
		t.Errorf("took %v to exit with a 300ms grace time", took)
	}
	if got := <-inFlight; got == "200 finished" {
		t.Error("the 30s request finished; the grace time was not enforced")
	}
	if !strings.Contains(c.stderr.String(), "server closed forcefully") {
		t.Errorf("stderr lacks \"server closed forcefully\":\n%s", c.stderr)
	}
	if !strings.Contains(c.stdout.String(), "shutdown hook ran") {
		t.Errorf("hooks did not run after a forced close:\n%s", c.stdout)
	}
}

// syncBuffer is a bytes.Buffer safe to read while the child writes to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// An open Server-Sent Events stream has no natural end. On a stop signal the
// server cancels its context (App.Stop), so the handler returns, the client
// sees the end of the stream and the process exits at once instead of
// waiting out the grace time and closing connections by force.
func TestShutdownOfARealProcessEndsOpenStreams(t *testing.T) {
	c := startShutdownChild(t, 10*time.Second)
	resp, err := c.http.Get("http://" + c.addr + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	first := make([]byte, 1)
	if _, err := io.ReadFull(resp.Body, first); err != nil {
		t.Fatalf("no event before the signal: %v", err)
	}
	streamDone := make(chan time.Duration, 1)
	start := time.Now()
	go func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		streamDone <- time.Since(start)
	}()

	sendStopSignal(t, c.cmd)
	took := c.waitExit(t, 10*time.Second)
	if took > 3*time.Second {
		t.Errorf("took %v to exit with a stream open; the 10s grace time was waited out", took)
	}
	select {
	case d := <-streamDone:
		if d > 3*time.Second {
			t.Errorf("the client saw the stream end after %v", d)
		}
	case <-time.After(5 * time.Second):
		t.Error("the client's stream never ended")
	}
	if !strings.Contains(c.stdout.String(), "stream ended by the server") {
		t.Errorf("the stream handler was not told to stop:\n%s", c.stdout)
	}
	if !strings.Contains(c.stderr.String(), "server stopped gracefully") {
		t.Errorf("stderr lacks \"server stopped gracefully\":\n%s", c.stderr)
	}
}

// A job that is running when the signal arrives finishes before the process
// exits (the jobs plugin stops the runner in a shutdown hook), and the hooks
// still complete with status 0.
func TestShutdownOfARealProcessFinishesRunningJobs(t *testing.T) {
	c := startShutdownChild(t, 10*time.Second)
	resp, err := c.http.Post("http://"+c.addr+"/job?ms=700", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(c.stdout.String(), "job started") {
		if time.Now().After(deadline) {
			t.Fatal("the job never started")
		}
		time.Sleep(10 * time.Millisecond)
	}

	sendStopSignal(t, c.cmd)
	c.waitExit(t, 10*time.Second)
	out := c.stdout.String()
	finished, returned := strings.Index(out, "job finished"), strings.Index(out, "Listen returned: <nil>")
	if finished < 0 || returned < 0 || finished > returned {
		t.Errorf("want \"job finished\" before \"Listen returned: <nil>\":\n%s", out)
	}
}

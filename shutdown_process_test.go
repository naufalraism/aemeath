package aemeath_test

import (
	"bytes"
	"context"
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
)

// The other shutdown tests signal their own process. These start the test
// binary again as a real server, stop it the way Docker, Kubernetes or a
// terminal does (SIGTERM on Unix, Ctrl+Break on Windows), and check what
// the outside world sees: in-flight requests finish, the hooks run and the
// process exits on its own with status 0.

const shutdownChildEnv = "AEMEATH_TEST_SHUTDOWN_CHILD"

// runShutdownChild serves until stopped, then reports and exits. mode is
// "addr|grace" (grace in milliseconds).
func runShutdownChild(mode string) {
	addr, graceMS, _ := strings.Cut(mode, "|")
	grace, _ := strconv.Atoi(graceMS)
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
	app.OnShutdown(func(context.Context) error {
		fmt.Println("shutdown hook ran")
		return nil
	})
	err := app.Listen(addr)
	fmt.Printf("Listen returned: %v\n", err)
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

type shutdownChild struct {
	cmd            *exec.Cmd
	addr           string
	stdout, stderr *syncBuffer
	exited         chan struct{} // closed when the process has exited
	waitErr        error         // valid once exited is closed
}

func startShutdownChild(t *testing.T, grace time.Duration) *shutdownChild {
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
	c.cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%s|%d", shutdownChildEnv, c.addr, grace.Milliseconds()))
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
		resp, err := http.Get("http://" + c.addr + "/ping")
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
		resp, err := http.Get(fmt.Sprintf("http://%s/slow?ms=%d", c.addr, d.Milliseconds()))
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

package aemeath_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
)

func freeLocalPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	_, port, _ := net.SplitHostPort(l.Addr().String())
	return port
}

func TestShutdownHooksRunInReverseOrderWhenTheServerStops(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sends SIGTERM to the test process")
	}
	addr := "127.0.0.1:" + freeLocalPort(t)
	app := aemeath.New()

	var mu sync.Mutex
	var order []string
	record := func(name string, err error) func(context.Context) error {
		return func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			order = append(order, name)
			return err
		}
	}
	app.OnShutdown(record("first", nil))
	app.OnShutdown(record("second", errors.New("a failing hook must not stop the others")))
	app.OnShutdown(record("third", nil))

	done := make(chan error, 1)
	go func() { done <- app.Listen(addr) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if c, err := net.Dial("tcp", addr); err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the server did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // the signal handler is installed right after the listener starts

	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Listen returned %v after a clean shutdown", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Listen did not return after SIGTERM")
	}

	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(order, ","); got != "third,second,first" {
		t.Fatalf("hooks ran in the order %q, want third,second,first", got)
	}
}

func TestShutdownHooksDoNotRunWhenOnlyGeneratingTypeScript(t *testing.T) {
	t.Setenv(aemeath.EnvTypeScriptOut, t.TempDir())
	app := aemeath.New()
	ran := false
	app.OnShutdown(func(context.Context) error { ran = true; return nil })
	if err := app.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Fatal("hooks must not run when the program only writes TypeScript and exits")
	}
}

// /readyz must fail as soon as the stop signal arrives, while the server keeps
// serving for the configured delay, so load balancers can drain it.
func TestShutdownDelayDrainsWithReadyz503(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sends SIGTERM to the test process")
	}
	addr := "127.0.0.1:" + freeLocalPort(t)
	app := aemeath.New()
	app.EnableHealth()
	app.SetShutdownDelay(700 * time.Millisecond)
	app.Handle("GET", "/work", func(c *aemeath.Ctx) error { return c.Text(200, "ok") })

	done := make(chan error, 1)
	go func() { done <- app.Listen(addr) }()
	get := func(path string) int {
		resp, err := http.Get("http://" + addr + path)
		if err != nil {
			return 0
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	deadline := time.Now().Add(5 * time.Second)
	for get("/readyz") != 200 {
		if time.Now().After(deadline) {
			t.Fatal("the server did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)

	p, _ := os.FindProcess(os.Getpid())
	start := time.Now()
	if err := p.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if !app.ShuttingDown() {
		t.Fatal("ShuttingDown() = false after the signal")
	}
	if code := get("/readyz"); code != 503 {
		t.Fatalf("/readyz during the drain = %d, want 503", code)
	}
	if code := get("/work"); code != 200 {
		t.Fatalf("normal route during the drain = %d, want 200", code)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Listen did not return")
	}
	if waited := time.Since(start); waited < 600*time.Millisecond {
		t.Fatalf("stopped after %v, before the 700ms delay", waited)
	}
}

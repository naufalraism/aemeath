package aemeath_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/aetest"
)

func TestRequestID(t *testing.T) {
	app := aemeath.New()
	app.Use(aemeath.RequestID())
	app.Handle("GET", "/r", func(c *aemeath.Ctx) error { return c.JSON(200, map[string]string{"id": c.RequestID()}) })

	rec := get(app, "/r")
	generated := rec.Header().Get("X-Request-Id")
	if len(generated) != 32 || !strings.Contains(rec.Body.String(), generated) {
		t.Fatalf("generated id = %q, body = %s", generated, rec.Body)
	}
	if rec := get(app, "/r", "X-Request-Id", "trace-123.abc"); rec.Header().Get("X-Request-Id") != "trace-123.abc" {
		t.Fatalf("a safe incoming id must be kept, got %q", rec.Header().Get("X-Request-Id"))
	}
	for _, bad := range []string{"has space", "new\nline", strings.Repeat("a", 65), "semi;colon"} {
		if got := get(app, "/r", "X-Request-Id", bad).Header().Get("X-Request-Id"); got == bad || len(got) != 32 {
			t.Fatalf("unsafe id %q was not replaced (got %q)", bad, got)
		}
	}
}

func TestSlogLoggerRecordsStructuredFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	app := aemeath.New()
	app.Use(aemeath.RequestID(), aemeath.SlogLogger(logger))
	app.Handle("GET", "/ok", func(c *aemeath.Ctx) error { return c.JSON(200, "x") })
	app.Handle("GET", "/bad", func(c *aemeath.Ctx) error { return aemeath.BadRequest("nope") })
	app.Handle("GET", "/boom", func(c *aemeath.Ctx) error { return errors.New("db down") })

	get(app, "/ok", "X-Request-Id", "rid-1")
	get(app, "/bad")
	get(app, "/boom")

	var recs []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		recs = append(recs, m)
	}
	if len(recs) != 3 {
		t.Fatalf("got %d records, want 3: %s", len(recs), buf.String())
	}
	want := []struct {
		level  string
		status float64
	}{{"INFO", 200}, {"WARN", 400}, {"ERROR", 500}}
	for i, w := range want {
		if recs[i]["level"] != w.level || recs[i]["status"] != w.status || recs[i]["method"] != "GET" {
			t.Errorf("record %d = %v, want level %s status %v", i, recs[i], w.level, w.status)
		}
	}
	if recs[0]["request_id"] != "rid-1" || recs[0]["path"] != "/ok" {
		t.Errorf("record 0 = %v", recs[0])
	}
}

func TestTimeoutReturns504WhenContextExpires(t *testing.T) {
	app := aemeath.New()
	app.Use(aemeath.Timeout(50 * time.Millisecond))
	app.Handle("GET", "/slow", func(c *aemeath.Ctx) error {
		select {
		case <-c.Context().Done():
			return c.Context().Err()
		case <-time.After(5 * time.Second):
			return c.JSON(200, "late")
		}
	})
	app.Handle("GET", "/fast", func(c *aemeath.Ctx) error { return c.JSON(200, "ok") })

	start := time.Now()
	if rec := get(app, "/slow"); rec.Code != 504 {
		t.Fatalf("slow: status = %d, want 504", rec.Code)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("the handler was not interrupted by the deadline")
	}
	if rec := get(app, "/fast"); rec.Code != 200 {
		t.Fatalf("fast: status = %d", rec.Code)
	}
}

func TestHealthEndpoints(t *testing.T) {
	app := aemeath.New()
	app.EnableHealth()
	healthy := true
	app.AddReadyCheck("db", func(context.Context) error {
		if !healthy {
			return errors.New("connection refused: secret-host:5432")
		}
		return nil
	})

	if rec := get(app, "/healthz"); rec.Code != 200 {
		t.Fatalf("healthz = %d", rec.Code)
	}
	if rec := get(app, "/readyz"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"db":"ok"`) {
		t.Fatalf("readyz = %d %s", rec.Code, rec.Body)
	}
	healthy = false
	rec := get(app, "/readyz")
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), `"db":"failed"`) {
		t.Fatalf("readyz (failing) = %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "secret-host") {
		t.Fatal("the failure details must not be sent to the client")
	}
	if get(app, "/healthz").Code != 200 {
		t.Fatal("liveness must not depend on the readiness checks")
	}
}

func bigJSON(c *aemeath.Ctx) error {
	return c.JSON(200, map[string]string{"data": strings.Repeat("hello ", 1000)})
}

func TestGzip(t *testing.T) {
	app := aemeath.New()
	app.Use(aemeath.Gzip())
	app.Handle("GET", "/big", bigJSON)
	app.Handle("GET", "/small", func(c *aemeath.Ctx) error { return c.JSON(200, "tiny") })
	app.Handle("GET", "/empty", func(c *aemeath.Ctx) error { c.W.WriteHeader(204); return nil })
	app.Handle("GET", "/err", func(c *aemeath.Ctx) error { return aemeath.NotFound("missing") })
	app.Handle("GET", "/bin", func(c *aemeath.Ctx) error {
		c.W.Header().Set("Content-Type", "image/png")
		_, err := c.W.Write(bytes.Repeat([]byte{1}, 5000))
		return err
	})
	app.Handle("GET", "/stream", func(c *aemeath.Ctx) error {
		sse, err := c.SSE()
		if err != nil {
			return err
		}
		return sse.Send("tick", map[string]int{"n": 1})
	})

	// Compressed, and the content survives the round trip.
	rec := get(app, "/big", "Accept-Encoding", "gzip, deflate")
	if rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Vary") != "Accept-Encoding" || rec.Header().Get("Content-Length") != "" {
		t.Fatalf("headers = %v", rec.Header())
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := io.ReadAll(zr)
	if !strings.Contains(string(plain), strings.Repeat("hello ", 1000)) || rec.Body.Len() > 1000 {
		t.Fatalf("round trip failed (plain %d bytes)", len(plain))
	}

	// Not compressed: no Accept-Encoding, q=0, small body, 204, binary, stream.
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"no header": get(app, "/big"),
		"gzip;q=0":  get(app, "/big", "Accept-Encoding", "gzip;q=0"),
		"small":     get(app, "/small", "Accept-Encoding", "gzip"),
		"204":       get(app, "/empty", "Accept-Encoding", "gzip"),
		"binary":    get(app, "/bin", "Accept-Encoding", "gzip"),
		"sse":       get(app, "/stream", "Accept-Encoding", "gzip"),
	} {
		if rec.Header().Get("Content-Encoding") != "" {
			t.Errorf("%s: must not be compressed", name)
		}
	}
	if rec := get(app, "/small", "Accept-Encoding", "gzip"); strings.TrimSpace(rec.Body.String()) != `"tiny"` {
		t.Fatalf("small body = %q", rec.Body)
	}
	if rec := get(app, "/empty", "Accept-Encoding", "gzip"); rec.Code != 204 {
		t.Fatalf("204 status lost: %d", rec.Code)
	}
	if rec := get(app, "/err", "Accept-Encoding", "gzip"); rec.Code != 404 || !strings.Contains(rec.Body.String(), "missing") {
		t.Fatalf("error response = %d %s", rec.Code, rec.Body)
	}
	if rec := get(app, "/stream", "Accept-Encoding", "gzip"); !strings.Contains(rec.Body.String(), "event: tick") {
		t.Fatalf("stream body = %q", rec.Body)
	}
}

func TestAetestClientCallsTheAppInProcess(t *testing.T) {
	app := newApp()
	client := aetest.NewClient(app, aemeath.WithHeader("X-Test", "1"))
	create := aemeath.NewEndpoint[userIn, userOut]("POST", "/users")

	out, err := create.Call(context.Background(), client, userIn{Name: "Aemeath"})
	if err != nil || out.ID != 1 || out.Name != "Aemeath" {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
	_, err = create.Call(context.Background(), client, userIn{})
	var he *aemeath.HTTPError
	if !errors.As(err, &he) || he.Code != 422 || len(he.Errors) == 0 {
		t.Fatalf("err = %v", err)
	}
}

var _ http.Handler = (*aemeath.App)(nil)

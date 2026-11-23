// Command load compares net/http, Aemeath and Gin under load over real TCP.
//
//	go run ./load all                   (3 servers x 2 routes x 2 concurrencies x 3 runs, ~20 min)
//	go run ./load all -d 10s -runs 1    (quick check, ~3 min)
//	go run ./load all -c 50,100,200,400,800,1600 -d 15s -runs 1   (stress: find the knee)
//	go run ./load server aemeath :18082 (one server, to try by hand)
//
// It only ever targets its own servers on 127.0.0.1.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var servers = []struct {
	name string
	port int
	make func() http.Handler
}{
	{"net/http", 18081, netHTTPServer},
	{"Aemeath", 18082, aemeathServer},
	{"Gin", 18083, func() http.Handler { return ginMaker() }},
}

// ginMaker is set by server_gin.go; nil when built with -tags nogin.
var ginMaker func() http.Handler

func main() {
	if len(os.Args) >= 4 && os.Args[1] == "server" {
		for _, s := range servers {
			if strings.EqualFold(s.name, os.Args[2]) || (os.Args[2] == "nethttp" && s.name == "net/http") {
				srv := &http.Server{Addr: os.Args[3], Handler: s.make(), ReadHeaderTimeout: 10 * time.Second}
				if err := srv.ListenAndServe(); err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
			}
		}
		fmt.Fprintln(os.Stderr, "unknown server:", os.Args[2])
		os.Exit(2)
	}
	if len(os.Args) >= 2 && os.Args[1] == "all" {
		fs := flag.NewFlagSet("all", flag.ExitOnError)
		dur := fs.Duration("d", 30*time.Second, "duration of each measurement")
		warm := fs.Duration("warmup", 3*time.Second, "warm-up before measuring")
		conc := fs.String("c", "50,200", "concurrent connections, comma separated")
		runs := fs.Int("runs", 3, "repetitions; the median is reported")
		_ = fs.Parse(os.Args[2:])
		runAll(*dur, *warm, *conc, *runs)
		return
	}
	fmt.Println("usage: go run ./load all   (see the comment at the top of main.go)")
}

type result struct {
	Server, Route   string
	Conc            int
	RPS             float64
	P50, P95, P99   time.Duration
	Errors, Total   int64
	SysMiB, HeapMiB float64
	Goroutines      uint64
}

var validUser = []byte(`{"name":"Naufal","email":"naufal@example.com"}`)

func runAll(dur, warm time.Duration, concList string, runs int) {
	self, err := os.Executable()
	if err != nil {
		panic(err)
	}
	var concs []int
	for _, s := range strings.Split(concList, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n < 1 {
			panic("bad -c value: " + s)
		}
		concs = append(concs, n)
	}
	fmt.Printf("CPU: %d cores, %s/%s, %s · each measurement %v (+%v warm-up), %d runs, median\n\n",
		runtime.NumCPU(), runtime.GOOS, runtime.GOARCH, runtime.Version(), dur, warm, runs)

	var all []result
	for _, s := range servers {
		if s.name == "Gin" && ginMaker == nil {
			fmt.Println("  (Gin skipped: built with -tags nogin)")
			continue
		}
		addr := fmt.Sprintf("127.0.0.1:%d", s.port)
		cmd := exec.Command(self, "server", s.name, addr)
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			panic(err)
		}
		base := "http://" + addr
		if err := waitReady(base + "/hello"); err != nil {
			_ = cmd.Process.Kill()
			panic(fmt.Sprintf("%s did not start: %v", s.name, err))
		}
		if err := sanity(base); err != nil {
			_ = cmd.Process.Kill()
			panic(fmt.Sprintf("%s: correctness check failed: %v", s.name, err))
		}
		for _, route := range []string{"GET /hello", "POST /users"} {
			for _, c := range concs {
				var rs []result
				for i := 0; i < runs; i++ {
					r := load(base, route, c, warm, dur)
					r.Server = s.name
					rs = append(rs, r)
					fmt.Printf("  %-8s %-12s c=%-4d run %d: %8.0f req/s  p50 %-9v p99 %-9v err %d\n", s.name, route, c, i+1, r.RPS, r.P50, r.P99, r.Errors)
				}
				m := median(rs)
				m.SysMiB, m.HeapMiB, m.Goroutines = memOf(base)
				all = append(all, m)
			}
		}
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	report(all, dur, runs)
}

func waitReady(url string) error {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(url); err == nil {
			_ = resp.Body.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("no answer within 15 seconds")
}

// sanity makes sure every server really validates: bad input must be refused.
func sanity(base string) error {
	check := func(body string, want int) error {
		resp, err := http.Post(base+"/users", "application/json", strings.NewReader(body))
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			return fmt.Errorf("%s -> %d, want %d", body, resp.StatusCode, want)
		}
		return nil
	}
	if err := check(string(validUser), 201); err != nil {
		return err
	}
	if err := check(`{"name":"Na","email":"naufal@example.com"}`, 422); err != nil {
		return err
	}
	return check(`{"name":"Naufal","email":"bukan-email"}`, 422)
}

func load(base, route string, conc int, warm, dur time.Duration) result {
	tr := &http.Transport{MaxIdleConns: conc * 2, MaxIdleConnsPerHost: conc * 2, DisableCompression: true}
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	defer tr.CloseIdleConnections()

	method, path, _ := strings.Cut(route, " ")
	want := 200
	if method == "POST" {
		want = 201
	}
	do := func() (bool, error) {
		var body io.Reader
		if method == "POST" {
			body = bytes.NewReader(validUser)
		}
		req, _ := http.NewRequest(method, base+path, body)
		if method == "POST" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			return false, err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode == want, nil
	}

	var measuring atomic.Bool
	var errs, total atomic.Int64
	lat := make([][]time.Duration, conc)
	ctx, cancel := context.WithTimeout(context.Background(), warm+dur)
	defer cancel()
	var wg sync.WaitGroup
	for w := 0; w < conc; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			lat[w] = make([]time.Duration, 0, 1<<16)
			for ctx.Err() == nil {
				start := time.Now()
				ok, err := do()
				if !measuring.Load() || ctx.Err() != nil {
					continue
				}
				total.Add(1)
				if err != nil || !ok {
					errs.Add(1)
					continue
				}
				lat[w] = append(lat[w], time.Since(start))
			}
		}(w)
	}
	time.Sleep(warm)
	measuring.Store(true)
	begin := time.Now()
	wg.Wait()
	elapsed := time.Since(begin)

	var merged []time.Duration
	for _, l := range lat {
		merged = append(merged, l...)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i] < merged[j] })
	pct := func(p float64) time.Duration {
		if len(merged) == 0 {
			return 0
		}
		return merged[int(p*float64(len(merged)-1))].Round(time.Microsecond)
	}
	return result{Route: route, Conc: conc, RPS: float64(len(merged)) / elapsed.Seconds(),
		P50: pct(0.50), P95: pct(0.95), P99: pct(0.99), Errors: errs.Load(), Total: total.Load()}
}

func median(rs []result) result {
	sort.Slice(rs, func(i, j int) bool { return rs[i].RPS < rs[j].RPS })
	return rs[len(rs)/2]
}

func memOf(base string) (sysMiB, heapMiB float64, goroutines uint64) {
	resp, err := http.Get(base + "/__mem")
	if err != nil {
		return 0, 0, 0
	}
	defer resp.Body.Close()
	var m map[string]uint64
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return float64(m["sys"]) / (1 << 20), float64(m["heap_inuse"]) / (1 << 20), m["goroutines"]
}

func report(all []result, dur time.Duration, runs int) {
	var b strings.Builder
	fmt.Fprintf(&b, "# Load test results\n\n%s, %d CPU cores, %s/%s. Each row: median of %d measurements of %v. Servers and load generator run on the same machine.\n\n",
		runtime.Version(), runtime.NumCPU(), runtime.GOOS, runtime.GOARCH, runs, dur)
	b.WriteString("| Route | Connections | Server | req/s | p50 | p95 | p99 | Errors | Process memory (Sys) |\n|---|---|---|---|---|---|---|---|---|\n")
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Route != all[j].Route {
			return all[i].Route < all[j].Route
		}
		return all[i].Conc < all[j].Conc
	})
	for _, r := range all {
		fmt.Fprintf(&b, "| %s | %d | %s | %.0f | %v | %v | %v | %d of %d | %.1f MiB |\n",
			r.Route, r.Conc, r.Server, r.RPS, r.P50, r.P95, r.P99, r.Errors, r.Total, r.SysMiB)
	}
	fmt.Println()
	fmt.Print(b.String())
	_ = os.WriteFile("results.md", []byte(b.String()), 0o644)
	fmt.Println("\nSaved to results.md")
}

package aemeath_test

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestREADMEAllocationTable keeps the README's allocation table honest: it runs
// the two benchmarks it quotes and compares the counts with the numbers in the
// file. Allocation counts do not depend on the machine, so a change in either
// the code or the README that makes them disagree fails here.
func TestREADMEAllocationTable(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector adds an allocation per request; run without -race")
	}
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]func(*testing.B){
		"plain `GET`, no input":             BenchmarkHelloRoute,
		"JSON `POST`, two validated fields": BenchmarkValidatedJSONPost,
	}
	for label, bench := range rows {
		row := regexp.MustCompile(`(?m)^\| ` + regexp.QuoteMeta(label) + ` \| (\d+) \|`).FindSubmatch(readme)
		if row == nil {
			t.Errorf("the README has no allocation row for %q; update it or this test", label)
			continue
		}
		claimed, err := strconv.Atoi(string(row[1]))
		if err != nil {
			t.Fatal(err)
		}
		res := testing.Benchmark(bench)
		if got := int(res.AllocsPerOp()); got != claimed {
			t.Errorf("%s: the README claims %d allocations, the benchmark does %d", label, claimed, got)
		}
	}
	if !strings.Contains(string(readme), "BenchmarkValidatedJSONPost") && !strings.Contains(string(readme), "go test -run '^$' -bench . -benchmem .") {
		t.Error("the README no longer says how to reproduce these numbers")
	}
}

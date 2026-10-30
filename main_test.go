package aemeath_test

import (
	"flag"
	"io"
	"log"
	"os"
	"testing"
)

// Several tests trigger panics and errors on purpose to check that Recover and
// the error responder work. Those are logged with stack traces, which look like
// failures in CI output. Hide them unless the tests run with -v.
func TestMain(m *testing.M) {
	flag.Parse() // testing.Verbose needs the flags to be parsed
	if !testing.Verbose() {
		log.SetOutput(io.Discard)
	}
	os.Exit(m.Run())
}

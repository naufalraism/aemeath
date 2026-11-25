package aemeath_test

import (
	"testing"

	"github.com/naufalraism/aemeath"
)

// Stop is how a program that runs its own http.Server signals a shutdown.
// It closed Stopping but left ShuttingDown false and /readyz at 200, so the
// two signals contradicted each other and the load balancer kept sending
// traffic.
func TestStopAlsoMarksTheAppShuttingDown(t *testing.T) {
	app := aemeath.New()
	app.EnableHealth()
	if app.ShuttingDown() || get(app, "/readyz").Code != 200 {
		t.Fatal("a new app already reports shutting down")
	}
	app.Stop()
	select {
	case <-app.Stopping():
	default:
		t.Fatal("Stop did not close Stopping")
	}
	if !app.ShuttingDown() {
		t.Error("after Stop, ShuttingDown() = false")
	}
	if code := get(app, "/readyz").Code; code != 503 {
		t.Errorf("after Stop, /readyz = %d, want 503", code)
	}
}

package aemeath_test

import (
	"testing"

	"github.com/naufalraism/aemeath"
)

type applyOnly struct{ applied bool }

func (p *applyOnly) Apply(*aemeath.App) error { p.applied = true; return nil }

// Plugins written for earlier versions still have a Name method.
type withName struct{ applyOnly }

func (*withName) Name() string { return "old" }

// A plugin needs only Apply. Name was never read, and plugins that still
// have it keep working.
func TestAPluginNeedsOnlyApply(t *testing.T) {
	app := aemeath.New()
	a, b := &applyOnly{}, &withName{}
	for _, p := range []aemeath.Plugin{a, b} {
		if err := app.RegisterPlugin(p); err != nil {
			t.Fatal(err)
		}
	}
	if !a.applied || !b.applied {
		t.Errorf("applied: %v, %v", a.applied, b.applied)
	}
}

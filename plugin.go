package aemeath

import (
	"fmt"
	"reflect"
)

// Plugin is the base interface for all Aemeath plugins: Apply registers
// what the plugin provides (services, routes, readiness checks, shutdown
// hooks) on the application.
type Plugin interface {
	Apply(app *App) error
}

// RegisterPlugin registers a plugin with the application by calling its
// Apply. Registering the same plugin (the same pointer) twice is an error:
// Apply would add its readiness checks and shutdown hooks a second time.
// Check the error: a plugin that fails leaves the app half configured.
func (a *App) RegisterPlugin(p Plugin) error {
	if v := reflect.ValueOf(p); v.Kind() == reflect.Pointer && !v.IsNil() {
		a.depsMu.Lock()
		if a.plugins == nil {
			a.plugins = map[uintptr]bool{}
		}
		id := v.Pointer()
		seen := a.plugins[id]
		a.plugins[id] = true
		a.depsMu.Unlock()
		if seen {
			return fmt.Errorf("aemeath: plugin %T is already registered", p)
		}
	}
	return p.Apply(a)
}

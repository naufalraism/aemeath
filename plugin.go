package aemeath

// Plugin is the base interface for all Aemeath plugins: Apply registers
// what the plugin provides (services, routes, readiness checks, shutdown
// hooks) on the application.
type Plugin interface {
	Apply(app *App) error
}

// RegisterPlugin registers a plugin with the application.
func (a *App) RegisterPlugin(p Plugin) error {
	return p.Apply(a)
}

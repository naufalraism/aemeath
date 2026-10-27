package aemeath

// Plugin is the base interface for all Aemeath plugins.
type Plugin interface {
	Name() string
	Apply(app *App) error
}

// RegisterPlugin registers a plugin with the application.
func (a *App) RegisterPlugin(p Plugin) error {
	return p.Apply(a)
}

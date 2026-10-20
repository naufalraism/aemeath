package aemeath

// Plugin adalah antarmuka dasar untuk semua plugin Aemeath.
type Plugin interface {
	Name() string
	Apply(app *App) error
}

// RegisterPlugin mendaftarkan plugin ke aplikasi.
func (a *App) RegisterPlugin(p Plugin) error {
	return p.Apply(a)
}

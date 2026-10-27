package aemeath

import "io"

// Storage is the contract implemented by every file storage backend.
type Storage interface {
	Save(filename string, src io.Reader) (string, error)
	Delete(filename string) error
}

// StoragePlugin wraps a Storage so it can be registered with Aemeath.
type StoragePlugin struct {
	Storage
}

// Name implements Plugin.
func (p *StoragePlugin) Name() string { return "storage" }

// Apply provides the storage as an aemeath.Storage; read it in a handler with
// aemeath.MustGet[aemeath.Storage](c).
func (p *StoragePlugin) Apply(app *App) error {
	Provide[Storage](app, p.Storage)
	return nil
}

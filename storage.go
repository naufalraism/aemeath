package aemeath

import "github.com/naufalraism/aemeath/storage"

// Storage is the contract implemented by every file storage backend. It is
// the same type as storage.Storage, so a storage provided under one name is
// found under the other.
//
// Deprecated: use storage.Storage. Storage will be removed before v1.0.
type Storage = storage.Storage

// StoragePlugin wraps a Storage so it can be registered with Aemeath.
//
// Deprecated: provide the storage directly:
// aemeath.Provide[storage.Storage](app, storage.NewLocal("uploads")).
// StoragePlugin will be removed before v1.0.
type StoragePlugin struct {
	Storage
}

// Apply provides the storage as a storage.Storage; read it in a handler with
// aemeath.MustGet[storage.Storage](c).
func (p *StoragePlugin) Apply(app *App) error {
	Provide(app, p.Storage) // p.Storage is declared as the Storage interface, so T is Storage
	return nil
}

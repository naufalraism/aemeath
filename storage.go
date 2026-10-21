package aemeath

import "io"

// Storage adalah kontrak (interface) untuk semua jenis penyimpanan file.
type Storage interface {
	Save(filename string, src io.Reader) (string, error)
	Delete(filename string) error
}

// StoragePlugin membungkus Storage agar bisa didaftarkan ke Aemeath.
type StoragePlugin struct {
	Storage
}

func (p *StoragePlugin) Name() string { return "storage" }

func (p *StoragePlugin) Apply(app *App) error {
	app.Use(func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) error {
			c.State["storage"] = p.Storage
			return next(c)
		}
	})
	return nil
}

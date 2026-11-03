package aemeath_test

import (
	"testing"

	"github.com/naufalraism/aemeath"
)

// Handlers read the storage as the aemeath.Storage interface. StoragePlugin must
// provide it under that type, not under the concrete type that was passed in.
func TestStoragePluginProvidesTheStorageInterface(t *testing.T) {
	store := aemeath.NewLocalStorage(t.TempDir())
	app := aemeath.New()
	if err := app.RegisterPlugin(&aemeath.StoragePlugin{Storage: store}); err != nil {
		t.Fatal(err)
	}

	called := false
	app.Handle("GET", "/s", func(c *aemeath.Ctx) error {
		called = true
		s, ok := aemeath.Get[aemeath.Storage](c)
		if !ok {
			t.Error("the storage is not available as aemeath.Storage")
		} else if s != aemeath.Storage(store) {
			t.Error("a different storage was provided")
		}
		if _, ok := aemeath.Get[*aemeath.LocalStorage](c); ok {
			t.Error("the storage must be provided as the interface only")
		}
		return nil
	})
	get(app, "/s")
	if !called {
		t.Fatal("the handler did not run")
	}
}

// Package dbtest is the behavior every database must show, run against SQLite,
// PostgreSQL and MySQL so that "works with Postgres" is a tested claim.
package dbtest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/db"
)

// Item is the model the suite uses.
type Item struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	Name      string `gorm:"size:100;uniqueIndex" json:"name"`
	CreatedAt time.Time
}

type newItem struct {
	Name string `json:"name" validate:"required,min=1,max=100"`
}

type byID struct {
	ID uint `path:"id"`
}

// Options says what the database under test supports.
type Options struct {
	Concurrent bool // run the concurrent writes test (SQLite files cannot take many writers)
}

func newApp(t *testing.T, p *db.Plugin) *aemeath.App {
	t.Helper()
	app := aemeath.New()
	app.EnableHealth()
	if err := app.RegisterPlugin(p); err != nil {
		t.Fatal(err)
	}
	aemeath.POST(app, "/items", func(c *aemeath.Ctx, in newItem) (Item, error) {
		item := Item{Name: in.Name}
		if err := db.From(c).Create(&item).Error; err != nil {
			return Item{}, db.Translate(err, "Item")
		}
		return item, nil
	}, aemeath.Status(201))
	aemeath.GET(app, "/items", func(c *aemeath.Ctx, _ struct{}) ([]Item, error) {
		items := []Item{}
		return items, db.From(c).Order("id").Find(&items).Error
	})
	aemeath.GET(app, "/items/{id}", func(c *aemeath.Ctx, in byID) (Item, error) {
		var item Item
		if err := db.From(c).First(&item, in.ID).Error; err != nil {
			return item, db.Translate(err, "Item")
		}
		return item, nil
	})
	aemeath.POST(app, "/fail-after-insert", func(c *aemeath.Ctx, in newItem) (struct{}, error) {
		err := db.From(c).Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&Item{Name: in.Name}).Error; err != nil {
				return err
			}
			return aemeath.BadRequest("rolled back on purpose")
		})
		return struct{}{}, err
	})
	return app
}

func call(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Run opens a fresh database through open and checks the plugin against it.
func Run(t *testing.T, open func(t *testing.T) *db.Plugin, opts Options) {
	t.Helper()
	fresh := func(t *testing.T) (*aemeath.App, *db.Plugin) {
		t.Helper()
		p := open(t)
		t.Cleanup(func() { _ = p.Close() }) // Windows cannot delete an open database file
		_ = p.DB.Migrator().DropTable(&Item{})
		if err := p.DB.AutoMigrate(&Item{}); err != nil {
			t.Fatalf("migrating: %v", err)
		}
		t.Cleanup(func() { _ = p.DB.Migrator().DropTable(&Item{}) })
		return newApp(t, p), p
	}

	t.Run("CRUD through handlers", func(t *testing.T) {
		app, _ := fresh(t)
		rec := call(app, "POST", "/items", `{"name":"first"}`)
		if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"id":1`) {
			t.Fatalf("create: %d %s", rec.Code, rec.Body)
		}
		call(app, "POST", "/items", `{"name":"second"}`)
		rec = call(app, "GET", "/items", "")
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "first") || !strings.Contains(rec.Body.String(), "second") {
			t.Fatalf("list: %d %s", rec.Code, rec.Body)
		}
		if rec = call(app, "GET", "/items/1", ""); rec.Code != 200 {
			t.Fatalf("get: %d %s", rec.Code, rec.Body)
		}
		if rec = call(app, "POST", "/items", `{}`); rec.Code != 422 {
			t.Fatalf("validation must still apply in front of the database: %d", rec.Code)
		}
	})

	t.Run("no row is a 404", func(t *testing.T) {
		app, _ := fresh(t)
		if rec := call(app, "GET", "/items/999", ""); rec.Code != 404 || !strings.Contains(rec.Body.String(), "Item not found") {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
	})

	t.Run("a duplicate key is a 409", func(t *testing.T) {
		app, _ := fresh(t)
		call(app, "POST", "/items", `{"name":"unique"}`)
		rec := call(app, "POST", "/items", `{"name":"unique"}`)
		if rec.Code != 409 || !strings.Contains(rec.Body.String(), "Item already exists") {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		if rec = call(app, "GET", "/items", ""); strings.Count(rec.Body.String(), `"unique"`) != 1 {
			t.Fatalf("the duplicate must not have been stored: %s", rec.Body)
		}
	})

	t.Run("time values round trip", func(t *testing.T) {
		_, p := fresh(t)
		before := time.Now().Add(-2 * time.Second)
		item := Item{Name: "timed"}
		if err := p.DB.Create(&item).Error; err != nil {
			t.Fatal(err)
		}
		var got Item
		if err := p.DB.First(&got, item.ID).Error; err != nil {
			t.Fatalf("reading a time column back failed (MySQL needs parseTime): %v", err)
		}
		if got.CreatedAt.Before(before) || got.CreatedAt.After(time.Now().Add(2*time.Second)) {
			t.Fatalf("CreatedAt = %v, want about now", got.CreatedAt)
		}
	})

	t.Run("a failed transaction is rolled back", func(t *testing.T) {
		app, p := fresh(t)
		if rec := call(app, "POST", "/fail-after-insert", `{"name":"ghost"}`); rec.Code != 400 {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		var n int64
		p.DB.Model(&Item{}).Count(&n)
		if n != 0 {
			t.Fatalf("%d rows survived a rolled back transaction", n)
		}
	})

	t.Run("readiness follows the connection", func(t *testing.T) {
		app, p := fresh(t)
		if rec := call(app, "GET", "/readyz", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"database":"ok"`) {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		_ = p.Close()
		if rec := call(app, "GET", "/readyz", ""); rec.Code != 503 || !strings.Contains(rec.Body.String(), `"database":"failed"`) {
			t.Fatalf("a closed database must not be ready: %d %s", rec.Code, rec.Body)
		}
		if err := p.Close(); err != nil {
			t.Fatalf("closing twice must be harmless: %v", err)
		}
	})

	if opts.Concurrent {
		t.Run("concurrent writes", func(t *testing.T) {
			app, p := fresh(t)
			var wg sync.WaitGroup
			failures := make(chan string, 40)
			for i := 0; i < 40; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					rec := call(app, "POST", "/items", `{"name":"n`+strings.Repeat("x", i%5)+string(rune('a'+i%26))+string(rune('a'+i/26))+`"}`)
					if rec.Code != 201 && rec.Code != 409 {
						failures <- rec.Body.String()
					}
				}(i)
			}
			wg.Wait()
			close(failures)
			for f := range failures {
				t.Errorf("a concurrent insert failed: %s", f)
			}
			var n int64
			p.DB.Model(&Item{}).Count(&n)
			if n < 20 {
				t.Errorf("only %d rows after 40 concurrent inserts", n)
			}
		})
	}
}

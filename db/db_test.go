package db_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/db"
)

type Task struct {
	ID    uint   `gorm:"primaryKey" json:"id"`
	Title string `json:"title"`
}

type newTask struct {
	Title string `json:"title" validate:"required"`
}

func newApp(t *testing.T) (*aemeath.App, *db.Plugin) {
	t.Helper()
	p, err := db.Open(filepath.Join(t.TempDir(), "test.db"), &Task{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() }) // Windows cannot delete an open database file
	app := aemeath.New()
	app.EnableHealth()
	if err := app.RegisterPlugin(p); err != nil {
		t.Fatal(err)
	}
	aemeath.POST(app, "/tasks", func(c *aemeath.Ctx, in newTask) (Task, error) {
		task := Task{Title: in.Title}
		if err := db.From(c).Create(&task).Error; err != nil {
			return Task{}, err
		}
		return task, nil
	}, aemeath.Status(201))
	aemeath.GET(app, "/tasks", func(c *aemeath.Ctx, _ struct{}) ([]Task, error) {
		tasks := []Task{}
		return tasks, db.From(c).Find(&tasks).Error
	})
	return app, p
}

func call(app http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	return rec
}

func TestOpenMigratesAndHandlersUseTheDatabase(t *testing.T) {
	app, _ := newApp(t)

	rec := call(app, "POST", "/tasks", `{"title":"write tests"}`)
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"id":1`) {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	call(app, "POST", "/tasks", `{"title":"ship it"}`)

	rec = call(app, "GET", "/tasks", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "write tests") || !strings.Contains(rec.Body.String(), "ship it") {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	if rec = call(app, "POST", "/tasks", `{}`); rec.Code != 422 {
		t.Fatalf("validation still applies in front of the database: %d", rec.Code)
	}
}

func TestDataSurvivesReopeningTheDatabaseFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "keep.db")
	first, err := db.Open(file, &Task{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if err := first.DB.Create(&Task{Title: "persisted"}).Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := first.DB.DB()
	_ = sqlDB.Close()

	second, err := db.Open(file, &Task{}) // migrating again must not lose rows
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	var got Task
	if err := second.DB.First(&got).Error; err != nil || got.Title != "persisted" {
		t.Fatalf("got %+v, err = %v", got, err)
	}
}

func TestReadinessReflectsTheDatabase(t *testing.T) {
	app, p := newApp(t)
	if rec := call(app, "GET", "/readyz", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"database":"ok"`) {
		t.Fatalf("readyz = %d %s", rec.Code, rec.Body)
	}
	var sqlDB *sql.DB
	sqlDB, err := p.DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	_ = sqlDB.Close()
	rec := call(app, "GET", "/readyz", "")
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), `"database":"failed"`) {
		t.Fatalf("readyz with a closed database = %d %s", rec.Code, rec.Body)
	}
	if call(app, "GET", "/healthz", "").Code != 200 {
		t.Fatal("liveness must not depend on the database")
	}
}

func TestOpenReportsAnUnusableLocation(t *testing.T) {
	_, err := db.Open(filepath.Join(t.TempDir(), "missing", "dir", "x.db"))
	if err == nil || !strings.Contains(err.Error(), "aemeath/db") {
		t.Fatalf("err = %v, want an aemeath/db error", err)
	}
}

func TestFromPanicsWithAClearMessageWhenThePluginIsMissing(t *testing.T) {
	defer func() {
		r := recover()
		if s, _ := r.(string); !strings.Contains(s, "gorm.DB") {
			t.Fatalf("panic = %v, want a message naming *gorm.DB", r)
		}
	}()
	db.From(&aemeath.Ctx{})
}

func TestCloseIsIdempotentAndRunsAtShutdown(t *testing.T) {
	p, err := db.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("a second Close must be harmless: %v", err)
	}
	if err := p.DB.Exec("select 1").Error; err == nil {
		t.Fatal("the database is still usable after Close")
	}
}

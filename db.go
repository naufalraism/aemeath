package aemeath

import (
	"log"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// DBPlugin adalah implementasi Plugin untuk database GORM.
type DBPlugin struct {
	DB *gorm.DB
}

// NewDBPlugin membuat koneksi ke database (default: SQLite).
func NewDBPlugin(filename string) *DBPlugin {
	db, err := gorm.Open(sqlite.Open(filename), &gorm.Config{})
	if err != nil {
		log.Fatal("Gagal koneksi ke database:", err)
	}

	// Auto-migrate untuk tabel User
	if err := db.AutoMigrate(&User{}); err != nil {
		log.Printf("Gagal migrasi database: %v", err)
	}
	return &DBPlugin{DB: db}
}

func (p *DBPlugin) Name() string { return "database" }

func (p *DBPlugin) Apply(a *App) error {
	// Suntikkan DB ke dalam middleware global
	a.Use(func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) error {
			c.State["db"] = p.DB
			return next(c)
		}
	})
	return nil
}

// User adalah model contoh untuk database.
type User struct {
	ID    uint   `gorm:"primaryKey" json:"id"`
	Name  string `json:"name"`
	Email string `gorm:"unique" json:"email"`
}

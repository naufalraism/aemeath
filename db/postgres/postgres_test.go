package postgres_test

import (
	"os"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath/db"
	"github.com/naufalraism/aemeath/db/internal/dbtest"
	"github.com/naufalraism/aemeath/db/postgres"
)

// These tests need a PostgreSQL server. Set AEMEATH_TEST_POSTGRES_DSN, for example
//
//	postgres://aemeath:aemeath@localhost:5432/aemeath_test?sslmode=disable
//
// The tests create and drop their own table. Without the variable they are skipped.
func dsn(t *testing.T) string {
	t.Helper()
	v := os.Getenv("AEMEATH_TEST_POSTGRES_DSN")
	if v == "" {
		t.Skip("AEMEATH_TEST_POSTGRES_DSN is not set")
	}
	return v
}

func TestPostgres(t *testing.T) {
	d := dsn(t)
	dbtest.Run(t, func(t *testing.T) *db.Plugin {
		p, err := postgres.Open(d)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}, dbtest.Options{Concurrent: true})
}

func TestOpenAppliesThePoolDefaultsAndLetsYouChangeThem(t *testing.T) {
	d := dsn(t)
	p, err := postgres.Open(d)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	sqlDB, _ := p.DB.DB()
	if got := sqlDB.Stats().MaxOpenConnections; got != 25 {
		t.Errorf("default MaxOpenConnections = %d, want 25", got)
	}
	q, err := postgres.Open(d, db.WithPool(3, 1, 0))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q.Close() }()
	sqlDB, _ = q.DB.DB()
	if got := sqlDB.Stats().MaxOpenConnections; got != 3 {
		t.Errorf("MaxOpenConnections = %d, want 3", got)
	}
}

func TestOpenReportsConnectionProblemsWithoutLeakingThePassword(t *testing.T) {
	_, err := postgres.Open("postgres://someone:s3cr3t-password@127.0.0.1:1/nope?sslmode=disable&connect_timeout=2")
	if err == nil {
		t.Fatal("connecting to a closed port must fail")
	}
	if !strings.Contains(err.Error(), "aemeath/db") {
		t.Errorf("err = %v", err)
	}
	if strings.Contains(err.Error(), "s3cr3t-password") {
		t.Errorf("the password must never appear in an error: %v", err)
	}
}

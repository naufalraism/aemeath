package mysql_test

import (
	"os"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath/db"
	"github.com/naufalraism/aemeath/db/internal/dbtest"
	"github.com/naufalraism/aemeath/db/mysql"
)

// These tests need a MySQL or MariaDB server. Set AEMEATH_TEST_MYSQL_DSN, for example
//
//	aemeath:aemeath@tcp(localhost:3306)/aemeath_test
//
// The tests create and drop their own table. Without the variable they are skipped.
func dsn(t *testing.T) string {
	t.Helper()
	v := os.Getenv("AEMEATH_TEST_MYSQL_DSN")
	if v == "" {
		t.Skip("AEMEATH_TEST_MYSQL_DSN is not set")
	}
	return v
}

func TestMySQL(t *testing.T) {
	d := dsn(t)
	dbtest.Run(t, func(t *testing.T) *db.Plugin {
		p, err := mysql.Open(d) // note: the DSN has no parseTime, Open adds it
		if err != nil {
			t.Fatal(err)
		}
		return p
	}, dbtest.Options{Concurrent: true})
}

func TestOpenRejectsAMalformedDSNWithoutLeakingIt(t *testing.T) {
	_, err := mysql.Open("user:s3cr3t-password@not-a-valid-dsn")
	if err == nil {
		t.Fatal("a malformed DSN must be rejected")
	}
	if strings.Contains(err.Error(), "s3cr3t-password") {
		t.Errorf("the password must never appear in an error: %v", err)
	}
}

func TestOpenReportsConnectionProblemsWithoutLeakingThePassword(t *testing.T) {
	_, err := mysql.Open("someone:s3cr3t-password@tcp(127.0.0.1:1)/nope?timeout=2s")
	if err == nil {
		t.Fatal("connecting to a closed port must fail")
	}
	if strings.Contains(err.Error(), "s3cr3t-password") {
		t.Errorf("the password must never appear in an error: %v", err)
	}
}

package db_test

import (
	"errors"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/db"
)

func TestFromPanicsWithAClearMessageWhenThePluginIsMissing(t *testing.T) {
	defer func() {
		r := recover()
		if s, _ := r.(string); !strings.Contains(s, "gorm.DB") {
			t.Fatalf("panic = %v, want a message naming *gorm.DB", r)
		}
	}()
	db.From(&aemeath.Ctx{})
}

func TestTranslate(t *testing.T) {
	if db.Translate(nil, "X") != nil {
		t.Error("nil must stay nil")
	}
	other := errors.New("boom")
	if db.Translate(other, "X") != other {
		t.Error("an unknown error must be returned unchanged")
	}
	if he := aemeath.AsHTTPError(db.Translate(gorm.ErrRecordNotFound, "Task")); he.Code != 404 || he.Message != "Task not found" {
		t.Errorf("not found -> %+v", he)
	}
	if he := aemeath.AsHTTPError(db.Translate(gorm.ErrDuplicatedKey, "Task")); he.Code != 409 || he.Message != "Task already exists" {
		t.Errorf("duplicate -> %+v", he)
	}
}

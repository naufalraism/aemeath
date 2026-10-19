package aemeath_test

import (
	"testing"

	"github.com/naufalraism/aemeath"
)

func TestPanicsOnInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		fn   func()
	}{
		{"int required tanpa pointer", func() {
			type badIn struct {
				Age int `json:"age" validate:"required"`
			}
			aemeath.POST(aemeath.New(), "/x", func(*aemeath.Ctx, badIn) (struct{}, error) {
				return struct{}{}, nil
			})
		}},
		{"embedded struct", func() {
			type Base struct{ ID int }
			type badIn struct {
				Base
				Name string `json:"name"`
			}
			aemeath.POST(aemeath.New(), "/x", func(*aemeath.Ctx, badIn) (struct{}, error) {
				return struct{}{}, nil
			})
		}},
		{"required dan omitempty sekaligus", func() {
			type badIn struct {
				Name string `json:"name,omitempty" validate:"required"`
			}
			aemeath.POST(aemeath.New(), "/x", func(*aemeath.Ctx, badIn) (struct{}, error) {
				return struct{}{}, nil
			})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatal("expected panic, got none")
				}
			}()
			tt.fn()
		})
	}
}

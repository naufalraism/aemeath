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
		{"non-pointer int with required", func() {
			type badIn struct {
				Age int `json:"age" validate:"required"`
			}
			aemeath.POST(aemeath.New(), "/x", func(*aemeath.Ctx, badIn) (struct{}, error) {
				return struct{}{}, nil
			})
		}},
		{"unknown rule", func() {
			type badIn struct {
				Name string `json:"name" validate:"minimum=3"`
			}
			aemeath.POST(aemeath.New(), "/x", func(*aemeath.Ctx, badIn) (struct{}, error) {
				return struct{}{}, nil
			})
		}},
		{"min on a bool", func() {
			type badIn struct {
				On *bool `json:"on" validate:"min=1"`
			}
			aemeath.POST(aemeath.New(), "/x", func(*aemeath.Ctx, badIn) (struct{}, error) {
				return struct{}{}, nil
			})
		}},
		{"email on a number", func() {
			type badIn struct {
				Age int `json:"age" validate:"email"`
			}
			aemeath.POST(aemeath.New(), "/x", func(*aemeath.Ctx, badIn) (struct{}, error) {
				return struct{}{}, nil
			})
		}},
		{"min without a value", func() {
			type badIn struct {
				Name string `json:"name" validate:"min"`
			}
			aemeath.POST(aemeath.New(), "/x", func(*aemeath.Ctx, badIn) (struct{}, error) {
				return struct{}{}, nil
			})
		}},
		{"min that is not a number", func() {
			type badIn struct {
				Name string `json:"name" validate:"min=abc"`
			}
			aemeath.POST(aemeath.New(), "/x", func(*aemeath.Ctx, badIn) (struct{}, error) {
				return struct{}{}, nil
			})
		}},
		{"invalid regular expression", func() {
			type badIn struct {
				Name string `json:"name" validate:"pattern=(["`
			}
			aemeath.POST(aemeath.New(), "/x", func(*aemeath.Ctx, badIn) (struct{}, error) {
				return struct{}{}, nil
			})
		}},
		{"oneof with a non-numeric value on a number", func() {
			type badIn struct {
				N int `json:"n" validate:"oneof=1 two"`
			}
			aemeath.POST(aemeath.New(), "/x", func(*aemeath.Ctx, badIn) (struct{}, error) {
				return struct{}{}, nil
			})
		}},
		{"len on a number", func() {
			type badIn struct {
				N int `json:"n" validate:"len=3"`
			}
			aemeath.POST(aemeath.New(), "/x", func(*aemeath.Ctx, badIn) (struct{}, error) {
				return struct{}{}, nil
			})
		}},
		{"required and omitempty together", func() {
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

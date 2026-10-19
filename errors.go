package aemeath

import (
	"errors"
	"log"
	"net/http"
)

// HTTPError adalah error yang membawa status code HTTP.
type HTTPError struct {
	Code    int    `json:"-"`
	Message string `json:"error"`
}

func (e *HTTPError) Error() string { return e.Message }

func NewError(code int, msg string) *HTTPError { return &HTTPError{Code: code, Message: msg} }

// Helper untuk status code yang umum.
func BadRequest(msg string) *HTTPError   { return NewError(http.StatusBadRequest, msg) }
func Unauthorized(msg string) *HTTPError { return NewError(http.StatusUnauthorized, msg) }
func Forbidden(msg string) *HTTPError    { return NewError(http.StatusForbidden, msg) }
func NotFound(msg string) *HTTPError     { return NewError(http.StatusNotFound, msg) }
func MethodNotAllowed(msg string) *HTTPError {
	return NewError(http.StatusMethodNotAllowed, msg)
}
func Conflict(msg string) *HTTPError { return NewError(http.StatusConflict, msg) }
func InternalError(msg string) *HTTPError {
	return NewError(http.StatusInternalServerError, msg)
}

// toHTTPError mengubah error biasa menjadi 500 tanpa membocorkan detail ke klien.
func toHTTPError(err error) *HTTPError {
	var he *HTTPError
	if errors.As(err, &he) {
		return he
	}
	log.Printf("aemeath: error internal: %v", err)
	return NewError(http.StatusInternalServerError, "terjadi kesalahan internal")
}

package aemeath

import (
	"errors"
	"log"
	"net/http"
)

// HTTPError is an error that carries an HTTP status code.
type HTTPError struct {
	Code    int          `json:"-"`
	Message string       `json:"error"`
	Errors  []FieldError `json:"errors,omitempty"`
}

// Error implements the built-in error interface.
func (e *HTTPError) Error() string { return e.Message }

// NewError creates an HTTPError with the given status code and message.
func NewError(code int, msg string) *HTTPError { return &HTTPError{Code: code, Message: msg} }

// Helpers for common status codes.
func BadRequest(msg string) *HTTPError          { return NewError(http.StatusBadRequest, msg) }
func Unauthorized(msg string) *HTTPError        { return NewError(http.StatusUnauthorized, msg) }
func Forbidden(msg string) *HTTPError           { return NewError(http.StatusForbidden, msg) }
func NotFound(msg string) *HTTPError            { return NewError(http.StatusNotFound, msg) }
func MethodNotAllowed(msg string) *HTTPError    { return NewError(http.StatusMethodNotAllowed, msg) }
func UnprocessableEntity(msg string) *HTTPError { return NewError(http.StatusUnprocessableEntity, msg) }
func Conflict(msg string) *HTTPError            { return NewError(http.StatusConflict, msg) }
func PayloadTooLarge(msg string) *HTTPError     { return NewError(http.StatusRequestEntityTooLarge, msg) }
func UnsupportedMediaType(msg string) *HTTPError {
	return NewError(http.StatusUnsupportedMediaType, msg)
}
func TooManyRequests(msg string) *HTTPError { return NewError(http.StatusTooManyRequests, msg) }
func InternalError(msg string) *HTTPError   { return NewError(http.StatusInternalServerError, msg) }

// toHTTPError converts an arbitrary error into an HTTPError. Unknown errors
// become a generic 500 so internal details are never leaked to clients.
func toHTTPError(err error) *HTTPError {
	var he *HTTPError
	if errors.As(err, &he) {
		return he
	}
	log.Printf("aemeath: internal error: %v", err)
	return NewError(http.StatusInternalServerError, "internal server error")
}

// AsHTTPError converts any error into an *HTTPError: an *HTTPError is returned
// as is, anything else becomes a generic 500 (and is logged, never leaked).
func AsHTTPError(err error) *HTTPError { return toHTTPError(err) }

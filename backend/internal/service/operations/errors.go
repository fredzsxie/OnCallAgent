package operations

import (
	"context"
	"errors"
)

var (
	ErrInvalidInput    = errors.New("invalid_input")
	ErrAuthentication  = errors.New("authentication_failed")
	ErrInvalidResponse = errors.New("invalid_response")
	ErrUpstream        = errors.New("upstream_error")
	ErrConfiguration   = errors.New("invalid_configuration")
)

type Error struct {
	Tool      string `json:"tool"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	cause     error
}

func (e *Error) Error() string { return e.Tool + ": " + e.Message }
func (e *Error) Unwrap() error { return e.cause }

// Only public categories and context errors survive; dependency bodies and URLs never escape.
func Failure(tool string, cause error) *Error {
	code, message, retryable := "upstream_error", "upstream request failed", true
	var safe error = ErrUpstream
	for _, item := range []struct {
		err           error
		code, message string
		retryable     bool
	}{
		{context.Canceled, "cancelled", "request cancelled", false},
		{context.DeadlineExceeded, "timeout", "tool request timed out", true},
		{ErrInvalidInput, "invalid_input", "invalid tool input", false},
		{ErrAuthentication, "authentication_failed", "authentication failed", false},
		{ErrInvalidResponse, "invalid_response", "invalid upstream response", false},
		{ErrConfiguration, "invalid_configuration", "invalid tool configuration", false},
	} {
		if errors.Is(cause, item.err) {
			code, message, retryable, safe = item.code, item.message, item.retryable, item.err
			break
		}
	}
	return &Error{tool, code, message, retryable, safe}
}

func RequestFailure(ctx context.Context, tool string, err error) *Error {
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return Failure(tool, err)
}

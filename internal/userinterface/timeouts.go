package userinterface

import (
	"context"
	"errors"
	"time"
)

const (
	defaultRequestTimeout = 10 * time.Second
	defaultLoadTimeout    = 30 * time.Second
)

// Timeouts are the deadlines of the GitHub calls a view makes. A zero field
// selects its default, so the zero value is usable.
type Timeouts struct {
	// Request bounds one small call: a status check or a write.
	Request time.Duration
	// Load bounds a bigger read, paged ones as a whole: a diff, a pull
	// request's details, its threads and the pull request lists.
	Load time.Duration
}

// RequestContext returns a context for one small GitHub call. The caller must
// call the cancel function.
func (timeouts Timeouts) RequestContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), orDefault(timeouts.Request, defaultRequestTimeout))
}

// LoadContext returns a context for one load, however many pages it takes.
// The caller must call the cancel function.
func (timeouts Timeouts) LoadContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), orDefault(timeouts.Load, defaultLoadTimeout))
}

func orDefault(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}

// writeTimeoutError is a write that hit its deadline. GitHub may have done it
// anyway; the error still matches context.DeadlineExceeded.
type writeTimeoutError struct{ err error }

func (writeTimeoutError) Error() string {
	return "timed out; it may have gone through, refresh to check"
}

func (timeoutError writeTimeoutError) Unwrap() error { return timeoutError.err }

// IsWriteTimeout reports whether err came out of WriteError as a timeout: a
// write that GitHub may have applied, so a view reloads what it affects.
func IsWriteTimeout(err error) bool {
	var timeoutError writeTimeoutError
	return errors.As(err, &timeoutError)
}

// WriteError marks err, from a write to GitHub, as possibly applied when it is
// a timeout, and returns any other error as it is.
func WriteError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return writeTimeoutError{err: err}
	}
	return err
}

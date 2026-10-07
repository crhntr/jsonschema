package hypertext

import (
	"database/sql"
	"errors"
	"net/http"
)

// PublishableError is an error that is safe to show: its text is the
// HTTP status text, and its status code tells muxt what to respond with.
// The original error is kept for logging and errors.Is.
type PublishableError struct {
	err  error
	code int
}

func (e *PublishableError) Error() string   { return http.StatusText(e.code) }
func (e *PublishableError) StatusCode() int { return e.code }
func (e *PublishableError) Unwrap() error   { return e.err }

// publish wraps err so it can be shown, logging anything that is not a
// plain not-found.
func (s *Server) publish(err error) error {
	if err == nil {
		return nil
	}
	var published *PublishableError
	if errors.As(err, &published) {
		return err
	}
	code := http.StatusInternalServerError
	if errors.Is(err, sql.ErrNoRows) {
		code = http.StatusNotFound
	} else {
		s.log.Error("request failed", "error", err)
	}
	return &PublishableError{err: err, code: code}
}

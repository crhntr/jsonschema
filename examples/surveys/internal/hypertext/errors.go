package hypertext

import (
	"errors"
	"net/http"

	"github.com/crhntr/jsonschema/examples/surveys/internal/store"
)

// message is an error worded for the person using the page.
type message string

func (m message) Error() string { return string(m) }

// notFound is a message for a page that does not exist.
type notFound string

func (n notFound) Error() string   { return string(n) }
func (n notFound) StatusCode() int { return http.StatusNotFound }

// fail returns err for a page. Messages and missing surveys are shown as
// they are; anything else is logged and replaced by a general message,
// since its text may describe the server rather than the request.
func (s *Server) fail(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return notFound("There is no such survey.")
	}
	var m message
	var n notFound
	if errors.As(err, &m) || errors.As(err, &n) {
		return err
	}
	s.log.Error("request failed", "error", err)
	return message("Something went wrong. Try again.")
}

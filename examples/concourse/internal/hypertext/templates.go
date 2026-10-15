package hypertext

import (
	"embed"
	"errors"
	"html/template"
	"net/http"
)

//go:embed *.gohtml
var templateFiles embed.FS

var templates = template.Must(template.New("concourse").Funcs(template.FuncMap{
	"errorPage": errorPage,
	"status":    status,
}).ParseFS(templateFiles, "*.gohtml"))

// status is the response status for an error: its own when it has one.
func status(err error) int {
	var coded interface{ StatusCode() int }
	if errors.As(err, &coded) {
		return coded.StatusCode()
	}
	return http.StatusInternalServerError
}

// ErrorPage is what an error renders as. Errors reaching a page have been
// through fail, so their text is safe to show.
type ErrorPage struct {
	Layout
	Message string
}

func errorPage(err error) ErrorPage { return ErrorPage{Message: err.Error()} }

package jsonhandler

import (
	"embed"
	"html/template"
	"strings"
)

//go:embed *.gohtml
var templateFiles embed.FS

// templates holds the route templates muxt generates handlers for and the
// partials they render. It is parsed once and never modified. muxt reads
// this expression to find the templates, so it is spelled out rather than
// calling Templates.
var templates = template.Must(template.New("jsonhandler").Funcs(template.FuncMap{
	"describedBy": describedBy,
}).ParseFS(templateFiles, "*.gohtml"))

// Templates returns a new copy of the form templates, for a page that
// renders the form itself rather than loading it from the routes. Parse
// the page's own templates into it, then execute "jsonhandler-form" with a
// [*Form] (from [Handler.Form] or [Handler.SubmitForm]) after calling
// [Form.WithActions].
func Templates() (*template.Template, error) {
	// html/template cannot clone a set once it has executed, so each call
	// parses its own.
	return template.New("jsonhandler").Funcs(template.FuncMap{
		"describedBy": describedBy,
	}).ParseFS(templateFiles, "*.gohtml")
}

// describedBy lists the ids of a field's description and errors, for
// aria-describedby.
func describedBy(f *Field) string {
	ids := []string{f.ErrorsID()}
	if f.Description != "" {
		ids = append([]string{f.HelpID()}, ids...)
	}
	return strings.Join(ids, " ")
}

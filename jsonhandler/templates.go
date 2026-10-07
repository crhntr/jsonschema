package jsonhandler

import (
	"embed"
	"html/template"
	"strings"
)

//go:embed *.gohtml
var templateFiles embed.FS

// templates holds the route templates muxt generates handlers for and the
// partials they render. It is parsed once and never modified.
var templates = template.Must(template.New("jsonhandler").Funcs(template.FuncMap{
	"describedBy": describedBy,
}).ParseFS(templateFiles, "*.gohtml"))

// Templates returns a copy of the form templates, for a page that renders
// the form itself rather than loading it from the routes. Parse the page's
// own templates into the copy, then execute "jsonhandler-form" with a
// [*Form] (from [Handler.Form] or [Handler.SubmitForm]) whose Action is
// set.
func Templates() (*template.Template, error) {
	return templates.Clone()
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

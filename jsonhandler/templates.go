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

// HTML renders the form with the package's templates: the form, or the
// result partial for a valid submission. Call [Form.WithActions] first.
// It is for pages that place the form themselves without changing its
// markup; to change the markup, parse your own templates into
// [Templates] instead.
func (f *Form) HTML() (template.HTML, error) {
	if f.Valid {
		return execute("jsonhandler-result", f)
	}
	return execute("jsonhandler-form", f)
}

// ValidationHTML renders the response to validation as you type: each
// error list, marked to replace the one on the page by id.
func (f *Form) ValidationHTML() (template.HTML, error) {
	return execute("jsonhandler-validation", f)
}

func execute(name string, f *Form) (template.HTML, error) {
	var b strings.Builder
	if err := templates.ExecuteTemplate(&b, name, f); err != nil {
		return "", err
	}
	return template.HTML(b.String()), nil
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

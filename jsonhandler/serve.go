package jsonhandler

import (
	"encoding/json/jsontext"
	"net/http"

	"github.com/crhntr/jsonschema"
)

// Options adjust a form made by [Handle] or [Serve]. The zero value, and a
// nil *Options, give the defaults.
type Options struct {
	// Action is where the form posts. It defaults to the request's own URL,
	// so a page that serves the form and receives its post is one handler.
	Action string
	// ID, SubmitLabel, Outline, NoJSON, NoWizard and MaxFields are those of
	// [Handler].
	ID, SubmitLabel  string
	Outline          bool
	NoJSON, NoWizard bool
	MaxFields        int
}

// Handle is the main entry point: it answers one request for a form. All
// the state is in the request. A GET gives the form for initial (which may
// be nil, and is where readOnly values are taken from). A POST is the
// form's own submission, or one of its edit buttons: the returned Form is
// then the form again, with errors, or, when f.Valid, holds the submitted
// JSON in f.Value. Render the Form with [Form.HTML], or with your own
// templates (see [Templates]), and set the response status from
// [Form.StatusCode].
func Handle(r *http.Request, schema *jsonschema.Schema, initial jsontext.Value, o *Options) (*Form, error) {
	if schema == nil {
		return nil, errNoSchema
	}
	if o == nil {
		o = &Options{}
	}
	h := &Handler{
		Schema: schema, Value: initial, ID: o.ID, SubmitLabel: o.SubmitLabel,
		Outline: o.Outline, NoJSON: o.NoJSON, NoWizard: o.NoWizard, MaxFields: o.MaxFields,
	}
	var f *Form
	var err error
	if r.Method == http.MethodPost {
		if err = r.ParseForm(); err != nil {
			return nil, err
		}
		f, err = h.SubmitForm(r.Context(), r.PostForm)
	} else {
		f, err = h.Form(r.Context())
	}
	if err != nil {
		return nil, err
	}
	action := o.Action
	if action == "" {
		action = r.URL.RequestURI()
	}
	return f.WithActions(action, ""), nil
}

// Serve is [Handle] for a handler that writes the response itself, as
// [http.ServeContent] does for a file. A valid submission is returned as
// JSON and nothing is written: save it, then redirect or write your own
// response. In every other case Serve writes the form as HTML with its
// status and returns nil, and the handler is done.
//
//	func settings(w http.ResponseWriter, r *http.Request) {
//		value, err := jsonhandler.Serve(w, r, schema, stored, nil)
//		if err != nil || value == nil {
//			if err != nil { http.Error(w, "bad request", 400) }
//			return
//		}
//		save(value)
//		http.Redirect(w, r, r.URL.Path, http.StatusSeeOther)
//	}
func Serve(w http.ResponseWriter, r *http.Request, schema *jsonschema.Schema, initial jsontext.Value, o *Options) (jsontext.Value, error) {
	f, err := Handle(r, schema, initial, o)
	if err != nil {
		return nil, err
	}
	if f.Valid {
		return f.Value, nil
	}
	html, err := f.HTML()
	if err != nil {
		return nil, err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(f.StatusCode())
	_, err = w.Write([]byte(html))
	return nil, err
}

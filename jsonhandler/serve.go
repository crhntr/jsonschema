package jsonhandler

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"html/template"
	"net/http"

	"github.com/crhntr/jsonschema"
	"github.com/crhntr/jsonschema/jsonptr"
)

// Options adjust a form made by [Handle] or [Serve]. The zero value, and a
// nil *Options, give the defaults.
type Options struct {
	// Action is where the form posts. It defaults to the request's own URL,
	// so a page that serves the form and receives its post is one handler.
	Action string
	// ValidateAction is where the form posts its state as it is typed, for
	// the handler that answers with [ValidateHTML]. Empty turns validation
	// as you type off.
	ValidateAction string
	// ID, SubmitLabel, Outline, NoJSON, NoWizard and MaxFields are those of
	// [Handler].
	ID, SubmitLabel  string
	Outline          bool
	NoJSON, NoWizard bool
	MaxFields        int
}

func (o *Options) handler(schema *jsonschema.Schema, initial jsontext.Value) *Handler {
	if o == nil {
		o = &Options{}
	}
	return &Handler{
		Schema: schema, Value: initial, ID: o.ID, SubmitLabel: o.SubmitLabel,
		Outline: o.Outline, NoJSON: o.NoJSON, NoWizard: o.NoWizard, MaxFields: o.MaxFields,
	}
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
	h := o.handler(schema, initial)
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
	var action, validate string
	if o != nil {
		action, validate = o.Action, o.ValidateAction
	}
	if action == "" {
		action = r.URL.RequestURI()
	}
	return f.WithActions(action, validate), nil
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

// ValidateHTML answers a request for validation as you type: the post of
// a form's current state, from a form made with [Handle] whose
// [Options.ValidateAction] is this request's URL. It returns the error
// lists, each marked to replace the one on the page by id, to be written as
// the response.
func ValidateHTML(r *http.Request, schema *jsonschema.Schema, initial jsontext.Value, o *Options) (template.HTML, error) {
	if schema == nil {
		return "", errNoSchema
	}
	if err := r.ParseForm(); err != nil {
		return "", err
	}
	f, err := o.handler(schema, initial).Validate(r.Context(), r.PostForm)
	if err != nil {
		return "", err
	}
	return f.ValidationHTML()
}

// ParseSchemaAt is [ParseSchema] for the part of the document at a JSON
// Pointer, such as "/$defs/job", for a form that edits one piece of a
// larger value. References inside it resolve against the whole document.
func ParseSchemaAt(ctx context.Context, document []byte, pointer string) (*jsonschema.Schema, error) {
	s, err := ParseSchema(ctx, document)
	if err != nil || pointer == "" {
		return s, err
	}
	// The walk keeps identity, so the subschema found is the linked one.
	_, v, err := jsonptr.FindValue(jsonptr.Pointer(pointer), s)
	if err != nil {
		return nil, fmt.Errorf("find %s in the schema: %w", pointer, err)
	}
	sub, ok := v.(*jsonschema.Schema)
	if !ok {
		return nil, fmt.Errorf("%s is not a schema", pointer)
	}
	return sub, nil
}

// ParseSchema loads a schema document and resolves its references, for
// [Handle], [Serve] and [ValidateHTML]. References may point within the
// document or at the JSON Schema 2020-12 meta-schemas, which are built in;
// nothing is fetched over the network. Parsing is cheap, but an app whose
// schemas are data can keep the result by schema.
func ParseSchema(ctx context.Context, document []byte) (*jsonschema.Schema, error) {
	r := jsonschema.NewResolver(&http.Client{Transport: offline{}})
	const uri = "urn:jsonhandler:schema"
	if _, err := r.Load(uri, document); err != nil {
		return nil, fmt.Errorf("load schema: %w", err)
	}
	s, err := r.Resolve(ctx, uri)
	if err != nil {
		return nil, fmt.Errorf("resolve schema: %w", err)
	}
	return s, nil
}

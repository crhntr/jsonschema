package jsonhandler

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/crhntr/jsonschema"
	"github.com/crhntr/jsonschema/internal/metaschema"
)

//go:generate muxt generate --use-receiver-type=Handler --output-routes-func-with-path-prefix-param --output-htmx

// Handler serves a form for editing a JSON value that Schema describes.
// Register its routes with [TemplateRoutes]; README.md describes the
// endpoints and the markup they render.
type Handler struct {
	// Schema is the resolved schema the value must match.
	Schema *jsonschema.Schema
	// Value is the value the form starts with. When nil the form starts
	// from the schema's default, or empty.
	Value jsontext.Value
	// ID is the id of the form element and the prefix of every id inside
	// it. Give each form on a page its own. Defaults to "jsonform".
	ID string
	// SubmitLabel is the text of the submit button. Defaults to "Submit".
	SubmitLabel string
	// OnSubmit, when set, is called with each submitted value that passes
	// validation. A returned message is shown with the value; a returned
	// error is shown on the form, which is rendered again for correction.
	OnSubmit func(ctx context.Context, value jsontext.Value) (message string, err error)
}

// New returns a Handler for a schema document. References may point within
// the document or at the JSON Schema 2020-12 meta-schemas, which are built
// in; nothing is fetched over the network. To use other references,
// resolve the schema with a [jsonschema.Resolver] and set Handler.Schema.
func New(ctx context.Context, schema []byte) (*Handler, error) {
	r := jsonschema.NewResolver(&http.Client{Transport: offline{}})
	if err := metaschema.Seed(r); err != nil {
		return nil, err
	}
	const uri = "urn:jsonhandler:schema"
	if _, err := r.Load(uri, schema); err != nil {
		return nil, fmt.Errorf("load schema: %w", err)
	}
	s, err := r.Resolve(ctx, uri)
	if err != nil {
		return nil, fmt.Errorf("resolve schema: %w", err)
	}
	return &Handler{Schema: s}, nil
}

type offline struct{}

func (offline) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("jsonhandler: %s is not built in and fetching schemas is disabled", req.URL)
}

var errNoSchema = errors.New("jsonhandler: Handler.Schema is nil")

// Form is what the form templates render.
type Form struct {
	// ID is the form element's id.
	ID string
	// Action is the URL the form posts to; see [Form.WithAction].
	Action string
	// SubmitLabel is the text of the submit button.
	SubmitLabel string
	// Root is the field for the whole value.
	Root *Field
	// Errors are problems that belong to no single field.
	Errors []string

	// Valid reports a submission that passed validation; Value holds the
	// submitted JSON and Message what OnSubmit returned.
	Valid   bool
	Value   jsontext.Value
	Message string
	// State is the submitted form state, so a valid submission can be
	// opened for editing again.
	State []Control

	status int
}

// Control is a hidden form control that carries form state.
type Control struct{ Name, Value string }

// WithAction sets the URL the form posts to and returns f. Route
// templates call it with the generated path; a page rendering the form
// itself passes wherever it registered the routes.
func (f *Form) WithAction(action string) *Form {
	f.Action = action
	return f
}

// StatusCode is the response status: 422 when a submission has errors,
// 500 when there is no form, otherwise 200.
func (f *Form) StatusCode() int {
	switch {
	case f == nil:
		return http.StatusInternalServerError
	case f.status == 0:
		return http.StatusOK
	}
	return f.status
}

// Invalid reports that the form has errors to show.
func (f *Form) Invalid() bool { return f.status == http.StatusUnprocessableEntity }

// JSON returns the submitted value, indented.
func (f *Form) JSON() string { return indentJSON(f.Value) }

// FormAction returns the URL of the form the field belongs to.
func (f *Field) FormAction() string {
	if f.form == nil {
		return ""
	}
	return f.form.Action
}

// Form renders the form for h.Value.
func (h *Handler) Form(ctx context.Context) (*Form, error) {
	if h.Schema == nil {
		return nil, errNoSchema
	}
	sh := h.root()
	s := state{}
	v := h.Value
	if len(v) == 0 {
		v = sh.def
	}
	if len(v) > 0 {
		e := encoder{state: s}
		e.encode(sh, "", v, 0)
	}
	return h.build(sh, s), nil
}

// SubmitForm handles a post of the form. When an edit button was pressed
// (the @action control is set) it applies the edit and renders the form
// again. Otherwise it decodes the form to JSON and validates it: an
// invalid value renders the form with errors and status 422, a valid one
// is passed to OnSubmit and rendered as JSON.
func (h *Handler) SubmitForm(ctx context.Context, form url.Values) (*Form, error) {
	if h.Schema == nil {
		return nil, errNoSchema
	}
	sh := h.root()
	s := state{}
	for name, values := range form {
		if _, _, ok := parseName(name); ok {
			s[name] = slices.Clone(values)
		}
	}
	if action := form.Get(ActionName); action != "" {
		applyAction(s, action)
		return h.build(sh, s), nil
	}
	f := h.build(sh, s)
	r := f.Root.decode()
	if r.missing {
		r.errs = append(r.errs, fieldError{path: "", message: messageRequired})
	}
	if len(r.errs) > 0 {
		f.attach(r.errs, nil)
		return f, nil
	}
	if r.value == nil {
		r.value = jsontext.Value("null")
	}
	if out := h.Schema.Validate("form", r.value); !out.Valid {
		f.attach(nil, &out)
		return f, nil
	}
	if h.OnSubmit != nil {
		message, err := h.OnSubmit(ctx, r.value)
		if err != nil {
			f.Errors = append(f.Errors, err.Error())
			f.status = http.StatusUnprocessableEntity
			return f, nil
		}
		f.Message = message
	}
	f.Valid, f.Value = true, r.value
	for _, name := range s.sortedNames() {
		for _, v := range s[name] {
			f.State = append(f.State, Control{Name: name, Value: v})
		}
	}
	return f, nil
}

func (h *Handler) root() *shape {
	return flatten(node{schema: h.Schema})
}

func (h *Handler) build(sh *shape, s state) *Form {
	f := &Form{
		ID:          firstNonEmpty(h.ID, "jsonform"),
		SubmitLabel: firstNonEmpty(h.SubmitLabel, "Submit"),
	}
	b := builder{state: s, idPrefix: f.ID, form: f}
	f.Root = b.build(sh, site{required: true, htmlRequired: true}, 0)
	return f
}

// attach puts decoding errors and validation errors on the fields they
// are about. A validation error at a location with no field of its own
// goes to the nearest enclosing field.
func (f *Form) attach(errs []fieldError, out *jsonschema.Output) {
	f.status = http.StatusUnprocessableEntity
	byPath := map[string]*Field{}
	byPointer := map[string]*Field{}
	choices := map[string]*Field{}
	var walk func(*Field)
	walk = func(field *Field) {
		byPath[field.Path] = field
		byPointer[field.Pointer] = field
		if field.Kind == KindChoice {
			choices[field.Pointer] = field
		}
		for _, c := range field.Fields {
			walk(c)
		}
		for _, c := range field.Entries {
			walk(c)
		}
		if field.Choice != nil {
			walk(field.Choice)
		}
	}
	walk(f.Root)
	for _, e := range errs {
		field, ok := byPath[e.path]
		switch {
		case !ok:
			f.Errors = appendNew(f.Errors, e.message)
		case e.key && field.Key != nil:
			field.Key.Errors = appendNew(field.Key.Errors, e.message)
		default:
			field.Errors = appendNew(field.Errors, e.message)
		}
	}
	if out == nil {
		return
	}
	collect(*out, choices, func(pointer, message string) {
		for {
			if field, ok := byPointer[pointer]; ok {
				field.Errors = appendNew(field.Errors, message)
				return
			}
			if pointer == "" {
				f.Errors = appendNew(f.Errors, message)
				return
			}
			pointer, _ = splitLast(pointer)
		}
	})
}

// collect reports the most specific errors in a verbose output tree: a
// unit's own error only when none of its children explain the failure.
// Under a oneOf or anyOf whose location is a choice field, only the
// branch for the chosen variant is reported; the others failing is
// expected.
func collect(o jsonschema.Output, choices map[string]*Field, report func(pointer, message string)) int {
	if o.Valid {
		return 0
	}
	n := 0
	keyword := lastToken(o.KeywordLocation)
	choice, isChoice := choices[o.InstanceLocation]
	for _, child := range o.Errors {
		if isChoice && choice.variantOf == keyword && (keyword == "oneOf" || keyword == "anyOf") {
			if child.KeywordLocation != fmt.Sprintf("%s/%d", o.KeywordLocation, choice.variant) {
				continue
			}
		}
		n += collect(child, choices, report)
	}
	if n == 0 && o.Error != "" {
		report(o.InstanceLocation, o.Error)
		n = 1
	}
	return n
}

func appendNew(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}
	return append(list, strings.TrimSpace(s))
}

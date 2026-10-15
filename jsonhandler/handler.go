package jsonhandler

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/crhntr/jsonschema"
	"github.com/crhntr/jsonschema/internal/metaschema"
	"github.com/crhntr/jsonschema/jsonptr"
)

//go:generate go tool muxt generate --use-receiver-type=Handler --output-routes-func-with-path-prefix-param --output-htmx

// Handler serves a form for editing a JSON value that Schema describes.
// Register its routes with [TemplateRoutes]; README.md describes the
// endpoints and the markup they render.
//
// A Handler is configuration and holds no state of its own, so it is safe
// to copy, and to use from many requests at once. To edit a different
// value per request, use [Handler.WithValue]. Making one parses the schema,
// which takes microseconds, or under a millisecond when the schema declares
// $schema (the resolver then loads that meta-schema); an app whose schemas
// are data can make one per request, or keep them by schema to avoid it.
type Handler struct {
	// Schema is the resolved schema the value must match.
	Schema *jsonschema.Schema
	// Value is the value the form starts with. When nil the form starts
	// from the schema's default, or empty.
	//
	// Value is also what readOnly locations keep: whatever a submission
	// says for them is replaced by what Value (or the default) has there,
	// so a client cannot change, add or remove them. writeOnly locations
	// are never sent to the browser, so they start empty and are left out
	// of the result unless something is entered.
	Value jsontext.Value
	// MaxFields limits how many fields one form may have, since the
	// submitted form state decides how many array items and map entries
	// to render. A larger form is refused with status 413. Defaults to
	// 10000.
	MaxFields int
	// ID is the id of the form element and the prefix of every id inside
	// it. Give each form on a page its own. Defaults to "jsonform".
	ID string
	// SubmitLabel is the text of the submit button. Defaults to "Submit".
	SubmitLabel string
	// Outline adds a navigation list of links to every object, array, map
	// and choice in the form, for large or deeply nested values.
	Outline bool
	// NoJSON and NoWizard turn off the JSON and wizard edit modes: their
	// buttons are not offered, and a request for one (or a form already in
	// it) is answered with the fields. Use NoJSON on a form filled in by
	// the public, who have no use for raw JSON.
	NoJSON, NoWizard bool
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
	return NewAt(ctx, schema, "")
}

// NewAt returns a Handler for the part of a schema document at a JSON
// Pointer, such as "/$defs/job", for a form that edits one piece of a
// larger value. References inside that part resolve against the whole
// document, as they do for New.
func NewAt(ctx context.Context, schema []byte, pointer string) (*Handler, error) {
	r := jsonschema.NewResolver(&http.Client{Transport: offline{}})
	const uri = "urn:jsonhandler:schema"
	if _, err := r.Load(uri, schema); err != nil {
		return nil, fmt.Errorf("load schema: %w", err)
	}
	s, err := r.Resolve(ctx, uri)
	if err != nil {
		return nil, fmt.Errorf("resolve schema: %w", err)
	}
	if pointer == "" {
		return &Handler{Schema: s}, nil
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
	return &Handler{Schema: sub}, nil
}

// offline is the transport schemas are resolved with: it serves the JSON
// Schema meta-schemas that are built in, only when something refers to
// them, and refuses every other URL, so nothing is ever fetched from the
// network.
type offline struct{}

func (offline) RoundTrip(req *http.Request) (*http.Response, error) {
	if body, ok := metaschema.BytesForURL(req.URL.String()); ok {
		return &http.Response{
			Status:        "200 OK",
			StatusCode:    http.StatusOK,
			Proto:         "HTTP/1.1",
			ProtoMajor:    1,
			ProtoMinor:    1,
			Header:        http.Header{"Content-Type": {"application/schema+json"}},
			Body:          io.NopCloser(bytes.NewReader(body)),
			ContentLength: int64(len(body)),
			Request:       req,
		}, nil
	}
	return nil, fmt.Errorf("jsonhandler: %s is not built in and fetching schemas is disabled", req.URL)
}

var errNoSchema = errors.New("jsonhandler: Handler.Schema is nil")

// Edit actions that switch between the fields, one JSON text area for the
// whole value, and the wizard. Other edit actions are described at
// [applyAction]; "step" followed by a form path opens the wizard at that
// group.
const (
	ActionJSON   = "json"
	ActionFields = "fields"
	ActionWizard = "step"
	// ModeJSON is the value of the @mode control while the form shows
	// the whole value as JSON.
	ModeJSON = "json"
	// ModeWizard is the value of the @mode control while the form shows
	// one field at a time.
	ModeWizard = "wizard"
	metaMode   = "mode"
)

// Form is what the form templates render.
type Form struct {
	// ID is the form element's id.
	ID string
	// Action is the URL the form posts to and ValidateAction the URL that
	// checks it as the user types; see [Form.WithActions].
	Action, ValidateAction string
	// SubmitLabel is the text of the submit button.
	SubmitLabel string
	// Mode is ModeJSON while the form shows the whole value as JSON,
	// ModeWizard while it shows one field at a time, and empty while it
	// shows every field.
	Mode string
	// Root is the field for the whole value.
	Root *Field
	// ShowOutline reports that the form renders its outline.
	ShowOutline bool
	// NoJSON and NoWizard report edit modes the form does not offer; see
	// [Handler].
	NoJSON, NoWizard bool
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

	status    int
	writeOnly map[string]bool // pointers of writeOnly values, hidden by JSON

	state state // the form state the fields were built from
	step  string
	view  *view
}

// Control is a hidden form control that carries form state.
type Control struct{ Name, Value string }

// WithActions sets the URLs the form posts to and returns f: action for
// submissions and edits, validate for checking as the user types (empty
// turns that off). Route templates call it with the generated paths; a
// page rendering the form itself passes wherever it registered the routes.
func (f *Form) WithActions(action, validate string) *Form {
	f.Action, f.ValidateAction = action, validate
	return f
}

// WithValue returns a copy of h that starts from v (and keeps readOnly
// locations as v has them); h is unchanged.
func (h *Handler) WithValue(v jsontext.Value) *Handler {
	c := *h
	c.Value = v
	return &c
}

// Reject turns a valid submission back into the form, with err shown above
// the fields and status 422, for an app that saves the value itself after
// SubmitForm and finds it cannot. The fields keep what was submitted. err's
// text is shown to the person filling in the form. It returns f.
func (f *Form) Reject(err error) *Form {
	f.Valid, f.Value, f.Message, f.State, f.writeOnly = false, nil, "", nil, nil
	f.Errors = append(f.Errors, err.Error())
	f.status = http.StatusUnprocessableEntity
	return f
}

// StatusCode is the response status: 422 when a submission has errors,
// 413 when the form is too large, 500 when there is no form, otherwise
// 200.
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

// ErrorsID is the id of the list of form-level errors.
func (f *Form) ErrorsID() string { return f.ID + "_errors" }

// JSON returns the submitted value, indented, with writeOnly values
// replaced by "********".
func (f *Form) JSON() string { return indentJSON(redact(f.Value, "", f.writeOnly)) }

// redact replaces the values at the given pointers.
func redact(v jsontext.Value, ptr string, hidden map[string]bool) jsontext.Value {
	if hidden[ptr] {
		return jsontext.Value(`"********"`)
	}
	switch v.Kind() {
	case '{':
		var members []member
		for name, raw := range objectMembers(v) {
			members = append(members, member{name: name, value: redact(raw, propertyPath(ptr, name), hidden)})
		}
		return mustObject(members, v)
	case '[':
		items := arrayElements(v)
		for i, item := range items {
			items[i] = redact(item, itemPath(ptr, i), hidden)
		}
		return mustArray(items, v)
	}
	return v
}

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
	return h.build(sh, encodeState(sh, h.initialValue(sh))), nil
}

func (h *Handler) initialValue(sh *shape) jsontext.Value {
	if len(h.Value) > 0 {
		return h.Value
	}
	return sh.def
}

func encodeState(sh *shape, v jsontext.Value) state {
	s := state{}
	if len(v) > 0 {
		e := encoder{state: s}
		e.encode(sh, "", v, 0)
	}
	return s
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
	s, action := h.submitted(form)
	switch action {
	case "":
	case ActionJSON:
		return h.showJSON(sh, s), nil
	case ActionFields:
		return h.showFields(sh, s), nil
	default:
		if op, _, _ := strings.Cut(action, "/"); op == "next" {
			if f := h.refuseNext(sh, s); f != nil {
				return f, nil
			}
		}
		applyAction(s, action)
		if mode, _ := s.meta(metaMode, ""); mode == ModeWizard {
			// Moving between steps shows what is wrong with the values
			// filled in, so a step listed as having errors shows them
			// when you get there; fields still blank are not flagged.
			f, _, _ := h.check(sh, s, true)
			if f.status == http.StatusUnprocessableEntity {
				f.status = http.StatusOK
			}
			return f, nil
		}
		return h.build(sh, s), nil
	}
	f, value, ok := h.check(sh, s, false)
	if !ok {
		return f, nil
	}
	if h.OnSubmit != nil {
		message, err := h.OnSubmit(ctx, value)
		if err != nil {
			f.Errors = append(f.Errors, err.Error())
			f.status = http.StatusUnprocessableEntity
			return f, nil
		}
		f.Message = message
	}
	f.Valid, f.Value = true, value
	f.writeOnly = map[string]bool{}
	var secret []string
	for _, field := range f.fields(func(field *Field) bool { return field.WriteOnly }) {
		f.writeOnly[field.Pointer] = true
		secret = append(secret, field.Path)
	}
names:
	for _, name := range s.sortedNames() {
		_, p, _ := parseName(name)
		for _, path := range secret {
			if within(p, path) {
				continue names
			}
		}
		for _, v := range s[name] {
			f.State = append(f.State, Control{Name: name, Value: v})
		}
	}
	return f, nil
}

// refuseNext checks the wizard's current step before Next leaves it. It
// returns the step again, with what is wrong, when a required field on it
// is empty or it has errors; otherwise nil. Errors on other steps do not
// hold it back.
func (h *Handler) refuseNext(sh *shape, s state) *Form {
	f, _, _ := h.check(sh, s, false)
	if f.Root == nil || f.Wizard() == nil {
		return nil
	}
	errs := problems(f.View(), true)
	if len(errs) == 0 {
		return nil
	}
	again := h.build(sh, s)
	again.attach(errs, nil, false)
	return again
}

// Validate checks the form without submitting it, so errors can be shown
// as the user types. Values that are only missing are not reported yet;
// the submission will. The response status is 200 unless the form is too
// large.
func (h *Handler) Validate(ctx context.Context, form url.Values) (*Form, error) {
	if h.Schema == nil {
		return nil, errNoSchema
	}
	s, _ := h.submitted(form)
	f, _, _ := h.check(h.root(), s, true)
	if f.status == http.StatusUnprocessableEntity {
		f.status = http.StatusOK
	}
	return f, nil
}

// submitted reads the form state and the action out of a post, leaving out
// the modes the handler does not offer: a request for one, or already in
// one, is treated as an edit that changes nothing, so the form stays as
// the fields.
func (h *Handler) submitted(form url.Values) (s state, action string) {
	s, action = submittedControls(form), form.Get(ActionName)
	op, _, _ := strings.Cut(action, "/")
	mode, _ := s.meta(metaMode, "")
	if h.NoJSON && (action == ActionJSON || mode == ModeJSON) {
		delete(s, metaName(metaMode, ""))
		if action == ActionJSON {
			action = "refresh"
		}
	}
	if h.NoWizard && (op == ActionWizard || op == "next" || mode == ModeWizard) {
		delete(s, metaName(metaMode, ""))
		delete(s, metaName(metaStep, ""))
		if op == ActionWizard || op == "next" {
			action = "refresh"
		}
	}
	return s, action
}

// submittedControls keeps the form controls of a submission and drops anything
// else posted with it.
func submittedControls(form url.Values) state {
	s := state{}
	for name, values := range form {
		if _, _, ok := parseName(name); ok {
			s[name] = slices.Clone(values)
		}
	}
	return s
}

// check builds the form for s, decodes it, keeps readOnly values and
// validates the result. live leaves out errors about missing values.
func (h *Handler) check(sh *shape, s state, live bool) (*Form, jsontext.Value, bool) {
	f := h.build(sh, s)
	if f.Root == nil {
		return f, nil, false
	}
	r := f.Root.decode()
	if r.missing {
		r.errs = append(r.errs, fieldError{path: "", message: messageRequired})
	}
	errs := r.errs
	if live {
		errs = slices.DeleteFunc(slices.Clone(errs), func(e fieldError) bool { return e.message == messageRequired })
	}
	value := r.value
	if value == nil {
		value = jsontext.Value("null")
	}
	value = keepReadOnly(sh, h.initialValue(sh), value)
	if len(errs) > 0 || (len(r.errs) > 0 && !live) {
		f.attach(errs, nil, live)
		// Show what is wrong with the rest too, not only the fields that
		// could not be read, so a form is not fixed one round at a time.
		if out := h.Schema.Validate("form", value); !out.Valid {
			f.attachPartial(out, live)
		}
		if live {
			f.quiet()
		}
		return f, nil, false
	}
	if out := h.Schema.Validate("form", value); !out.Valid {
		f.attach(nil, &out, live)
		if live {
			f.quiet()
		}
		return f, nil, false
	}
	return f, value, len(r.errs) == 0
}

// showJSON switches the form to one JSON text area holding the value the
// fields decode to. Fields that cannot be decoded keep the form as it is,
// with their errors. writeOnly values are left out of the text.
func (h *Handler) showJSON(sh *shape, s state) *Form {
	f := h.build(sh, s)
	if f.Root == nil || f.Mode == ModeJSON {
		return f
	}
	r := f.Root.decode()
	if errs := slices.DeleteFunc(r.errs, func(e fieldError) bool { return e.message == messageRequired }); len(errs) > 0 {
		f.attach(errs, nil, false)
		return f
	}
	value := r.value
	for _, field := range f.fields(func(field *Field) bool { return field.WriteOnly }) {
		value = deleteAt(value, slices.Collect(pointerTokens(field.Pointer)))
	}
	text := ""
	if len(value) > 0 {
		text = indentJSON(value)
	}
	return h.build(sh, state{metaName(metaMode, ""): {ModeJSON}, valueName(""): {text}})
}

// showFields switches the form back to every field, from the wizard or
// from JSON. JSON that does not parse keeps the text area, with the error.
func (h *Handler) showFields(sh *shape, s state) *Form {
	switch mode, _ := s.meta(metaMode, ""); mode {
	case ModeJSON:
	case ModeWizard:
		delete(s, metaName(metaMode, ""))
		delete(s, metaName(metaStep, ""))
		return h.build(sh, s)
	default:
		return h.build(sh, s)
	}
	text, _ := s.value("")
	v := jsontext.Value(strings.TrimSpace(text))
	if len(v) > 0 && !v.IsValid() {
		f := h.build(sh, s)
		f.attach([]fieldError{{path: "", message: messageJSON}}, nil, false)
		return f
	}
	return h.build(sh, encodeState(sh, v))
}

// fields returns the fields that match, outermost first.
func (f *Form) fields(match func(*Field) bool) []*Field {
	var out []*Field
	var walk func(*Field)
	walk = func(field *Field) {
		if match(field) {
			out = append(out, field)
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
	if f.Root != nil {
		walk(f.Root)
	}
	return out
}

func (h *Handler) root() *shape {
	return flatten(node{schema: h.Schema})
}

func (h *Handler) build(sh *shape, s state) *Form {
	f := &Form{
		ID:          firstNonEmpty(h.ID, "jsonform"),
		SubmitLabel: firstNonEmpty(h.SubmitLabel, "Submit"),
		ShowOutline: h.Outline,
		NoJSON:      h.NoJSON,
		NoWizard:    h.NoWizard,
	}
	limit := h.MaxFields
	if limit <= 0 {
		limit = defaultMaxFields
	}
	f.state = s
	b := builder{state: s, idPrefix: f.ID, form: f, remaining: limit}
	switch mode, _ := s.meta(metaMode, ""); mode {
	case ModeJSON:
		f.Mode = ModeJSON
		b.jsonRoot = true
	case ModeWizard:
		f.Mode = ModeWizard
		f.step, _ = s.meta(metaStep, "")
	}
	f.Root = b.build(sh, site{required: true, htmlRequired: true}, 0)
	f.Root.Implied = true // the value is there whenever the form is
	if b.tooLarge {
		f.Root = nil
		f.Errors = []string{fmt.Sprintf("the form would have more than %d fields", limit)}
		f.status = http.StatusRequestEntityTooLarge
	}
	return f
}

const defaultMaxFields = 10000

// report is one problem the validator found, at a JSON Pointer.
type report struct{ pointer, keyword, message string }

// index maps the form's fields by form path and by JSON Pointer, and picks
// out the choices.
func (f *Form) index() (byPath, byPointer, choices map[string]*Field) {
	byPath, byPointer, choices = map[string]*Field{}, map[string]*Field{}, map[string]*Field{}
	// A choice and its chosen variant share a location; the variant comes
	// later, so its control gets the errors.
	for _, field := range f.fields(func(*Field) bool { return true }) {
		byPath[field.Path] = field
		byPointer[field.Pointer] = field
		if field.Kind == KindChoice {
			choices[field.Pointer] = field
		}
	}
	return byPath, byPointer, choices
}

// attach puts decoding errors and validation errors on the fields they
// are about. A validation error at a location with no field of its own
// goes to the nearest enclosing field, prefixed with its location. live
// leaves out errors from the required keyword, which only say a value is
// missing.
func (f *Form) attach(errs []fieldError, out *jsonschema.Output, live bool) {
	f.status = http.StatusUnprocessableEntity
	byPath, byPointer, choices := f.index()
	for _, e := range errs {
		field, ok := byPath[e.path]
		switch {
		case !ok:
			f.Errors = appendNew(f.Errors, e.message)
		case e.key && field.Key != nil:
			field.Key.Errors = appendNew(field.Key.Errors, e.message)
		case e.message == messageRequired:
			field.Errors = appendNew(field.Errors, field.message("required", e.message))
		default:
			field.Errors = appendNew(field.Errors, e.message)
		}
	}
	if out != nil {
		f.attachOutput(*out, live, byPointer, choices, nil)
	}
}

// valueKeywords are the keywords that judge a value on its own: what they
// report stays true whatever else is wrong with the form.
var valueKeywords = map[string]bool{
	"type": true, "enum": true, "const": true, "format": true, "pattern": true,
	"minLength": true, "maxLength": true,
	"minimum": true, "maximum": true, "exclusiveMinimum": true, "exclusiveMaximum": true, "multipleOf": true,
	"minItems": true, "maxItems": true, "uniqueItems": true,
	"minProperties": true, "maxProperties": true,
}

// attachPartial adds what validating a partly decoded value found, for a
// form that already has problems of the kind found while reading it. It
// keeps only what is about a field with no problem yet, and only
// keywords that judge a value on its own: the value is missing the parts
// that failed to read, so "required" and the keywords that combine
// subschemas would only repeat or distort what is already shown.
func (f *Form) attachPartial(out jsonschema.Output, live bool) {
	_, byPointer, choices := f.index()
	f.attachOutput(out, live, byPointer, choices, func(r report) bool {
		field, ok := byPointer[r.pointer]
		return ok && valueKeywords[r.keyword] && len(field.Errors) == 0
	})
}

// attachOutput puts the validator's errors on the fields they are about,
// or on the nearest enclosing field. keep, if set, chooses which to show.
func (f *Form) attachOutput(out jsonschema.Output, live bool, byPointer, choices map[string]*Field, keep func(report) bool) {
	var reports []report
	collect(out, choices, func(pointer, keyword, message string) {
		reports = append(reports, report{pointer, keyword, message})
	})
	// A property in a failing subschema does not count as evaluated, so
	// unevaluatedProperties also rejects it; its own error says more.
	unknown := func(r report) bool {
		return r.keyword == "unevaluatedProperties" || r.keyword == "additionalProperties"
	}
	known := map[string]bool{}
	for _, r := range reports {
		if !unknown(r) {
			known[r.pointer] = true
		}
	}
	for _, r := range reports {
		pointer, keyword, message := r.pointer, r.keyword, r.message
		if unknown(r) && known[pointer] {
			continue
		}
		if live && (keyword == "required" || keyword == "dependentRequired") {
			continue
		}
		if keep != nil && !keep(r) {
			continue
		}
		for at := pointer; ; at, _ = splitLast(at) {
			if at != pointer {
				message = pointer + ": " + strings.TrimPrefix(message, pointer+": ")
			}
			if field, ok := byPointer[at]; ok {
				if at == pointer {
					message = field.message(keyword, message)
				}
				field.Errors = appendNew(field.Errors, message)
				break
			}
			if at == "" {
				f.Errors = appendNew(f.Errors, message)
				break
			}
		}
	}
}

// collect reports the most specific errors in a verbose output tree: a
// unit's own error only when none of its children explain the failure.
// Under a oneOf or anyOf whose location is a choice field, only the
// branch for the chosen variant is reported; the others failing is
// expected.
func collect(o jsonschema.Output, choices map[string]*Field, report func(pointer, keyword, message string)) int {
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
		report(o.InstanceLocation, keyword, o.Error)
		n = 1
	}
	return n
}

func appendNew(list []string, s string) []string {
	s = strings.TrimSpace(s)
	if slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}

package jsonhandler

import (
	"context"
	"encoding/json/jsontext"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/typelate/dom/domtest"
	"github.com/typelate/dom/spec"
	"golang.org/x/net/html/atom"

	"github.com/crhntr/jsonschema"
	"github.com/crhntr/jsonschema/internal/metaschema"
)

func newHandler(t *testing.T, schema string) *Handler {
	t.Helper()
	h, err := New(context.Background(), []byte(schema))
	if err != nil {
		t.Fatalf("New(%s): %v", schema, err)
	}
	return h
}

func metaHandler(t *testing.T) *Handler {
	t.Helper()
	body, ok := metaschema.BytesForURL(metaschema.SchemaURI)
	if !ok {
		t.Fatal("meta-schema is not embedded")
	}
	return newHandler(t, string(body))
}

func serve(t *testing.T, h *Handler, req *http.Request) *http.Response {
	t.Helper()
	mux := http.NewServeMux()
	TemplateRoutes(mux, h, "")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Result()
}

func getForm(t *testing.T, h *Handler) *http.Response {
	t.Helper()
	return serve(t, h, httptest.NewRequest(http.MethodGet, "/form", nil))
}

func postForm(t *testing.T, h *Handler, form url.Values) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/form", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return serve(t, h, req)
}

func fragment(t *testing.T, res *http.Response) spec.DocumentFragment {
	t.Helper()
	return domtest.ParseResponseDocumentFragment(t, res, atom.Div)
}

func readBody(t *testing.T, res *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// formState returns the controls a browser would submit for the form h
// renders: every named input, select and textarea with its current value,
// checkboxes and radios only when checked, and no buttons.
func formState(t *testing.T, res *http.Response) url.Values {
	t.Helper()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d: %s", res.StatusCode, readBody(t, res))
	}
	doc := fragment(t, res)
	form := doc.QuerySelector("form.jsonform")
	if form == nil {
		t.Fatalf("no form in response")
	}
	return controls(form)
}

func controls(form spec.Element) url.Values {
	values := url.Values{}
	for el := range form.QuerySelectorSequence("*") {
		if !el.HasAttribute("name") || el.HasAttribute("disabled") {
			continue
		}
		name := el.GetAttribute("name")
		switch strings.ToLower(el.TagName()) {
		case "input":
			switch el.GetAttribute("type") {
			case "checkbox", "radio":
				if el.HasAttribute("checked") {
					values.Add(name, el.GetAttribute("value"))
				}
			default:
				values.Add(name, el.GetAttribute("value"))
			}
		case "select":
			selected := ""
			if first := el.QuerySelector("option"); first != nil {
				selected = first.GetAttribute("value")
			}
			if opt := el.QuerySelector("option[selected]"); opt != nil {
				selected = opt.GetAttribute("value")
			}
			values.Add(name, selected)
		case "textarea":
			values.Add(name, strings.TrimPrefix(el.TextContent(), "\n"))
		}
	}
	return values
}

// submit posts state and returns the decoded value, failing the test when
// the submission does not succeed.
func submit(t *testing.T, h *Handler, state url.Values) jsontext.Value {
	t.Helper()
	f, err := h.SubmitForm(context.Background(), state)
	if err != nil {
		t.Fatalf("SubmitForm: %v", err)
	}
	if !f.Valid {
		t.Fatalf("SubmitForm(%v) invalid: form errors %q, field errors %v", state, f.Errors, fieldErrors(f.Root))
	}
	return f.Value
}

// fieldErrors maps form paths to the errors shown there.
func fieldErrors(f *Field) map[string][]string {
	out := map[string][]string{}
	var walk func(*Field)
	walk = func(f *Field) {
		if len(f.Errors) > 0 {
			out[f.Path] = append(out[f.Path], f.Errors...)
		}
		if f.Key != nil && len(f.Key.Errors) > 0 {
			out["key:"+f.Path] = append(out["key:"+f.Path], f.Key.Errors...)
		}
		for _, c := range f.Fields {
			walk(c)
		}
		for _, c := range f.Entries {
			walk(c)
		}
		if f.Choice != nil {
			walk(f.Choice)
		}
	}
	walk(f)
	return out
}

// formRequest is a POST of form to /form, or a GET of /form when form is
// nil.
func formRequest(form url.Values) *http.Request {
	if form == nil {
		return httptest.NewRequest(http.MethodGet, "/form", nil)
	}
	req := httptest.NewRequest(http.MethodPost, "/form", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func newRecorder() *httptest.ResponseRecorder { return httptest.NewRecorder() }

func assertJSONEqual(t *testing.T, got jsontext.Value, want string) {
	t.Helper()
	eq, err := jsonschema.Equal(got, []byte(want))
	if err != nil {
		t.Fatalf("compare %s with %s: %v", got, want, err)
	}
	if !eq {
		t.Errorf("got %s, want %s", got, want)
	}
}

package jsonhandler

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestSubmitForm_errors(t *testing.T) {
	for _, tc := range []struct {
		name, schema string
		form         url.Values
		want         map[string][]string // form path → errors; "key:" prefix for property names
		wantForm     []string
	}{
		{
			name:   "not a number",
			schema: `{"type":"object","properties":{"n":{"type":"number"}}}`,
			form:   url.Values{"@present": {"1"}, "$/n": {"abc"}},
			want:   map[string][]string{"/n": {messageNumber}},
		},
		{
			name:   "not an integer",
			schema: `{"type":"object","properties":{"n":{"type":"integer"}}}`,
			form:   url.Values{"@present": {"1"}, "$/n": {"1.5"}},
			want:   map[string][]string{"/n": {messageInteger}},
		},
		{
			name:   "invalid JSON",
			schema: `{"type":"object","properties":{"v":true}}`,
			form:   url.Values{"@present": {"1"}, "$/v": {"{"}},
			want:   map[string][]string{"/v": {messageJSON}},
		},
		{
			name:   "invalid JSON option",
			schema: `{"enum":[1,2]}`,
			form:   url.Values{"$": {"{"}},
			want:   map[string][]string{"": {messageChoice}},
		},
		{
			name:   "missing required number",
			schema: `{"type":"object","required":["n"],"properties":{"n":{"type":"number"}}}`,
			form:   url.Values{"$/n": {""}},
			want:   map[string][]string{"/n": {messageRequired}},
		},
		{
			name:   "missing root",
			schema: `{"type":"number"}`,
			form:   url.Values{"$": {""}},
			want:   map[string][]string{"": {messageRequired}},
		},
		{
			name:   "missing required item value",
			schema: `{"type":"array","items":{"type":"integer"}}`,
			form:   url.Values{"@len": {"1"}, "$/0": {""}},
			want:   map[string][]string{"/0": {messageRequired}},
		},
		{
			name:   "required inside a provided optional object",
			schema: `{"type":"object","properties":{"a":{"type":"object","required":["n"],"properties":{"n":{"type":"number"},"s":{"type":"string"}}}}}`,
			form:   url.Values{"$/a/s": {"x"}, "$/a/n": {""}},
			want:   map[string][]string{"/a/n": {messageRequired}},
		},
		{
			name:   "entry without a property name",
			schema: `{"type":"object","additionalProperties":{"type":"string"}}`,
			form:   url.Values{"@len": {"1"}, "@key/~k0": {""}, "$/~k0": {"v"}},
			want:   map[string][]string{"key:/~k0": {messageKey}},
		},
		{
			name:   "duplicate property name",
			schema: `{"type":"object","additionalProperties":{"type":"string"}}`,
			form:   url.Values{"@len": {"2"}, "@key/~k0": {"a"}, "$/~k0": {"1"}, "@key/~k1": {"a"}, "$/~k1": {"2"}},
			want:   map[string][]string{"key:/~k1": {messageDuplicate}},
		},
		{
			name:   "entry duplicating a declared property",
			schema: `{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":{"type":"string"}}`,
			form:   url.Values{"$/a": {"x"}, "@len": {"1"}, "@key/~k0": {"a"}, "$/~k0": {"y"}},
			want:   map[string][]string{"key:/~k0": {messageDuplicate}},
		},
		{
			name:   "entry with a missing value",
			schema: `{"type":"object","additionalProperties":{"type":"integer"}}`,
			form:   url.Values{"@len": {"1"}, "@key/~k0": {"a"}, "$/~k0": {""}},
			want:   map[string][]string{"/~k0": {messageRequired}},
		},
		{
			name:   "validation error on a field",
			schema: `{"type":"object","properties":{"name":{"type":"string","minLength":3}}}`,
			form:   url.Values{"$/name": {"ab"}},
			want:   map[string][]string{"/name": {"string length 2 less than minLength 3"}},
		},
		{
			name:   "validation error inside a map entry",
			schema: `{"type":"object","additionalProperties":{"type":"string","maxLength":1}}`,
			form:   url.Values{"@len": {"1"}, "@key/~k0": {"k"}, "$/~k0": {"long"}},
			want:   map[string][]string{"/~k0": {"string length 4 greater than maxLength 1"}},
		},
		{
			name:   "validation error below a JSON field goes to the field",
			schema: `{"type":"object","properties":{"v":{"x-form":{"widget":"json"},"type":"object","properties":{"a":{"type":"string"}}}}}`,
			form:   url.Values{"$/v": {`{"a":1}`}},
			want:   map[string][]string{"/v": {"type string does not match number"}},
		},
		{
			name:   "errors only from the chosen variant",
			schema: `{"type":"object","properties":{"v":{"oneOf":[{"type":"string","minLength":3},{"type":"integer","minimum":10}]}}}`,
			form:   url.Values{"@variant/v": {"1"}, "$/v": {"5"}},
			want:   map[string][]string{"/v": {"less than minimum 10"}},
		},
		{
			name:     "error at the root of a closed object",
			schema:   `{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":false}`,
			form:     url.Values{"$/a": {"x"}, "@len": {"1"}, "@key/~k0": {"b"}, "$/~k0": {"1"}},
			wantForm: nil,
			want:     map[string][]string{"/~k0": {"nothing allowed here"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHandler(t, tc.schema)
			f, err := h.SubmitForm(context.Background(), tc.form)
			if err != nil {
				t.Fatal(err)
			}
			if f.Valid {
				t.Fatalf("SubmitForm(%v) valid with %s, want errors", tc.form, f.Value)
			}
			if f.StatusCode() != http.StatusUnprocessableEntity {
				t.Errorf("StatusCode() = %d, want 422", f.StatusCode())
			}
			if got := fieldErrors(f.Root); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("field errors = %q, want %q", got, tc.want)
			}
			if !reflect.DeepEqual(f.Errors, tc.wantForm) {
				t.Errorf("form errors = %q, want %q", f.Errors, tc.wantForm)
			}
		})
	}
}

func TestSubmitForm_values(t *testing.T) {
	for _, tc := range []struct {
		name, schema string
		form         url.Values
		want         string
	}{
		{
			name:   "blank optional object is left out",
			schema: `{"type":"object","properties":{"a":{"type":"object","required":["n"],"properties":{"n":{"type":"number"},"b":{"type":"boolean"}}}}}`,
			form:   url.Values{"$/a/n": {""}, "$/a/b": {"false"}},
			want:   `{}`,
		},
		{
			name:   "required empty string",
			schema: `{"type":"object","required":["s"],"properties":{"s":{"type":"string"}}}`,
			form:   url.Values{"$/s": {""}},
			want:   `{"s":""}`,
		},
		{
			name:   "required blank nullable is null",
			schema: `{"type":"object","required":["n"],"properties":{"n":{"type":["number","null"]}}}`,
			form:   url.Values{"$/n": {""}},
			want:   `{"n":null}`,
		},
		{
			name:   "required blank nullable enum is null",
			schema: `{"type":"object","required":["e"],"properties":{"e":{"type":["string","null"],"enum":["a",null]}}}`,
			form:   url.Values{"$/e": {""}},
			want:   `{"e":null}`,
		},
		{
			name:   "required null type",
			schema: `{"type":"object","required":["z"],"properties":{"z":{"type":"null"},"o":{"type":"null"}}}`,
			form:   url.Values{},
			want:   `{"z":null}`,
		},
		{
			name:   "optional const is left out",
			schema: `{"type":"object","properties":{"c":{"const":1}}}`,
			form:   url.Values{},
			want:   `{}`,
		},
		{
			name:   "checkbox takes the last value",
			schema: `{"type":"object","required":["b"],"properties":{"b":{"type":"boolean"}}}`,
			form:   url.Values{"$/b": {"false", "true"}},
			want:   `{"b":true}`,
		},
		{
			name:   "blank map entry is ignored",
			schema: `{"type":"object","additionalProperties":{"type":"string"}}`,
			form:   url.Values{"@len": {"2"}, "@key/~k0": {""}, "$/~k0": {""}, "@key/~k1": {"k"}, "$/~k1": {"v"}},
			want:   `{"k":"v"}`,
		},
		{
			name:   "number keeps its spelling",
			schema: `{"type":"number"}`,
			form:   url.Values{"$": {" 1e3 "}},
			want:   `1000`,
		},
		{
			name:   "integer with zero fraction",
			schema: `{"type":"integer"}`,
			form:   url.Values{"$": {"2.0"}},
			want:   `2`,
		},
		{
			name:   "datetime-local without seconds",
			schema: `{"type":"string","format":"date-time"}`,
			form:   url.Values{"$": {"2024-05-06T07:08"}},
			want:   `"2024-05-06T07:08:00Z"`,
		},
		{
			name:   "time without seconds",
			schema: `{"type":"string","format":"time"}`,
			form:   url.Values{"$": {"07:08"}},
			want:   `"07:08:00Z"`,
		},
		{
			name:   "date-time typed by hand passes through",
			schema: `{"type":"string"}`,
			form:   url.Values{"$": {"2024-05-06T07:08"}},
			want:   `"2024-05-06T07:08"`,
		},
		{
			name:   "invalid UTF-8 is replaced",
			schema: `{"type":"object","additionalProperties":{"type":"string"}}`,
			form:   url.Values{"@len": {"1"}, "@key/~k0": {"a\xff"}, "$/~k0": {"b\xff"}},
			want:   `{"a\ufffd":"b\ufffd"}`,
		},
		{
			name:   "unknown controls are ignored",
			schema: `{"type":"object","properties":{"a":{"type":"string"}}}`,
			form:   url.Values{"$/a": {"x"}, "csrf": {"token"}, "@nope": {"1"}, "$bad": {"1"}},
			want:   `{"a":"x"}`,
		},
		{
			name:   "variant out of range uses the first",
			schema: `{"oneOf":[{"type":"string"},{"type":"integer"}]}`,
			form:   url.Values{"@variant": {"7"}, "$": {"s"}},
			want:   `"s"`,
		},
		{
			name:   "checkbox group with JSON options",
			schema: `{"type":"array","uniqueItems":true,"items":{"enum":[1,2,3]}}`,
			form:   url.Values{"$": {"", "3", "1"}},
			want:   `[1,3]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHandler(t, tc.schema)
			assertJSONEqual(t, submit(t, h, tc.form), tc.want)
		})
	}
}

func TestSubmitForm_actions(t *testing.T) {
	const schema = `{"type":"object","properties":{"tags":{"type":"array","items":{"type":"string"}},"m":{"type":"object","additionalProperties":{"type":"string"}},"next":{"$ref":"#"}}}`
	for _, tc := range []struct {
		name   string
		form   url.Values
		action string
		want   url.Values
	}{
		{
			name:   "add an item",
			form:   url.Values{"@len/tags": {"1"}, "$/tags/0": {"a"}},
			action: "add/tags",
			want:   url.Values{"@len/tags": {"2"}, "@present/tags": {"1"}, "$/tags/0": {"a"}, "$/tags/1": {""}},
		},
		{
			name:   "remove an item",
			form:   url.Values{"@len/tags": {"3"}, "$/tags/0": {"a"}, "$/tags/1": {"b"}, "$/tags/2": {"c"}},
			action: "remove/tags/0",
			want:   url.Values{"@len/tags": {"2"}, "$/tags/0": {"b"}, "$/tags/1": {"c"}},
		},
		{
			name:   "move an item up",
			form:   url.Values{"@len/tags": {"2"}, "$/tags/0": {"a"}, "$/tags/1": {"b"}},
			action: "up/tags/1",
			want:   url.Values{"@len/tags": {"2"}, "$/tags/0": {"b"}, "$/tags/1": {"a"}},
		},
		{
			name:   "move an item down",
			form:   url.Values{"@len/tags": {"2"}, "$/tags/0": {"a"}, "$/tags/1": {"b"}},
			action: "down/tags/0",
			want:   url.Values{"@len/tags": {"2"}, "$/tags/0": {"b"}, "$/tags/1": {"a"}},
		},
		{
			name:   "move the first item up does nothing",
			form:   url.Values{"@len/tags": {"2"}, "$/tags/0": {"a"}, "$/tags/1": {"b"}},
			action: "up/tags/0",
			want:   url.Values{"@len/tags": {"2"}, "$/tags/0": {"a"}, "$/tags/1": {"b"}},
		},
		{
			name:   "move the last item down does nothing",
			form:   url.Values{"@len/tags": {"2"}, "$/tags/0": {"a"}, "$/tags/1": {"b"}},
			action: "down/tags/1",
			want:   url.Values{"@len/tags": {"2"}, "$/tags/0": {"a"}, "$/tags/1": {"b"}},
		},
		{
			name:   "remove an entry",
			form:   url.Values{"@len/m": {"2"}, "@key/m/~k0": {"a"}, "$/m/~k0": {"1"}, "@key/m/~k1": {"b"}, "$/m/~k1": {"2"}},
			action: "remove/m/~k0",
			want:   url.Values{"@len/m": {"1"}, "@key/m/~k0": {"b"}, "$/m/~k0": {"2"}},
		},
		{
			name:   "move an entry",
			form:   url.Values{"@len/m": {"2"}, "@key/m/~k0": {"a"}, "$/m/~k0": {"1"}, "@key/m/~k1": {"b"}, "$/m/~k1": {"2"}},
			action: "down/m/~k0",
			want:   url.Values{"@len/m": {"2"}, "@key/m/~k0": {"b"}, "$/m/~k0": {"2"}, "@key/m/~k1": {"a"}, "$/m/~k1": {"1"}},
		},
		{
			name:   "expand a recursive value",
			form:   url.Values{},
			action: "expand/next",
			want:   url.Values{"@present/next": {"1"}, "@len/next/tags": {"0"}, "@len/next/m": {"0"}},
		},
		{
			name:   "collapse a recursive value",
			form:   url.Values{"@present/next": {"1"}, "@len/next/tags": {"1"}, "$/next/tags/0": {"x"}},
			action: "remove/next",
			want:   url.Values{},
		},
		{
			name:   "remove past the end does nothing",
			form:   url.Values{"@len/tags": {"1"}, "$/tags/0": {"a"}},
			action: "remove/tags/5",
			want:   url.Values{"@len/tags": {"1"}, "$/tags/0": {"a"}},
		},
		{
			name:   "unknown action re-renders",
			form:   url.Values{"@len/tags": {"1"}, "$/tags/0": {"a"}},
			action: "refresh",
			want:   url.Values{"@len/tags": {"1"}, "$/tags/0": {"a"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHandler(t, schema)
			form := url.Values{}
			for k, v := range tc.form {
				form[k] = v
			}
			form.Set(ActionName, tc.action)
			res := postForm(t, h, form)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", res.StatusCode)
			}
			got := formState(t, res)
			want := url.Values{"@len/tags": {"0"}, "@len/m": {"0"}}
			for k, v := range tc.want {
				want[k] = v
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("after %s:\n got  %v\n want %v", tc.action, got, want)
			}
		})
	}
}

func TestRoutes(t *testing.T) {
	const schema = `{"type":"object","required":["name"],"properties":{"name":{"type":"string","minLength":1}}}`

	t.Run("get renders a form partial", func(t *testing.T) {
		res := getForm(t, newHandler(t, schema))
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", res.StatusCode)
		}
		body := readBody(t, res)
		if strings.Contains(body, "<html") || strings.Contains(body, "<body") {
			t.Errorf("response is a full page, want a partial:\n%s", body)
		}
		if !strings.HasPrefix(body, `<form class="jsonform"`) {
			t.Errorf("response does not start with the form:\n%s", body)
		}
	})

	t.Run("invalid submission is 422 with the form", func(t *testing.T) {
		res := postForm(t, newHandler(t, schema), url.Values{"$/name": {""}})
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", res.StatusCode)
		}
		doc := fragment(t, res)
		field := doc.QuerySelector(`[data-pointer="/name"]`)
		if field == nil || !field.HasAttribute("data-invalid") {
			t.Fatalf("name field is not marked invalid")
		}
		input := field.QuerySelector("input")
		if got := input.GetAttribute("aria-invalid"); got != "true" {
			t.Errorf("aria-invalid = %q, want true", got)
		}
		if got := input.GetAttribute("aria-describedby"); got != "jsonform-.name-errors" {
			t.Errorf("aria-describedby = %q", got)
		}
		if el := doc.QuerySelector("#jsonform-\\.name-errors li"); el == nil {
			t.Errorf("no error list item")
		}
	})

	t.Run("valid submission renders the result", func(t *testing.T) {
		h := newHandler(t, schema)
		var got jsontext.Value
		h.OnSubmit = func(_ context.Context, v jsontext.Value) (string, error) {
			got = v
			return "saved", nil
		}
		res := postForm(t, h, url.Values{"$/name": {"Ada"}, "@present": {"1"}})
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", res.StatusCode)
		}
		assertJSONEqual(t, got, `{"name":"Ada"}`)
		doc := fragment(t, res)
		if code := doc.QuerySelector(".jsonform-result output code"); code == nil || !strings.Contains(code.TextContent(), `"Ada"`) {
			t.Errorf("result does not show the value")
		}
		if msg := doc.QuerySelector(".jsonform-message"); msg == nil || msg.TextContent() != "saved" {
			t.Errorf("result does not show the message")
		}
		edit := doc.QuerySelector(".jsonform-result form")
		state := controls(edit)
		if state.Get("$/name") != "Ada" || state.Get("@present") != "1" {
			t.Errorf("edit form state = %v", state)
		}
	})

	t.Run("edit reopens the submitted form", func(t *testing.T) {
		res := postForm(t, newHandler(t, schema), url.Values{"$/name": {"Ada"}, ActionName: {"edit"}})
		if got := formState(t, res).Get("$/name"); got != "Ada" {
			t.Errorf("name = %q, want Ada", got)
		}
	})

	t.Run("OnSubmit error is shown on the form", func(t *testing.T) {
		h := newHandler(t, schema)
		h.OnSubmit = func(context.Context, jsontext.Value) (string, error) { return "", errors.New("name taken") }
		res := postForm(t, h, url.Values{"$/name": {"Ada"}})
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", res.StatusCode)
		}
		if el := fragment(t, res).QuerySelector("form > .jsonform-errors li"); el == nil || el.TextContent() != "name taken" {
			t.Errorf("form error not shown")
		}
	})

	t.Run("mounted under a prefix", func(t *testing.T) {
		mux := http.NewServeMux()
		paths := TemplateRoutes(mux, newHandler(t, schema), "/people/new")
		if got := paths.Form().String(); got != "/people/new/form" {
			t.Errorf("Form path = %q", got)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/people/new/form", nil))
		form := fragment(t, rec.Result()).QuerySelector("form")
		if form == nil {
			t.Fatal("no form")
		}
		for _, attr := range []string{"action", "hx-post"} {
			if got := form.GetAttribute(attr); got != "/people/new/form" {
				t.Errorf("%s = %q, want /people/new/form", attr, got)
			}
		}
	})

	t.Run("missing schema", func(t *testing.T) {
		for _, req := range []*http.Request{
			httptest.NewRequest(http.MethodGet, "/form", nil),
			httptest.NewRequest(http.MethodPost, "/form", nil),
		} {
			res := serve(t, &Handler{}, req)
			if res.StatusCode != http.StatusInternalServerError {
				t.Errorf("%s status = %d, want 500", req.Method, res.StatusCode)
			}
			if el := fragment(t, res).QuerySelector(".jsonform-failure"); el == nil {
				t.Errorf("%s: no failure message", req.Method)
			}
		}
	})
}

func TestNew(t *testing.T) {
	for _, schema := range []string{
		`{`,
		`{"$ref":"https://example.com/elsewhere.json"}`,
	} {
		if _, err := New(context.Background(), []byte(schema)); err == nil {
			t.Errorf("New(%s) succeeded, want an error", schema)
		}
	}
	if _, err := New(context.Background(), []byte(`{"$ref":"https://json-schema.org/draft/2020-12/schema"}`)); err != nil {
		t.Errorf("New with a meta-schema reference: %v", err)
	}
}

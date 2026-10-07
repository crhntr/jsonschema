package jsonhandler

import (
	"context"
	"encoding/json/jsontext"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestJSONMode(t *testing.T) {
	const schema = `{"type":"object","required":["name"],"properties":{"name":{"type":"string","minLength":2},"tags":{"type":"array","items":{"type":"string"}},"password":{"type":"string","writeOnly":true}}}`

	t.Run("switch to JSON shows the decoded value", func(t *testing.T) {
		h := newHandler(t, schema)
		res := postForm(t, h, url.Values{"$/name": {"Ada"}, "@len/tags": {"1"}, "$/tags/0": {"x"}, "$/password": {"s3cret"}, ActionName: {ActionJSON}})
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", res.StatusCode)
		}
		doc := fragment(t, res)
		form := doc.QuerySelector("form.jsonform")
		if got := form.GetAttribute("data-mode"); got != ModeJSON {
			t.Errorf("data-mode = %q", got)
		}
		state := controls(form)
		if state.Get("@mode") != ModeJSON {
			t.Errorf("@mode = %q", state.Get("@mode"))
		}
		assertJSONEqual(t, jsontext.Value(state.Get("$")), `{"name":"Ada","tags":["x"]}`)
		if button := doc.QuerySelector(`.jsonform-mode`); button == nil || button.GetAttribute("value") != ActionFields {
			t.Errorf("no button to switch back")
		}
		if doc.QuerySelector(`[data-kind="json"][data-pointer=""] textarea`) == nil {
			t.Errorf("no JSON text area for the root")
		}
	})

	t.Run("switch to JSON keeps field errors", func(t *testing.T) {
		res := postForm(t, newHandler(t, schema), url.Values{"$/name": {"Ada"}, "@len/tags": {"0"}, ActionName: {ActionJSON}, "$/n": {"x"}})
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", res.StatusCode)
		}
		h := newHandler(t, `{"type":"object","properties":{"n":{"type":"number"}}}`)
		f, err := h.SubmitForm(context.Background(), url.Values{"$/n": {"x"}, ActionName: {ActionJSON}})
		if err != nil {
			t.Fatal(err)
		}
		if f.Mode != "" || f.StatusCode() != http.StatusUnprocessableEntity {
			t.Errorf("mode = %q status = %d, want fields and 422", f.Mode, f.StatusCode())
		}
		if got := fieldErrors(f.Root); !reflect.DeepEqual(got, map[string][]string{"/n": {messageNumber}}) {
			t.Errorf("errors = %v", got)
		}
	})

	t.Run("switch back to fields", func(t *testing.T) {
		res := postForm(t, newHandler(t, schema), url.Values{"@mode": {ModeJSON}, "$": {`{"name":"Bo","tags":["a","b"]}`}, ActionName: {ActionFields}})
		state := formState(t, res)
		want := url.Values{"$/name": {"Bo"}, "@present": {"1"}, "@present/tags": {"1"}, "@len/tags": {"2"}, "$/tags/0": {"a"}, "$/tags/1": {"b"}, "$/password": {""}}
		if !reflect.DeepEqual(state, want) {
			t.Errorf("state = %v\nwant    %v", state, want)
		}
	})

	t.Run("switch back with invalid JSON", func(t *testing.T) {
		res := postForm(t, newHandler(t, schema), url.Values{"@mode": {ModeJSON}, "$": {`{"name":`}, ActionName: {ActionFields}})
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("status = %d, want 422", res.StatusCode)
		}
		doc := fragment(t, res)
		if doc.QuerySelector(`form[data-mode="json"]`) == nil {
			t.Errorf("left JSON mode")
		}
		if el := doc.QuerySelector("#jsonform_root_errors li"); el == nil || el.TextContent() != messageJSON {
			t.Errorf("no JSON error")
		}
	})

	t.Run("switch back from fields does nothing", func(t *testing.T) {
		state := formState(t, postForm(t, newHandler(t, schema), url.Values{"$/name": {"Ada"}, ActionName: {ActionFields}}))
		if state.Get("$/name") != "Ada" {
			t.Errorf("state = %v", state)
		}
	})

	t.Run("submit in JSON mode", func(t *testing.T) {
		h := newHandler(t, schema)
		assertJSONEqual(t, submit(t, h, url.Values{"@mode": {ModeJSON}, "$": {`{"name":"Ada"}`}}), `{"name":"Ada"}`)
	})

	t.Run("validation errors in JSON mode name their location", func(t *testing.T) {
		h := newHandler(t, schema)
		f, err := h.SubmitForm(context.Background(), url.Values{"@mode": {ModeJSON}, "$": {`{"name":"A"}`}})
		if err != nil {
			t.Fatal(err)
		}
		if got := fieldErrors(f.Root); !reflect.DeepEqual(got, map[string][]string{"": {"/name: string length 1 less than minLength 2"}}) {
			t.Errorf("errors = %q", got)
		}
	})
}

func TestValidate(t *testing.T) {
	const schema = `{"type":"object","required":["name","age"],"properties":{"name":{"type":"string","minLength":2},"age":{"type":"integer"},"m":{"type":"object","additionalProperties":{"type":"string"}}}}`
	for _, tc := range []struct {
		name string
		form url.Values
		want map[string][]string
	}{
		{
			name: "reports invalid values",
			form: url.Values{"$/name": {"A"}, "$/age": {"3"}},
			want: map[string][]string{"/name": {"string length 1 less than minLength 2"}},
		},
		{
			name: "reports values that do not decode",
			form: url.Values{"$/name": {"Ada"}, "$/age": {"1.5"}},
			want: map[string][]string{"/age": {messageInteger}},
		},
		{
			name: "holds back missing values",
			form: url.Values{"$/name": {"A"}, "$/age": {""}},
			want: map[string][]string{"/name": {"string length 1 less than minLength 2"}},
		},
		{
			name: "valid",
			form: url.Values{"$/name": {"Ada"}, "$/age": {"3"}},
			want: map[string][]string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHandler(t, schema)
			called := false
			h.OnSubmit = func(context.Context, jsontext.Value) (string, error) {
				called = true
				return "", nil
			}
			f, err := h.Validate(context.Background(), tc.form)
			if err != nil {
				t.Fatal(err)
			}
			if f.StatusCode() != http.StatusOK {
				t.Errorf("status = %d, want 200", f.StatusCode())
			}
			if got := fieldErrors(f.Root); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("errors = %q, want %q", got, tc.want)
			}
			if called {
				t.Errorf("Validate called OnSubmit")
			}
		})
	}

	t.Run("route swaps every error list out of band", func(t *testing.T) {
		h := newHandler(t, schema)
		form := url.Values{"$/name": {"A"}, "$/age": {"3"}, "@len/m": {"1"}, "@key/m/~k0": {""}, "$/m/~k0": {"v"}}
		req := httptest.NewRequest(http.MethodPost, "/form/validate", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res := serve(t, h, req)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", res.StatusCode)
		}
		doc := fragment(t, res)
		var ids []string
		for el := range doc.QuerySelectorSequence("ul") {
			if el.GetAttribute("hx-swap-oob") != "true" {
				t.Errorf("%s is not swapped out of band", el.ID())
			}
			ids = append(ids, el.ID())
		}
		want := []string{"jsonform_errors", "jsonform_root_errors", "jsonform-name_errors", "jsonform-age_errors", "jsonform-m_errors", "jsonform-m-_7ek0_errors", "jsonform-m-_7ek0_key_errors"}
		if !reflect.DeepEqual(ids, want) {
			t.Errorf("ids = %q\nwant  %q", ids, want)
		}
		if el := doc.QuerySelector("#jsonform-m-_7ek0_key_errors li"); el == nil || el.TextContent() != messageKey {
			t.Errorf("no key error")
		}
	})

	t.Run("every error list is in the form", func(t *testing.T) {
		h := newHandler(t, schema)
		form := url.Values{"$/name": {"A"}, "$/age": {"3"}, "@len/m": {"1"}, "@key/m/~k0": {"k"}, "$/m/~k0": {"v"}}
		doc := fragment(t, postForm(t, h, url.Values{ActionName: {"refresh"}, "@len/m": form["@len/m"], "@key/m/~k0": form["@key/m/~k0"], "$/m/~k0": form["$/m/~k0"]}))
		for _, id := range []string{"jsonform_errors", "jsonform_root_errors", "jsonform-name_errors", "jsonform-age_errors", "jsonform-m_errors", "jsonform-m-_7ek0_errors", "jsonform-m-_7ek0_key_errors"} {
			if doc.QuerySelector("#"+id) == nil {
				t.Errorf("no #%s in the form", id)
			}
		}
		live := doc.QuerySelector(".jsonform-live")
		if live == nil || live.GetAttribute("hx-post") != "/form/validate" || live.GetAttribute("hx-swap") != "none" {
			t.Errorf("live validation trigger = %v", live)
		}
	})
}

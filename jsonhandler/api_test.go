package jsonhandler

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// A document whose definitions refer to each other, with its own $id.
const definitions = `{
	"$id": "https://example.com/catalog.json",
	"type": "object",
	"properties": {"items": {"type": "array", "items": {"$ref": "#/$defs/item"}}},
	"$defs": {
		"item": {"title": "Item", "type": "object", "required": ["name", "price"], "properties": {"name": {"type": "string"}, "price": {"$ref": "#/$defs/money"}}},
		"money": {"type": "number", "minimum": 0}
	}
}`

// TestSaveAfterSubmit is the flow an app uses without OnSubmit: start from
// the stored value, save after a valid submission, and show the form again
// when saving fails.
func TestSaveAfterSubmit(t *testing.T) {
	ctx := context.Background()
	base, err := NewAt(ctx, []byte(definitions), "/$defs/item")
	if err != nil {
		t.Fatal(err)
	}
	h := base.WithValue(jsontext.Value(`{"name":"pen","price":2}`))
	if base.Value != nil {
		t.Errorf("WithValue changed the handler it was called on")
	}
	f, err := h.Form(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Root.Fields[0].Value; got != "pen" {
		t.Errorf("form starts with name %q, want pen", got)
	}

	f, err = h.SubmitForm(ctx, url.Values{"$/name": {"pen"}, "$/price": {"3"}})
	if err != nil || !f.Valid {
		t.Fatalf("submission valid = %v, err %v", f != nil && f.Valid, err)
	}
	f.Reject(errors.New("That name is taken."))
	if f.Valid || f.Value != nil || f.StatusCode() != http.StatusUnprocessableEntity {
		t.Errorf("after Reject: valid %v, value %s, status %d", f.Valid, f.Value, f.StatusCode())
	}
	if !slices.Equal(f.Errors, []string{"That name is taken."}) {
		t.Errorf("form errors = %q", f.Errors)
	}
	if got := f.Root.Fields[1].Value; got != "3" {
		t.Errorf("the rejected form shows price %q, want what was submitted", got)
	}
}

func TestFormHTML(t *testing.T) {
	ctx := context.Background()
	h, err := NewAt(ctx, []byte(definitions), "/$defs/item")
	if err != nil {
		t.Fatal(err)
	}
	render := func(name string, f *Form) string {
		t.Helper()
		tmpl, err := Templates()
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		if err := tmpl.ExecuteTemplate(&b, name, f); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}

	f, err := h.Form(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.WithActions("/items/form", "/items/form/validate")
	got, err := f.HTML()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != render("jsonhandler-form", f) {
		t.Errorf("HTML() differs from the jsonhandler-form template")
	}

	v, err := h.Validate(ctx, url.Values{"$/name": {"pen"}, "$/price": {"-1"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err = v.ValidationHTML()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != render("jsonhandler-validation", v) || !strings.Contains(string(got), "less than minimum 0") {
		t.Errorf("ValidationHTML() = %s", got)
	}

	ok, err := h.SubmitForm(ctx, url.Values{"$/name": {"pen"}, "$/price": {"2"}})
	if err != nil || !ok.Valid {
		t.Fatalf("valid submission: %v", err)
	}
	got, err = ok.HTML()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != render("jsonhandler-result", ok) {
		t.Errorf("HTML() of a valid submission is not the result partial")
	}
}

func TestNewAt(t *testing.T) {
	ctx := context.Background()
	h, err := NewAt(ctx, []byte(definitions), "/$defs/item")
	if err != nil {
		t.Fatal(err)
	}
	f, err := h.SubmitForm(ctx, url.Values{"$/name": {"pen"}, "$/price": {"-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := fieldErrors(f.Root)["/price"]; len(got) != 1 || got[0] != "less than minimum 0" {
		t.Errorf("errors on /price = %q; the reference to money did not resolve", got)
	}
	f, err = h.SubmitForm(ctx, url.Values{"$/name": {"pen"}, "$/price": {"2.5"}})
	if err != nil {
		t.Fatal(err)
	}
	if !f.Valid || string(f.Value) != `{"name":"pen","price":2.5}` {
		t.Errorf("valid submission = %v %s", f.Valid, f.Value)
	}
	if _, err := NewAt(ctx, []byte(definitions), "/$defs/nope"); err == nil {
		t.Errorf("NewAt with a pointer to nothing succeeded")
	}
}

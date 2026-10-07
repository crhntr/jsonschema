package jsonhandler

import (
	"context"
	"encoding/json/jsontext"
	"net/url"
	"strconv"
	"testing"

	"github.com/crhntr/jsonschema"
	"github.com/crhntr/jsonschema/internal/metaschema"
)

// fuzzSchemas exercise every kind: objects, maps, arrays, tuples, choices,
// enums, consts, nullable values, formats, recursion, readOnly and
// writeOnly.
var fuzzSchemas = []string{
	`{"type":"object","required":["name"],"properties":{
		"name":{"type":"string","minLength":1},
		"age":{"type":"integer","minimum":0},
		"score":{"type":["number","null"]},
		"tags":{"type":"array","items":{"type":"string"}},
		"flags":{"type":"array","uniqueItems":true,"items":{"enum":["a","b",1]}},
		"pet":{"oneOf":[{"type":"object","required":["kind"],"properties":{"kind":{"const":"dog"}}},{"type":"string"}]},
		"labels":{"type":"object","additionalProperties":{"type":"integer"}},
		"when":{"type":"string","format":"date-time"},
		"id":{"type":"string","readOnly":true},
		"secret":{"type":"string","writeOnly":true},
		"any":true,
		"next":{"$ref":"#"},
		"pair":{"type":"array","prefixItems":[{"type":"string"},{"type":"boolean"}],"items":false}
	}}`,
}

func fuzzHandlers(f *testing.F) []*Handler {
	ctx := context.Background()
	var hs []*Handler
	for _, s := range fuzzSchemas {
		h, err := New(ctx, []byte(s))
		if err != nil {
			f.Fatal(err)
		}
		h.MaxFields = 500
		hs = append(hs, h)
	}
	body, _ := metaschema.BytesForURL(metaschema.SchemaURI)
	h, err := New(ctx, body)
	if err != nil {
		f.Fatal(err)
	}
	h.MaxFields = 500
	return append(hs, h)
}

// FuzzSubmitForm posts arbitrary form bodies. Nothing may panic, and a
// submission reported valid must hold a value the schema accepts.
func FuzzSubmitForm(f *testing.F) {
	for _, seed := range []string{
		"",
		"$/name=Ada&@len/tags=2&$/tags/0=x&$/tags/1=y",
		"@variant/pet=0&@present/pet=1&$/name=x",
		"@len/labels=2&@key/labels/~k0=a&$/labels/~k0=1&@key/labels/~k1=a&$/labels/~k1=x",
		"@mode=json&$=%7B%22name%22%3A%22x%22%7D",
		"@mode=json&$=%7B&@action=fields",
		"$/name=x&@action=json",
		"@len/tags=3&$/tags/2=z&@action=remove/tags/0",
		"@len/next/tags=1&@present/next=1&@action=up/next/tags/0",
		"@pick=description&@action=pick",
		"@len=99999999999999999999&@len/tags=-1&@variant/pet=-3",
		"$/when=2024-01-01T10:00&$/name=%FF",
		"@action=remove&@action=add/tags",
		"$/id=forged&$/secret=s&$/name=n",
	} {
		f.Add(seed)
	}
	hs := fuzzHandlers(f)
	f.Fuzz(func(t *testing.T, body string) {
		form, err := url.ParseQuery(body)
		if err != nil {
			return
		}
		for _, h := range hs {
			out, err := h.SubmitForm(context.Background(), form)
			if err != nil {
				t.Fatalf("SubmitForm(%q): %v", body, err)
			}
			if out.Valid {
				if v := h.Schema.Validate("fuzz", out.Value); !v.Valid {
					t.Fatalf("SubmitForm(%q) valid with %s, which fails validation: %v", body, out.Value, v.AsError())
				}
			}
			if _, err := h.Validate(context.Background(), form); err != nil {
				t.Fatalf("Validate(%q): %v", body, err)
			}
		}
	})
}

// FuzzEncode renders a form for arbitrary JSON values and submits it
// unchanged. Encoding then decoding may normalize a value (an unchecked
// optional box, a checkbox group in enum order), but doing it again must
// change nothing.
func FuzzEncode(f *testing.F) {
	for _, seed := range []string{
		`{"name":"x","tags":["a"],"pet":{"kind":"dog"},"labels":{"a":1},"next":{"name":"y"}}`,
		`{"name":"x","flags":[1,"b"],"pair":["a",true],"any":{"z":[null]}}`,
		`{"name":"","score":null,"when":"2024-01-02T03:04:05Z"}`,
		`[1,2,3]`,
		`"string"`,
		`{"type":["object","boolean"],"properties":{"a":{"$ref":"#/$defs/a"}},"$defs":{"a":true}}`,
		`{"$id":"x","allOf":[{"enum":[1,{"a":[]}]}],"x-form":{"order":["a"]}}`,
	} {
		f.Add(seed)
	}
	hs := fuzzHandlers(f)
	f.Fuzz(func(t *testing.T, value string) {
		v := jsontext.Value(value)
		if !v.IsValid() {
			return
		}
		for _, h := range hs {
			first, ok := roundTrip(t, h, v)
			if !ok {
				continue
			}
			second, ok := roundTrip(t, h, first)
			if !ok {
				t.Fatalf("value %s round-tripped to %s, which does not submit", value, first)
			}
			if eq, err := jsonschema.Equal(first, second); err != nil || !eq {
				t.Fatalf("round trip of %s is not stable:\n first  %s\n second %s", value, first, second)
			}
		}
	})
}

// roundTrip encodes v into form state and submits it. ok is false when
// the result is not a valid submission, which is expected for values the
// schema rejects.
func roundTrip(t *testing.T, h *Handler, v jsontext.Value) (jsontext.Value, bool) {
	t.Helper()
	sh := h.root()
	s := encodeState(sh, v)
	f := h.build(sh, s)
	if f.Root == nil {
		return nil, false
	}
	// Submit what a browser would: the state as rendered, which the
	// builder has filled in with defaults and counts.
	form := url.Values{}
	for _, field := range f.fields(func(*Field) bool { return true }) {
		renderedControls(field, form)
	}
	out, err := h.SubmitForm(context.Background(), form)
	if err != nil {
		t.Fatal(err)
	}
	return out.Value, out.Valid
}

// renderedControls adds the controls the templates render for a field.
func renderedControls(f *Field, form url.Values) {
	if f.Collapsed {
		return
	}
	if f.PresentName != "" {
		form.Set(f.PresentName, presentValue)
	}
	if f.LenName != "" {
		form.Set(f.LenName, strconv.Itoa(f.Len))
	}
	if f.Key != nil {
		form.Set(f.Key.Name, f.Key.Value)
	}
	switch f.Kind {
	case KindText, KindTextarea, KindNumber, KindJSON:
		form.Set(f.Name, f.Value)
	case KindCheckbox:
		form.Set(f.Name, "false")
		if f.Checked {
			form.Add(f.Name, "true")
		}
	case KindSelect, KindRadio:
		form.Set(f.Name, f.Value)
	case KindCheckboxes:
		form.Set(f.Name, "")
		for _, o := range f.Options {
			if o.Selected {
				form.Add(f.Name, o.Value)
			}
		}
	case KindChoice:
		form.Set(f.VariantName, strconv.Itoa(f.variant))
	}
}

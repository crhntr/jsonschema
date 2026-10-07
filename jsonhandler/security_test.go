package jsonhandler

import (
	"context"
	"encoding/json/jsontext"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestReadOnlyCannotBeChanged(t *testing.T) {
	const schema = `{"type":"object","properties":{"id":{"type":"string","readOnly":true},"name":{"type":"string"},"audit":{"readOnly":true,"type":"object","properties":{"by":{"type":"string"}}}}}`
	for _, tc := range []struct {
		name  string
		value string
		form  url.Values
		want  string
	}{
		{
			name:  "keeps the original value",
			value: `{"id":"42","name":"a","audit":{"by":"ops"}}`,
			form:  url.Values{"@present": {"1"}, "$/id": {"hacked"}, "$/name": {"b"}, "$/audit/by": {"me"}, "@present/audit": {"1"}},
			want:  `{"id":"42","name":"b","audit":{"by":"ops"}}`,
		},
		{
			name: "leaves out a value the original lacks",
			form: url.Values{"$/id": {"hacked"}, "$/name": {"b"}, "$/audit/by": {"me"}},
			want: `{"name":"b"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHandler(t, schema)
			if tc.value != "" {
				h.Value = jsontext.Value(tc.value)
			}
			assertJSONEqual(t, submit(t, h, tc.form), tc.want)
		})
	}

	t.Run("rendered read-only", func(t *testing.T) {
		h := newHandler(t, schema)
		h.Value = jsontext.Value(`{"id":"42"}`)
		input := fragment(t, getForm(t, h)).QuerySelector(`[data-pointer="/id"] input`)
		if input == nil || !input.HasAttribute("readonly") || input.GetAttribute("value") != "42" {
			t.Errorf("id input = %v", input)
		}
	})
}

func TestWriteOnlyIsNotSentBack(t *testing.T) {
	const schema = `{"type":"object","properties":{"user":{"type":"string"},"password":{"type":"string","writeOnly":true}}}`
	h := newHandler(t, schema)
	h.Value = jsontext.Value(`{"user":"ada","password":"hunter2"}`)
	var submitted jsontext.Value
	h.OnSubmit = func(_ context.Context, v jsontext.Value) (string, error) {
		submitted = v
		return "", nil
	}

	body := readBody(t, getForm(t, h))
	if strings.Contains(body, "hunter2") {
		t.Fatalf("form contains the write-only value:\n%s", body)
	}
	if !strings.Contains(body, `type="password"`) {
		t.Errorf("write-only string is not a password input")
	}

	res := postForm(t, h, url.Values{"$/user": {"ada"}, "$/password": {"s3cret"}})
	body = readBody(t, res)
	if strings.Contains(body, "s3cret") {
		t.Errorf("result contains the write-only value:\n%s", body)
	}
	if !strings.Contains(body, `&#34;********&#34;`) {
		t.Errorf("result does not show the redacted value:\n%s", body)
	}
	assertJSONEqual(t, submitted, `{"user":"ada","password":"s3cret"}`)

	t.Run("not echoed after an error", func(t *testing.T) {
		h := newHandler(t, `{"type":"object","required":["n"],"properties":{"n":{"type":"number"},"password":{"type":"string","writeOnly":true}}}`)
		body := readBody(t, postForm(t, h, url.Values{"$/n": {"x"}, "$/password": {"s3cret"}}))
		if strings.Contains(body, "s3cret") {
			t.Errorf("form contains the write-only value:\n%s", body)
		}
	})
}

func TestMaxFields(t *testing.T) {
	for _, tc := range []struct {
		name, schema, value string
		form                url.Values
	}{
		{
			name:   "items requested by the form state",
			schema: `{"type":"array","items":{"type":"string"}}`,
			form:   url.Values{"@len": {"999"}},
		},
		{
			name:   "nested items",
			schema: `{"type":"array","items":{"type":"array","items":{"type":"string"}}}`,
			form:   url.Values{"@len": {"5"}, "@len/0": {"5"}, "@len/1": {"5"}},
		},
		{
			name:   "entries requested by the form state",
			schema: `{"type":"object","additionalProperties":{"type":"string"}}`,
			form:   url.Values{"@len": {"999"}, ActionName: {"add"}},
		},
		{
			name:   "large initial value",
			schema: `{"type":"array","items":{"type":"integer"}}`,
			value:  `[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20]`,
		},
		{
			name:   "required recursion that branches",
			schema: `{"$defs":{"n":{"type":"object","required":["l","r"],"properties":{"l":{"$ref":"#/$defs/n"},"r":{"$ref":"#/$defs/n"}}}},"$ref":"#/$defs/n"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHandler(t, tc.schema)
			h.MaxFields = 10
			var res *http.Response
			switch {
			case tc.form != nil:
				res = postForm(t, h, tc.form)
			default:
				h.Value = jsontext.Value(tc.value)
				res = getForm(t, h)
			}
			if res.StatusCode != http.StatusRequestEntityTooLarge {
				t.Errorf("status = %d, want 413", res.StatusCode)
			}
			doc := fragment(t, res)
			if doc.QuerySelector(".jsonform-field") != nil {
				t.Errorf("form has fields")
			}
			if el := doc.QuerySelector("form > .jsonform-errors li"); el == nil || el.TextContent() != "the form would have more than 10 fields" {
				t.Errorf("no size error")
			}
		})
	}

	t.Run("default limit", func(t *testing.T) {
		h := newHandler(t, `{"type":"array","items":{"type":"string"}}`)
		if res := postForm(t, h, url.Values{"@len": {"999"}, ActionName: {"refresh"}}); res.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", res.StatusCode)
		}
	})
}

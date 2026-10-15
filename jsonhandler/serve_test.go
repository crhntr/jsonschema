package jsonhandler

import (
	"context"
	"encoding/json/jsontext"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func serveSchema(t *testing.T) *Handler {
	t.Helper()
	return newHandler(t, `{"type":"object","required":["n"],"properties":{"n":{"type":"integer","minimum":1},"s":{"type":"string"}}}`)
}

func post(path string, v url.Values) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func TestServe(t *testing.T) {
	schema := serveSchema(t).Schema

	t.Run("a GET writes the form, from the initial value, posting back to the same URL", func(t *testing.T) {
		w := httptest.NewRecorder()
		v, err := Serve(w, httptest.NewRequest(http.MethodGet, "/things/7?x=1", nil), schema, jsontext.Value(`{"n":5,"s":"hi"}`), nil)
		if err != nil || v != nil {
			t.Fatalf("value %s, err %v", v, err)
		}
		body := w.Body.String()
		for _, want := range []string{`action="/things/7?x=1"`, `value="5"`, `value="hi"`} {
			if !strings.Contains(body, want) {
				t.Errorf("the form lacks %s:\n%s", want, body)
			}
		}
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
			t.Errorf("status %d, content type %q", w.Code, w.Header().Get("Content-Type"))
		}
	})

	t.Run("an invalid POST writes the form again with 422 and returns nothing", func(t *testing.T) {
		w := httptest.NewRecorder()
		v, err := Serve(w, post("/f", url.Values{"$/n": {"0"}}), schema, nil, &Options{SubmitLabel: "Go"})
		if err != nil || v != nil || w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("value %s, err %v, status %d", v, err, w.Code)
		}
		if !strings.Contains(w.Body.String(), "less than minimum 1") || !strings.Contains(w.Body.String(), ">Go<") {
			t.Errorf("body:\n%s", w.Body)
		}
	})

	t.Run("a valid POST returns the JSON and writes nothing", func(t *testing.T) {
		w := httptest.NewRecorder()
		v, err := Serve(w, post("/f", url.Values{"$/n": {"3"}}), schema, nil, nil)
		if err != nil || string(v) != `{"n":3}` {
			t.Fatalf("value %s, err %v", v, err)
		}
		if w.Body.Len() != 0 || w.Code != http.StatusOK {
			t.Errorf("something was written: %d %q", w.Code, w.Body)
		}
	})
}

func TestHandle(t *testing.T) {
	schema := serveSchema(t).Schema
	f, err := Handle(post("/f", url.Values{"$/n": {"3"}}), schema, nil, nil)
	if err != nil || !f.Valid || string(f.Value) != `{"n":3}` {
		t.Fatalf("valid %v, value %s, err %v", f != nil && f.Valid, f.Value, err)
	}
	f, err = Handle(post("/f", url.Values{"$/n": {"x"}}), schema, nil, nil)
	if err != nil || f.Valid || f.StatusCode() != http.StatusUnprocessableEntity {
		t.Fatalf("an invalid post: %v", err)
	}
	html, err := f.HTML()
	if err != nil || !strings.Contains(string(html), `action="/f"`) {
		t.Errorf("html %s, err %v", html, err)
	}
	if _, err := Handle(httptest.NewRequest(http.MethodGet, "/f", nil), nil, nil, nil); err == nil {
		t.Errorf("a nil schema was accepted")
	}
}

func TestParseSchema(t *testing.T) {
	s, err := ParseSchema(context.Background(), []byte(definitions))
	if err != nil {
		t.Fatal(err)
	}
	if f, err := Handle(post("/f", url.Values{}), s, nil, nil); err != nil || f == nil {
		t.Fatal(err)
	}
	if _, err := ParseSchema(context.Background(), []byte(`{`)); err == nil {
		t.Errorf("invalid JSON accepted")
	}
	if _, err := ParseSchema(context.Background(), []byte(`{"$ref":"https://example.com/x.json"}`)); err == nil {
		t.Errorf("a remote reference was fetched")
	}
}

func TestValidateHTML(t *testing.T) {
	schema := serveSchema(t).Schema
	html, err := ValidateHTML(post("/f/validate", url.Values{"$/n": {"0"}}), schema, nil, nil)
	if err != nil || !strings.Contains(string(html), "less than minimum 1") || !strings.Contains(string(html), `hx-swap-oob="true"`) {
		t.Errorf("html %s, err %v", html, err)
	}
	html, err = ValidateHTML(post("/f/validate", url.Values{"$/n": {"3"}}), schema, nil, nil)
	if err != nil || strings.Contains(string(html), "<li>") {
		t.Errorf("a valid value shows errors: %s", html)
	}
}

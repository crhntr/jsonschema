package jsonschema_test

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crhntr/jsonschema"
)

// findResolved walks schema.allOf for a $ref containing pathFragment and
// returns the resolved target (or nil).
func findResolved(t *testing.T, schema *jsonschema.Schema, pathFragment string) *jsonschema.Schema {
	t.Helper()
	obj := mustObject(t, schema)
	for _, sub := range obj.AllOf {
		ref := mustObject(t, sub).Ref
		if strings.Contains(ref, pathFragment) {
			return sub.Resolved()
		}
	}
	return nil
}

// findFirstDynamicRef walks the subtree under m looking for the first Schema
// whose SchemaObject has a non-empty $dynamicRef. Used to spot-check
// bookending of $dynamicRef.
func findFirstDynamicRef(m *jsonschema.Schema) *jsonschema.Schema {
	if m == nil {
		return nil
	}
	obj, ok := m.TypeObject()
	if !ok {
		return nil
	}
	if obj.DynamicRef != "" {
		return m
	}
	for _, c := range obj.Defs {
		if r := findFirstDynamicRef(c); r != nil {
			return r
		}
	}
	for _, c := range obj.Properties {
		if r := findFirstDynamicRef(c); r != nil {
			return r
		}
	}
	for _, c := range obj.AllOf {
		if r := findFirstDynamicRef(c); r != nil {
			return r
		}
	}
	for _, c := range obj.AnyOf {
		if r := findFirstDynamicRef(c); r != nil {
			return r
		}
	}
	for _, c := range obj.OneOf {
		if r := findFirstDynamicRef(c); r != nil {
			return r
		}
	}
	for _, c := range []*jsonschema.Schema{obj.If, obj.Then, obj.Else, obj.Not, obj.Items, obj.AdditionalProperties, obj.PropertyNames} {
		if r := findFirstDynamicRef(c); r != nil {
			return r
		}
	}
	return nil
}

func mustObject(t *testing.T, m *jsonschema.Schema) jsonschema.SchemaObject {
	t.Helper()
	obj, ok := m.TypeObject()
	if !ok {
		t.Fatalf("expected object schema, got bool")
	}
	return obj
}

// originalHostHeader carries the canonical schema host across the test
// client's URL rewrite so the httptest handler can pick a response folder.
const originalHostHeader = "X-Original-Host"

// startSchemaServer serves files rooted at testdata/schema, mapping
// <X-Original-Host><path>.json to a file on disk. The returned client's
// transport rewrites every outgoing URL to the httptest.Server and puts the
// real schema host in X-Original-Host.
func startSchemaServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Header.Get(originalHostHeader)
		if host == "" {
			http.Error(w, "missing "+originalHostHeader, http.StatusBadRequest)
			return
		}
		clean := filepath.Clean(r.URL.Path)
		if strings.Contains(clean, "..") {
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		p := filepath.Join("testdata", "schema", host, filepath.FromSlash(clean))
		if filepath.Ext(p) == "" {
			p += ".json"
		}
		if _, err := os.Stat(p); err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/schema+json")
		http.ServeFile(w, r, p)
	}))
	t.Cleanup(srv.Close)

	client := srv.Client()
	client.Transport = rewriteTransport{serverURL: srv.URL, base: srv.Client().Transport}
	return srv, client
}

// rewriteTransport sends every request to serverURL while stashing the
// original request host in X-Original-Host so the handler can pick a
// response folder.
type rewriteTransport struct {
	serverURL string // httptest.Server.URL
	base      http.RoundTripper
}

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r2, err := http.NewRequestWithContext(req.Context(), req.Method, t.serverURL+req.URL.RequestURI(), req.Body)
	if err != nil {
		return nil, err
	}
	maps.Copy(r2.Header, req.Header)
	r2.Header.Set(originalHostHeader, req.URL.Host)
	return t.base.RoundTrip(r2)
}

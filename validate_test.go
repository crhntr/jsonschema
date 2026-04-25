package jsonschema_test

import (
	"bytes"
	"flag"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"

	"github.com/crhntr/jsonschema"
)

var suiteVerbose = flag.Bool("suite.verbose", false, "log every suite case (default off — only the summary)")

// TestValidationSuite runs every draft-2020-12 conformance case as a
// subtest. Use `go test -run TestValidationSuite/<file>/<group>/<case>`
// to focus on one. The summary line at the end reports pass/fail
// counts so progress is easy to track.
func TestValidationSuite(t *testing.T) {
	root := "testdata/JSON-Schema-Test-Suite/tests-draft2020-12"

	var passed, failed atomic.Int64

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		t.Run(filepath.ToSlash(rel), func(t *testing.T) {
			for _, g := range readJSONFile[[]suiteGroup](t, path) {
				t.Run(suiteName(g.Description), func(t *testing.T) {
					schema := loadSuiteSchema(t, g.Schema)
					for _, c := range g.Tests {
						t.Run(suiteName(c.Description), func(t *testing.T) {
							runSuiteCase(t, schema, g, c, &passed, &failed)
						})
					}
				})
			}
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("suite summary: passed=%d failed=%d total=%d",
		passed.Load(), failed.Load(), passed.Load()+failed.Load())
}

func readJSONFile[T any](t *testing.T, path string) T {
	t.Helper()
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var data T
	if err := json.Unmarshal(buf, &data); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return data
}

// loadSuiteSchema parses the schema and runs it through a Resolver so
// internal $refs are linked. The synthetic URI lets Resolver index even
// schemas that don't declare $id. External refs to localhost:1234/...
// are served from the conformance suite's remotes/ directory.
func loadSuiteSchema(t *testing.T, body []byte) *jsonschema.Schema {
	t.Helper()
	schema, err := jsonschema.Parse(body)
	if err != nil {
		t.Fatalf("parse schema: %v\nschema: %s", err, body)
	}
	r := &jsonschema.Resolver{Client: suiteRemotesClient(t)}
	if _, err := r.Load("https://suite.test/", body); err != nil {
		if *suiteVerbose {
			t.Logf("load failed: %v", err)
		}
		return schema
	}
	linked, err := r.Resolve(t.Context(), "https://suite.test/")
	if err != nil {
		if *suiteVerbose {
			t.Logf("resolve failed: %v", err)
		}
		return schema
	}
	return linked
}

// suiteRemotesClient returns an http.Client that resolves
// http://localhost:1234/... requests against the conformance suite's
// remotes/ directory served from disk.
func suiteRemotesClient(t *testing.T) *http.Client {
	t.Helper()
	srv, ok := remotesServer(t)
	if !ok {
		return http.DefaultClient
	}
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	// Don't mutate srv.Client() (it's cached on the server and shared
	// across tests — wrapping its transport repeatedly would be a real
	// bug). Build a fresh client per test.
	return &http.Client{
		Transport: &remotesTransport{target: target.Host, base: srv.Client().Transport},
	}
}

// remotesServer is created lazily once per test process and serves the
// conformance suite's remotes/ tree under any host (the handler ignores
// the Host header and looks up files by path).
var remotesSrvOnce struct {
	once sync.Once
	srv  *httptest.Server
	ok   bool
}

func remotesServer(t *testing.T) (*httptest.Server, bool) {
	t.Helper()
	remotesSrvOnce.once.Do(func() {
		root := "testdata/JSON-Schema-Test-Suite/remotes"
		if _, err := os.Stat(root); err != nil {
			return
		}
		remotesSrvOnce.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clean := filepath.Clean(r.URL.Path)
			if strings.Contains(clean, "..") {
				http.Error(w, "bad path", http.StatusBadRequest)
				return
			}
			// Look up by original host first (json-schema.org/...)
			// from testdata/schema/<host>/<path>.json, then fall
			// back to the conformance remotes/ tree.
			host := r.Header.Get(originalHostHeader)
			var candidates []string
			if host != "" {
				schemaPath := filepath.Join("testdata", "schema", host, filepath.FromSlash(clean))
				if filepath.Ext(schemaPath) == "" {
					schemaPath += ".json"
				}
				candidates = append(candidates, schemaPath)
			}
			candidates = append(candidates, filepath.Join(root, filepath.FromSlash(clean)))
			for _, p := range candidates {
				if _, err := os.Stat(p); err == nil {
					w.Header().Set("Content-Type", "application/schema+json")
					http.ServeFile(w, r, p)
					return
				}
			}
			http.NotFound(w, r)
		}))
		remotesSrvOnce.ok = true
	})
	return remotesSrvOnce.srv, remotesSrvOnce.ok
}

type remotesTransport struct {
	target string
	base   http.RoundTripper
}

func (rt *remotesTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r2 := req.Clone(req.Context())
	r2.Header.Set(originalHostHeader, req.URL.Host)
	r2.URL = &url.URL{
		Scheme:   "http",
		Host:     rt.target,
		Path:     req.URL.Path,
		RawQuery: req.URL.RawQuery,
	}
	r2.Host = rt.target
	return rt.base.RoundTrip(r2)
}

func runSuiteCase(t *testing.T, schema *jsonschema.Schema, g suiteGroup, c suiteCase, passed, failed *atomic.Int64) {
	t.Helper()
	var err error
	if shouldAssertFormat(t.Name(), g.Schema) {
		err = schema.EvaluateWithFormatAssertion(t.Name(), c.Data)
	} else {
		err = schema.Evaluate(t.Name(), c.Data)
	}
	got := err == nil
	if got == c.Valid {
		passed.Add(1)
		if *suiteVerbose {
			t.Logf("ok: want valid=%v got valid=%v", c.Valid, got)
		}
		return
	}
	failed.Add(1)
	t.Errorf("validation mismatch: want valid=%v got valid=%v\nschema: %s\ndata: %s\nerr: %v",
		c.Valid, got, g.Schema, c.Data, err)
}

// shouldAssertFormat reports whether the given schema/test should be
// validated with format assertion enabled. We opt in for tests under
// optional/format/ and for any schema whose $schema points at a
// format-assertion metaschema.
func shouldAssertFormat(name string, schema []byte) bool {
	if strings.Contains(name, "/optional/format/") {
		return true
	}
	if bytes.Contains(schema, []byte("format-assertion")) {
		return true
	}
	return false
}

// suiteName makes a description safe for `go test -run` by replacing
// runs of non-alphanum with "_".
func suiteName(s string) string {
	var b strings.Builder
	prevUnderscore := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevUnderscore = false
		default:
			if !prevUnderscore {
				b.WriteByte('_')
				prevUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

type suiteGroup struct {
	Description string         `json:"description"`
	Schema      jsontext.Value `json:"schema"`
	Tests       []suiteCase    `json:"tests"`
}

type suiteCase struct {
	Description string         `json:"description"`
	Data        jsontext.Value `json:"data"`
	Valid       bool           `json:"valid"`
}

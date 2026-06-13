package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rsc.io/script"
	"rsc.io/script/scripttest"
)

func Test(t *testing.T) {
	// Serve the entire testdata/schema tree. The rewriteTransport
	// below encodes the original Host as the first path segment so
	// e.g. https://json-schema.org/draft/2020-12/schema resolves to
	// testdata/schema/json-schema.org/draft/2020-12/schema and
	// https://example.com/refs/embedded.json to
	// testdata/schema/example.com/refs/embedded.json. When the
	// requested path has no extension and the literal file is not
	// present, the handler falls back to <path>.json so URLs
	// published without a suffix (the JSON Schema convention)
	// resolve to the on-disk fixture.
	rootFS := os.DirFS(filepath.FromSlash("../../testdata/schema"))
	fileHandler := http.FileServerFS(rootFS)
	server := httptest.NewServer(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		res.Header().Set("Content-Type", "application/schema+json")
		clean := strings.TrimPrefix(req.URL.Path, "/")
		if filepath.Ext(clean) == "" {
			if _, err := fs.Stat(rootFS, clean); err != nil {
				if _, err := fs.Stat(rootFS, clean+".json"); err == nil {
					req2 := *req
					u := *req.URL
					u.Path = req.URL.Path + ".json"
					req2.URL = &u
					fileHandler.ServeHTTP(res, &req2)
					return
				}
			}
		}
		fileHandler.ServeHTTP(res, req)
	}))
	t.Cleanup(server.Close)

	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	// Reroute https://example.com/... requests to the test server so
	// --schema-url tests can hit fixtures from disk without real DNS.
	client := &http.Client{
		Transport: &rewriteTransport{target: target.Host, base: server.Client().Transport},
	}

	e := script.NewEngine()
	e.Cmds = script.DefaultCmds()
	e.Conds = script.DefaultConds()

	// GOEXPERIMENT=jsonv2 makes encoding/json/v2 + encoding/json/jsontext
	// resolvable to "exec go test" / "exec go build" inside fixtures.
	scriptEnv := []string{
		"GOEXPERIMENT=jsonv2",
		"HOME=" + os.Getenv("HOME"),
		"PATH=" + os.Getenv("PATH"),
	}
	for _, k := range []string{"GOCACHE", "GOMODCACHE", "GOPATH", "GOROOT"} {
		if v := os.Getenv(k); v != "" {
			scriptEnv = append(scriptEnv, k+"="+v)
		}
	}

	e.Cmds["jsch"] = script.Command(script.CmdUsage{
		Summary: "jsch",
		Args:    "",
	}, func(state *script.State, args ...string) (script.WaitFunc, error) {
		return func(state *script.State) (string, string, error) {
			var stdout, stderr bytes.Buffer
			code := run(state.Context(), state.Getwd(), args, &stdout, &stderr, strings.NewReader(""), client)
			if code != 0 {
				return stdout.String(), stderr.String(), ExitCode(code)
			}
			return stdout.String(), stderr.String(), nil
		}, nil
	})

	t.Run("validate", func(t *testing.T) {
		scripttest.Test(t, t.Context(), e, scriptEnv, "testdata/validate/*.txt")
	})
	t.Run("generate", func(t *testing.T) {
		scripttest.Test(t, t.Context(), e, scriptEnv, "testdata/generate/*.txt")
	})
}

type rewriteTransport struct {
	target string
	base   http.RoundTripper
}

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Encode the original host as the first path segment so the
	// shared file server can route between schema vendors
	// (example.com, json-schema.org, etc.) without a Host-aware
	// router.
	r2 := req.Clone(req.Context())
	originalHost := req.URL.Host
	r2.URL = &url.URL{
		Scheme:   "http",
		Host:     rt.target,
		Path:     "/" + originalHost + req.URL.Path,
		RawQuery: req.URL.RawQuery,
	}
	r2.Host = rt.target
	return rt.base.RoundTrip(r2)
}

type ExitCode int

func (ec ExitCode) Error() string {
	return fmt.Sprintf("exit %d", ec)
}

// TestValidateStdin exercises the "-" instance argument that reads
// from stdin. The script test harness has no stdin command so we
// drive run() directly here.
func TestValidateStdin(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "schema.json"), []byte(`{"type":"string"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), dir, []string{"validate", "--schema", "schema.json", "-"},
		&stdout, &stderr, strings.NewReader(`"hello"`), http.DefaultClient)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "<stdin>: ok") {
		t.Errorf("stdout = %q, want '<stdin>: ok'", stdout.String())
	}
}

func TestValidateStdinInvalid(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "schema.json"), []byte(`{"type":"string"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), dir, []string{"validate", "--schema", "schema.json", "-"},
		&stdout, &stderr, strings.NewReader(`42`), http.DefaultClient)
	if code == 0 {
		t.Fatalf("exit=%d, want non-zero (instance is invalid)", code)
	}
	if !strings.Contains(stderr.String(), "type string does not match") {
		t.Errorf("stderr = %q, want type-mismatch message", stderr.String())
	}
}

func TestValidateOutputFlag(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "schema.json"), []byte(`{"type":"string"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		format  string
		body    string
		wantOK  bool
		wantSub string // substring required in stdout
	}{
		{"flag valid", "flag", `"x"`, true, `"valid":true`},
		{"flag invalid", "flag", `42`, false, `"valid":false`},
		{"basic invalid carries error", "basic", `42`, false, `"errors"`},
		{"detailed invalid", "detailed", `42`, false, `"keywordLocation":"/type"`},
		{"verbose invalid", "verbose", `42`, false, `"keywordLocation":"/type"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := []string{"validate", "--schema", "schema.json", "--output", tc.format, "-"}
			code := run(t.Context(), dir, args, &stdout, &stderr, strings.NewReader(tc.body), http.DefaultClient)
			if tc.wantOK && code != 0 {
				t.Fatalf("exit=%d, want 0; stderr=%s", code, stderr.String())
			}
			if !tc.wantOK && code == 0 {
				t.Fatalf("exit=0, want non-zero; stdout=%s", stdout.String())
			}
			if !strings.Contains(stdout.String(), tc.wantSub) {
				t.Errorf("stdout missing %q; got %s", tc.wantSub, stdout.String())
			}
		})
	}
}

func TestValidateOutputUnknownFormatRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "schema.json"), []byte(`{"type":"string"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), dir, []string{"validate", "--schema", "schema.json", "--output", "bogus", "-"},
		&stdout, &stderr, strings.NewReader(`"x"`), http.DefaultClient)
	if code == 0 {
		t.Fatalf("exit=0, want non-zero for unknown --output value")
	}
	if !strings.Contains(stderr.String(), "bogus") {
		t.Errorf("stderr should mention the bad value; got %s", stderr.String())
	}
}

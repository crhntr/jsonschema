package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"rsc.io/script"
	"rsc.io/script/scripttest"
)

func Test(t *testing.T) {
	fileHandler := http.FileServerFS(os.DirFS(filepath.FromSlash("../../testdata/schema/example.com")))
	server := httptest.NewServer(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		res.Header().Set("Content-Type", "application/schema+json")
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

	e.Cmds["go-jsonschema"] = script.Command(script.CmdUsage{
		Summary: "go-jsonschema",
		Args:    "",
	}, func(state *script.State, args ...string) (script.WaitFunc, error) {
		return func(state *script.State) (string, string, error) {
			var stdout, stderr bytes.Buffer
			code := run(state.Context(), state.Getwd(), args, &stdout, &stderr, client)
			if code != 0 {
				return stdout.String(), stderr.String(), ExitCode(code)
			}
			return stdout.String(), stderr.String(), nil
		}, nil
	})

	t.Run("validate", func(t *testing.T) {
		scripttest.Test(t, t.Context(), e, nil, "testdata/validate/*.txt")
	})
	t.Run("generate", func(t *testing.T) {
		scripttest.Test(t, t.Context(), e, nil, "testdata/generate/*.txt")
	})
}

type rewriteTransport struct {
	target string
	base   http.RoundTripper
}

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r2 := req.Clone(req.Context())
	r2.URL = &url.URL{
		Scheme:   "http",
		Host:     rt.target,
		Path:     req.URL.Path,
		RawQuery: req.URL.RawQuery,
	}
	r2.Host = rt.target
	return rt.base.RoundTrip(r2)
}

type ExitCode int

func (ec ExitCode) Error() string {
	return fmt.Sprintf("exit %d", ec)
}

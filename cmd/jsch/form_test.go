package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func formRequest(method, target string, form url.Values) *http.Request {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, target, body)
	req.Host = "localhost:8080"
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return req
}

func do(t *testing.T, h http.Handler, req *http.Request) (*http.Response, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := rec.Result()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, string(b)
}

func TestFormServer(t *testing.T) {
	t.Run("edits a schema and writes it back", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "schema.json")
		if err := os.WriteFile(file, []byte(`{"title":"Before","type":"string"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		h, err := newFormServer(context.Background(), dir, "", "schema.json", true, io.Discard, nil)
		if err != nil {
			t.Fatal(err)
		}

		res, body := do(t, h, formRequest(http.MethodGet, "/", nil))
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET / status = %d", res.StatusCode)
		}
		for _, want := range []string{
			`<html lang="en">`,
			`open-props@1.7.23/open-props.min.css`,
			`integrity="sha384-`,
			`href="/jsonform.css"`,
			`class="jsonform"`,
			`value="Before"`,
			`action="/form"`,
			`hx-post="/form/validate"`,
			`class="jsonform-outline"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("page does not contain %s", want)
			}
		}

		res, css := do(t, h, formRequest(http.MethodGet, "/jsonform.css", nil))
		if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") || !strings.Contains(css, ".jsonform") {
			t.Errorf("stylesheet: %s, %d bytes", ct, len(css))
		}

		res, body = do(t, h, formRequest(http.MethodPost, "/form", url.Values{
			"@variant": {"0"}, "@present": {"1"}, "$/title": {"After"}, "@variant/type": {"0"}, "$/type": {"string"},
		}))
		if res.StatusCode != http.StatusOK || !strings.Contains(body, "Saved to schema.json.") {
			t.Fatalf("POST /form status = %d:\n%s", res.StatusCode, body)
		}
		saved, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if want := "{\n  \"type\": \"string\",\n  \"title\": \"After\"\n}\n"; string(saved) != want {
			t.Errorf("saved %q, want %q", saved, want)
		}

		_, body = do(t, h, formRequest(http.MethodGet, "/", nil))
		if !strings.Contains(body, `value="After"`) {
			t.Errorf("the page does not start from the saved value")
		}
	})

	t.Run("prints valid values without --write", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "s.json"), []byte(`{"type":"object","properties":{"n":{"type":"integer"}}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		var stdout bytes.Buffer
		h, err := newFormServer(context.Background(), dir, "s.json", "", false, &stdout, nil)
		if err != nil {
			t.Fatal(err)
		}
		res, body := do(t, h, formRequest(http.MethodPost, "/form", url.Values{"$/n": {"4"}}))
		if res.StatusCode != http.StatusOK || !strings.Contains(body, "Printed to standard output") {
			t.Fatalf("status = %d:\n%s", res.StatusCode, body)
		}
		if stdout.String() != "{\n  \"n\": 4\n}\n" {
			t.Errorf("stdout = %q", stdout.String())
		}
	})

	t.Run("creates the file with --write", func(t *testing.T) {
		dir := t.TempDir()
		h, err := newFormServer(context.Background(), dir, "", filepath.Join(dir, "new.json"), true, io.Discard, nil)
		if err != nil {
			t.Fatal(err)
		}
		if res, _ := do(t, h, formRequest(http.MethodPost, "/form", url.Values{"@variant": {"1"}, "$": {"false", "true"}})); res.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", res.StatusCode)
		}
		if b, err := os.ReadFile(filepath.Join(dir, "new.json")); err != nil || string(b) != "true\n" {
			t.Errorf("new.json = %q, %v", b, err)
		}
	})

	t.Run("refuses other host names", func(t *testing.T) {
		h, err := newFormServer(context.Background(), t.TempDir(), "", "", false, io.Discard, nil)
		if err != nil {
			t.Fatal(err)
		}
		for host, want := range map[string]int{
			"localhost":         http.StatusOK,
			"localhost:8080":    http.StatusOK,
			"127.0.0.1:8080":    http.StatusOK,
			"[::1]:8080":        http.StatusOK,
			"192.168.1.5":       http.StatusOK,
			"evil.example:8080": http.StatusMisdirectedRequest,
			"localhost.evil":    http.StatusMisdirectedRequest,
		} {
			req := formRequest(http.MethodGet, "/", nil)
			req.Host = host
			if res, _ := do(t, h, req); res.StatusCode != want {
				t.Errorf("Host %s: status = %d, want %d", host, res.StatusCode, want)
			}
		}
	})

	t.Run("refuses cross-origin posts", func(t *testing.T) {
		h, err := newFormServer(context.Background(), t.TempDir(), "", "", false, io.Discard, nil)
		if err != nil {
			t.Fatal(err)
		}
		req := formRequest(http.MethodPost, "/form", url.Values{"$": {"true"}})
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("Origin", "https://evil.example")
		if res, _ := do(t, h, req); res.StatusCode != http.StatusForbidden {
			t.Errorf("status = %d, want 403", res.StatusCode)
		}
	})

	t.Run("errors", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte(`{`), 0o644); err != nil {
			t.Fatal(err)
		}
		for name, args := range map[string]struct {
			schema, instance string
			write            bool
		}{
			"missing instance":       {instance: "missing.json"},
			"invalid instance":       {instance: "bad.json"},
			"missing schema":         {schema: "missing.json"},
			"invalid schema":         {schema: "bad.json"},
			"directory as instance":  {instance: "."},
			"directory with --write": {instance: ".", write: true},
		} {
			if _, err := newFormServer(context.Background(), dir, args.schema, args.instance, args.write, io.Discard, nil); err == nil {
				t.Errorf("%s: no error", name)
			}
		}
	})

	t.Run("flags", func(t *testing.T) {
		for _, args := range [][]string{
			{"--write"},
			{"a.json", "b.json"},
			{"--nope"},
			{"--addr", "256.0.0.1:99999", "--schema", "x.json"},
		} {
			var stderr bytes.Buffer
			if err := formCommand(context.Background(), t.TempDir(), args, io.Discard, &stderr, nil); err == nil {
				t.Errorf("form %q: no error", args)
			}
		}
	})

	t.Run("serves until the context ends", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		var stderr syncBuffer
		done := make(chan error, 1)
		go func() {
			done <- formCommand(ctx, t.TempDir(), []string{"--addr", "127.0.0.1:0"}, io.Discard, &stderr, nil)
		}()
		for !strings.Contains(stderr.String(), "serving form at http://127.0.0.1:") {
			select {
			case err := <-done:
				t.Fatalf("form returned early: %v", err)
			case <-time.After(10 * time.Millisecond):
			}
		}
		cancel()
		if err := <-done; err != nil {
			t.Errorf("form = %v, want nil after cancel", err)
		}
	})
}

// syncBuffer is a bytes.Buffer safe to write from the server goroutine
// while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

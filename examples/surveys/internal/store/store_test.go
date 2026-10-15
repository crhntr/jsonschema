package store

import (
	"encoding/json/jsontext"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func TestValid(t *testing.T) {
	for name, want := range map[string]bool{
		"docs-feedback": true, "q3": true, "a": true,
		"": false, "-a": false, "A": false, "a b": false, "a/b": false, "..": false, "../x": false,
		"a.json": false, strings.Repeat("a", 61): false, strings.Repeat("a", 60): true,
	} {
		if got := Valid(name); got != want {
			t.Errorf("Valid(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestStore(t *testing.T) {
	s, dir := open(t)
	if got, err := s.List(); err != nil || len(got) != 0 {
		t.Fatalf("an empty store lists %v, %v", got, err)
	}
	if err := s.Create("docs", jsontext.Value(`{"title":"Docs","questions":[]}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.Create("docs", jsontext.Value(`{}`)); !errors.Is(err, ErrExists) {
		t.Errorf("creating a taken name = %v, want ErrExists", err)
	}
	if err := s.Create("a/b", jsontext.Value(`{}`)); err == nil {
		t.Errorf("creating a survey named a/b succeeded")
	}
	text, err := os.ReadFile(filepath.Join(dir, "surveys", "docs.json"))
	if err != nil || !strings.HasPrefix(string(text), "{\n  \"title\": \"Docs\"") || !strings.HasSuffix(string(text), "\n") {
		t.Errorf("the file is not indented JSON: %q (%v)", text, err)
	}

	for _, r := range []string{`{"a": 1}`, "{\n\"a\": 2\n}"} {
		if err := s.AddResponse("docs", jsontext.Value(r)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Responses("docs")
	if err != nil || len(got) != 2 || string(got[0]) != `{"a":1}` || string(got[1]) != `{"a":2}` {
		t.Errorf("responses = %s, %v", got, err)
	}
	if err := s.Update("docs", jsontext.Value(`{"title":"Docs v2","questions":[]}`)); err != nil {
		t.Fatal(err)
	}
	list, err := s.List()
	if err != nil || len(list) != 1 || list[0] != (Entry{Name: "docs", Title: "Docs v2", Responses: 2}) {
		t.Errorf("list = %+v, %v", list, err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "surveys"))
	if len(entries) != 1 {
		t.Errorf("the surveys directory holds %d files after an update, want 1", len(entries))
	}

	if err := s.Delete("docs"); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"get":       func() error { _, err := s.Get("docs"); return err }(),
		"update":    s.Update("docs", jsontext.Value(`{}`)),
		"delete":    s.Delete("docs"),
		"add":       s.AddResponse("docs", jsontext.Value(`{}`)),
		"responses": func() error { _, err := s.Responses("docs"); return err }(),
	} {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s after delete = %v, want ErrNotFound", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "responses", "docs.jsonl")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the responses outlived the survey: %v", err)
	}
}

func TestStore_names(t *testing.T) {
	s, dir := open(t)
	secret := filepath.Join(dir, "secret.json")
	if err := os.WriteFile(secret, []byte(`{"title":"secret"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../secret", "..%2fsecret", "surveys/../../secret", ""} {
		if _, err := s.Get(name); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%q) = %v, want ErrNotFound", name, err)
		}
		if err := s.AddResponse(name, jsontext.Value(`{}`)); !errors.Is(err, ErrNotFound) {
			t.Errorf("AddResponse(%q) = %v, want ErrNotFound", name, err)
		}
	}
}

func TestStore_responses(t *testing.T) {
	s, _ := open(t)
	if err := s.Create("a", jsontext.Value(`{"title":"A"}`)); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Responses("a"); err != nil || got != nil {
		t.Errorf("no responses yet: %v, %v", got, err)
	}
	if err := s.AddResponse("a", jsontext.Value(`{"x":`)); err == nil {
		t.Errorf("a response that is not JSON was stored")
	}
	if err := s.AddResponse("a", jsontext.Value(`{"x":"`+strings.Repeat("y", maxResponse)+`"}`)); err == nil {
		t.Errorf("a huge response was stored")
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if err := s.AddResponse("a", jsontext.Value(`{"x":1}`)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	got, err := s.Responses("a")
	if err != nil || len(got) != 20 || slices.ContainsFunc(got, func(v jsontext.Value) bool { return string(v) != `{"x":1}` }) {
		t.Errorf("after 20 concurrent responses: %d stored, %v", len(got), err)
	}
}

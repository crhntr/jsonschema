package survey

import (
	"context"
	"encoding/json/jsontext"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/crhntr/jsonschema"
)

const sample = `{
	"title": "Docs feedback",
	"description": "Two minutes, we promise.",
	"questions": [
		{"kind": "scale", "prompt": "How useful are the docs?", "required": true, "low": "Useless", "high": "Essential"},
		{"kind": "choice", "prompt": "Which parts do you use?", "options": ["Tutorial", "Reference", "Examples"], "multiple": true, "required": true},
		{"kind": "choice", "prompt": "Where do you work?", "options": ["Office", "Home"]},
		{"kind": "yesno", "prompt": "Would you recommend them?"},
		{"kind": "text", "prompt": "What is missing?", "long": true}
	]
}`

func mustParse(t *testing.T) Definition {
	t.Helper()
	d, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func answersSchema(t *testing.T, d Definition) *jsonschema.Schema {
	t.Helper()
	doc, err := d.Schema()
	if err != nil {
		t.Fatal(err)
	}
	r := jsonschema.NewResolver(nil)
	const uri = "urn:test:answers"
	if _, err := r.Load(uri, doc); err != nil {
		t.Fatal(err)
	}
	s, err := r.Resolve(context.Background(), uri)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestIDs(t *testing.T) {
	d := Definition{Questions: []Question{
		{Prompt: "How useful are the docs?"},
		{Prompt: "How useful are the docs!"},
		{Prompt: "  "},
		{Prompt: "Is this a very long question that goes on and on, well past what a name needs?"},
		{Prompt: "Café ☕"},
	}}
	want := []string{"how-useful-are-the-docs", "how-useful-are-the-docs-2", "q3", "is-this-a-very-long-question-that-goes", "café"}
	if got := d.IDs(); !slices.Equal(got, want) {
		t.Errorf("IDs() = %q, want %q", got, want)
	}
}

func TestSlug(t *testing.T) {
	for title, want := range map[string]string{
		"Docs feedback":          "docs-feedback",
		"  Q3: Roadmap & Plans!": "q3-roadmap-plans",
		"日本語":                    "",
		"a/../b":                 "a-b",
		"":                       "",
	} {
		if got := Slug(title); got != want {
			t.Errorf("Slug(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestSchema(t *testing.T) {
	d := mustParse(t)
	s := answersSchema(t, d)
	ids := d.IDs()
	good := `{"` + ids[0] + `":4,"` + ids[1] + `":["Tutorial","Examples"],"` + ids[2] + `":"Home","` + ids[3] + `":"Yes","` + ids[4] + `":"More recipes"}`
	if err := s.Validate("a", []byte(good)).AsError(); err != nil {
		t.Errorf("a full answer is rejected: %v", err)
	}
	if err := s.Validate("a", []byte(`{"`+ids[0]+`":1,"`+ids[1]+`":["Reference"]}`)).AsError(); err != nil {
		t.Errorf("optional questions left out are rejected: %v", err)
	}
	for name, bad := range map[string]string{
		"a required answer missing": `{"` + ids[1] + `":["Tutorial"]}`,
		"an empty required list":    `{"` + ids[0] + `":3,"` + ids[1] + `":[]}`,
		"a scale out of range":      `{"` + ids[0] + `":6,"` + ids[1] + `":["Tutorial"]}`,
		"an option not offered":     `{"` + ids[0] + `":3,"` + ids[1] + `":["Podcast"]}`,
		"a repeated pick":           `{"` + ids[0] + `":3,"` + ids[1] + `":["Tutorial","Tutorial"]}`,
		"yes or no as a boolean":    `{"` + ids[0] + `":3,"` + ids[1] + `":["Tutorial"],"` + ids[3] + `":true}`,
		"an answer to no question":  `{"` + ids[0] + `":3,"` + ids[1] + `":["Tutorial"],"extra":"x"}`,
		"text too long":             `{"` + ids[0] + `":3,"` + ids[1] + `":["Tutorial"],"` + ids[4] + `":"` + strings.Repeat("x", 4001) + `"}`,
	} {
		if err := s.Validate("a", []byte(bad)).AsError(); err == nil {
			t.Errorf("%s: accepted %s", name, bad)
		}
	}
}

func TestSchema_errors(t *testing.T) {
	for name, q := range map[string]Question{
		"unknown kind":         {Kind: "ranking", Prompt: "x"},
		"one option":           {Kind: Choice, Prompt: "x", Options: []string{"a"}},
		"an upside-down scale": {Kind: Scale, Prompt: "x", Min: ptr(5), Max: ptr(2)},
	} {
		if _, err := (Definition{Title: "t", Questions: []Question{q}}).Schema(); err == nil {
			t.Errorf("%s: Schema succeeded", name)
		}
	}
}

func ptr(n int) *int { return &n }

func TestSummarize(t *testing.T) {
	d := mustParse(t)
	ids := d.IDs()
	responses := []jsontext.Value{
		jsontext.Value(`{"` + ids[0] + `":5,"` + ids[1] + `":["Tutorial","Reference"],"` + ids[2] + `":"Home","` + ids[3] + `":"Yes","` + ids[4] + `":"More recipes"}`),
		jsontext.Value(`{"` + ids[0] + `":4,"` + ids[1] + `":["Tutorial"],"` + ids[3] + `":"No","` + ids[4] + `":"  "}`),
		jsontext.Value(`{"` + ids[0] + `":4,"` + ids[1] + `":["Podcast"],"` + ids[2] + `":"Office"}`), // an option since removed
		jsontext.Value(`not json`),
	}
	got := d.Summarize(responses)
	if len(got) != 5 {
		t.Fatalf("%d results", len(got))
	}
	counts := func(r Result) string {
		var parts []string
		for _, c := range r.Counts {
			parts = append(parts, c.Label+"="+strconv.Itoa(c.N)+"/"+strconv.Itoa(c.Percent)+"%")
		}
		return strings.Join(parts, " ")
	}
	for i, want := range []struct {
		answered int
		counts   string
	}{
		{3, "1=0/0% 2=0/0% 3=0/0% 4=2/67% 5=1/33%"},
		{3, "Tutorial=2/67% Reference=1/33% Examples=0/0%"},
		{2, "Office=1/50% Home=1/50%"},
		{2, "Yes=1/50% No=1/50%"},
		{1, ""},
	} {
		if got[i].Answered != want.answered || counts(got[i]) != want.counts {
			t.Errorf("question %d: answered %d, counts %q; want %d, %q", i+1, got[i].Answered, counts(got[i]), want.answered, want.counts)
		}
	}
	if got[0].Mean < 4.33 || got[0].Mean > 4.34 {
		t.Errorf("mean = %v, want 4.33", got[0].Mean)
	}
	if !slices.Equal(got[4].Texts, []string{"More recipes"}) {
		t.Errorf("texts = %q", got[4].Texts)
	}
	if empty := d.Summarize(nil); empty[0].Answered != 0 || empty[0].Counts[0].Percent != 0 {
		t.Errorf("no responses: %+v", empty[0])
	}
}

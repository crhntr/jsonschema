package pipeline

import (
	"encoding/json/jsontext"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// lineDiff shows the lines where want and got differ.
func lineDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	for i := range max(len(w), len(g)) {
		var a, c string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			c = g[i]
		}
		if a != c {
			fmt.Fprintf(&b, "line %d:\n  want %q\n  got  %q\n", i+1, a, c)
		}
	}
	return b.String()
}

func TestDocument(t *testing.T) {
	d, err := Parse([]byte(sample(t)))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("sections", func(t *testing.T) {
		var names []string
		for _, s := range d.Sections(Jobs) {
			names = append(names, s.Name)
		}
		if want := []string{"unit", "build", "deploy-staging", "deploy-production"}; !slices.Equal(names, want) {
			t.Errorf("jobs = %q, want %q", names, want)
		}
		s, ok := d.Section(Resources, "nightly")
		if !ok || s.Pointer != "/resources/2" || s.Line != 33 {
			t.Errorf("Section(resources, nightly) = %+v, %v", s, ok)
		}
		if _, ok := d.Section(Jobs, "nope"); ok {
			t.Errorf("found a job that does not exist")
		}
		if got := d.Sections(ResourceTypes); len(got) != 1 || got[0].Name != "slack-notification" {
			t.Errorf("resource types = %+v", got)
		}
	})

	t.Run("replace keeps the rest of the file", func(t *testing.T) {
		s, _ := d.Section(Resources, "nightly")
		e, err := d.Replace(s.Pointer, jsontext.Value(`{"name":"nightly","type":"time","icon":"clock-outline","source":{"interval":"12h"}}`))
		if err != nil {
			t.Fatal(err)
		}
		text, err := e.YAML()
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"# The application's source code.", "on_failure: *slack_alert", "slack_alert: &slack_alert", "interval: 12h"} {
			if !strings.Contains(string(text), want) {
				t.Errorf("after Replace, the YAML lacks %q", want)
			}
		}
		if v, _ := d.Value("/resources/2/source/interval"); string(v) != `"24h"` {
			t.Errorf("Replace changed the original document: %s", v)
		}
	})

	t.Run("append and remove", func(t *testing.T) {
		e, err := d.Append(Jobs, jsontext.Value(`{"name":"lint","plan":[{"get":"repo"}]}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := e.Section(Jobs, "lint"); !ok {
			t.Errorf("appended job is missing")
		}
		f, err := e.Remove("/jobs/0")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := f.Section(Jobs, "unit"); ok {
			t.Errorf("removed job is still there")
		}
		empty, err := Parse([]byte("jobs: []\n"))
		if err != nil {
			t.Fatal(err)
		}
		g, err := empty.Append(Resources, jsontext.Value(`{"name":"r","type":"git","source":{}}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := g.Section(Resources, "r"); !ok {
			t.Errorf("appending to a missing list did not create it")
		}
	})

	t.Run("edits change only their own lines", func(t *testing.T) {
		src := sample(t)
		s, _ := d.Section(Jobs, "deploy-staging")
		v, err := d.Value(s.Pointer)
		if err != nil {
			t.Fatal(err)
		}
		edited := jsontext.Value(strings.Replace(string(v), `"10m"`, `"15m"`, 1))
		e, err := d.Replace(s.Pointer, edited)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := e.YAML()
		if want := strings.Replace(src, "timeout: 10m", "timeout: 15m", 1); string(got) != want {
			t.Errorf("Replace changed more than the timeout line:\n%s", lineDiff(want, string(got)))
		}

		same, err := d.Replace(s.Pointer, v)
		if err != nil || same != d {
			t.Errorf("an unchanged value made a new document")
		}

		f, err := d.Append(Resources, jsontext.Value(`{"name":"docs","type":"git","source":{"uri":"https://example.com/docs.git"}}`))
		if err != nil {
			t.Fatal(err)
		}
		got, _ = f.YAML()
		added := "  source:\n    url: ((slack.webhook))\n- name: docs\n  type: git\n  source:\n    uri: https://example.com/docs.git\n"
		if want := strings.Replace(src, "  source:\n    url: ((slack.webhook))\n", added, 1); string(got) != want {
			t.Errorf("Append:\n%s", lineDiff(want, string(got)))
		}

		r, _ := d.Section(Resources, "repo")
		g, err := d.Remove(r.Pointer)
		if err != nil {
			t.Fatal(err)
		}
		got, _ = g.YAML()
		removed := "# The application's source code.\n- name: repo\n  type: git\n  icon: github\n  source:\n    uri: https://github.com/example/app.git\n    branch: main\n\n"
		if want := strings.Replace(src, removed, "", 1); string(got) != want {
			t.Errorf("Remove:\n%s", lineDiff(want, string(got)))
		}
	})

	t.Run("an edit through an anchor is refused", func(t *testing.T) {
		if _, err := d.Replace("/jobs/0/on_failure", jsontext.Value(`{"put":"notify"}`)); err == nil {
			t.Errorf("replacing an alias succeeded")
		}
	})
}

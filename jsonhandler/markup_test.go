package jsonhandler

import (
	"encoding/json/jsontext"
	"slices"
	"strings"
	"testing"
)

// TestMarkup checks the wizard, outline and stub markup README.md
// documents.
func TestMarkup(t *testing.T) {
	h := newHandler(t, `{"type":"object","properties":{
		"a":{"title":"Alpha","type":"object","properties":{"b":{"type":"object","properties":{"n":{"type":"integer","maximum":1}}}}},
		"c":{"type":"object","deprecated":true,"x-form":{"class":"extra"},"properties":{"s":{"type":"string"}}},
		"v":{"oneOf":[{"title":"Dog","type":"object","properties":{"d":{"type":"string"}}},{"title":"Cat","type":"string"}]}},
		"required":["a"]}`)
	h.Outline = true
	h.Value = jsontext.Value(`{"a":{"b":{"n":5}},"c":{"s":"x"},"v":{"d":"y"}}`)

	t.Run("wizard", func(t *testing.T) {
		// Deprecated fields come last: the steps are n, v, d, s.
		p := open(t, h).jump("/v/d").submit()
		progress := p.doc.QuerySelector(".jsonform-progress")
		if progress == nil || progress.ID() != "jsonform_progress" {
			t.Fatalf("progress = %v", progress)
		}
		values := func(selector string) []string {
			var out []string
			for el := range p.doc.QuerySelectorSequence(selector) {
				out = append(out, el.GetAttribute("value")+"="+strings.TrimSpace(el.TextContent()))
			}
			return out
		}
		for selector, want := range map[string][]string{
			".jsonform-crumbs button":    {"step=Value", "step/v=v"},
			".jsonform-elsewhere button": {"step/a/b/n=n"},
			".jsonform-back":             {"step/v=Back"},
			".jsonform-next":             {"next/c/s=Next: s"},
		} {
			if got := values(selector); !slices.Equal(got, want) {
				t.Errorf("%s = %q, want %q", selector, got, want)
			}
		}
		next := p.doc.QuerySelector(".jsonform-next")
		if next.GetAttribute("aria-describedby") != progress.ID() || next.HasAttribute("formnovalidate") {
			t.Errorf("next = %s", next.OuterHTML())
		}
		last := open(t, h).jump("/c/s")
		if last.has(".jsonform-next") {
			t.Errorf("the last step has a next button")
		}
		if b := last.doc.QuerySelector("button[hidden]"); b.HasAttribute("name") || b.HasAttribute("value") {
			t.Errorf("enter on the last step = %s, want submit", b.OuterHTML())
		}
	})

	t.Run("outline in the fields view", func(t *testing.T) {
		p := open(t, h).press("fold/c")
		items := outlineItems(p)
		for label, want := range map[string]string{
			"Alpha": "A:Alpha [step/a Edit / Edit Alpha with the wizard] [fold/a Hide / Hide Alpha]",
			"b":     "A:b [step/a/b Edit / Edit b with the wizard] [fold/a/b Hide / Hide b]",
			"c":     "A:c data-folded [step/c Edit / Edit c with the wizard] [unfold/c Show / Show c]",
			"v":     "A:v [step/v Edit / Edit v with the wizard] [fold/v Hide / Hide v]",
		} {
			if items[label] != want {
				t.Errorf("outline %s =\n  %s\nwant\n  %s", label, items[label], want)
			}
		}
		if note := p.doc.QuerySelector(".jsonform-outline .jsonform-note"); note == nil || note.TextContent() != "Dog" {
			t.Errorf("choice note = %v", note)
		}
		if a := p.doc.QuerySelector(`.jsonform-outline a[href="#jsonform-a_group"]`); a == nil {
			t.Errorf("outline does not link to the group")
		}
	})

	t.Run("outline in the wizard", func(t *testing.T) {
		p := open(t, h).jump("/c/s")
		for label, want := range map[string]string{
			"Alpha": "BUTTON:Alpha data-invalid [step/a Alpha / ] [fold/a Hide / Hide Alpha]",
			"c":     "BUTTON:c aria-current [step/c c / ] [fold/c Hide / Hide c]",
			"v":     "BUTTON:v [step/v v / ] [fold/v Hide / Hide v]",
		} {
			if got := outlineItems(p)[label]; got != want {
				t.Errorf("outline %s =\n  %s\nwant\n  %s", label, got, want)
			}
		}
	})

	t.Run("stubs", func(t *testing.T) {
		p := open(t, h).press("fold/a").press("fold/c").press("fold/v").submit()
		a := p.doc.QuerySelector(`[data-pointer="/a"]`)
		c := p.doc.QuerySelector(`[data-pointer="/c"]`)
		v := p.doc.QuerySelector(`[data-pointer="/v"]`)
		for name, tc := range map[string]struct{ got, want string }{
			"a": {attrs(a), "jsonform-field jsonform-stub data-stub data-folded data-invalid data-required"},
			"c": {attrs(c), "jsonform-field jsonform-stub extra data-stub data-folded data-deprecated"},
			"v": {attrs(v), "jsonform-field jsonform-stub data-stub data-folded"},
		} {
			if tc.got != tc.want {
				t.Errorf("%s stub = %q, want %q", name, tc.got, tc.want)
			}
		}
		for name, stub := range map[string]struct{ id, kind, label string }{
			"a": {"jsonform-a_group", "object", "Alpha"},
			"c": {"jsonform-c_group", "object", "c"},
			"v": {"jsonform-v_group", "choice", "v"},
		} {
			el := p.doc.QuerySelector(`[data-pointer="/` + name + `"]`)
			if el.ID() != stub.id || el.GetAttribute("data-kind") != stub.kind {
				t.Errorf("%s stub id %q kind %q", name, el.ID(), el.GetAttribute("data-kind"))
			}
			label := el.QuerySelector("span.jsonform-label")
			show := el.QuerySelector("button.jsonform-unfold")
			if label == nil || strings.TrimSuffix(strings.TrimSpace(label.TextContent()), " *") != stub.label || label.ID() == "" {
				t.Errorf("%s stub label %v", name, label)
			}
			if show == nil || show.GetAttribute("value") != "unfold/"+name || show.GetAttribute("aria-describedby") != label.ID() {
				t.Errorf("%s stub show button %v", name, show)
			}
		}
		if s := v.QuerySelector(".jsonform-summary"); s == nil || s.TextContent() != "Dog" {
			t.Errorf("choice stub summary = %v", s)
		}
		if a.QuerySelector(".jsonform-summary") != nil {
			t.Errorf("object stub has a summary")
		}
		if a.QuerySelector("abbr.jsonform-required") == nil {
			t.Errorf("required stub has no mark")
		}
		if b := c.QuerySelector(".jsonform-remove"); b == nil || b.GetAttribute("value") != "remove/c" {
			t.Errorf("an optional hidden group cannot be removed")
		}
		if el := c.QuerySelector(`input[type="hidden"][name="$/c/s"]`); el == nil || el.GetAttribute("value") != "x" {
			t.Errorf("hidden group does not carry its values")
		}
	})
}

// outlineItems describes each outline entry by its label: the label's
// element and text, its state attributes, and its own buttons as
// [value text / aria-label].
func outlineItems(p *page) map[string]string {
	items := map[string]string{}
	for li := range p.doc.QuerySelectorSequence(".jsonform-outline li") {
		label := li.QuerySelector(".jsonform-outline-label")
		var b strings.Builder
		b.WriteString(label.TagName() + ":" + label.TextContent())
		for _, attr := range []string{"data-folded", "data-invalid", "aria-current"} {
			if li.HasAttribute(attr) {
				b.WriteString(" " + attr)
			}
		}
		for btn := range li.QuerySelectorSequence("button") {
			if btn.Closest("li").OuterHTML() != li.OuterHTML() {
				continue
			}
			b.WriteString(" [" + btn.GetAttribute("value") + " " + btn.TextContent() + " / " + btn.GetAttribute("aria-label") + "]")
		}
		items[label.TextContent()] = b.String()
	}
	return items
}

// attrs describes a stub's class and state attributes.
func attrs(el interface {
	GetAttribute(string) string
	HasAttribute(string) bool
},
) string {
	out := []string{el.GetAttribute("class")}
	for _, attr := range []string{"data-stub", "data-folded", "data-invalid", "data-required", "data-deprecated"} {
		if el.HasAttribute(attr) {
			out = append(out, attr)
		}
	}
	return strings.Join(out, " ")
}

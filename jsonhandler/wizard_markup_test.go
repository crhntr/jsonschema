package jsonhandler

import (
	"encoding/json/jsontext"
	"slices"
	"strings"
	"testing"
)

// TestWizardMarkup checks the markup README.md documents for the wizard,
// stubs and the outline.
func TestWizardMarkup(t *testing.T) {
	h := newHandler(t, `{"type":"object","properties":{
		"a":{"title":"Alpha","type":"object","properties":{"b":{"type":"object","properties":{"n":{"type":"integer","maximum":1}}}}},
		"c":{"type":"object","deprecated":true,"x-form":{"class":"extra"},"properties":{"s":{"type":"string"}}},
		"v":{"oneOf":[{"title":"Dog","type":"object","properties":{"d":{"type":"string"}}},{"title":"Cat","type":"string"}]}},
		"required":["a"]}`)
	h.Outline = true
	h.Value = jsontext.Value(`{"a":{"b":{"n":5}},"c":{"s":"x"},"v":{"d":"y"}}`)

	t.Run("crumbs and navigation", func(t *testing.T) {
		p := open(t, h).press("step/a/b")
		var crumbs []string
		for li := range p.doc.QuerySelectorSequence(".jsonform-crumbs li") {
			switch b := li.QuerySelector("button"); {
			case li.HasAttribute("aria-current"):
				if b != nil || li.QuerySelector("span.jsonform-label") == nil {
					t.Errorf("current crumb is not a plain label")
				}
				crumbs = append(crumbs, "*"+strings.TrimSpace(li.TextContent()))
			default:
				if b == nil {
					t.Fatalf("crumb without a button")
				}
				crumbs = append(crumbs, b.GetAttribute("value")+"="+b.TextContent())
			}
		}
		if want := []string{"step=Value", "step/a=Alpha", "*b"}; !slices.Equal(crumbs, want) {
			t.Errorf("crumbs = %q, want %q", crumbs, want)
		}
		for selector, want := range map[string]string{
			".jsonform-back":    "step/a=Back",
			".jsonform-up-step": "step/a=Up to Alpha",
			".jsonform-next":    "step/v=Next: v", // c is deprecated, so it comes last
		} {
			b := p.doc.QuerySelector(selector)
			if b == nil || b.GetAttribute("value")+"="+b.TextContent() != want {
				t.Errorf("%s = %v, want %s", selector, b, want)
			}
		}
		if p.has(".jsonform-elsewhere") {
			t.Errorf("errors elsewhere listed before submitting")
		}

		p = open(t, h).press("step/c").submit()
		var elsewhere []string
		for b := range p.doc.QuerySelectorSequence(".jsonform-elsewhere button") {
			elsewhere = append(elsewhere, b.GetAttribute("value")+"="+b.TextContent())
		}
		if want := []string{"step/a/b=b"}; !slices.Equal(elsewhere, want) {
			t.Errorf("errors elsewhere = %q, want %q", elsewhere, want)
		}
	})

	t.Run("outline in the fields view", func(t *testing.T) {
		p := open(t, h).press("fold/c")
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
		for label, want := range map[string]string{
			"Alpha": "A:Alpha [step/a Edit / Edit Alpha with the wizard] [fold/a Hide / Hide Alpha]",
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
	})

	t.Run("outline in the wizard", func(t *testing.T) {
		p := open(t, h).press("step/a")
		current := p.doc.QuerySelector(`.jsonform-outline li[aria-current="step"]`)
		if current == nil {
			t.Fatalf("no current step in the outline")
		}
		label := current.QuerySelector("button.jsonform-outline-label")
		if label == nil || label.TextContent() != "Alpha" || label.GetAttribute("value") != "step/a" {
			t.Errorf("current outline label = %v", label)
		}
		if n := len(selectAll(p.doc, `.jsonform-outline li[aria-current]`)); n != 1 {
			t.Errorf("%d current steps, want 1", n)
		}
		for li := range p.doc.QuerySelectorSequence(".jsonform-outline li") {
			if li.HasAttribute("data-folded") {
				t.Errorf("an unfolded group is marked folded")
			}
			for btn := range li.QuerySelectorSequence("button") {
				if btn.TextContent() == "Edit" {
					t.Errorf("the wizard outline has separate Edit buttons")
				}
			}
		}
	})

	t.Run("stubs", func(t *testing.T) {
		p := open(t, h).press(ActionWizard)
		a := p.doc.QuerySelector(`[data-pointer="/a"]`)
		c := p.doc.QuerySelector(`[data-pointer="/c"]`)
		v := p.doc.QuerySelector(`[data-pointer="/v"]`)
		for name, tc := range map[string]struct{ got, want string }{
			"a": {attrs(a), "jsonform-field jsonform-stub data-stub data-invalid data-required"},
			"c": {attrs(c), "jsonform-field jsonform-stub extra data-stub data-deprecated"},
			"v": {attrs(v), "jsonform-field jsonform-stub data-stub"},
		} {
			if tc.got != tc.want {
				t.Errorf("%s stub = %q, want %q", name, tc.got, tc.want)
			}
		}
		for name, stub := range map[string]struct{ id, kind, label string }{
			"a": {"jsonform-a_group", "object", "Alpha"},
			"v": {"jsonform-v_group", "choice", "v"},
		} {
			el := p.doc.QuerySelector(`[data-pointer="/` + name + `"]`)
			if el.ID() != stub.id || el.GetAttribute("data-kind") != stub.kind {
				t.Errorf("%s stub id %q kind %q", name, el.ID(), el.GetAttribute("data-kind"))
			}
			label := el.QuerySelector("span.jsonform-label")
			edit := el.QuerySelector("button.jsonform-step")
			if label == nil || strings.TrimSuffix(strings.TrimSpace(label.TextContent()), " *") != stub.label || label.ID() == "" || edit.GetAttribute("aria-describedby") != label.ID() {
				t.Errorf("%s stub label %v edit %v", name, label, edit)
			}
		}
		if s := v.QuerySelector(".jsonform-summary"); s == nil || s.TextContent() != "Dog" {
			t.Errorf("choice stub summary = %v", s)
		}
		if a.QuerySelector("abbr.jsonform-required") == nil {
			t.Errorf("required stub has no mark")
		}

		p = open(t, h).press("fold/c")
		c = p.doc.QuerySelector(`[data-pointer="/c"]`)
		label := c.QuerySelector("span.jsonform-label")
		show := c.QuerySelector("button.jsonform-unfold")
		if show == nil || show.GetAttribute("aria-describedby") != label.ID() || c.QuerySelector("button.jsonform-step") != nil {
			t.Errorf("folded stub buttons: %s", c.OuterHTML())
		}
		if b := c.QuerySelector(".jsonform-remove"); b == nil || b.GetAttribute("value") != "remove/c" {
			t.Errorf("an optional folded group cannot be removed")
		}
		if a := p.doc.QuerySelector(`[data-pointer="/a"]`); a.HasAttribute("data-stub") {
			t.Errorf("an unfolded group is a stub")
		}
	})
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

package jsonhandler

import (
	"encoding/json/jsontext"
	"slices"
	"strings"
	"testing"
)

const nestedSchema = `{"type":"object","required":["name"],"properties":{
	"name":{"type":"string"},
	"address":{"type":"object","properties":{"street":{"type":"string"},"geo":{"type":"object","properties":{"lat":{"type":"number"}}}}},
	"tags":{"type":"array","items":{"type":"string"}},
	"labels":{"type":"object","additionalProperties":{"type":"object","properties":{"color":{"type":"string"}}}}
}}`

func TestFold(t *testing.T) {
	h := newHandler(t, nestedSchema)
	h.Outline = true
	h.Value = jsontext.Value(`{"name":"Ada","address":{"street":"Main","geo":{"lat":1}},"tags":["x","y"]}`)
	p := open(t, h)
	if b := p.doc.QuerySelector(`.jsonform-outline button[value="fold/address"]`); b == nil || b.TextContent() != "Hide" || b.GetAttribute("aria-label") != "Hide address" {
		t.Fatalf("no hide button for address")
	}
	p = p.press("fold/address")
	stub := p.doc.QuerySelector(`[data-pointer="/address"]`)
	if stub == nil || !stub.HasAttribute("data-stub") || !stub.HasAttribute("data-folded") {
		t.Fatalf("address is not hidden: %v", stub)
	}
	if b := stub.QuerySelector(`button[value="unfold/address"]`); b == nil || b.TextContent() != "Show" {
		t.Errorf("hidden group has no show button")
	}
	var hidden []string
	for el := range stub.QuerySelectorSequence(`input[type="hidden"]`) {
		hidden = append(hidden, el.GetAttribute("name")+"="+el.GetAttribute("value"))
	}
	for _, want := range []string{"$/address/street=Main", "$/address/geo/lat=1", "@folded/address=1"} {
		if !slices.Contains(hidden, want) {
			t.Errorf("hidden group does not carry %s: %q", want, hidden)
		}
	}
	item := p.doc.QuerySelector(`.jsonform-outline li[data-folded]`)
	if item == nil || item.QuerySelector("ol") != nil || item.QuerySelector(`button[value="unfold/address"]`) == nil {
		t.Errorf("outline item of a hidden group = %v", item)
	}
	// Hidden stays hidden across edits, and the value is unchanged.
	p = p.press("add/tags")
	if !p.has(`[data-pointer="/address"][data-folded]`) {
		t.Errorf("the group came back after an edit")
	}
	res := p.submit()
	assertJSONEqual(t, jsontext.Value(res.doc.QuerySelector(".jsonform-result code").TextContent()), `{"name":"Ada","address":{"street":"Main","geo":{"lat":1}},"tags":["x","y",""]}`)

	p = p.press("unfold/address")
	if el := p.doc.QuerySelector(`[data-pointer="/address"]`); el.HasAttribute("data-stub") || !p.has(`input[name="$/address/street"]:not([type="hidden"])`) {
		t.Errorf("show did not bring the group back")
	}
}

func TestFold_summary(t *testing.T) {
	h := newHandler(t, nestedSchema)
	h.Outline = true
	h.Value = jsontext.Value(`{"name":"Ada","tags":["x"],"labels":{"bug":{},"ui":{}}}`)
	p := open(t, h).press("fold/tags").press("fold/labels")
	for pointer, want := range map[string]string{"/tags": "1 item", "/labels": "2 properties"} {
		if got := p.texts(`[data-pointer="` + pointer + `"] .jsonform-summary`); !slices.Equal(got, []string{want}) {
			t.Errorf("summary of %s = %q, want %q", pointer, got, want)
		}
	}
}

func TestReasons(t *testing.T) {
	doc := render(t, `{"type":"object","properties":{
		"a":{"type":"array","items":{"type":"string"},"maxItems":1},
		"one":{"type":"array","items":{"type":"string"},"maxItems":1},
		"t":{"type":"array","prefixItems":[{"type":"string"},{"type":"string"}],"items":false},
		"m":{"type":"object","additionalProperties":{"type":"string"},"maxProperties":2},
		"c":{"type":"object","properties":{"x":{"type":"string"}},"additionalProperties":false},
		"r":{"type":"string","readOnly":true},
		"v":{"oneOf":[{"title":"Dog","type":"string"},{"title":"Cat","type":"integer"},{"title":"Bird","type":"boolean"}]}}}`,
		`{"a":["x"],"t":["p","q"],"m":{"k":"1","l":"2"},"c":{"x":"1","y":"2"},"r":"z","v":"woof"}`)
	for ptr, want := range map[string]string{
		"/a": "At most 1 item.",
		"/t": "Exactly 2 items.",
		"/m": "At most 2 properties.",
		"/c": "No other properties are allowed.",
	} {
		field := doc.QuerySelector(`[data-pointer="` + ptr + `"]`)
		button := field.QuerySelector("button.jsonform-add")
		reason := field.QuerySelector(".jsonform-reason")
		if button == nil || !button.HasAttribute("disabled") || reason == nil || reason.TextContent() != want || button.GetAttribute("aria-describedby") != reason.ID() {
			t.Errorf("%s: add %v, reason %v, want %q", ptr, button, reason, want)
		}
	}
	if b := doc.QuerySelector(`[data-pointer="/one"] button.jsonform-add`); b == nil || b.HasAttribute("disabled") {
		t.Errorf("an array with room has no working add button")
	}
	if got := strings.TrimSpace(doc.QuerySelector(`[data-pointer="/r"] .jsonform-reason`).TextContent()); got != "Read-only" {
		t.Errorf("read-only reason = %q", got)
	}
	note := doc.QuerySelector(`[data-pointer="/v"] .jsonform-inactive`)
	if note == nil || !strings.Contains(note.TextContent(), "Not in use: Cat, Bird.") {
		t.Errorf("inactive variants note = %v", note)
	}
	if doc.QuerySelector(`[data-pointer="/v/x"]`) != nil {
		t.Errorf("fields of an inactive variant are rendered")
	}
}

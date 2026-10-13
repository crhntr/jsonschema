package jsonhandler

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/typelate/dom/spec"
)

// TestRender_readOnlyContainers checks that readOnly on an object, array,
// map or choice reaches everything inside it: the server keeps such a
// value as it was, so the form offers no way to change it.
func TestRender_readOnlyContainers(t *testing.T) {
	t.Run("fields inside a read-only object", func(t *testing.T) {
		doc := render(t, `{"type":"object","properties":{"audit":{"readOnly":true,"type":"object","properties":{"by":{"type":"string"}}}}}`, `{"audit":{"by":"ada"}}`)
		input := doc.QuerySelector(`input[name="$/audit/by"]`)
		if input == nil || !input.HasAttribute("readonly") {
			t.Errorf("audit/by = %v, want a readonly input", input)
		}
	})

	t.Run("the reason is given once", func(t *testing.T) {
		doc := render(t, `{"type":"object","properties":{"audit":{"readOnly":true,"type":"object","properties":{"by":{"type":"string"},"at":{"type":"string"}}}}}`, `{"audit":{"by":"ada","at":"noon"}}`)
		var reasons []string
		for reason := range doc.QuerySelectorSequence(`[data-pointer="/audit"] .jsonform-reason`) {
			reasons = append(reasons, reason.TextContent())
		}
		if len(reasons) != 1 || reasons[0] != "Read-only" {
			t.Errorf("reasons in audit = %q, want one Read-only", reasons)
		}
	})

	t.Run("a read-only array", func(t *testing.T) {
		doc := render(t, `{"type":"object","properties":{"tags":{"readOnly":true,"type":"array","items":{"type":"string"}}}}`, `{"tags":["x","y"]}`)
		tags := doc.QuerySelector(`[data-pointer="/tags"]`)
		assertUnchangeable(t, tags)
		if input := tags.QuerySelector(`input[name="$/tags/0"]`); input == nil || !input.HasAttribute("readonly") {
			t.Errorf("tags/0 = %v, want a readonly input", input)
		}
	})

	t.Run("a read-only map", func(t *testing.T) {
		doc := render(t, `{"type":"object","properties":{"labels":{"readOnly":true,"type":"object","additionalProperties":{"type":"string"}}}}`, `{"labels":{"bug":"red","ui":"blue"}}`)
		labels := doc.QuerySelector(`[data-pointer="/labels"]`)
		assertUnchangeable(t, labels)
		for _, name := range []string{"@key/labels/~k0", "$/labels/~k0"} {
			if input := labels.QuerySelector(`input[name="` + name + `"]`); input == nil || !input.HasAttribute("readonly") {
				t.Errorf("%s = %v, want a readonly input", name, input)
			}
		}
	})

	t.Run("a read-only choice", func(t *testing.T) {
		doc := render(t, `{"type":"object","properties":{"pet":{"readOnly":true,"oneOf":[
			{"title":"Dog","type":"object","properties":{"name":{"type":"string"}}},
			{"title":"Cat","type":"string"}]}}}`, `{"pet":"tom"}`)
		pet := doc.QuerySelector(`[data-pointer="/pet"]`)
		assertUnchangeable(t, pet)
		picker := pet.QuerySelector("select")
		if picker == nil || !picker.HasAttribute("disabled") || picker.HasAttribute("name") {
			t.Fatalf("variant picker = %v, want a disabled select with no name", picker)
		}
		if label := pet.QuerySelector(".jsonform-variant label"); label.GetAttribute("for") != picker.ID() || picker.ID() == "" {
			t.Errorf("variant label is for %q, picker id %q", label.GetAttribute("for"), picker.ID())
		}
		var options []string
		for option := range picker.QuerySelectorSequence("option") {
			selected := ""
			if option.HasAttribute("selected") {
				selected = "*"
			}
			options = append(options, selected+option.GetAttribute("value")+"="+option.TextContent())
		}
		if want := []string{"0=Dog", "*1=Cat"}; !slices.Equal(options, want) {
			t.Errorf("variant options = %q, want %q", options, want)
		}
		var kept []string
		for input := range pet.QuerySelectorSequence(`input[type="hidden"]`) {
			if input.GetAttribute("name") == "@variant/pet" {
				kept = append(kept, input.GetAttribute("value"))
			}
		}
		if !slices.Equal(kept, []string{"1"}) {
			t.Errorf("chosen variant carried as %q, want one hidden input with 1", kept)
		}
		if input := pet.QuerySelector(`input[name="$/pet"]`); input == nil || !input.HasAttribute("readonly") {
			t.Errorf("pet = %v, want a readonly input", input)
		}
	})

	t.Run("a map that is not read-only", func(t *testing.T) {
		doc := render(t, `{"type":"object","properties":{"labels":{"type":"object","additionalProperties":{"type":"string"}}}}`, `{"labels":{"bug":"red"}}`)
		if key := doc.QuerySelector(`input[name="@key/labels/~k0"]`); key == nil || key.HasAttribute("readonly") {
			t.Errorf("key = %v, want an editable input", key)
		}
	})

	t.Run("a choice inside a read-only object", func(t *testing.T) {
		doc := render(t, `{"type":"object","properties":{"audit":{"readOnly":true,"type":"object","properties":{"pet":{"oneOf":[
			{"title":"Dog","type":"object","properties":{"name":{"type":"string"}}},
			{"title":"Cat","type":"string"}]}}}}}`, `{"audit":{"pet":{"name":"Rex"}}}`)
		pet := doc.QuerySelector(`[data-pointer="/audit/pet"]`)
		if picker := pet.QuerySelector("select"); picker == nil || !picker.HasAttribute("disabled") {
			t.Errorf("variant picker = %v, want a disabled select", picker)
		}
		if input := pet.QuerySelector(`input[name="$/audit/pet/name"]`); input == nil || !input.HasAttribute("readonly") {
			t.Errorf("audit/pet/name = %v, want a readonly input", input)
		}
	})

	t.Run("a value left out of a read-only object", func(t *testing.T) {
		doc := render(t, `{"type":"object","properties":{"audit":{"readOnly":true,"type":"object","properties":{
			"by":{"type":"string"},
			"geo":{"type":"object","x-form":{"collapsed":true},"properties":{"lat":{"type":"number"}}}}}}}`, `{"audit":{"by":"ada"}}`)
		audit := doc.QuerySelector(`[data-pointer="/audit"]`)
		assertUnchangeable(t, audit)
		if geo := audit.QuerySelector(`[data-pointer="/audit/geo"][data-collapsed]`); geo == nil {
			t.Errorf("audit/geo is not shown as left out")
		}
	})

	t.Run("a read-only value that is left out", func(t *testing.T) {
		doc := render(t, `{"type":"object","properties":{"geo":{"readOnly":true,"type":"object","x-form":{"collapsed":true},"properties":{"lat":{"type":"number"}}}}}`, `{}`)
		geo := doc.QuerySelector(`[data-pointer="/geo"][data-collapsed]`)
		if geo == nil {
			t.Fatalf("geo is not shown as left out")
		}
		assertUnchangeable(t, geo)
	})

	t.Run("a wide read-only object", func(t *testing.T) {
		var props []string
		for i := range foldThreshold + 1 {
			props = append(props, fmt.Sprintf(`"p%d":{"type":"string"}`, i))
		}
		doc := render(t, `{"type":"object","properties":{"wide":{"readOnly":true,"type":"object","properties":{`+strings.Join(props, ",")+`}}}}`, `{"wide":{"p0":"x"}}`)
		wide := doc.QuerySelector(`[data-pointer="/wide"]`)
		assertUnchangeable(t, wide)
		if picker := wide.QuerySelector(".jsonform-pick"); picker != nil {
			t.Errorf("wide has a picker of properties to add: %s", picker.OuterHTML())
		}
	})
}

func TestWizard_readOnly(t *testing.T) {
	t.Run("next passes a blank required field nobody can fill in", func(t *testing.T) {
		h := newHandler(t, `{"type":"object","required":["id","name"],"properties":{"id":{"type":"string","readOnly":true},"name":{"type":"string"}}}`)
		first := open(t, h).press(ActionWizard)
		if _, _, label := first.stepOf(); label != "id" {
			t.Fatalf("first step = %q, want id", label)
		}
		next := first.press("next/name")
		if _, _, label := next.stepOf(); label != "name" {
			t.Errorf("next from a blank read-only id went to %q, want name", label)
		}
	})
}

// assertUnchangeable checks that a container offers no edits: no Add,
// Remove or Move buttons, and a reason saying it is read-only.
func assertUnchangeable(t *testing.T, container spec.Element) {
	t.Helper()
	for button := range container.QuerySelectorSequence("button") {
		t.Errorf("read-only %s has a button: %s", container.GetAttribute("data-pointer"), button.OuterHTML())
	}
	if reason := container.QuerySelector(".jsonform-reason"); reason == nil || reason.TextContent() != "Read-only" {
		t.Errorf("read-only %s reason = %v, want Read-only", container.GetAttribute("data-pointer"), reason)
	}
}

package jsonhandler

import (
	"context"
	"encoding/json/jsontext"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/typelate/dom/spec"
)

// TestMarkupContract renders every kind in three states and checks the
// markup README.md promises a stylesheet can rely on: the wrapper's
// data attributes and classes, the label and control ids, the description
// and error list, and aria-describedby.
func TestMarkupContract(t *testing.T) {
	kinds := []struct {
		kind, schema, value string
		group               bool   // rendered as a fieldset with a legend
		control             string // selector for the control inside the wrapper, if it has one with a label
	}{
		{kind: "text", schema: `{"type":"string"}`, value: `"x"`, control: "input.jsonform-control"},
		{kind: "number", schema: `{"type":"number"}`, value: `1`, control: "input.jsonform-control"},
		{kind: "textarea", schema: `{"type":"string","x-form":{"widget":"textarea"}}`, value: `"x"`, control: "textarea"},
		{kind: "json", schema: `{"x-form":{"widget":"json"}}`, value: `[1]`, control: "textarea"},
		{kind: "checkbox", schema: `{"type":"boolean"}`, value: `true`, control: "input.jsonform-control"},
		{kind: "select", schema: `{"enum":["a","b"]}`, value: `"a"`, control: "select"},
		{kind: "radio", schema: `{"enum":["a","b"],"x-form":{"widget":"radio"}}`, value: `"a"`, group: true},
		{kind: "checkboxes", schema: `{"type":"array","uniqueItems":true,"items":{"enum":["a","b"]}}`, value: `["a"]`, group: true},
		{kind: "const", schema: `{"const":"c"}`, value: `"c"`},
		{kind: "null", schema: `{"type":"null"}`, value: `null`},
		{kind: "object", schema: `{"type":"object","properties":{"a":{"type":"string"}}}`, value: `{"a":"x"}`, group: true},
		{kind: "map", schema: `{"type":"object","additionalProperties":{"type":"string"}}`, value: `{"k":"x"}`, group: true},
		{kind: "array", schema: `{"type":"array","items":{"type":"string"}}`, value: `["x"]`, group: true},
		{kind: "choice", schema: `{"oneOf":[{"type":"string"},{"type":"integer"}]}`, value: `"x"`, group: true},
	}
	with := func(schema, extra string) string {
		return strings.TrimSuffix(schema, "}") + strings.Repeat(",", min(1, len(schema)-2)) + extra + "}"
	}
	for _, k := range kinds {
		t.Run(k.kind, func(t *testing.T) {
			t.Run("flagged", func(t *testing.T) {
				f := with(k.schema, `"description":"About f","deprecated":true,"readOnly":true`)
				f = strings.Replace(f, `"x-form":{`, `"x-form":{"class":"extra",`, 1)
				if !strings.Contains(f, `"x-form"`) {
					f = with(f, `"x-form":{"class":"extra"}`)
				}
				doc := render(t, `{"type":"object","required":["f"],"properties":{"f":`+f+`}}`, `{"f":`+k.value+`}`)
				el := wrapper(t, doc, k.kind)
				for _, attr := range []string{"data-required", "data-deprecated", "data-readonly"} {
					if k.kind == "const" || k.kind == "null" {
						if attr != "data-required" {
							continue
						}
					}
					if !el.HasAttribute(attr) {
						t.Errorf("no %s", attr)
					}
				}
				if el.HasAttribute("data-invalid") {
					t.Errorf("data-invalid on a valid field")
				}
				if !strings.Contains(" "+el.GetAttribute("class")+" ", " extra ") || !strings.HasPrefix(el.GetAttribute("class"), "jsonform-field") {
					t.Errorf("class = %q", el.GetAttribute("class"))
				}
				label := el.QuerySelector(".jsonform-label")
				if label == nil || !strings.Contains(label.TextContent(), "f") {
					t.Fatalf("no label")
				}
				if k.kind != "const" && k.kind != "null" && label.QuerySelector("abbr.jsonform-required") == nil {
					t.Errorf("label of a required field has no mark")
				}
				help := el.QuerySelector(".jsonform-description")
				if help == nil || help.TextContent() != "About f" {
					t.Fatalf("description = %v", help)
				}
				errs := el.QuerySelector("ul.jsonform-errors")
				if errs == nil || !errs.HasAttribute("hidden") || errs.QuerySelector("li") != nil {
					t.Errorf("error list of a valid field = %v", errs)
				}
				if errs != nil && help.ID() != strings.TrimSuffix(errs.ID(), "_errors")+"_help" {
					t.Errorf("help id %q and errors id %q do not share a field id", help.ID(), errs.ID())
				}
				describer := el
				if k.control != "" {
					describer = el.QuerySelector(k.control)
				}
				if k.kind != "const" && k.kind != "null" {
					if got := strings.Fields(describer.GetAttribute("aria-describedby")); !slices.Contains(got, help.ID()) || (errs != nil && !slices.Contains(got, errs.ID())) {
						t.Errorf("aria-describedby = %q, want %q and %q", got, help.ID(), errs.ID())
					}
				}
				if k.control != "" && k.kind != "checkbox" {
					control := el.QuerySelector(k.control)
					if control.ID() == "" || label.GetAttribute("for") != control.ID() {
						t.Errorf("label for %q, control id %q", label.GetAttribute("for"), control.ID())
					}
				}
			})

			t.Run("plain", func(t *testing.T) {
				doc := render(t, `{"type":"object","properties":{"f":`+k.schema+`}}`, `{"f":`+k.value+`}`)
				el := wrapper(t, doc, k.kind)
				for _, attr := range []string{"data-required", "data-deprecated", "data-readonly", "data-invalid", "data-collapsed"} {
					if el.HasAttribute(attr) {
						t.Errorf("has %s", attr)
					}
				}
				if el.GetAttribute("class") != "jsonform-field" {
					t.Errorf("class = %q", el.GetAttribute("class"))
				}
				if doc.QuerySelector(`[data-pointer="/f"] > .jsonform-description`) != nil {
					t.Errorf("description without one in the schema")
				}
				if label := el.QuerySelector(".jsonform-label"); label == nil || label.QuerySelector("abbr") != nil || strings.TrimSpace(label.TextContent()) != "f" {
					t.Errorf("label = %v", label)
				}
				if k.control != "" {
					control := el.QuerySelector(k.control)
					if control.HasAttribute("aria-invalid") || control.HasAttribute("readonly") || control.HasAttribute("required") || control.HasAttribute("disabled") {
						t.Errorf("control has state attributes: %s", control.OuterHTML())
					}
				}
			})

			t.Run("invalid", func(t *testing.T) {
				h := newHandler(t, `{"type":"object","required":["f"],"properties":{"f":`+with(k.schema, `"not":true`)+`}}`)
				h.Value = jsontext.Value(`{"f":` + k.value + `}`)
				res := postForm(t, h, formState(t, getForm(t, h)))
				if res.StatusCode != http.StatusUnprocessableEntity {
					t.Fatalf("status = %d", res.StatusCode)
				}
				doc := fragment(t, res)
				if form := doc.QuerySelector("form.jsonform"); !form.HasAttribute("data-invalid") {
					t.Errorf("form is not marked invalid")
				}
				var invalid spec.Element
				for el := range doc.QuerySelectorSequence(`[data-pointer="/f"]`) {
					if el.HasAttribute("data-invalid") {
						invalid = el
					}
				}
				if invalid == nil {
					t.Fatalf("no field at /f is marked invalid")
				}
				errs := invalid.QuerySelector("ul.jsonform-errors")
				if errs == nil || errs.HasAttribute("hidden") || errs.QuerySelector("li") == nil {
					t.Errorf("error list = %v", errs)
				}
				if k.control != "" && k.kind != "choice" {
					if c := invalid.QuerySelector(k.control); c == nil || c.GetAttribute("aria-invalid") != "true" {
						t.Errorf("control is not aria-invalid")
					}
				}
			})
		})
	}
}

// wrapper returns the outermost field element at /f and checks its kind.
func wrapper(t *testing.T, doc spec.DocumentFragment, kind string) spec.Element {
	t.Helper()
	el := doc.QuerySelector(`[data-pointer="/f"]`)
	if el == nil {
		t.Fatalf("no field at /f")
	}
	if got := el.GetAttribute("data-kind"); got != kind {
		t.Fatalf("data-kind = %q, want %q", got, kind)
	}
	return el
}

func TestMarkupDetails(t *testing.T) {
	t.Run("read-only controls", func(t *testing.T) {
		doc := render(t, `{"type":"object","properties":{
			"b":{"type":"boolean","readOnly":true},
			"s":{"enum":["a","b"],"readOnly":true},
			"r":{"enum":["a","b"],"readOnly":true,"x-form":{"widget":"radio"}},
			"t":{"type":"string","readOnly":true},
			"x":{"type":"string","readOnly":true,"x-form":{"widget":"textarea"}}}}`,
			`{"b":true,"s":"b","r":"a","t":"v","x":"w"}`)
		if el := doc.QuerySelector(`[data-pointer="/b"] input[type="checkbox"]`); el == nil || !el.HasAttribute("disabled") || !el.HasAttribute("checked") || el.HasAttribute("name") {
			t.Errorf("read-only checkbox = %v", el)
		}
		if el := doc.QuerySelector(`[data-pointer="/s"] select`); el == nil || !el.HasAttribute("disabled") || el.HasAttribute("name") {
			t.Errorf("read-only select = %v", el)
		}
		for el := range doc.QuerySelectorSequence(`[data-pointer="/r"] input[type="radio"]`) {
			if !el.HasAttribute("disabled") {
				t.Errorf("read-only radio is not disabled")
			}
		}
		if el := doc.QuerySelector(`[data-pointer="/t"] input`); el == nil || !el.HasAttribute("readonly") {
			t.Errorf("read-only text = %v", el)
		}
		if el := doc.QuerySelector(`[data-pointer="/x"] textarea`); el == nil || !el.HasAttribute("readonly") || el.HasAttribute("spellcheck") {
			t.Errorf("read-only textarea = %v", el)
		}
	})

	t.Run("checkbox states", func(t *testing.T) {
		doc := render(t, `{"type":"object","properties":{"on":{"type":"boolean"},"off":{"type":"boolean"}}}`, `{"on":true,"off":false}`)
		on := doc.QuerySelector(`[data-pointer="/on"] input[type="checkbox"]`)
		off := doc.QuerySelector(`[data-pointer="/off"] input[type="checkbox"]`)
		if on == nil || !on.HasAttribute("checked") || on.GetAttribute("name") != "$/on" || on.GetAttribute("value") != "true" {
			t.Errorf("checked box = %v", on)
		}
		if off == nil || off.HasAttribute("checked") {
			t.Errorf("unchecked box = %v", off)
		}
		if hidden := doc.QuerySelector(`[data-pointer="/on"] input[type="hidden"]`); hidden == nil || hidden.GetAttribute("value") != "false" || hidden.GetAttribute("name") != "$/on" {
			t.Errorf("hidden false input = %v", hidden)
		}
		if label := doc.QuerySelector(`[data-pointer="/on"] label`); label == nil || label.GetAttribute("for") != on.ID() || label.TextContent() != "on" {
			t.Errorf("checkbox label = %v", label)
		}
	})

	t.Run("text and textarea attributes", func(t *testing.T) {
		doc := render(t, `{"type":"object","required":["t","x"],"properties":{
			"t":{"type":"string","writeOnly":true},
			"x":{"type":"string","minLength":2,"maxLength":9,"x-form":{"widget":"textarea","placeholder":"ph"}},
			"j":{"x-form":{"widget":"json"}}}}`, "")
		if el := doc.QuerySelector(`[name="$/t"]`); el == nil || el.GetAttribute("autocomplete") != "new-password" || el.HasAttribute("readonly") {
			t.Errorf("write-only input = %v", el)
		}
		x := doc.QuerySelector(`textarea[name="$/x"]`)
		for attr, want := range map[string]string{"placeholder": "ph", "minlength": "2", "maxlength": "9", "required": ""} {
			if x == nil || !x.HasAttribute(attr) || x.GetAttribute(attr) != want {
				t.Errorf("textarea %s = %q, want %q", attr, x.GetAttribute(attr), want)
			}
		}
		if x.HasAttribute("spellcheck") {
			t.Errorf("a string textarea has spellcheck=false")
		}
		if j := doc.QuerySelector(`textarea[name="$/j"]`); j == nil || j.GetAttribute("spellcheck") != "false" || j.HasAttribute("required") {
			t.Errorf("json textarea = %v", j)
		}
	})

	t.Run("select options", func(t *testing.T) {
		doc := render(t, `{"type":"object","required":["s"],"properties":{"s":{"enum":["a",{"k":1}]}}}`, `{"s":{"k":1}}`)
		sel := doc.QuerySelector(`select[name="$/s"]`)
		if sel == nil || !sel.HasAttribute("required") {
			t.Fatalf("select = %v", sel)
		}
		var got []string
		for o := range sel.QuerySelectorSequence("option") {
			got = append(got, o.GetAttribute("value")+"="+o.TextContent()+map[bool]string{true: "*"}[o.HasAttribute("selected")])
		}
		if want := []string{"=", `"a"=a`, `{"k":1}={"k":1}*`}; !slices.Equal(got, want) {
			t.Errorf("options = %q, want %q", got, want)
		}
	})

	t.Run("radio and checkbox groups", func(t *testing.T) {
		doc := render(t, `{"type":"object","required":["r"],"properties":{
			"r":{"enum":["a","b"],"x-form":{"widget":"radio"}},
			"c":{"type":"array","uniqueItems":true,"items":{"enum":["a","b"]}}}}`, `{"r":"b","c":["a"]}`)
		var radios []string
		for el := range doc.QuerySelectorSequence(`[data-pointer="/r"] .jsonform-option`) {
			in := el.QuerySelector("input")
			radios = append(radios, in.GetAttribute("type")+":"+in.GetAttribute("value")+":"+strings.TrimSpace(el.TextContent())+map[bool]string{true: ":checked"}[in.HasAttribute("checked")]+map[bool]string{true: ":required"}[in.HasAttribute("required")])
		}
		if want := []string{"radio:a:a:required", "radio:b:b:checked:required"}; !slices.Equal(radios, want) {
			t.Errorf("radios = %q, want %q", radios, want)
		}
		var boxes []string
		for el := range doc.QuerySelectorSequence(`[data-pointer="/c"] .jsonform-option input`) {
			boxes = append(boxes, el.GetAttribute("type")+":"+el.GetAttribute("value")+map[bool]string{true: ":checked"}[el.HasAttribute("checked")]+map[bool]string{true: ":required"}[el.HasAttribute("required")])
		}
		if want := []string{"checkbox:a:checked", "checkbox:b"}; !slices.Equal(boxes, want) {
			t.Errorf("checkboxes = %q, want %q", boxes, want)
		}
		var hidden []string
		for el := range doc.QuerySelectorSequence(`[data-pointer="/c"] input[type="hidden"]`) {
			hidden = append(hidden, el.GetAttribute("name")+"="+el.GetAttribute("value"))
		}
		if want := []string{"$/c=", "@present/c=1"}; !slices.Equal(hidden, want) {
			t.Errorf("hidden inputs = %q, want %q", hidden, want)
		}
		if doc.QuerySelector(`[data-pointer="/r"] input[type="hidden"]`) != nil {
			t.Errorf("a radio group has hidden inputs")
		}
		if legend := doc.QuerySelector(`[data-pointer="/c"] > legend`); legend == nil || legend.TextContent() != "c" {
			t.Errorf("group legend = %v", legend)
		}
	})

	t.Run("const and null", func(t *testing.T) {
		doc := render(t, `{"type":"object","required":["c","n"],"properties":{"c":{"const":{"a":1}},"n":{"type":"null"}}}`, "")
		for ptr, want := range map[string]string{"/c": `{"a":1}`, "/n": "null"} {
			out := doc.QuerySelector(`[data-pointer="` + ptr + `"] output`)
			label := doc.QuerySelector(`[data-pointer="` + ptr + `"] .jsonform-label`)
			if out == nil || out.TextContent() != want || label == nil || out.GetAttribute("aria-labelledby") != label.ID() || label.ID() == "" {
				t.Errorf("%s output = %v", ptr, out)
			}
			if want := "jsonform-" + strings.TrimPrefix(map[string]string{"/c": "const", "/n": "null"}[ptr], ""); out.GetAttribute("class") != want {
				t.Errorf("%s output class = %q, want %q", ptr, out.GetAttribute("class"), want)
			}
		}
	})

	t.Run("collapsed", func(t *testing.T) {
		doc := render(t, `{"type":"object","properties":{"old":{"type":"object","deprecated":true,"description":"Gone","x-form":{"class":"c"},"properties":{"a":{"type":"string"}}},"next":{"$ref":"#"}}}`, "")
		el := doc.QuerySelector(`[data-pointer="/old"]`)
		if el == nil || el.GetAttribute("data-kind") != "object" || !el.HasAttribute("data-collapsed") || !el.HasAttribute("data-deprecated") || el.GetAttribute("class") != "jsonform-field c" {
			t.Fatalf("collapsed field = %v", el)
		}
		label := el.QuerySelector(".jsonform-label")
		button := el.QuerySelector(".jsonform-expand")
		if label == nil || label.TextContent() != "old" || label.ID() == "" || button.GetAttribute("aria-describedby") != label.ID() {
			t.Errorf("collapsed label %v and button %v", label, button)
		}
		if p := el.QuerySelector(".jsonform-description"); p == nil || p.TextContent() != "Gone" {
			t.Errorf("collapsed description = %v", p)
		}
		if next := doc.QuerySelector(`[data-pointer="/next"]`); next.HasAttribute("data-deprecated") || next.QuerySelector(".jsonform-description") != nil {
			t.Errorf("collapsed field without flags = %s", next.OuterHTML())
		}
	})

	t.Run("remove button", func(t *testing.T) {
		doc := render(t, `{"type":"object","properties":{"next":{"$ref":"#","title":"Next"}}}`, `{"next":{}}`)
		b := doc.QuerySelector(`[data-pointer="/next"] > .jsonform-remove`)
		if b == nil || b.GetAttribute("value") != "remove/next" || b.TextContent() != "Remove Next" || !b.HasAttribute("formnovalidate") {
			t.Errorf("remove button = %v", b)
		}
	})

	t.Run("groups", func(t *testing.T) {
		doc := render(t, `{"type":"object","properties":{
			"a":{"type":"array","description":"Items","items":{"type":"string"}},
			"m":{"type":"object","additionalProperties":{"type":"string"}},
			"c":{"description":"Pick","oneOf":[{"type":"string"},{"type":"integer"}]}}}`, `{"a":["x","y"],"m":{"k":"v"},"c":3}`)
		for _, ptr := range []string{"/a", "/m", "/c"} {
			el := doc.QuerySelector(`[data-pointer="` + ptr + `"]`)
			if el == nil || el.ID() != "jsonform"+strings.ReplaceAll(ptr, "/", "-")+"_group" {
				t.Errorf("%s group id = %q", ptr, el.ID())
			}
		}
		if d := doc.QuerySelector(`[data-pointer="/a"] > .jsonform-description`); d == nil || d.TextContent() != "Items" {
			t.Errorf("array description = %v", d)
		}
		if n := len(selectAll(doc, `[data-pointer="/a"] .jsonform-item`)); n != 2 {
			t.Errorf("%d array items, want 2", n)
		}
		if add := doc.QuerySelector(`[data-pointer="/a"] > .jsonform-add`); add == nil || add.GetAttribute("value") != "add/a" || add.TextContent() != "Add item" {
			t.Errorf("array add button = %v", add)
		}
		if add := doc.QuerySelector(`[data-pointer="/m"] .jsonform-add`); add == nil || add.GetAttribute("value") != "add/m" || add.TextContent() != "Add property" {
			t.Errorf("map add button = %v", add)
		}
		up := doc.QuerySelector(`button[value="up/a/1"]`)
		if up == nil || up.GetAttribute("aria-label") != "Move a 2 up" {
			t.Errorf("up button = %v", up)
		}
		if down := doc.QuerySelector(`button[value="down/a/0"]`); down == nil || down.GetAttribute("aria-label") != "Move a 1 down" {
			t.Errorf("down button = %v", down)
		}
		key := doc.QuerySelector(`[data-pointer="/m"] .jsonform-key`)
		keyInput := key.QuerySelector("input")
		if label := key.QuerySelector("label"); label == nil || label.GetAttribute("for") != keyInput.ID() || keyInput.ID() == "" {
			t.Errorf("key label = %v", label)
		}
		if errs := key.QuerySelector("ul"); errs == nil || errs.ID() != keyInput.GetAttribute("aria-describedby") || !errs.HasAttribute("hidden") {
			t.Errorf("key errors = %v", errs)
		}
		if keyInput.HasAttribute("aria-invalid") {
			t.Errorf("a valid key is aria-invalid")
		}
		if d := doc.QuerySelector(`[data-pointer="/c"] > .jsonform-description`); d == nil || d.TextContent() != "Pick" {
			t.Errorf("choice description = %v", d)
		}
		sel := doc.QuerySelector(`[data-pointer="/c"] .jsonform-variant select`)
		if label := doc.QuerySelector(`[data-pointer="/c"] .jsonform-variant label`); sel == nil || label.GetAttribute("for") != sel.ID() || sel.GetAttribute("name") != "@variant/c" || sel.GetAttribute("hx-post") != "/form" {
			t.Errorf("variant picker = %v", sel)
		}
		if opt := sel.QuerySelector("option[selected]"); opt == nil || opt.GetAttribute("value") != "1" || opt.TextContent() != "integer" {
			t.Errorf("selected variant = %v", opt)
		}
	})

	t.Run("an empty array or map has no list", func(t *testing.T) {
		doc := render(t, `{"type":"object","properties":{"a":{"type":"array","items":{"type":"string"}},"m":{"type":"object","additionalProperties":{"type":"string"}}}}`, `{"a":[],"m":{}}`)
		if doc.QuerySelector(".jsonform-items") != nil {
			t.Errorf("an empty collection rendered a list")
		}
	})

	t.Run("key errors", func(t *testing.T) {
		h := newHandler(t, `{"type":"object","additionalProperties":{"type":"string"}}`)
		doc := fragment(t, postForm(t, h, url.Values{"@len": {"1"}, "@key/~k0": {""}, "$/~k0": {"v"}}))
		key := doc.QuerySelector(".jsonform-key input")
		errs := doc.QuerySelector(".jsonform-key ul")
		if key.GetAttribute("aria-invalid") != "true" || errs.HasAttribute("hidden") || errs.QuerySelector("li") == nil || errs.QuerySelector("li").TextContent() != messageKey {
			t.Errorf("key errors not shown: %s", doc.QuerySelector(".jsonform-key").OuterHTML())
		}
	})

	t.Run("picker", func(t *testing.T) {
		var props []string
		for _, p := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m"} {
			props = append(props, `"`+p+`":{"type":"string","title":"`+strings.ToUpper(p)+`"}`)
		}
		doc := render(t, `{"type":"object","properties":{`+strings.Join(props, ",")+`}}`, `{"a":"x"}`)
		pick := doc.QuerySelector(".jsonform-pick")
		sel := pick.QuerySelector("select")
		if label := pick.QuerySelector("label"); label == nil || label.GetAttribute("for") != sel.ID() || sel.ID() != "jsonform_root_pick" {
			t.Errorf("picker label = %v", label)
		}
		var options []string
		for o := range sel.QuerySelectorSequence("option") {
			options = append(options, o.GetAttribute("value")+"="+o.TextContent())
		}
		if len(options) != 13 || options[0] != "=" || options[1] != "b=B" {
			t.Errorf("options = %q", options)
		}
		button := pick.QuerySelector("button")
		if button.GetAttribute("value") != "pick" || button.GetAttribute("hx-post") != "/form" || button.GetAttribute("hx-trigger") != "click, change from:#jsonform_root_pick" {
			t.Errorf("picker button = %s", button.OuterHTML())
		}
	})

	t.Run("label marks", func(t *testing.T) {
		doc := render(t, `{"type":"object","required":["a"],"properties":{"a":{"type":"array","items":{"type":"string"}}}}`, `{"a":["x"]}`)
		if doc.QuerySelector(`[data-pointer="/a"] > legend abbr`) == nil {
			t.Errorf("required array has no mark")
		}
		if doc.QuerySelector(`[data-pointer="/a/0"] label abbr`) != nil {
			t.Errorf("an array item has a required mark")
		}
	})

	t.Run("the form", func(t *testing.T) {
		h := newHandler(t, `{"type":"object","properties":{"a":{"type":"string"}}}`)
		h.ID, h.SubmitLabel = "settings", "Save it"
		doc := fragment(t, getForm(t, h))
		form := doc.QuerySelector("form")
		if form.ID() != "settings" || form.HasAttribute("data-invalid") || form.HasAttribute("data-mode") {
			t.Errorf("form = %s", form.OuterHTML()[:120])
		}
		if b := doc.QuerySelector(".jsonform-submit"); b == nil || b.TextContent() != "Save it" {
			t.Errorf("submit button = %v", b)
		}
		if errs := doc.QuerySelector("#settings_errors"); errs == nil || !errs.HasAttribute("hidden") {
			t.Errorf("form errors list = %v", errs)
		}
		if doc.QuerySelector("#settings-a") == nil {
			t.Errorf("ids do not use the handler's ID")
		}

		h.MaxFields = 1
		doc = fragment(t, getForm(t, h))
		if doc.QuerySelector(".jsonform-toolbar") != nil || doc.QuerySelector(".jsonform-submit") != nil {
			t.Errorf("a form without fields has a toolbar or submit button")
		}
		if errs := doc.QuerySelector("#settings_errors"); errs.HasAttribute("hidden") {
			t.Errorf("form errors are hidden")
		}
	})

	t.Run("outline markup", func(t *testing.T) {
		h := newHandler(t, `{"type":"object","properties":{"a":{"type":"object","properties":{"b":{"type":"array","items":{"type":"number"}}}},"c":{"type":"object"}}}`)
		h.Outline = true
		doc := fragment(t, postForm(t, h, url.Values{"@present/a": {"1"}, "@len/a/b": {"1"}, "$/a/b/0": {"x"}, "@present/c": {"1"}, "@len/c": {"0"}}))
		var got []string
		for li := range doc.QuerySelectorSequence(".jsonform-outline li") {
			a := li.QuerySelector("a")
			got = append(got, a.GetAttribute("href")+" "+a.TextContent()+map[bool]string{true: " !"}[li.HasAttribute("data-invalid")])
		}
		want := []string{"#jsonform-a_group a !", "#jsonform-a-b_group b !", "#jsonform-c_group c"}
		if !slices.Equal(got, want) {
			t.Errorf("outline = %q, want %q", got, want)
		}
		if doc.QuerySelector(`.jsonform-outline li > ol > li > a[href="#jsonform-a-b_group"]`) == nil {
			t.Errorf("outline is not nested")
		}
	})

	t.Run("result markup", func(t *testing.T) {
		h := newHandler(t, `{"type":"object","properties":{"a":{"type":"string"}}}`)
		h.ID = "f"
		mux := http.NewServeMux()
		TemplateRoutes(mux, h, "/p")
		req := formRequest(url.Values{"$/a": {"x"}})
		req.URL.Path = "/p/form"
		rec := newRecorder()
		mux.ServeHTTP(rec, req)
		doc := fragment(t, rec.Result())
		result := doc.QuerySelector(".jsonform-result")
		if result == nil || result.ID() != "f" {
			t.Fatalf("result = %v", result)
		}
		edit := result.QuerySelector("form")
		if edit.ID() != "f_edit" || edit.GetAttribute("action") != "/p/form" || edit.GetAttribute("hx-post") != "/p/form" {
			t.Errorf("edit form = %s", edit.OuterHTML()[:150])
		}
		if out := result.QuerySelector("output"); out.GetAttribute("form") != "f_edit" {
			t.Errorf("output form = %q", out.GetAttribute("form"))
		}
		if result.QuerySelector(".jsonform-message") != nil {
			t.Errorf("message without one")
		}
	})

	t.Run("validation response", func(t *testing.T) {
		h := newHandler(t, `{"type":"object","properties":{"a":{"type":"string","minLength":3},"b":{"type":"string"}},"not":{"required":["b"]}}`)
		req := formRequest(url.Values{"$/a": {"x"}, "$/b": {"y"}})
		req.URL.Path = "/form/validate"
		res := serve(t, h, req)
		doc := fragment(t, res)
		formErrs := doc.QuerySelector("#jsonform_errors")
		if formErrs == nil || formErrs.GetAttribute("role") != "alert" {
			t.Fatalf("no form errors list")
		}
		aErrs := doc.QuerySelector("#jsonform-a_errors")
		if aErrs.HasAttribute("hidden") || aErrs.QuerySelector("li").TextContent() != "string length 1 less than minLength 3" {
			t.Errorf("a errors = %s", aErrs.OuterHTML())
		}
		if bErrs := doc.QuerySelector("#jsonform-b_errors"); !bErrs.HasAttribute("hidden") || bErrs.QuerySelector("li") != nil {
			t.Errorf("b errors = %s", bErrs.OuterHTML())
		}
		root := doc.QuerySelector("#jsonform_root_errors")
		if root == nil || root.HasAttribute("hidden") || root.QuerySelector("li") == nil {
			t.Errorf("root errors = %v", root)
		}

		res = serve(t, &Handler{}, req)
		if el := fragment(t, res).QuerySelector(".jsonform-failure"); el == nil || el.TextContent() != errNoSchema.Error() {
			t.Errorf("validate without a schema = %v", el)
		}
		if got := fieldErrors(mustValidate(t, h, url.Values{"$/a": {"long enough"}}).Root); len(got) != 0 {
			t.Errorf("errors on a valid form: %v", got)
		}
	})

	t.Run("route error message", func(t *testing.T) {
		res := serve(t, &Handler{}, formRequest(nil))
		if el := fragment(t, res).QuerySelector(".jsonform-failure"); el == nil || el.TextContent() != errNoSchema.Error() {
			t.Errorf("failure = %v", el)
		}
	})
}

func mustValidate(t *testing.T, h *Handler, form url.Values) *Form {
	t.Helper()
	f, err := h.Validate(context.Background(), form)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

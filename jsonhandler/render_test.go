package jsonhandler

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/typelate/dom/domtest"
	"github.com/typelate/dom/spec"
	"golang.org/x/net/html/atom"
)

func render(t *testing.T, schema, value string) spec.DocumentFragment {
	t.Helper()
	h := newHandler(t, schema)
	if value != "" {
		h.Value = jsontext.Value(value)
	}
	return fragment(t, getForm(t, h))
}

// TestRender checks the attributes each keyword and annotation turns into.
// Each case names an element by selector and the attributes it must have;
// an empty want value means the attribute must be absent.
func TestRender(t *testing.T) {
	for _, tc := range []struct {
		name, schema, value string
		selector            string
		want                map[string]string
	}{
		{
			name:     "string constraints",
			schema:   `{"type":"object","required":["s"],"properties":{"s":{"type":"string","minLength":2,"maxLength":5,"pattern":"^a"}}}`,
			selector: `[name="$/s"]`,
			want:     map[string]string{"type": "text", "minlength": "2", "maxlength": "5", "pattern": ".*(?:^a).*", "required": ""},
		},
		{
			name:     "zero minLength is left out",
			schema:   `{"type":"string","minLength":0}`,
			selector: `[name="$"]`,
			want:     map[string]string{"minlength": "-"},
		},
		{
			name:     "pattern is left to the validator when there is a format",
			schema:   `{"type":"string","format":"email","pattern":"x"}`,
			selector: `[name="$"]`,
			want:     map[string]string{"type": "email", "pattern": "-"},
		},
		{
			name:     "integer limits",
			schema:   `{"type":"integer","exclusiveMinimum":1.5,"exclusiveMaximum":10}`,
			selector: `[name="$"]`,
			want:     map[string]string{"type": "number", "min": "2", "max": "9", "step": "1"},
		},
		{
			name:     "minimum wins over exclusiveMinimum",
			schema:   `{"type":"integer","minimum":0,"exclusiveMinimum":5,"maximum":3,"exclusiveMaximum":1}`,
			selector: `[name="$"]`,
			want:     map[string]string{"min": "0", "max": "3"},
		},
		{
			name:     "number step",
			schema:   `{"type":"number"}`,
			selector: `[name="$"]`,
			want:     map[string]string{"step": "any", "min": "-", "max": "-"},
		},
		{
			name:     "multipleOf becomes step",
			schema:   `{"type":"number","minimum":10,"multipleOf":2.5}`,
			selector: `[name="$"]`,
			want:     map[string]string{"step": "2.5", "min": "10"},
		},
		{
			name:     "multipleOf not aligned with min",
			schema:   `{"type":"number","minimum":1,"multipleOf":2}`,
			selector: `[name="$"]`,
			want:     map[string]string{"step": "any"},
		},
		{
			name:     "fractional multipleOf of an integer",
			schema:   `{"type":"integer","multipleOf":0.5}`,
			selector: `[name="$"]`,
			want:     map[string]string{"step": "1"},
		},
		{
			name:     "integer multipleOf",
			schema:   `{"type":"integer","multipleOf":5}`,
			selector: `[name="$"]`,
			want:     map[string]string{"step": "5"},
		},
		{
			name:     "date",
			schema:   `{"type":"string","format":"date"}`,
			selector: `[name="$"]`,
			want:     map[string]string{"type": "date"},
		},
		{
			name:     "uri",
			schema:   `{"type":"string","format":"uri"}`,
			selector: `[name="$"]`,
			want:     map[string]string{"type": "url"},
		},
		{
			name:     "time",
			schema:   `{"type":"string","format":"time"}`,
			value:    `"10:11:12+02:00"`,
			selector: `[name="$"]`,
			want:     map[string]string{"type": "time", "value": "08:11:12"},
		},
		{
			name:     "date-time",
			schema:   `{"type":"string","format":"date-time"}`,
			value:    `"2024-01-02T03:04:05-01:00"`,
			selector: `[name="$"]`,
			want:     map[string]string{"type": "datetime-local", "value": "2024-01-02T04:04:05"},
		},
		{
			name:     "unparsable date-time is shown as is",
			schema:   `{"type":"string","format":"date-time"}`,
			value:    `"soon"`,
			selector: `[name="$"]`,
			want:     map[string]string{"value": "soon"},
		},
		{
			name:     "write-only",
			schema:   `{"type":"string","writeOnly":true}`,
			selector: `[name="$"]`,
			want:     map[string]string{"type": "password", "autocomplete": "new-password"},
		},
		{
			name:     "widget",
			schema:   `{"type":"string","x-form":{"widget":"color"}}`,
			selector: `[name="$"]`,
			want:     map[string]string{"type": "color"},
		},
		{
			name:     "range widget on a number",
			schema:   `{"type":"number","x-form":{"widget":"range"}}`,
			selector: `[name="$"]`,
			want:     map[string]string{"type": "range"},
		},
		{
			name:     "unknown widget",
			schema:   `{"type":"string","x-form":{"widget":"spinner"}}`,
			selector: `[name="$"]`,
			want:     map[string]string{"type": "text"},
		},
		{
			name:     "read-only",
			schema:   `{"type":"string","readOnly":true}`,
			selector: `[name="$"]`,
			want:     map[string]string{"readonly": ""},
		},
		{
			name:     "placeholder from x-form",
			schema:   `{"type":"string","examples":["ex"],"x-form":{"placeholder":"type here"}}`,
			selector: `[name="$"]`,
			want:     map[string]string{"placeholder": "type here"},
		},
		{
			name:     "placeholder from examples",
			schema:   `{"type":"string","examples":["ex"]}`,
			selector: `[name="$"]`,
			want:     map[string]string{"placeholder": "ex"},
		},
		{
			name:     "required default is the value",
			schema:   `{"type":"object","required":["n"],"properties":{"n":{"type":"integer","default":3}}}`,
			selector: `[name="$/n"]`,
			want:     map[string]string{"value": "3", "placeholder": "-"},
		},
		{
			name:     "optional default is the placeholder",
			schema:   `{"type":"object","properties":{"n":{"type":"integer","default":3}}}`,
			selector: `[name="$/n"]`,
			want:     map[string]string{"value": "", "placeholder": "3"},
		},
		{
			name:     "optional string default is the placeholder",
			schema:   `{"type":"object","properties":{"s":{"type":"string","default":"hi"}}}`,
			selector: `[name="$/s"]`,
			want:     map[string]string{"placeholder": "hi"},
		},
		{
			name:     "required JSON default is the value",
			schema:   `{"type":"object","required":["v"],"properties":{"v":{"default":[1]}}}`,
			selector: `textarea[name="$/v"]`,
			want:     map[string]string{"spellcheck": "false", "rows": "3"},
		},
		{
			name:     "textarea rows",
			schema:   `{"type":"string","x-form":{"widget":"textarea","rows":7}}`,
			selector: `textarea[name="$"]`,
			want:     map[string]string{"rows": "7"},
		},
		{
			name:     "required only when every enclosing value is",
			schema:   `{"type":"object","properties":{"a":{"type":"object","required":["s"],"properties":{"s":{"type":"string"}}}}}`,
			selector: `[name="$/a/s"]`,
			want:     map[string]string{"required": "-"},
		},
		{
			name:     "class annotation",
			schema:   `{"type":"string","x-form":{"class":"wide accent"}}`,
			selector: `.jsonform-field.wide.accent`,
			want:     map[string]string{"data-kind": "text"},
		},
		{
			name:     "deprecated",
			schema:   `{"type":"object","properties":{"old":{"type":"string","deprecated":true}}}`,
			value:    `{"old":"x"}`,
			selector: `[data-pointer="/old"]`,
			want:     map[string]string{"data-deprecated": ""},
		},
		{
			name:     "property names of a map",
			schema:   `{"type":"object","propertyNames":{"pattern":"^[a-z]+$","minLength":2,"maxLength":8},"additionalProperties":{"type":"string"}}`,
			value:    `{"ab":"x"}`,
			selector: `[name="@key/~k0"]`,
			want:     map[string]string{"pattern": ".*(?:^[a-z]+$).*", "minlength": "2", "maxlength": "8", "value": "ab", "required": ""},
		},
		{
			name:     "read-only checkbox",
			schema:   `{"type":"boolean","readOnly":true}`,
			value:    `true`,
			selector: `input[type="hidden"][name="$"]`,
			want:     map[string]string{"value": "true"},
		},
		{
			name:     "read-only select",
			schema:   `{"enum":["a","b"],"readOnly":true}`,
			value:    `"b"`,
			selector: `input[type="hidden"][name="$"]`,
			want:     map[string]string{"value": "b"},
		},
		{
			name:     "list widget keeps an array of enum values a list",
			schema:   `{"type":"array","uniqueItems":true,"items":{"enum":["a","b"]},"x-form":{"widget":"list"}}`,
			selector: `[data-pointer=""]`,
			want:     map[string]string{"data-kind": "array"},
		},
		{
			name:     "json widget",
			schema:   `{"type":"object","properties":{"a":{"type":"string"}},"x-form":{"widget":"json"}}`,
			selector: `[data-pointer=""]`,
			want:     map[string]string{"data-kind": "json"},
		},
		{
			name:     "boolean select widget",
			schema:   `{"type":"boolean","x-form":{"widget":"select"}}`,
			selector: `[data-pointer=""]`,
			want:     map[string]string{"data-kind": "select"},
		},
		{
			name:     "boolean radio widget",
			schema:   `{"type":"boolean","x-form":{"widget":"radio"}}`,
			selector: `[data-pointer=""]`,
			want:     map[string]string{"data-kind": "radio"},
		},
		{
			name:     "nullable boolean",
			schema:   `{"type":["boolean","null"]}`,
			selector: `[data-pointer=""]`,
			want:     map[string]string{"data-kind": "choice"},
		},
		{
			name:     "nullable array",
			schema:   `{"type":["array","null"]}`,
			selector: `[data-pointer=""]`,
			want:     map[string]string{"data-kind": "choice"},
		},
		{
			name:     "null",
			schema:   `{"type":"null"}`,
			selector: `[data-pointer=""]`,
			want:     map[string]string{"data-kind": "null"},
		},
		{
			name:     "no types left",
			schema:   `{"allOf":[{"type":"string"},{"type":"number"}]}`,
			selector: `[data-pointer=""]`,
			want:     map[string]string{"data-kind": "json"},
		},
		{
			name:     "integer and number",
			schema:   `{"allOf":[{"type":["number","string"]},{"type":"integer"}]}`,
			selector: `[name="$"]`,
			want:     map[string]string{"step": "1"},
		},
		{
			name:     "inferred object",
			schema:   `{"required":["a"]}`,
			selector: `[data-pointer=""]`,
			want:     map[string]string{"data-kind": "map"},
		},
		{
			name:     "inferred array",
			schema:   `{"minItems":1}`,
			selector: `[data-pointer=""]`,
			want:     map[string]string{"data-kind": "array"},
		},
		{
			name:     "inferred string",
			schema:   `{"maxLength":3}`,
			selector: `[data-pointer=""]`,
			want:     map[string]string{"data-kind": "text"},
		},
		{
			name:     "inferred number",
			schema:   `{"multipleOf":3}`,
			selector: `[data-pointer=""]`,
			want:     map[string]string{"data-kind": "number"},
		},
		{
			name:     "items false after a tuple",
			schema:   `{"type":"array","prefixItems":[{"type":"string"}],"items":false}`,
			value:    `["a"]`,
			selector: `.jsonform-add`,
			want:     map[string]string{"value": "-"},
		},
		{
			name:     "maxItems reached",
			schema:   `{"type":"array","items":{"type":"string"},"maxItems":1}`,
			value:    `["a"]`,
			selector: `[name="@len"]`,
			want:     map[string]string{"value": "1"},
		},
		{
			name:     "required array starts with minItems items",
			schema:   `{"type":"array","items":{"type":"string"},"minItems":2}`,
			selector: `[name="@len"]`,
			want:     map[string]string{"value": "2"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := render(t, tc.schema, tc.value)
			el := doc.QuerySelector(tc.selector)
			if el == nil {
				if tc.want["value"] == "-" {
					return
				}
				t.Fatalf("no %s in\n%s", tc.selector, outerHTML(doc))
			}
			for attr, want := range tc.want {
				switch {
				case want == "-":
					if el.HasAttribute(attr) {
						t.Errorf("%s has %s=%q, want none", tc.selector, attr, el.GetAttribute(attr))
					}
				case !el.HasAttribute(attr):
					t.Errorf("%s has no %s, want %q", tc.selector, attr, want)
				case el.GetAttribute(attr) != want:
					t.Errorf("%s %s = %q, want %q", tc.selector, attr, el.GetAttribute(attr), want)
				}
			}
		})
	}
}

func outerHTML(doc spec.DocumentFragment) string {
	var sb strings.Builder
	for el := range doc.QuerySelectorSequence("form") {
		sb.WriteString(el.OuterHTML())
	}
	return sb.String()
}

func TestRender_labels(t *testing.T) {
	for _, tc := range []struct {
		name, schema, value string
		want                []string // the text of every .jsonform-label, in order
	}{
		{
			name:   "property name, own title, x-form label, inherited title",
			schema: `{"$defs":{"d":{"title":"Shared","type":"string"}},"type":"object","properties":{"a":{"type":"string"},"b":{"title":"Bee","type":"string"},"c":{"type":"string","x-form":{"label":"See"}},"d":{"$ref":"#/$defs/d"}}}`,
			want:   []string{"a", "Bee", "See", "d"},
		},
		{
			name:   "x-form order",
			schema: `{"type":"object","x-form":{"order":["c","nope","a","c"]},"properties":{"a":{"type":"string"},"b":{"type":"string"},"c":{"type":"string"}}}`,
			want:   []string{"c", "a", "b"},
		},
		{
			name:   "allOf merges a property declared twice",
			schema: `{"allOf":[{"type":"object","properties":{"a":{"type":"string"}}},{"properties":{"a":{"title":"Ay"}}}]}`,
			want:   []string{"Ay"},
		},
		{
			name:   "deprecated properties go last",
			schema: `{"type":"object","properties":{"old":{"type":"string","deprecated":true},"new":{"type":"string"}}}`,
			value:  `{"old":"x","new":"y"}`,
			want:   []string{"new", "old"},
		},
		{
			name:   "variant labels",
			schema: `{"$defs":{"pt":{"type":"object"}},"type":"object","required":["v"],"properties":{"v":{"oneOf":[{"title":"T"},{"x-form":{"label":"L"}},{"$ref":"#/$defs/pt"},{"const":3},{"type":"string"},{"minItems":1},{}]}}}`,
			want:   []string{"v", "Variant", "T"},
		},
		{
			name:   "items named by a referenced title",
			schema: `{"$defs":{"s":{"title":"Step","type":"string"}},"type":"object","properties":{"steps":{"type":"array","items":{"$ref":"#/$defs/s"}}}}`,
			value:  `{"steps":["a","b"]}`,
			want:   []string{"steps", "Step 1", "Step 2"},
		},
		{
			name:   "items and entries",
			schema: `{"type":"object","properties":{"list":{"type":"array","items":{"type":"string"}},"tagged":{"type":"array","items":{"title":"Tag","type":"string"}},"m":{"type":"object","additionalProperties":{"title":"Ignored","type":"string"}}}}`,
			value:  `{"list":["x"],"tagged":["y"],"m":{"k":"z"}}`,
			want:   []string{"list", "list 1", "tagged", "Tag 1", "m", "Property name", "k"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := render(t, tc.schema, tc.value)
			var got []string
			for el := range doc.QuerySelectorSequence(".jsonform-body .jsonform-label") {
				got = append(got, strings.TrimSuffix(strings.TrimSpace(el.TextContent()), " *"))
			}
			if tc.name != "variant labels" && len(got) > 0 && got[0] == "" {
				got = got[1:]
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("labels = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("every variant label", func(t *testing.T) {
		doc := render(t, `{"$defs":{"pt":{"type":"object"}},"oneOf":[{"title":"T"},{"x-form":{"label":"L"}},{"$ref":"#/$defs/pt"},{"const":3},{"type":"string"},{"minItems":1},{}]}`, "")
		var got []string
		for el := range doc.QuerySelectorSequence(".jsonform-variant option") {
			got = append(got, el.TextContent())
		}
		want := []string{"T", "L", "pt", "3", "string", "array", "Option 7"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("variants = %q, want %q", got, want)
		}
	})
}

func TestRender_structure(t *testing.T) {
	t.Run("collapsed values", func(t *testing.T) {
		doc := render(t, `{"type":"object","properties":{"next":{"$ref":"#"},"old":{"type":"string","deprecated":true},"opt":{"type":"object","x-form":{"collapsed":true},"properties":{"a":{"type":"string"}}},"open":{"$ref":"#","x-form":{"collapsed":false}}}}`, "")
		for _, ptr := range []string{"/next", "/old", "/opt"} {
			el := doc.QuerySelector(`[data-pointer="` + ptr + `"]`)
			if el == nil || !el.HasAttribute("data-collapsed") {
				t.Errorf("%s is not collapsed", ptr)
				continue
			}
			if b := el.QuerySelector(".jsonform-expand"); b == nil || b.GetAttribute("value") != "expand"+ptr {
				t.Errorf("%s has no expand button", ptr)
			}
		}
		if el := doc.QuerySelector(`[data-pointer="/open"]`); el == nil || el.HasAttribute("data-collapsed") {
			t.Errorf("/open is collapsed despite collapsed:false")
		}
	})

	t.Run("description is not repeated", func(t *testing.T) {
		doc := render(t, `{"description":"Node","type":"object","properties":{"child":{"$ref":"#"}}}`, `{"child":{}}`)
		if n := len(selectAll(doc, ".jsonform-description")); n != 1 {
			t.Errorf("%d descriptions, want 1", n)
		}
	})

	t.Run("outline", func(t *testing.T) {
		h := newHandler(t, `{"type":"object","properties":{"a":{"type":"object","properties":{"n":{"type":"number"},"b":{"type":"array","items":{"type":"string"}}}},"c":{"oneOf":[{"type":"object","properties":{"d":{"type":"object","properties":{"x":{"type":"string"}}}}},{"type":"string"}]},"m":{"type":"object","additionalProperties":{"type":"object","properties":{"y":{"type":"string"}}}},"s":{"type":"string"},"r":{"$ref":"#"}}}`)
		h.Outline = true
		h.Value = jsontext.Value(`{"a":{"b":[]},"m":{"k":{}}}`)
		f, err := h.SubmitForm(context.Background(), url.Values{"$/a/n": {"x"}, "@len/m": {"1"}, "@key/m/~k0": {"k"}, "@present/a": {"1"}})
		if err != nil {
			t.Fatal(err)
		}
		want := []OutlineItem{
			{Label: "a", Href: "#jsonform-a_group", Path: "/a", Invalid: true, Items: []OutlineItem{{Label: "b", Href: "#jsonform-a-b_group", Path: "/a/b"}}},
			{Label: "c", Href: "#jsonform-c_group", Path: "/c", Note: "object", Items: []OutlineItem{{Label: "d", Href: "#jsonform-c-d_group", Path: "/c/d"}}},
			{Label: "m", Href: "#jsonform-m_group", Path: "/m", Items: []OutlineItem{{Label: "k", Href: "#jsonform-m-_7ek0_group", Path: "/m/~k0"}}},
		}
		if got := f.Outline(); !reflect.DeepEqual(got, want) {
			t.Errorf("outline =\n%+v\nwant\n%+v", got, want)
		}
		var buf bytes.Buffer
		if err := templates.ExecuteTemplate(&buf, "jsonhandler-form", f.WithActions("/f", "")); err != nil {
			t.Fatal(err)
		}
		doc := domtest.ParseReaderDocumentFragment(t, &buf, atom.Div)
		if el := doc.QuerySelector(`.jsonform-outline li[data-invalid] > a[href="#jsonform-a_group"]`); el == nil {
			t.Errorf("outline does not mark the invalid group")
		}
		if doc.QuerySelector(`#jsonform-a_group`) == nil {
			t.Errorf("outline target is missing")
		}
		if doc.QuerySelector(`.jsonform-live`) != nil {
			t.Errorf("live validation rendered without a validate action")
		}
		if (&Form{}).Outline() != nil {
			t.Errorf("a form without fields has an outline")
		}
	})

	t.Run("no outline unless asked", func(t *testing.T) {
		doc := render(t, `{"type":"object","properties":{"a":{"type":"object"}}}`, "")
		if doc.QuerySelector(".jsonform-outline") != nil {
			t.Errorf("outline rendered")
		}
	})

	t.Run("property order follows the document", func(t *testing.T) {
		doc := render(t, `{"type":"object","properties":{"zeta":{"type":"string"},"alpha":{"type":"string"},"mid":{"type":"string"}}}`, "")
		var got []string
		for el := range doc.QuerySelectorSequence("[data-pointer]") {
			if p := el.GetAttribute("data-pointer"); p != "" {
				got = append(got, p)
			}
		}
		if want := []string{"/zeta", "/alpha", "/mid"}; !reflect.DeepEqual(got, want) {
			t.Errorf("order = %q, want %q", got, want)
		}
	})

	t.Run("Field.FormAction without a form", func(t *testing.T) {
		if got := (&Field{}).FormAction(); got != "" {
			t.Errorf("FormAction() = %q", got)
		}
	})
}

func selectAll(doc spec.DocumentFragment, selector string) []spec.Element {
	var out []spec.Element
	for el := range doc.QuerySelectorSequence(selector) {
		out = append(out, el)
	}
	return out
}

func TestAssets(t *testing.T) {
	css := Stylesheet()
	for _, class := range []string{".jsonform-field", ".jsonform-outline", ".jsonform-errors", "[data-kind="} {
		if !bytes.Contains(css, []byte(class)) {
			t.Errorf("stylesheet does not mention %s", class)
		}
	}
	css[0] = 'X'
	if Stylesheet()[0] == 'X' {
		t.Errorf("Stylesheet returned shared bytes")
	}

	meta := MetaSchema()
	h, err := New(context.Background(), meta)
	if err != nil {
		t.Fatal(err)
	}
	// The annotated meta-schema validates schemas that use x-form, and
	// rejects annotations it does not know.
	for _, tc := range []struct {
		schema string
		valid  bool
	}{
		{`{"type":"string","x-form":{"widget":"textarea","rows":3,"collapsed":false}}`, true},
		{`{"properties":{"a":{"x-form":{"label":"A"}}}}`, true},
		{`{"x-form":{"wat":1}}`, false},
		{`{"x-form":{"widget":"spinner"}}`, false},
	} {
		if out := h.Schema.Validate("schema", []byte(tc.schema)); out.Valid != tc.valid {
			t.Errorf("Validate(%s).Valid = %v, want %v", tc.schema, out.Valid, tc.valid)
		}
	}
	meta[0] = 'X'
	if MetaSchema()[0] == 'X' {
		t.Errorf("MetaSchema returned shared bytes")
	}
}

func TestTemplates(t *testing.T) {
	ts, err := Templates()
	if err != nil {
		t.Fatal(err)
	}
	page, err := ts.New("page").Parse(`<main>{{template "jsonhandler-form" .}}</main>`)
	if err != nil {
		t.Fatal(err)
	}
	h := newHandler(t, `{"type":"object","properties":{"a":{"type":"string"}}}`)
	f, err := h.Form(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := page.Execute(&buf, f.WithActions("/host/form", "/host/form/validate")); err != nil {
		t.Fatal(err)
	}
	doc := domtest.ParseReaderDocumentFragment(t, &buf, atom.Body)
	if form := doc.QuerySelector("main > form.jsonform"); form == nil || form.GetAttribute("action") != "/host/form" {
		t.Errorf("host page does not contain the form")
	}
	if live := doc.QuerySelector(".jsonform-live"); live == nil || live.GetAttribute("hx-post") != "/host/form/validate" {
		t.Errorf("no live validation")
	}
	// The copy is independent of the package's templates.
	if templates.Lookup("page") != nil {
		t.Errorf("Templates returned the package's own set")
	}
}

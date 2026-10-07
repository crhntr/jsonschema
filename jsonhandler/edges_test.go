package jsonhandler

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/crhntr/jsonschema"
)

// These tests pin down boundaries that mutation testing found unchecked.

func TestDepthLimit(t *testing.T) {
	// A value nested deeper than maxDepth renders its deepest part as a
	// JSON text area, and still round-trips.
	const schema = `{"$defs":{"n":{"type":"object","properties":{"v":{"type":"integer"},"next":{"$ref":"#/$defs/n"}}}},"$ref":"#/$defs/n"}`
	var value strings.Builder
	for i := range maxDepth + 3 {
		fmt.Fprintf(&value, `{"v":%d,"next":`, i)
	}
	value.WriteString(`{"v":-1}`)
	value.WriteString(strings.Repeat("}", maxDepth+3))

	h := newHandler(t, schema)
	h.Value = jsontext.Value(value.String())
	doc := fragment(t, getForm(t, h))
	var kinds []string
	for el := range doc.QuerySelectorSequence(`[data-kind="json"]`) {
		kinds = append(kinds, el.GetAttribute("data-pointer"))
	}
	// The object at depth maxDepth renders; its children are past it.
	deep := strings.Repeat("/next", maxDepth)
	if want := []string{deep + "/v", deep + "/next"}; !slices.Equal(kinds, want) {
		t.Errorf("JSON fields at %q, want %q", kinds, want)
	}
	got := submit(t, h, formState(t, getForm(t, h)))
	assertJSONEqual(t, got, value.String())
}

func TestItemButtons(t *testing.T) {
	doc := render(t, `{"type":"object","properties":{"a":{"type":"array","items":{"type":"string"}},"m":{"type":"object","additionalProperties":{"type":"string"}}}}`,
		`{"a":["x","y","z"],"m":{"p":"1","q":"2"}}`)
	for _, tc := range []struct {
		value    string
		disabled bool
	}{
		{"up/a/0", true},
		{"down/a/0", false},
		{"up/a/1", false},
		{"down/a/1", false},
		{"up/a/2", false},
		{"down/a/2", true},
		{"up/m/~k0", true},
		{"down/m/~k0", false},
		{"up/m/~k1", false},
		{"down/m/~k1", true},
	} {
		b := doc.QuerySelector(`button[value="` + tc.value + `"]`)
		if b == nil {
			t.Errorf("no %s button", tc.value)
			continue
		}
		if b.HasAttribute("disabled") != tc.disabled {
			t.Errorf("%s disabled = %v, want %v", tc.value, b.HasAttribute("disabled"), tc.disabled)
		}
	}
	if b := doc.QuerySelector(`button[value="remove/a/1"]`); b == nil || b.GetAttribute("aria-label") != "Remove a 2" {
		t.Errorf("remove button for the second item = %v", b)
	}
}

func TestAddButtons(t *testing.T) {
	for _, tc := range []struct {
		name, schema, value string
		canAdd              bool
	}{
		{"array below maxItems", `{"type":"array","items":{"type":"string"},"maxItems":2}`, `["a"]`, true},
		{"array at maxItems", `{"type":"array","items":{"type":"string"},"maxItems":1}`, `["a"]`, false},
		{"array without a limit", `{"type":"array","items":{"type":"string"}}`, `[]`, true},
		{"tuple with room", `{"type":"array","prefixItems":[{"type":"string"},{"type":"string"}],"items":false}`, `["a"]`, true},
		{"full tuple", `{"type":"array","prefixItems":[{"type":"string"}],"items":false}`, `["a"]`, false},
		{"open tuple", `{"type":"array","prefixItems":[{"type":"string"}]}`, `["a"]`, true},
		{"map below maxProperties", `{"type":"object","additionalProperties":{"type":"string"},"maxProperties":2}`, `{"a":"1"}`, true},
		{"map at maxProperties", `{"type":"object","additionalProperties":{"type":"string"},"maxProperties":1}`, `{"a":"1"}`, false},
		{"closed object with extras", `{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":false}`, `{"a":"x","b":"y"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := render(t, tc.schema, tc.value)
			if got := doc.QuerySelector(`button[value^="add"]`) != nil; got != tc.canAdd {
				t.Errorf("add button = %v, want %v", got, tc.canAdd)
			}
		})
	}
}

func TestAddAtMaxItems(t *testing.T) {
	s := state{}
	s[metaName(metaLen, "")] = []string{fmt.Sprint(maxItems)}
	applyAction(s, "add")
	if n, _ := s.count(""); n != maxItems {
		t.Errorf("count after add at the limit = %d, want %d", n, maxItems)
	}
	s[metaName(metaLen, "")] = []string{fmt.Sprint(maxItems - 1)}
	applyAction(s, "add")
	if n, _ := s.count(""); n != maxItems {
		t.Errorf("count after add below the limit = %d, want %d", n, maxItems)
	}
}

func TestEnumBlankOption(t *testing.T) {
	for _, tc := range []struct {
		name, schema string
		blank        bool
	}{
		{"required select", `{"type":"object","required":["e"],"properties":{"e":{"enum":["a","b"]}}}`, true},
		{"optional select", `{"type":"object","properties":{"e":{"enum":["a","b"]}}}`, true},
		{"required radio", `{"type":"object","required":["e"],"properties":{"e":{"enum":["a","b"],"x-form":{"widget":"radio"}}}}`, false},
		{"optional radio", `{"type":"object","properties":{"e":{"enum":["a","b"],"x-form":{"widget":"radio"}}}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := render(t, tc.schema, "")
			blank := doc.QuerySelector(`option[value=""], input[type="radio"][value=""]`) != nil
			if !blank {
				for el := range doc.QuerySelectorSequence("option, input") {
					if el.HasAttribute("value") && el.GetAttribute("value") == "" && (el.TagName() == "OPTION" || el.GetAttribute("type") == "radio") {
						blank = true
					}
				}
			}
			if blank != tc.blank {
				t.Errorf("blank option = %v, want %v", blank, tc.blank)
			}
		})
	}
}

func TestCheckboxesPresence(t *testing.T) {
	const schema = `{"type":"object","properties":{"f":{"type":"array","uniqueItems":true,"items":{"enum":["a","b"]}}}}`
	for _, tc := range []struct {
		name string
		form url.Values
		want string
	}{
		{"none checked, optional", url.Values{"$/f": {""}}, `{}`},
		{"none checked, added", url.Values{"$/f": {""}, "@present/f": {"1"}}, `{"f":[]}`},
		{"checked", url.Values{"$/f": {"", "b"}}, `{"f":["b"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertJSONEqual(t, submit(t, newHandler(t, schema), tc.form), tc.want)
		})
	}

	t.Run("an object with only an empty group is left out", func(t *testing.T) {
		h := newHandler(t, `{"type":"object","properties":{"o":{"type":"object","properties":{"f":{"type":"array","uniqueItems":true,"items":{"enum":["a"]}}}}}}`)
		assertJSONEqual(t, submit(t, h, url.Values{"$/o/f": {""}, "@present/o/f": {"1"}}), `{}`)
		assertJSONEqual(t, submit(t, h, url.Values{"$/o/f": {"", "a"}}), `{"o":{"f":["a"]}}`)
	})
}

func TestNullableEnum(t *testing.T) {
	h := newHandler(t, `{"type":"object","required":["e"],"properties":{"e":{"type":["string","null"],"enum":["a",null]}}}`)
	for _, v := range []string{`{"e":null}`, `{"e":"a"}`} {
		h.Value = jsontext.Value(v)
		assertJSONEqual(t, submit(t, h, formState(t, getForm(t, h))), v)
	}
}

func TestFoldThreshold(t *testing.T) {
	schema := func(n int) string {
		var props []string
		for i := range n {
			props = append(props, fmt.Sprintf(`"p%d":{"type":"string"}`, i))
		}
		return `{"type":"object","properties":{` + strings.Join(props, ",") + `}}`
	}
	if doc := render(t, schema(foldThreshold), ""); doc.QuerySelector(".jsonform-pick") != nil || doc.QuerySelector(`[data-pointer="/p0"]`) == nil {
		t.Errorf("an object with %d properties is folded", foldThreshold)
	}
	if doc := render(t, schema(foldThreshold+1), ""); doc.QuerySelector(".jsonform-pick") == nil || doc.QuerySelector(`[data-pointer="/p0"]`) != nil {
		t.Errorf("an object with %d properties is not folded", foldThreshold+1)
	}
	if doc := render(t, schema(foldThreshold+1), `{"p0":"x","p1":"y"}`); doc.QuerySelector(`[data-pointer="/p0"] .jsonform-remove`) == nil {
		t.Errorf("a property with a value in a folded object cannot be removed")
	}

	t.Run("a collapsible value that was added can be removed", func(t *testing.T) {
		doc := render(t, `{"type":"object","properties":{"next":{"$ref":"#"},"s":{"type":"string"}}}`, `{"next":{},"s":"x"}`)
		if doc.QuerySelector(`[data-pointer="/next"] > .jsonform-remove`) == nil {
			t.Errorf("an added recursive value has no remove button")
		}
		if doc.QuerySelector(`[data-pointer="/s"] .jsonform-remove`) != nil {
			t.Errorf("a plain optional value has a remove button")
		}
	})

	t.Run("no picker without absent properties", func(t *testing.T) {
		var props, values []string
		for i := range foldThreshold + 1 {
			props = append(props, fmt.Sprintf(`"p%d":{"type":"string"}`, i))
			values = append(values, fmt.Sprintf(`"p%d":"v"`, i))
		}
		doc := render(t, `{"type":"object","properties":{`+strings.Join(props, ",")+`}}`, `{`+strings.Join(values, ",")+`}`)
		if doc.QuerySelector(".jsonform-pick") != nil {
			t.Errorf("picker shown with nothing to pick")
		}
	})
}

func TestEntriesSection(t *testing.T) {
	for _, tc := range []struct {
		name, schema, value string
		entries             bool
	}{
		{"declared properties only", `{"type":"object","properties":{"a":{"type":"string"}}}`, `{"a":"x"}`, false},
		{"undeclared value present", `{"type":"object","properties":{"a":{"type":"string"}}}`, `{"z":1}`, true},
		{"additionalProperties schema", `{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":{"type":"string"}}`, `{}`, true},
		{"additionalProperties false", `{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":false}`, `{}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := render(t, tc.schema, tc.value).QuerySelector(".jsonform-entries") != nil; got != tc.entries {
				t.Errorf("entries section = %v, want %v", got, tc.entries)
			}
		})
	}
}

func TestChoiceDetails(t *testing.T) {
	t.Run("variant zero can be chosen", func(t *testing.T) {
		h := newHandler(t, `{"oneOf":[{"type":"string"},{"type":"integer"}],"default":3}`)
		assertJSONEqual(t, submit(t, h, url.Values{"@variant": {"0"}, "$": {"x"}}), `"x"`)
		assertJSONEqual(t, submit(t, h, url.Values{"@variant": {"1"}, "$": {"4"}}), `4`)
		assertJSONEqual(t, submit(t, h, url.Values{"@variant": {"-1"}, "$": {"5"}}), `5`) // falls back to the default's variant
	})

	t.Run("a variant with its own description keeps it", func(t *testing.T) {
		doc := render(t, `{"description":"outer","oneOf":[{"description":"inner","type":"string"}]}`, "")
		if n := len(selectAll(doc, ".jsonform-description")); n != 2 {
			t.Errorf("%d descriptions, want 2", n)
		}
	})

	t.Run("anyOf errors come from the chosen variant", func(t *testing.T) {
		h := newHandler(t, `{"anyOf":[{"type":"string","minLength":5},{"type":"integer","maximum":1}]}`)
		f, err := h.SubmitForm(context.Background(), url.Values{"@variant": {"0"}, "$": {"ab"}})
		if err != nil {
			t.Fatal(err)
		}
		if got := fieldErrors(f.Root); !reflect.DeepEqual(got, map[string][]string{"": {"string length 2 less than minLength 5"}}) {
			t.Errorf("errors = %q", got)
		}
	})

	t.Run("a oneOf nested in a different keyword is not filtered", func(t *testing.T) {
		h := newHandler(t, `{"type":"object","properties":{"v":{"anyOf":[{"type":"string"},{"type":"integer"}],"oneOf":[{"minimum":10},{"maximum":0}]}}}`)
		f, err := h.SubmitForm(context.Background(), url.Values{"@variant/v": {"0"}, "$/v": {"x"}, "@present": {"1"}})
		if err != nil {
			t.Fatal(err)
		}
		if f.Valid {
			t.Fatalf("valid: %s", f.Value)
		}
	})
}

func TestShowModes(t *testing.T) {
	h := newHandler(t, `{"type":"object","properties":{"a":{"type":"string"}}}`)
	f, err := h.SubmitForm(context.Background(), url.Values{"@mode": {ModeJSON}, "$": {`{"a":"x"}`}, ActionName: {ActionJSON}})
	if err != nil {
		t.Fatal(err)
	}
	if f.Mode != ModeJSON || f.Root.Value != `{"a":"x"}` {
		t.Errorf("JSON again: mode %q value %q", f.Mode, f.Root.Value)
	}
	f, err = h.SubmitForm(context.Background(), url.Values{"@mode": {ModeJSON}, "$": {"  "}, ActionName: {ActionFields}})
	if err != nil {
		t.Fatal(err)
	}
	if f.Mode != "" || f.StatusCode() != 200 {
		t.Errorf("blank JSON to fields: mode %q status %d", f.Mode, f.StatusCode())
	}
	f, err = h.SubmitForm(context.Background(), url.Values{ActionName: {ActionJSON}, "$/a": {""}})
	if err != nil {
		t.Fatal(err)
	}
	if f.Root.Value != "{}" {
		t.Errorf("empty object as JSON = %q", f.Root.Value)
	}
	if f.Invalid() {
		t.Errorf("a successful switch is invalid")
	}
}

func TestStateParsing(t *testing.T) {
	for _, tc := range []struct {
		name       string
		kind, path string
		ok         bool
	}{
		{"$", "$", "", true},
		{"$/a", "$", "/a", true},
		{"$a", "$", "a", false},
		{"@len", "len", "", true},
		{"@len/a/b", "len", "/a/b", true},
		{"@", "", "", false},
		{"@/a", "", "", false},
		{"@action", "", "", false},
		{"csrf", "", "", false},
	} {
		kind, path, ok := parseName(tc.name)
		if ok != tc.ok || (ok && (kind != tc.kind || path != tc.path)) {
			t.Errorf("parseName(%q) = %q, %q, %v, want %q, %q, %v", tc.name, kind, path, ok, tc.kind, tc.path, tc.ok)
		}
	}

	for _, tc := range []struct {
		token string
		i     int
		entry bool
		ok    bool
	}{
		{"0", 0, false, true},
		{"12", 12, false, true},
		{"~k3", 3, true, true},
		{"", 0, false, false},
		{"~k", 0, false, false},
		{"1a", 0, false, false},
		{"-1", 0, false, false},
		{fmt.Sprint(maxItems - 1), maxItems - 1, false, true},
		{fmt.Sprint(maxItems), 0, false, false},
	} {
		i, entry, ok := position(tc.token)
		if ok != tc.ok || (ok && (i != tc.i || entry != tc.entry)) {
			t.Errorf("position(%q) = %d, %v, %v, want %d, %v, %v", tc.token, i, entry, ok, tc.i, tc.entry, tc.ok)
		}
	}

	for _, tc := range []struct {
		path, prefix string
		want         bool
	}{
		{"/a", "/a", true},
		{"/a/b", "/a", true},
		{"/ab", "/a", false},
		{"/a", "", true},
		{"", "", true},
		{"/b", "/a", false},
	} {
		if got := within(tc.path, tc.prefix); got != tc.want {
			t.Errorf("within(%q, %q) = %v, want %v", tc.path, tc.prefix, got, tc.want)
		}
	}

	s := state{"@len": {"-4"}, "@len/a": {"x"}, "@len/b": {"7"}, "$/e": {}, "@present/e": {}}
	for path, want := range map[string]int{"": 0, "/a": 0, "/b": 7} {
		if n, ok := s.count(path); !ok || n != want {
			t.Errorf("count(%q) = %d, %v, want %d", path, n, ok, want)
		}
	}
	if _, ok := s.value("/e"); ok {
		t.Errorf("value of an empty control is present")
	}
	if _, ok := s.meta(metaPresent, "/e"); ok {
		t.Errorf("meta of an empty control is present")
	}
	if s.present("/e") {
		t.Errorf("present with an empty control")
	}
}

func TestActionsAtBounds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		start  state
		action string
		want   state
	}{
		{"remove the last item", state{"@len": {"2"}, "$/0": {"a"}, "$/1": {"b"}}, "remove/1", state{"@len": {"1"}, "$/0": {"a"}}},
		{"remove past the count", state{"@len": {"1"}, "$/0": {"a"}, "$/1": {"stale"}}, "remove/1", state{"@len": {"1"}, "$/0": {"a"}, "$/1": {"stale"}}},
		{"move past the count", state{"@len": {"2"}, "$/0": {"a"}, "$/1": {"b"}, "$/2": {"c"}}, "down/1", state{"@len": {"2"}, "$/0": {"a"}, "$/1": {"b"}, "$/2": {"c"}}},
		{"move from past the count", state{"@len": {"1"}, "$/0": {"a"}, "$/1": {"b"}}, "up/1", state{"@len": {"1"}, "$/0": {"a"}, "$/1": {"b"}}},
		{"move a non-item", state{"$/a": {"x"}}, "up/a", state{"$/a": {"x"}}},
		{"pick nothing", state{"@pick": {""}}, "pick", state{}},
		{"unknown operation", state{"$/a": {"x"}}, "explode/a", state{"$/a": {"x"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			applyAction(tc.start, tc.action)
			if !reflect.DeepEqual(tc.start, tc.want) {
				t.Errorf("got %v, want %v", tc.start, tc.want)
			}
		})
	}
}

func TestReadOnlyHelpers(t *testing.T) {
	for _, tc := range []struct {
		a, b []string
		want int
	}{
		{[]string{"a", "2"}, []string{"a", "10"}, -1},
		{[]string{"a", "10"}, []string{"a", "2"}, 1},
		{[]string{"a"}, []string{"a", "0"}, -1},
		{[]string{"b"}, []string{"a"}, 1},
		{[]string{"x1"}, []string{"x1"}, 0},
		{[]string{"1"}, []string{"a"}, -1},
	} {
		if got := comparePointers(tc.a, tc.b); got != tc.want {
			t.Errorf("comparePointers(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
	for _, tc := range []struct {
		v      string
		tokens []string
		want   string
	}{
		{`[1,2,3]`, []string{"1"}, `[1,3]`},
		{`[1,2,3]`, []string{"3"}, `[1,2,3]`},
		{`[1,2,3]`, []string{"-1"}, `[1,2,3]`},
		{`[1,[2,3]]`, []string{"1", "0"}, `[1,[3]]`},
		{`{"a":{"b":1,"c":2}}`, []string{"a", "b"}, `{"a":{"c":2}}`},
		{`"s"`, []string{"a"}, `"s"`},
	} {
		if got := deleteAt(jsontext.Value(tc.v), tc.tokens); !equal(got, tc.want) {
			t.Errorf("deleteAt(%s, %q) = %s, want %s", tc.v, tc.tokens, got, tc.want)
		}
	}
	for _, tc := range []struct {
		v      string
		tokens []string
		x      string
		want   string
	}{
		{`[1]`, []string{"1"}, `2`, `[1,2]`},
		{`[1]`, []string{"3"}, `2`, `[1]`},
		{`[1]`, []string{"0"}, `2`, `[2]`},
		{`{}`, []string{"a", "b"}, `1`, `{"a":{"b":1}}`},
		{`"s"`, []string{"a"}, `1`, `{"a":1}`},
	} {
		if got := setAt(jsontext.Value(tc.v), tc.tokens, jsontext.Value(tc.x)); !equal(got, tc.want) {
			t.Errorf("setAt(%s, %q, %s) = %s, want %s", tc.v, tc.tokens, tc.x, got, tc.want)
		}
	}

	t.Run("read-only through a choice", func(t *testing.T) {
		h := newHandler(t, `{"oneOf":[{"type":"object","required":["id"],"properties":{"id":{"type":"string","readOnly":true}}},{"type":"string"}]}`)
		h.Value = jsontext.Value(`{"id":"1"}`)
		assertJSONEqual(t, submit(t, h, url.Values{"@variant": {"0"}, "@present": {"1"}, "$/id": {"2"}}), `{"id":"1"}`)
	})

	t.Run("read-only items deeper in arrays", func(t *testing.T) {
		h := newHandler(t, `{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer","readOnly":true},"n":{"type":"string"}}}}`)
		h.Value = jsontext.Value(`[{"id":1,"n":"a"},{"id":2,"n":"b"}]`)
		got := submit(t, h, url.Values{"@len": {"2"}, "@present/0": {"1"}, "@present/1": {"1"}, "$/0/id": {"9"}, "$/0/n": {"x"}, "$/1/id": {"9"}, "$/1/n": {"y"}})
		assertJSONEqual(t, got, `[{"id":1,"n":"x"},{"id":2,"n":"y"}]`)
	})
}

func equal(got jsontext.Value, want string) bool {
	eq, err := jsonschema.Equal(got, []byte(want))
	return err == nil && eq
}

func TestShapeDetails(t *testing.T) {
	t.Run("a dynamic reference without a dynamic anchor", func(t *testing.T) {
		doc := render(t, `{"$defs":{"n":{"type":"object","properties":{"x":{"type":"integer"}}}},"type":"object","properties":{"d":{"$dynamicRef":"#/$defs/n"}}}`, `{"d":{"x":1}}`)
		if doc.QuerySelector(`[data-pointer="/d/x"][data-kind="number"]`) == nil {
			t.Errorf("$dynamicRef to a plain pointer was not followed")
		}
	})

	t.Run("accepts", func(t *testing.T) {
		for _, tc := range []struct {
			schema, value string
			want          bool
		}{
			{`{"type":"number"}`, `3`, true},
			{`{"type":"integer"}`, `3.5`, false},
			{`{"type":"integer"}`, `3.0`, true},
			{`{"type":"string"}`, `3`, false},
			{`{"minimum":1}`, `"s"`, false},
			{`{"enum":[1,2]}`, `3`, false},
			{`{"type":"object","required":["k"]}`, `{}`, false},
			{`{"type":"object","properties":{"k":{"const":"a"}}}`, `{"k":"b"}`, false},
			{`{"type":"object","properties":{"k":{"enum":["a"]}}}`, `{"k":"a"}`, true},
			{`{"type":"object","properties":{"k":{"type":"string"}}}`, `{"k":1}`, true},
			{`{"type":"object","properties":{"k":true},"additionalProperties":false}`, `{"z":1}`, false},
			{`false`, `1`, false},
		} {
			s, err := jsonschema.Parse([]byte(tc.schema))
			if err != nil {
				t.Fatal(err)
			}
			if got := flatten(node{schema: s}).accepts(jsontext.Value(tc.value)); got != tc.want {
				t.Errorf("%s accepts %s = %v, want %v", tc.schema, tc.value, got, tc.want)
			}
		}
	})

	t.Run("keyword numbers", func(t *testing.T) {
		for _, tc := range []struct {
			v  string
			n  int
			ok bool
		}{
			{"3", 3, true}, {"0", 0, true}, {"-1", 0, false}, {"1.5", 0, false}, {"3.0", 3, true}, {`"3"`, 0, false}, {"", 0, false}, {"3000000000", 0, false},
		} {
			n, ok := integerKeyword(jsontext.Value(tc.v))
			if ok != tc.ok || n != tc.n {
				t.Errorf("integerKeyword(%s) = %d, %v, want %d, %v", tc.v, n, ok, tc.n, tc.ok)
			}
		}
		for _, v := range []string{`"1"`, `true`, `{}`} {
			if _, ok := number(jsontext.Value(v)); ok {
				t.Errorf("number(%s) ok", v)
			}
		}
		if got := ordinal("", 0); got != "Item 1" {
			t.Errorf("ordinal = %q", got)
		}
		for v, want := range map[string]jsonschema.SimpleType{"1": "integer", "1.5": "number", "1e400": "number", `"s"`: "string", "null": "null", "[]": "array"} {
			if got := jsonType(jsontext.Value(v)); got != want {
				t.Errorf("jsonType(%s) = %q, want %q", v, got, want)
			}
		}
	})

	t.Run("malformed JSON helpers", func(t *testing.T) {
		if got := objectKeys(jsontext.Value(`[1]`)); got != nil {
			t.Errorf("objectKeys of an array = %q", got)
		}
		if got := arrayElements(jsontext.Value(`{}`)); got != nil {
			t.Errorf("arrayElements of an object = %q", got)
		}
		n := 0
		for range objectMembers(jsontext.Value(`[1]`)) {
			n++
		}
		if n != 0 {
			t.Errorf("objectMembers of an array yielded %d", n)
		}
		if got := compactJSON(jsontext.Value(`{`)); got != `{` {
			t.Errorf("compactJSON of bad JSON = %q", got)
		}
		if got := indentJSON(jsontext.Value(`{`)); got != `{` {
			t.Errorf("indentJSON of bad JSON = %q", got)
		}
		if got := compactJSON(jsontext.Value(` [ 1 ] `)); got != `[1]` {
			t.Errorf("compactJSON = %q", got)
		}
	})

	t.Run("scope", func(t *testing.T) {
		a, err := jsonschema.Parse([]byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		b, err := jsonschema.Parse([]byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		sc := scope{}.push(a).push(a).push(nil).push(b).push(b)
		if len(sc) != 2 || sc[0] != a || sc[1] != b {
			t.Errorf("scope = %v", sc)
		}
		again := sc.push(a)
		if len(again) != 3 || len(sc) != 2 {
			t.Errorf("push changed the original scope")
		}
	})
}

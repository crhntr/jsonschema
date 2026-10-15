package yamljson

import (
	"encoding/json/jsontext"
	"errors"
	"strings"
	"testing"

	"github.com/crhntr/jsonschema/jsonptr"
)

func TestDecode(t *testing.T) {
	for _, tc := range []struct {
		name, yaml, want string
	}{
		{"key order", "b: 1\na: 2\n", `{"b":1,"a":2}`},
		{"scalar types", "s: hi\ni: 3\nf: 1.5\nt: true\nn: null\nq: \"true\"\n", `{"s":"hi","i":3,"f":1.5,"t":true,"n":null,"q":"true"}`},
		{"timestamps stay strings", "at: 2024-01-02\n", `{"at":"2024-01-02"}`},
		{"concourse vars are strings", "trigger: ((auto))\n", `{"trigger":"((auto))"}`},
		{"literal block", "run: |\n  echo hi\n  echo bye\n", `{"run":"echo hi\necho bye\n"}`},
		{"alias", "a: &x {k: v}\nb: *x\n", `{"a":{"k":"v"},"b":{"k":"v"}}`},
		{"merge, explicit wins", "base: &b {k: 1, j: 2}\nm:\n  <<: *b\n  k: 3\n", `{"base":{"k":1,"j":2},"m":{"k":3,"j":2}}`},
		{"merge list", "x: &x {a: 1}\ny: &y {b: 2}\nm:\n  <<: [*x, *y]\n", `{"x":{"a":1},"y":{"b":2},"m":{"a":1,"b":2}}`},
		{"empty document", "", `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Parse([]byte(tc.yaml))
			if err != nil {
				t.Fatal(err)
			}
			got, _, err := Decode(doc)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("Decode(%q) = %s, want %s", tc.yaml, got, tc.want)
			}
		})
	}
}

func TestDecode_errors(t *testing.T) {
	for _, tc := range []struct{ name, yaml string }{
		{"infinity", "x: .inf\n"},
		{"non-string key", "? [a]\n: 1\n"},
		{"merge of a scalar", "m:\n  <<: 1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Parse([]byte(tc.yaml))
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := Decode(doc); err == nil {
				t.Errorf("Decode(%q) succeeded", tc.yaml)
			}
		})
	}
}

func TestPositions(t *testing.T) {
	doc, err := Parse([]byte("jobs:\n- name: unit\n  plan:\n  - get: repo\n    trigger: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	_, pos, err := Decode(doc)
	if err != nil {
		t.Fatal(err)
	}
	for ptr, want := range map[string]Pos{
		"/jobs":                   {1, 1},
		"/jobs/0":                 {2, 3},
		"/jobs/0/name":            {2, 3},
		"/jobs/0/plan/0/trigger":  {5, 5},
		"/jobs/0/plan/0/missing":  {4, 5}, // the nearest enclosing value
		"/jobs/0/plan/0/get/deep": {4, 5},
	} {
		if got := pos.At(ptr); got != want {
			t.Errorf("At(%q) = %v, want %v", ptr, got, want)
		}
	}
}

func TestEncode(t *testing.T) {
	for _, tc := range []struct {
		name, json, want string
	}{
		{"order and types", `{"b":1,"a":[true,null,2.5,"x"]}`, "b: 1\na:\n  - true\n  - null\n  - 2.5\n  - x\n"},
		{"strings that look like other types are quoted", `{"s":"true","n":"12","e":""}`, "s: \"true\"\nn: \"12\"\ne: \"\"\n"},
		{"multi-line strings become literal blocks", `{"run":"set -e\ngo test ./...\n"}`, "run: |\n  set -e\n  go test ./...\n"},
		{"empty containers", `{"o":{},"a":[]}`, "o: {}\na: []\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, err := Encode(jsontext.Value(tc.json))
			if err != nil {
				t.Fatal(err)
			}
			got, err := Format(n)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("Encode(%s) =\n%s\nwant\n%s", tc.json, got, tc.want)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	const src = `{"jobs":[{"name":"unit","plan":[{"get":"repo","trigger":true},{"task":"test","config":{"platform":"linux","run":{"path":"sh","args":["-c","go test ./...\n"]}}}]}]}`
	n, err := Encode(jsontext.Value(src))
	if err != nil {
		t.Fatal(err)
	}
	text, err := Format(n)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := Decode(doc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != src {
		t.Errorf("round trip = %s, want %s", got, src)
	}
}

const pipeline = `# The pipeline.
resources:
# Source code.
- name: repo # the app
  type: git
  source: {uri: https://example.com/app.git}

jobs:
# Runs the tests.
- name: unit
  plan:
  - get: repo
- name: shared
  <<: &defaults {serial: true}
`

func TestReplace(t *testing.T) {
	doc, err := Parse([]byte(pipeline))
	if err != nil {
		t.Fatal(err)
	}
	if err := Replace(doc, "/jobs/0", jsontext.Value(`{"name":"unit","serial":true,"plan":[{"get":"repo","trigger":true}]}`)); err != nil {
		t.Fatal(err)
	}
	out, err := Format(doc)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{
		"# The pipeline.",
		"# Source code.",
		"- name: repo # the app",
		"# Runs the tests.\n  - name: unit\n    plan:\n      - get: repo\n        trigger: true\n    serial: true",
		"\n    <<: &defaults {serial: true}",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("after Replace, the YAML lacks %q:\n%s", want, text)
		}
	}
}

func TestReplace_keepsWhatDidNotChange(t *testing.T) {
	const src = `alert: &alert {put: notify}
jobs:
- name: unit
  # Fetch the code.
  plan:
  - get: repo
    trigger: true # run on every commit
  serial: true
  on_failure: *alert
`
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	// The members come in schema order, as a form submits them, with
	// trigger turned off and a new field.
	if err := Replace(doc, "/jobs/0", jsontext.Value(`{"name":"unit","plan":[{"get":"repo","trigger":false}],"serial":true,"on_failure":{"put":"notify"},"max_in_flight":2}`)); err != nil {
		t.Fatal(err)
	}
	out, err := Format(doc)
	if err != nil {
		t.Fatal(err)
	}
	want := `alert: &alert {put: notify}
jobs:
  - name: unit
    # Fetch the code.
    plan:
      - get: repo
        trigger: false # run on every commit
    serial: true
    on_failure: *alert
    max_in_flight: 2
`
	if string(out) != want {
		t.Errorf("after Replace:\n%s\nwant\n%s", out, want)
	}

	if err := Replace(doc, "/alert", jsontext.Value(`{"put":"pager"}`)); !errors.Is(err, ErrShared) {
		t.Errorf("changing an anchored node = %v, want ErrShared", err)
	}
}

func TestFind_shared(t *testing.T) {
	doc, err := Parse([]byte(pipeline))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Find(doc, "/jobs/1/serial"); !errors.Is(err, ErrShared) {
		t.Errorf("Find through a merge key = %v, want ErrShared", err)
	}
	if _, err := Find(doc, "/jobs/9"); err == nil || errors.Is(err, ErrShared) {
		t.Errorf("Find past the end = %v", err)
	}
}

func TestAppendRemove(t *testing.T) {
	doc, err := Parse([]byte(pipeline))
	if err != nil {
		t.Fatal(err)
	}
	if err := Append(doc, "/jobs", jsontext.Value(`{"name":"build","plan":[]}`)); err != nil {
		t.Fatal(err)
	}
	if err := Remove(doc, "/jobs/0"); err != nil {
		t.Fatal(err)
	}
	if err := Remove(doc, "/resources/0/name"); err == nil {
		t.Errorf("Remove of a mapping member succeeded")
	}
	got, _, err := Decode(doc)
	if err != nil {
		t.Fatal(err)
	}
	names, err := jsonptr.Find(got, "/jobs/1/name")
	if err != nil || string(names) != `"build"` {
		t.Errorf("after Append and Remove, /jobs/1/name = %s (%v)", names, err)
	}
}

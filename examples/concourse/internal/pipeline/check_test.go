package pipeline

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/crhntr/jsonschema/examples/concourse/schema"
)

func sample(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile("../../testdata/pipeline.yml")
	if err != nil {
		t.Fatal(err)
	}
	return string(src)
}

func checker(t *testing.T) *Checker {
	t.Helper()
	s, err := schema.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return NewChecker(s, schema.Pipeline())
}

func problems(t *testing.T, src string) []string {
	t.Helper()
	d, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range checker(t).Check(d) {
		out = append(out, fmt.Sprintf("%d:%d %s: %s", p.Line, p.Column, p.Pointer, p.Message))
	}
	return out
}

func TestCheck(t *testing.T) {
	if got := problems(t, sample(t)); len(got) != 0 {
		t.Fatalf("the sample pipeline has problems: %q", got)
	}
	for _, tc := range []struct {
		name, old, new string
		want           []string
	}{
		{
			"a misspelled step field is reported once, as the step it is",
			"    trigger: true\n  - task: test", "    triger: true\n  - task: test",
			[]string{`47:5 /jobs/0/plan/0/triger: unknown field "triger"`},
		},
		{
			"a step of no known kind lists the kinds",
			"  - task: test\n", "  - tsk: test\n",
			[]string{`48:5 /jobs/0/plan/1: expected one of: get, put, task, set_pipeline, load_var, in_parallel, do, try`},
		},
		{
			"a pattern is explained by its description",
			"timeout: 10m", "timeout: soon",
			[]string{`93:5 /jobs/2/plan/2/timeout: expected a Go duration such as 30s, 5m or 1h30m, or a ((var))`},
		},
		{
			"an error inside in_parallel's list form",
			"      passed: [unit]", "      passed: unit",
			[]string{`72:7 /jobs/1/plan/0/in_parallel/0/passed: expected array, got string`},
		},
		{
			"a missing required field",
			"- name: nightly\n  type: time", "- name: nightly\n  typ: time",
			[]string{`33:3 /resources/2: missing required property "type"`, `34:3 /resources/2/typ: unknown field "typ"`},
		},
		{
			"an invalid identifier",
			"- name: deploy-staging", "- name: Deploy-Staging",
			[]string{`82:3 /jobs/2/name: expected a lowercase name: a letter, then letters, digits, '-', '_' or '.'`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := sample(t)
			if !strings.Contains(src, tc.old) {
				t.Fatalf("the sample does not contain %q", tc.old)
			}
			got := problems(t, strings.Replace(src, tc.old, tc.new, 1))
			if !slices.Equal(got, tc.want) {
				t.Errorf("problems =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

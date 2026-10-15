package pipeline

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestGraph(t *testing.T) {
	d, err := Parse([]byte(sample(t)))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("all jobs", func(t *testing.T) {
		g := d.Graph("")
		columns := map[int][]string{}
		for _, n := range g.Nodes {
			columns[n.X] = append(columns[n.X], n.Name)
		}
		var got []string
		for x := margin; x < g.Width; x += nodeW + colGap {
			got = append(got, strings.Join(columns[x], ","))
		}
		want := []string{"repo,nightly", "unit", "build", "image,deploy-staging", "deploy-production", "notify"}
		if !slices.Equal(got, want) {
			t.Errorf("columns = %q, want %q", got, want)
		}
		var edges []string
		for _, e := range g.Edges {
			trigger := ""
			if e.Trigger {
				trigger = " (trigger)"
			}
			edges = append(edges, fmt.Sprintf("%s -> %s via %s%s", e.From, e.To, e.Resource, trigger))
		}
		for _, want := range []string{
			"resources:repo -> jobs:unit via repo (trigger)",
			"jobs:unit -> jobs:build via repo (trigger)",
			"resources:nightly -> jobs:build via nightly (trigger)",
			"jobs:build -> resources:image via image (trigger)",
			"jobs:build -> jobs:deploy-staging via image (trigger)",
			"jobs:deploy-staging -> jobs:deploy-production via repo",
			"jobs:unit -> resources:notify via notify (trigger)",
		} {
			if !slices.Contains(edges, want) {
				t.Errorf("no edge %q in\n  %s", want, strings.Join(edges, "\n  "))
			}
		}
		for _, e := range g.Edges {
			if !strings.HasPrefix(e.Path, "M") {
				t.Errorf("edge %s -> %s has no path", e.From, e.To)
			}
		}
	})

	t.Run("a group draws upstream resources instead of jobs outside it", func(t *testing.T) {
		g := d.Graph("deploy")
		var names []string
		for _, n := range g.Nodes {
			names = append(names, n.ID())
		}
		slices.Sort(names)
		want := []string{"jobs:deploy-production", "jobs:deploy-staging", "resources:image", "resources:notify", "resources:repo"}
		if !slices.Equal(names, want) {
			t.Errorf("nodes = %q, want %q", names, want)
		}
	})

	t.Run("a cycle does not hang the layout", func(t *testing.T) {
		d, err := Parse([]byte("jobs:\n- name: a\n  plan:\n  - get: r\n    trigger: true\n  - put: r\n"))
		if err != nil {
			t.Fatal(err)
		}
		if g := d.Graph(""); len(g.Nodes) != 2 || len(g.Edges) != 2 {
			t.Errorf("graph = %+v", g)
		}
	})
}

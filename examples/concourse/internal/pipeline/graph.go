package pipeline

import (
	"cmp"
	"encoding/json/v2"
	"fmt"
	"path"
	"slices"
	"strings"
)

// Graph is the pipeline drawn as Concourse draws it: resources and jobs in
// columns, with an edge for each version that flows between them.
type Graph struct {
	Width, Height int
	Nodes         []Node
	Edges         []Edge
}

// Node is a job or resource box.
type Node struct {
	Kind       Kind
	Name       string
	X, Y, W, H int
}

// ID is the node's id in the drawing.
func (n Node) ID() string { return string(n.Kind) + ":" + n.Name }

// Edge is a version flowing from a resource to a job (a get), from a job to
// a resource (a put), or from one job to another (a get with passed).
type Edge struct {
	From, To string // node IDs
	Resource string
	Trigger  bool
	Path     string // SVG path data
}

// Group is a named subset of jobs.
type Group struct {
	Name string   `json:"name"`
	Jobs []string `json:"jobs"`
}

// Groups returns the pipeline's groups.
func (d *Document) Groups() []Group {
	var p struct {
		Groups []Group `json:"groups"`
	}
	if err := json.Unmarshal(d.json, &p); err != nil {
		return nil // malformed groups are the schema's to report
	}
	return p.Groups
}

// input is a get step: a resource version a job uses.
type input struct {
	Resource string
	Passed   []string
	Trigger  bool
}

type jobIO struct {
	Name    string
	Inputs  []input
	Outputs []string
}

const (
	nodeW, nodeH = 168, 34
	colGap       = 64
	rowGap       = 16
	margin       = 16
)

// Graph lays out the jobs in the named group (all jobs when group is
// empty) and the resources they use.
func (d *Document) Graph(group string) Graph {
	jobs := d.jobIO()
	var globs []string
	for _, g := range d.Groups() {
		if g.Name == group {
			globs = g.Jobs
		}
	}
	if group != "" {
		jobs = slices.DeleteFunc(jobs, func(j jobIO) bool {
			return !slices.ContainsFunc(globs, func(glob string) bool {
				ok, err := path.Match(glob, j.Name)
				return err == nil && ok
			})
		})
	}
	inGroup := map[string]bool{}
	for _, j := range jobs {
		inGroup[j.Name] = true
	}

	var nodes []Node
	index := map[string]int{}
	add := func(k Kind, name string) string {
		n := Node{Kind: k, Name: name, W: nodeW, H: nodeH}
		if _, ok := index[n.ID()]; !ok {
			index[n.ID()] = len(nodes)
			nodes = append(nodes, n)
		}
		return n.ID()
	}
	var edges []Edge
	for _, j := range jobs {
		job := add(Jobs, j.Name)
		for _, in := range j.Inputs {
			upstream := slices.DeleteFunc(slices.Clone(in.Passed), func(p string) bool { return !inGroup[p] })
			if len(upstream) == 0 {
				edges = append(edges, Edge{From: add(Resources, in.Resource), To: job, Resource: in.Resource, Trigger: in.Trigger})
				continue
			}
			for _, p := range upstream {
				edges = append(edges, Edge{From: Node{Kind: Jobs, Name: p}.ID(), To: job, Resource: in.Resource, Trigger: in.Trigger})
			}
		}
		for _, out := range j.Outputs {
			edges = append(edges, Edge{From: job, To: add(Resources, out), Resource: out, Trigger: true})
		}
	}
	edges = slices.CompactFunc(edges, func(a, b Edge) bool { return a == b })
	return layout(nodes, index, edges)
}

// layout puts each node in the column of its longest path from a source,
// orders each column by where its predecessors are, and draws the edges.
// An edge that spans several columns passes through an empty slot in each
// column it crosses, so it never runs behind another box.
func layout(nodes []Node, index map[string]int, edges []Edge) Graph {
	rank := make([]int, len(nodes))
	for range nodes { // a cycle stops growing after len(nodes) rounds
		changed := false
		for _, e := range edges {
			from, to := index[e.From], index[e.To]
			if rank[to] < rank[from]+1 {
				rank[to] = rank[from] + 1
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	// slots are the boxes and the empty places long edges pass through.
	type slot struct {
		rank int
		node int // index in nodes, or -1 for a pass-through
		row  float64
		y    int
	}
	slots := make([]slot, len(nodes))
	for i := range nodes {
		slots[i] = slot{rank: rank[i], node: i}
	}
	// chains lists, for each edge, the slots it goes through in order.
	chains := make([][]int, len(edges))
	for k, e := range edges {
		from, to := index[e.From], index[e.To]
		chain := []int{from}
		for r := rank[from] + 1; r < rank[to]; r++ {
			slots = append(slots, slot{rank: r, node: -1})
			chain = append(chain, len(slots)-1)
		}
		chains[k] = append(chain, to)
	}
	maxRank := 0
	columns := map[int][]int{}
	for i, s := range slots {
		columns[s.rank] = append(columns[s.rank], i)
		maxRank = max(maxRank, s.rank)
	}
	// Order each column by the mean row of the slots before it, so edges
	// cross less, keeping file order among equals.
	tallest := 0
	for r := 0; r <= maxRank; r++ {
		col := columns[r]
		weight := map[int]float64{}
		for _, i := range col {
			var sum, n float64
			for _, chain := range chains {
				for j := 1; j < len(chain); j++ {
					if chain[j] == i && slots[chain[j-1]].rank < r {
						sum += slots[chain[j-1]].row
						n++
					}
				}
			}
			weight[i] = float64(i) / 1e6
			if n > 0 {
				weight[i] += sum / n
			}
		}
		slices.SortStableFunc(col, func(a, b int) int { return cmp.Compare(weight[a], weight[b]) })
		for y, i := range col {
			slots[i].row = float64(y)
		}
		tallest = max(tallest, len(col))
	}

	g := Graph{
		Width:  2*margin + (maxRank+1)*nodeW + maxRank*colGap,
		Height: 2*margin + tallest*nodeH + max(tallest-1, 0)*rowGap,
	}
	x := func(r int) int { return margin + r*(nodeW+colGap) }
	for r := 0; r <= maxRank; r++ {
		col := columns[r]
		height := len(col)*nodeH + max(len(col)-1, 0)*rowGap
		top := (g.Height - height) / 2
		for y, i := range col {
			slots[i].y = top + y*(nodeH+rowGap)
			if n := slots[i].node; n >= 0 {
				nodes[n].X, nodes[n].Y = x(r), slots[i].y
			}
		}
	}
	g.Nodes = nodes
	for k, e := range edges {
		chain := chains[k]
		first := slots[chain[0]]
		var b strings.Builder
		px, py := x(first.rank)+nodeW, first.y+nodeH/2
		fmt.Fprintf(&b, "M%d %d", px, py)
		for _, i := range chain[1:] {
			s := slots[i]
			qx, qy := x(s.rank), s.y+nodeH/2
			dx := max((qx-px)/2, colGap)
			fmt.Fprintf(&b, " C%d %d %d %d %d %d", px+dx, py, qx-dx, qy, qx, qy)
			if s.node < 0 { // pass straight through the empty slot
				fmt.Fprintf(&b, " L%d %d", qx+nodeW, qy)
				qx += nodeW
			}
			px, py = qx, qy
		}
		e.Path = b.String()
		g.Edges = append(g.Edges, e)
	}
	return g
}

// jobIO lists every job's inputs and outputs, from all of its steps,
// including nested ones and hooks.
func (d *Document) jobIO() []jobIO {
	var p struct {
		Jobs []map[string]any `json:"jobs"`
	}
	if err := json.Unmarshal(d.json, &p); err != nil {
		return nil // malformed jobs are the schema's to report
	}
	var out []jobIO
	for _, job := range p.Jobs {
		name, _ := job["name"].(string)
		j := jobIO{Name: name}
		if plan, ok := job["plan"].([]any); ok {
			for _, step := range plan {
				j.walk(step)
			}
		}
		for _, hook := range hooks {
			j.walk(job[hook])
		}
		out = append(out, j)
	}
	return out
}

var hooks = []string{"on_success", "on_failure", "on_error", "on_abort", "ensure"}

func (j *jobIO) walk(v any) {
	step, ok := v.(map[string]any)
	if !ok {
		return
	}
	if get, ok := step["get"].(string); ok {
		in := input{Resource: get}
		if r, ok := step["resource"].(string); ok {
			in.Resource = r
		}
		in.Trigger, _ = step["trigger"].(bool)
		if passed, ok := step["passed"].([]any); ok {
			for _, p := range passed {
				if s, ok := p.(string); ok {
					in.Passed = append(in.Passed, s)
				}
			}
		}
		j.Inputs = append(j.Inputs, in)
	}
	if put, ok := step["put"].(string); ok {
		if r, ok := step["resource"].(string); ok {
			put = r
		}
		j.Outputs = append(j.Outputs, put)
	}
	switch p := step["in_parallel"].(type) {
	case []any:
		j.walkAll(p)
	case map[string]any:
		steps, _ := p["steps"].([]any)
		j.walkAll(steps)
	}
	if do, ok := step["do"].([]any); ok {
		j.walkAll(do)
	}
	j.walk(step["try"])
	for _, hook := range hooks {
		j.walk(step[hook])
	}
}

func (j *jobIO) walkAll(steps []any) {
	for _, s := range steps {
		j.walk(s)
	}
}

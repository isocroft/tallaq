package deptree

import (
	"encoding/json"
	"sort"
)

/* @HINT: Graph is the set of all analysed files. */
type Graph struct {
	// Nodes by absolute path.
	Nodes map[string]*Node
	// Entries are the entry points that were requested (all analysed
	// files for BuildDir).
	Entries []*Node
	// Errors are non-fatal problems met during BuildDir (e.g. unreadable
	// files).
	Errors []error
}

/* @HINT: `Sorted(...)` returns all nodes ordered by path. */
func (g *Graph) Sorted() []*Node {
	out := make([]*Node, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

/*
@HINT:

`Roots(...)` returns the files nothing else depends on (typically the entry
points). Files that only participate in an isolated cycle are included
too, so every node is reachable from some root.
*/
func (g *Graph) Roots() []*Node {
	hasDependent := make(map[string]bool, len(g.Nodes))
	for _, n := range g.Nodes {
		for _, d := range n.Dependencies {
			if d.Path != n.Path {
				hasDependent[d.Path] = true
			}
		}
	}

	var roots []*Node
	for _, n := range g.Sorted() {
		if !hasDependent[n.Path] {
			roots = append(roots, n)
		}
	}

	reached := make(map[string]bool, len(g.Nodes))
	var mark func(n *Node)
	mark = func(n *Node) {
		if reached[n.Path] {
			return
		}
		reached[n.Path] = true
		for _, d := range n.Dependencies {
			mark(d)
		}
	}

	for _, r := range roots {
		mark(r)
	}
	for _, n := range g.Sorted() {
		if !reached[n.Path] {
			roots = append(roots, n)
			mark(n)
		}
	}

	return roots
}

/* @HINT: `Dependents(...)` returns, for each file, the files that import it. */
func (g *Graph) Dependents() map[string][]string {
	out := make(map[string][]string, len(g.Nodes))
	for _, n := range g.Sorted() {
		for _, d := range n.Dependencies {
			out[d.Path] = append(out[d.Path], n.Path)
		}
	}
	return out
}

/*
	@HINT:

	`Cycles(...)` returns the circular-import groups (strongly connected components
	with more than one file), each sorted by path, using Tarjan's algorithm.
*/
/* @CHECK: https://www.geeksforgeeks.org/dsa/tarjan-algorithm-find-strongly-connected-components/ */
func (g *Graph) Cycles() [][]string {
	var (
		counter int
		stack   []string
		result  [][]string
	)

	indices := make(map[string]int, len(g.Nodes))
	lowlink := make(map[string]int, len(g.Nodes))
	onStack := make(map[string]bool, len(g.Nodes))

	var strong func(v *Node)
	strong = func(v *Node) {
		indices[v.Path] = counter
		lowlink[v.Path] = counter
		counter++
		stack = append(stack, v.Path)
		onStack[v.Path] = true

		for _, w := range v.Dependencies {
			if _, seen := indices[w.Path]; !seen {
				strong(w)
				if lowlink[w.Path] < lowlink[v.Path] {
					lowlink[v.Path] = lowlink[w.Path]
				}
			} else if onStack[w.Path] {
				if indices[w.Path] < lowlink[v.Path] {
					lowlink[v.Path] = indices[w.Path]
				}
			}
		}

		if lowlink[v.Path] == indices[v.Path] {
			var component []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				component = append(component, w)
				if w == v.Path {
					break
				}
			}

			if len(component) > 1 {
				sort.Strings(component)
				result = append(result, component)
			}
		}
	}

	for _, n := range g.Sorted() {
		if _, seen := indices[n.Path]; !seen {
			strong(n)
		}
	}

	sort.Slice(result, func(i, j int) bool { return result[i][0] < result[j][0] })
	return result
}

/* @HINT: `MarshalJSON(...)` serialises the graph as `{"nodes": [...], "cycles": [...]}`. */
func (g *Graph) MarshalJSON() ([]byte, error) {
	cycles := g.Cycles()
	if cycles == nil {
		cycles = [][]string{}
	}

	return json.Marshal(struct {
		Nodes  []*Node    `json:"nodes"`
		Cycles [][]string `json:"cycles"`
	}{Nodes: g.Sorted(), Cycles: cycles})
}

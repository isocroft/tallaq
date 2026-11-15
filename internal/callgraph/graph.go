package callgraph

import (
	"fmt"
	"sort"
	"strings"
)

/* @HINT: Edge is a directed call: Caller ---> Callee. */
type Edge struct {
	Caller string `json:"caller"`
	Callee string `json:"callee"`
}

/* @HINT: CallGraph is a directed graph. */
/* @NOTE: All accessors return sorted slices so output is deterministic. */
type CallGraph struct {
	graph map[string]map[string]struct{} // caller -> callees
	rev   map[string]map[string]struct{} // callee -> callers
	nodes map[string]struct{}
}

/* @HINT: `NewCallGraph(...)` creates an empty call graph. */
func NewCallGraph() *CallGraph {
	return &CallGraph{
		graph: make(map[string]map[string]struct{}),
		rev:   make(map[string]map[string]struct{}),
		nodes: make(map[string]struct{}),
	}
}

/* @HINT: `AddNode(...)` registers a function that may have no calls at all. */
func (cg *CallGraph) AddNode(name string) {
	cg.nodes[name] = struct{}{}
}

/* @HINT: `AddCall(...)` adds a directed edge [ caller ---> callee ]. */
func (cg *CallGraph) AddCall(caller, callee string) {
	if _, ok := cg.graph[caller]; !ok {
		cg.graph[caller] = make(map[string]struct{})
	}
	cg.graph[caller][callee] = struct{}{}

	if _, ok := cg.rev[callee]; !ok {
		cg.rev[callee] = make(map[string]struct{})
	}
	cg.rev[callee][caller] = struct{}{}

	cg.nodes[caller] = struct{}{}
	cg.nodes[callee] = struct{}{}
}

/* @HINT: `GetSuccessorNodes(...)` returns the functions called by functionName. */
func (cg *CallGraph) GetSuccessorNodes(functionName string) []string {
	return sortedKeys(cg.graph[functionName])
}

/* @HINT: `GetPredecessorNodes(...)` returns the functions that call functionName. */
func (cg *CallGraph) GetPredecessorNodes(functionName string) []string {
	return sortedKeys(cg.rev[functionName])
}

/* @HINT: `HasNode(...)` reports whether a function exists in the graph. */
func (cg *CallGraph) HasNode(functionName string) bool {
	_, ok := cg.nodes[functionName]
	return ok
}

/* @HINT: `GetAllNodes(...)` returns every function in the graph. */
func (cg *CallGraph) GetAllNodes() []string {
	return sortedKeys(cg.nodes)
}

/* @HINT: `FanOut(...)` is the number of distinct functions fn calls (a coupling metric if high). */
func (cg *CallGraph) FanOut(fn string) int { return len(cg.graph[fn]) }

/* @HINT: `FanIn(...)` is the number of distinct functions that call fn (a god function metric if high). */
func (cg *CallGraph) FanIn(fn string) int { return len(cg.rev[fn]) }

/* @HINT: `Edges(...)` returns every edge, sorted. */
func (cg *CallGraph) Edges() []Edge {
	var out []Edge
	for _, caller := range cg.GetAllNodes() {
		for _, callee := range cg.GetSuccessorNodes(caller) {
			out = append(out, Edge{Caller: caller, Callee: callee})
		}
	}
	return out
}

/* @HINT: `Reachable(...)` returns every function transitively called from fn. */
func (cg *CallGraph) Reachable(fn string) []string {
	seen := make(map[string]struct{})
	queue := []string{fn}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		for next := range cg.graph[cur] {
			if _, ok := seen[next]; ok {
				continue
			}
			seen[next] = struct{}{}
			queue = append(queue, next)
		}
	}

	return sortedKeys(seen)
}

/*
@HINT:

`Cycles(...)` returns recursion groups: strongly connected
components with more than one function, plus functions that
call themselves.
*/
func (cg *CallGraph) Cycles() [][]string {
	var (
		counter int
		stack   []string
		result  [][]string
	)

	indices := make(map[string]int)
	lowlink := make(map[string]int)
	onStack := make(map[string]bool)

	var strong func(v string)
	strong = func(v string) {
		indices[v] = counter
		lowlink[v] = counter
		counter++
		stack = append(stack, v)
		onStack[v] = true

		for _, w := range cg.GetSuccessorNodes(v) {
			if _, seen := indices[w]; !seen {
				strong(w)
				if lowlink[w] < lowlink[v] {
					lowlink[v] = lowlink[w]
				}
			} else if onStack[w] {
				if indices[w] < lowlink[v] {
					lowlink[v] = indices[w]
				}
			}
		}

		if lowlink[v] == indices[v] {
			var component []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				component = append(component, w)
				if w == v {
					break
				}
			}

			_, selfCall := cg.graph[v][v]
			if len(component) > 1 || selfCall {
				sort.Strings(component)
				result = append(result, component)
			}
		}
	}

	for _, n := range cg.GetAllNodes() {
		if _, seen := indices[n]; !seen {
			strong(n)
		}
	}

	sort.Slice(result, func(i, j int) bool { return result[i][0] < result[j][0] })
	return result
}

/* @NOTE: String implements fmt.Stringer: */
/* @USAGE: one "caller -> callee, callee" line per calling function. */
func (cg *CallGraph) String() string {
	var sb strings.Builder
	for _, caller := range cg.GetAllNodes() {
		callees := cg.GetSuccessorNodes(caller)
		if len(callees) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "%s -> %s\n", caller, strings.Join(callees, ", "))
	}
	return sb.String()
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

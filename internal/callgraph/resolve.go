package callgraph

import (
	"sort"
	"strings"
)

/*
@NOTE:

Graph links every function to what it calls. A callee is resolved to a
concrete function ID when a function of that name exists in the same
file or in a file it imports; otherwise the raw callee name (e.g.
"numpy.array", "print") is kept as an external node.
*/
func (r *Result) Graph() *CallGraph {
	cg := NewCallGraph()

	/* @HINT: file -> simple name -> functions */
	index := make(map[string]map[string][]FunctionNode, len(r.Calls))
	files := make([]string, 0, len(r.Calls))

	for file, fns := range r.Calls {
		files = append(files, file)

		byName := make(map[string][]FunctionNode)
		for _, fn := range fns {
			byName[fn.Name] = append(byName[fn.Name], fn)
		}
		index[file] = byName
	}
	sort.Strings(files)

	for _, file := range files {
		scope := append([]string{file}, r.Deps[file]...)

		for _, fn := range r.Calls[file] {
			cg.AddNode(fn.ID)

			for _, callee := range fn.Callees {
				if ids := resolveCallee(callee, scope, index); len(ids) > 0 {
					for _, id := range ids {
						cg.AddCall(fn.ID, id)
					}
					continue
				}
				cg.AddCall(fn.ID, callee)
			}
		}
	}

	return cg
}

func resolveCallee(callee string, scope []string, index map[string]map[string][]FunctionNode) []string {
	name := callee
	if i := strings.LastIndex(callee, "."); i >= 0 {
		name = callee[i+1:]
	}
	qualified := strings.Contains(callee, ".")

	for _, file := range scope {
		var ids []string

		for _, m := range index[file][name] {
			/* @NOTE: A bare call like `foo()` cannot be a method call. */
			if !qualified && m.Qualified != m.Name {
				continue
			}
			ids = append(ids, m.ID)
		}

		if len(ids) > 0 {
			return ids
		}
	}

	return nil
}

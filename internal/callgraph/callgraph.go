/*
!

	@INFO:

	Package `callgraph` scans the files of a dependency tree and builds a
	static call map plus a resolved call graph.
*/
package callgraph

import (
	"fmt"
	"hash/fnv"
	"path/filepath"

	"github.com/isocroft/tallaq/internal/deptree"
	"github.com/isocroft/tallaq/internal/langs"
	"github.com/isocroft/tallaq/internal/modname"
	"github.com/isocroft/tallaq/internal/parse"
)

/* @HINT: `FunctionNode` describes one function and what it calls. */
type FunctionNode struct {
	// ID looks like "main.py:doSomething", "pkg.utils:helpers.py:run",
	// "system:root.go:Liftoff", "root:index.php:array_push" or, for
	// methods, "main:server.go:Server.Handle". Module-level statements
	// appear as "<module>:<file>:<global>".
	ID string `json:"id"`

	File      string `json:"file"`
	Name      string `json:"name"`      // simple name
	Qualified string `json:"qualified"` // Class.method
	Line      uint32 `json:"line"`

	// Callees are the distinct callee names in call order, with import
	// aliases expanded (e.g. "np.array" -> "numpy.array").
	Callees []string `json:"callees"`

	// Signature is a lightweight, non-cryptographic (FNV-1a) hash of the
	// qualified name and parameter list.
	Signature string `json:"signature"`
}

/* @HINT: `CallMap` maps an absolute file path to the functions found in it. */
type CallMap map[string][]FunctionNode

/* @HINT: `Options` configures a scan. */
type Options struct {
	// Root is the project root used to build module names. Defaults to the
	// root detected from the first dependency node.
	Root string
}

/*
@HINT:

`Result` is the output of a scan: the call map and the file dependency
edges that were used to find it.
*/
type Result struct {
	Calls CallMap
	Deps  map[string][]string // file -> files it imports
}

/* @HINT: `Generate(...)` scans root and everything it depends on. */
func Generate(root *deptree.Node, opts Options) (*Result, error) {
	if root == nil {
		return nil, fmt.Errorf("root dependency node cannot be nil")
	}
	return generate([]*deptree.Node{root}, opts)
}

/* @HINT: `GenerateFromGraph(...)` scans every file of a dependency graph. */
func GenerateFromGraph(g *deptree.Graph, opts Options) (*Result, error) {
	if g == nil || len(g.Nodes) == 0 {
		return nil, fmt.Errorf("dependency graph cannot be empty")
	}
	return generate(g.Sorted(), opts)
}

/* @HINT: `GenerateAll(...)` returns only the call map from `Generate(...)`. */
func GenerateAll(root *deptree.Node) (CallMap, error) {
	res, err := Generate(root, Options{})
	if err != nil {
		return nil, err
	}
	return res.Calls, nil
}

func generate(starts []*deptree.Node, opts Options) (*Result, error) {
	if opts.Root == "" {
		opts.Root = deptree.DetectProjectRoot(filepath.Dir(starts[0].Path))
	}

	res := &Result{Calls: make(CallMap), Deps: make(map[string][]string)}

	/* @NOTE: Iterative walk (the dependency graph may be cyclic and deep). */
	visited := make(map[string]bool)
	stack := append([]*deptree.Node(nil), starts...)

	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if node == nil || visited[node.Path] {
			continue
		}
		visited[node.Path] = true

		deps := make([]string, 0, len(node.Dependencies))
		for _, d := range node.Dependencies {
			deps = append(deps, d.Path)
			stack = append(stack, d)
		}
		res.Deps[node.Path] = deps

		fns, err := scanFile(node.Path, opts.Root)
		if err != nil {
			return nil, err
		}
		res.Calls[node.Path] = fns
	}

	return res, nil
}

func scanFile(path, root string) ([]FunctionNode, error) {
	if _, ok := langs.FromPath(path); !ok {
		return nil, nil
	}

	pf, err := parse.ParseFile(path)
	if err != nil {
		return nil, err
	}
	defer pf.Close()

	module := modname.For(modname.Rel(root, path), pf.Source, pf.Lang)
	aliases := pf.AliasMap()
	scan := pf.Scan()

	nodes := make([]FunctionNode, 0, len(scan.Functions)+1)
	seen := make(map[string]int)

	for _, fn := range scan.Functions {
		nodes = append(nodes, FunctionNode{
			ID:        uniqueID(seen, module+":"+fn.Qualified),
			File:      path,
			Name:      fn.Name,
			Qualified: fn.Qualified,
			Line:      fn.StartLine,
			Callees:   normalizeCallees(fn.Calls, aliases),
			Signature: buildSignature(module+":"+fn.Qualified, fn.ParamText),
		})
	}

	// @INFO: Loose, top-level calls (not inside any function).
	if len(scan.ModuleCalls) > 0 {
		nodes = append(nodes, FunctionNode{
			ID:        uniqueID(seen, module+":<global>"),
			File:      path,
			Name:      "<global>",
			Qualified: "<global>",
			Line:      1,
			Callees:   normalizeCallees(scan.ModuleCalls, aliases),
			Signature: buildSignature(module+":global_scope", ""),
		})
	}

	return nodes, nil
}

/* @HINT: `uniqueID(...)` disambiguates overloads / redefinitions: "id", "id#2", ... */
func uniqueID(seen map[string]int, id string) string {
	seen[id]++
	if n := seen[id]; n > 1 {
		return fmt.Sprintf("%s#%d", id, n)
	}
	return id
}

func normalizeCallees(calls []parse.Call, aliases map[string]string) []string {
	out := make([]string, 0, len(calls))
	seen := make(map[string]struct{}, len(calls))

	for _, c := range calls {
		name := parse.ApplyAlias(c.Name, aliases)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}

	return out
}

/*
@HINT:

`buildSignature(...)` creates a lightweight pseudo-signature as a real
FNV-1a hash of name + parameter list.
*/
func buildSignature(name, params string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	_, _ = h.Write([]byte{'('})
	_, _ = h.Write([]byte(params))
	return fmt.Sprintf("sig_%016x", h.Sum64())
}

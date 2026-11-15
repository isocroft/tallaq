/*
!

	@INFO:

	Package deptree builds file-level dependency trees/graphs for JavaScript,
	Python, PHP and Go projects by following static local imports.

	[Improvements]:

	- no package-level mutable state: every `Builder` owns its own graph, so
		calling it twice no longer returns a stale tree;
	- one Node per file (shared dependencies are the same *Node, cycles are
		real back-edges) instead of a chain that silently re-parented
		siblings under the previously visited import;
	- imports are resolved per language (JS index files and extensions,
		Python relative levels and packages, PHP include/require, Go module
		packages) and unresolved local imports are recorded instead of
		aborting the whole analysis.
*/
package deptree

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/isocroft/tallaq/internal/debugging"
	"github.com/isocroft/tallaq/internal/langs"
)

/* @HINT: `DefaultMaxFileSize` is the largest source file the builder will read. */
const DefaultMaxFileSize = 5 << 20 // 5 MiB

/* @HINT: Node is one source file and the local files it depends on. */
type Node struct {
	Path         string
	Language     langs.Language
	Dependencies []*Node
	// Unresolved lists local-looking imports that could not be mapped to a
	// file (e.g. "./missing").
	Unresolved []string
}

/*
@HINT:

`MarshalJSON(...)` emits dependencies as plain paths so that cyclic graphs can
be serialised without infinite recursion.
*/
func (n *Node) MarshalJSON() ([]byte, error) {
	deps := make([]string, len(n.Dependencies))
	for i, d := range n.Dependencies {
		deps[i] = d.Path
	}

	return json.Marshal(struct {
		Path         string         `json:"path"`
		Language     langs.Language `json:"language"`
		Dependencies []string       `json:"dependencies"`
		Unresolved   []string       `json:"unresolved,omitempty"`
	}{n.Path, n.Language, deps, n.Unresolved})
}

/* @HINT: Options configures a Builder. */
type Options struct {
	// ProjectRoot is used to resolve absolute-style imports (Python
	// packages, PHP includes) and to skip vendored directories. When empty
	// it is detected from the first entry point.
	ProjectRoot string
	// MaxFileSize caps the size of files that are read. Default is 5 MiB.
	MaxFileSize int64
}

/* @HINT: `Builder` accumulates a dependency graph across one or more entry points. */
/* @NOTE: Also, Builder is not safe for concurrent use. */
type Builder struct {
	opts    Options
	root    string
	nodes   map[string]*Node
	entries []*Node
	errs    []error
	goMods  map[string]*goModule
}

/* @HINT: `NewBuilder(...)` creates an empty Builder. */
func NewBuilder(opts Options) *Builder {
	if opts.MaxFileSize <= 0 {
		opts.MaxFileSize = DefaultMaxFileSize
	}

	b := &Builder{
		opts:   opts,
		nodes:  make(map[string]*Node),
		goMods: make(map[string]*goModule),
	}

	if opts.ProjectRoot != "" {
		if abs, err := filepath.Abs(opts.ProjectRoot); err == nil {
			b.root = filepath.Clean(abs)
		}
	}

	return b
}

/*
@HINT:

`Root(...)` returns the project root in use ("" until the first Build/BuildDir
when it has not been configured).
*/
func (b *Builder) Root() string { return b.root }

/*
@HINT:

`Build(...)` analyses one entry-point file and everything it (transitively)
imports locally, returning the entry node.
*/
func (b *Builder) Build(entry string) (*Node, error) {
	abs, err := filepath.Abs(entry)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute path for %s: %w", entry, err)
	}
	abs = filepath.Clean(abs)

	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("could not generate tree; reason: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory; use BuildDir", abs)
	}

	if b.root == "" {
		b.root = DetectProjectRoot(filepath.Dir(abs))
	}

	node, err := b.visit(abs)
	if err != nil {
		return nil, fmt.Errorf("could not generate tree; reason: %w", err)
	}

	b.entries = append(b.entries, node)
	return node, nil
}

/*
@HINT:

`BuildDir(...)` analyses every supported source file below dir (skipping hidden
and vendored directories) and returns the combined graph. Also, files that
cannot be read are recorded in `*Graph`. Errors rather than aborting.
*/
func (b *Builder) BuildDir(dir string) (*Graph, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute path for %s: %w", dir, err)
	}
	abs = filepath.Clean(abs)

	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("could not generate graph; reason: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", abs)
	}

	if b.root == "" {
		b.root = abs
	}

	walkErr := filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if p != abs && langs.ShouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		if _, ok := langs.FromPath(p); !ok {
			return nil
		}

		node, err := b.visit(p)
		if err != nil {
			b.errs = append(b.errs, err)
			return nil
		}
		b.entries = append(b.entries, node)

		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	return b.Graph(), nil
}

/* @HINT: `Graph(...)` returns a snapshot of everything built so far. */
func (b *Builder) Graph() *Graph {
	nodes := make(map[string]*Node, len(b.nodes))
	for p, n := range b.nodes {
		nodes[p] = n
	}

	return &Graph{
		Nodes:   nodes,
		Entries: append([]*Node(nil), b.entries...),
		Errors:  append([]error(nil), b.errs...),
	}
}

/*
@HINT:

`visit(...)` returns the node for an absolute file path, analysing it (and,
recursively, its dependencies) the first time it is seen.
*/
func (b *Builder) visit(path string) (*Node, error) {
	if n, ok := b.nodes[path]; ok {
		return n, nil
	}

	debugging.Assert(filepath.IsAbs(path), "deptree: visit requires an absolute path")

	lang, ok := langs.FromPath(path)
	if !ok {
		return nil, fmt.Errorf("unsupported file extension: %q", filepath.Ext(path))
	}

	content, err := b.readSource(path)
	if err != nil {
		return nil, err
	}

	node := &Node{Path: path, Language: lang, Dependencies: []*Node{}}

	/* @NOTE: Register before recursing so cyclic imports terminate. */
	b.nodes[path] = node

	seen := make(map[string]struct{})

	for _, ref := range extractImports(lang, content) {
		res := b.resolve(lang, path, ref)

		if len(res.paths) == 0 {
			if res.local {
				node.Unresolved = append(node.Unresolved, ref.Spec)
			}
			continue
		}

		for _, dep := range res.paths {
			if dep == path {
				continue
			}
			if _, dup := seen[dep]; dup {
				continue
			}
			seen[dep] = struct{}{}

			/* @NOTE: Imports of assets such as .json/.css are not supported for visits. */
			if _, supported := langs.FromPath(dep); !supported {
				continue
			}

			child, err := b.visit(dep)
			if err != nil {
				node.Unresolved = append(node.Unresolved, fmt.Sprintf("%s (%v)", ref.Spec, err))
				continue
			}

			node.Dependencies = append(node.Dependencies, child)
		}
	}

	return node, nil
}

func (b *Builder) readSource(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("failed to read source file %s: %w", path, err)
	}
	if info.Size() > b.opts.MaxFileSize {
		return "", fmt.Errorf("source file %s is too large (%d bytes)", path, info.Size())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read source file %s: %w", path, err)
	}

	return string(data), nil
}

/* @HINT: Generate analyses a single entry point with default options. */
func Generate(entry string) (*Node, error) {
	return NewBuilder(Options{}).Build(entry)
}

/*
@HINT:

`GenerateAll(...)` analyses every source file below `rootDir`
with default options.
*/
func GenerateAll(rootDir string) (*Graph, error) {
	return NewBuilder(Options{}).BuildDir(rootDir)
}

var rootMarkers = []string{
	".git", "go.mod", "package.json", "pyproject.toml",
	"setup.py", "composer.json", "requirements.txt",
}

/*
@HINT:

`DetectProjectRoot(...)` walks upwards from start looking for a
well-known project root marker (.git, go.mod, package.json, ...).
If none is found, start itself is returned.
*/
func DetectProjectRoot(start string) string {
	dir := start
	for {
		for _, marker := range rootMarkers {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return start
		}
		dir = parent
	}
}

/*
!

	@INFO:

	Package `redundancy` finds two kinds of waste in a code base:

	- dead code: unreferenced functions, methods and types (including ones
		that are only used by other dead code), unused imports, statements
		after return/throw/break, and branches guarded by a constant;
	- similar code: functions that do the same thing, found at four levels
		of increasing looseness (see CloneKind).

	Both analyses are static and heuristic. They are tuned to avoid false
	positives (anything referenced by *name* anywhere counts as used), and
	every finding carries a confidence or similarity score so you can decide
	what to act on.
*/
package redundancy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/isocroft/tallaq/internal/langs"
	"github.com/isocroft/tallaq/internal/modname"
	"github.com/isocroft/tallaq/internal/parse"
)

/* @HINT: `Options` configures an Analyzer. */
type Options struct {
	// Root is the project root used for module names and relative paths.
	Root string
	// IncludeTests reports findings inside test files as well. Test files
	// are always scanned so that code only used by tests is not reported
	// as dead.
	IncludeTests bool
	// PrivateOnly drops medium-confidence dead-code findings (exported /
	// public symbols and methods, which may be used from outside the
	// project).
	PrivateOnly bool
	// IgnoreNames are function names that are never reported as unused
	// (entry points and framework hooks). nil uses a built-in list.
	IgnoreNames map[string]struct{}

	// MinLines / MinTokens: functions smaller than this are not compared
	// for similarity (defaults 5 lines, 25 canonical tokens).
	MinLines  int
	MinTokens int
	// NearThreshold is the minimum shingle Jaccard similarity for a
	// near-miss clone (default 0.80).
	NearThreshold float64
	// SemanticThreshold is the minimum behavioural-feature similarity for
	// "same behaviour" (default 0.85).
	SemanticThreshold float64
	// MinFeatures is the minimum size of a behaviour bag (default 6).
	MinFeatures int

	// Workers is the number of files parsed in parallel (default NumCPU).
	Workers int
}

func (o Options) withDefaults() Options {
	if o.MinLines <= 0 {
		o.MinLines = 5
	}
	if o.MinTokens <= 0 {
		o.MinTokens = 25
	}
	if o.NearThreshold <= 0 {
		o.NearThreshold = 0.80
	}
	if o.SemanticThreshold <= 0 {
		o.SemanticThreshold = 0.85
	}
	if o.MinFeatures <= 0 {
		o.MinFeatures = 6
	}
	if o.Workers <= 0 {
		o.Workers = runtime.NumCPU()
	}
	if o.IgnoreNames == nil {
		o.IgnoreNames = defaultIgnoreNames()
	}
	return o
}

func defaultIgnoreNames() map[string]struct{} {
	names := []string{
		// entry points
		"main", "init", "run", "handler", "handle", "boot", "register", "up", "down",
		// Go interface methods commonly satisfied implicitly
		"String", "Error", "Len", "Less", "Swap", "ServeHTTP", "MarshalJSON",
		"UnmarshalJSON", "MarshalText", "UnmarshalText", "Read", "Write", "Close",
		"Scan", "Value", "Unwrap", "Is", "As",
		// JS / React
		"constructor", "toString", "toJSON", "valueOf", "render", "componentDidMount",
		"componentDidUpdate", "componentWillUnmount", "shouldComponentUpdate",
		"getDerivedStateFromProps",
		// Python unittest
		"setUp", "tearDown", "setUpClass", "tearDownClass",
	}

	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}

/* @HINT: `funcInfo` is everything the analyses need to know about one function. */
/* @INFO: It holds no tree-sitter pointers, so the parse tree can be freed right away. */
type funcInfo struct {
	name      string
	qualified string
	line      uint32
	endLine   uint32
	calls     []string // last segment of every call made, with repeats
	exported  bool
	method    bool
	decorated bool
	arity     int

	// similarity data (only when eligible)
	eligible  bool
	shapeHash string
	textHash  string
	tokens    int
	shingles  []uint64
	features  map[string]int
}

type fileInfo struct {
	path        string
	module      string
	lang        langs.Language
	test        bool
	funcs       []funcInfo
	types       []parse.TypeDecl
	imports     []parse.Import
	idents      parse.Identifiers
	unreachable []parse.Unreachable
}

/* @HINT: `Analyzer` collects per-file facts and runs the analyses over the whole project. */
/* @INFO: `ProcessFile(...)/ProcessSource(...)/ProcessDir(...)` are safe for concurrent use. */
type Analyzer struct {
	opts Options

	mu    sync.Mutex
	files []*fileInfo
}

func New(opts Options) *Analyzer {
	return &Analyzer{opts: opts.withDefaults()}
}

/* @HINT: `ProcessFile(...)` parses one file and records its facts. */
func (a *Analyzer) ProcessFile(path string) error {
	pf, err := parse.ParseFile(path)
	if err != nil {
		return err
	}
	defer pf.Close()

	a.process(path, pf)
	return nil
}

/* @HINT: `ProcessSource(...)` is ProcessFile for in-memory source. */
func (a *Analyzer) ProcessSource(path string, src []byte) error {
	pf, err := parse.ParseSource(path, src)
	if err != nil {
		return err
	}
	defer pf.Close()

	a.process(path, pf)
	return nil
}

/* @HINT: `ProcessDir(...)` scans every supported file below root. */
/* @INFO: Unparsable files are reported in the joined error but do not stop the scan. */
func (a *Analyzer) ProcessDir(ctx context.Context, root string) error {
	var files []string

	err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if e.IsDir() {
			if p != root && langs.ShouldSkipDir(e.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		if _, ok := langs.FromPath(p); ok {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return err
	}

	jobs := make(chan string)
	var (
		wg   sync.WaitGroup
		emu  sync.Mutex
		errs []error
	)

	for i := 0; i < a.opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				if err := a.ProcessFile(p); err != nil {
					emu.Lock()
					errs = append(errs, fmt.Errorf("%s: %w", p, err))
					emu.Unlock()
				}
			}
		}()
	}

	for _, p := range files {
		if ctx.Err() != nil {
			break
		}
		jobs <- p
	}
	close(jobs)
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return err
	}

	sort.Slice(errs, func(i, j int) bool { return errs[i].Error() < errs[j].Error() })
	return errors.Join(errs...)
}

func (a *Analyzer) process(path string, pf *parse.File) {
	info := &fileInfo{
		path:        path,
		module:      modname.For(modname.Rel(a.opts.Root, path), pf.Source, pf.Lang),
		lang:        pf.Lang,
		test:        isTestFile(path),
		idents:      pf.Identifiers(),
		imports:     pf.Imports(),
		types:       pf.Types(),
		unreachable: pf.Unreachable(),
	}

	compare := !info.test || a.opts.IncludeTests

	for _, fn := range pf.Scan().Functions {
		fi := funcInfo{
			name:      fn.Name,
			qualified: fn.Qualified,
			line:      fn.StartLine,
			endLine:   fn.EndLine,
			exported:  fn.Exported,
			method:    fn.IsMethod,
			decorated: fn.Decorated,
			arity:     len(fn.Params),
		}
		for _, c := range fn.Calls {
			fi.calls = append(fi.calls, lastSegment(c.Name))
		}

		if compare && int(fn.EndLine-fn.StartLine)+1 >= a.opts.MinLines {
			canon := pf.Canonicalize(fn)
			if len(canon.Tokens) >= a.opts.MinTokens {
				fi.eligible = true
				fi.shapeHash = hashHex(canon.Shape)
				fi.textHash = hashHex(canon.Text)
				fi.tokens = len(canon.Tokens)
				fi.shingles = shingle(canon.Tokens)
				fi.features = canon.Features
			}
		}

		info.funcs = append(info.funcs, fi)
	}

	a.mu.Lock()
	a.files = append(a.files, info)
	a.mu.Unlock()
}

/* @HINT: `snapshot(...)` returns the collected files sorted by path. */
func (a *Analyzer) snapshot() []*fileInfo {
	a.mu.Lock()
	files := append([]*fileInfo(nil), a.files...)
	a.mu.Unlock()

	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	return files
}

func (fi *fileInfo) id(fn *funcInfo) string {
	return fi.module + ":" + fn.qualified
}

func lastSegment(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

func hashHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

func isTestFile(p string) bool {
	base := filepath.Base(p)

	switch {
	case strings.HasSuffix(base, "_test.go"):
		return true
	case strings.HasSuffix(base, ".py") && (strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py")):
		return true
	case strings.Contains(base, ".test.") || strings.Contains(base, ".spec."):
		return true
	case strings.HasSuffix(base, "Test.php"):
		return true
	}
	return false
}

/* @HINT: `Report` is the combined result of both analyses. */
type Report struct {
	Dead   []DeadItem   `json:"dead_code,omitempty"`
	Clones []CloneGroup `json:"similar_code,omitempty"`
}

/* @HINT: Analyze(...)` runs both analyses. */
func (a *Analyzer) Analyze() *Report {
	r := &Report{Dead: a.AnalyzeDead(), Clones: a.AnalyzeClones()}
	if r.Dead == nil {
		r.Dead = []DeadItem{}
	}
	if r.Clones == nil {
		r.Clones = []CloneGroup{}
	}
	return r
}

/*
!

	@INFO:

	Package clumper detects "clumps": groups of items that keep appearing
	together, which usually means a missing abstraction.

		DataClump        the same parameters in several function signatures (@TODO: same primitives in several source files)
		StructClump      the same fields in several Go structs
		ClassClump       the same fields in several classes
		DependencyClump  the same imports in several files
		CallClump        the same callees in several functions

	Here every item group is treated as a transaction and closed frequent itemsets are mined with
	package itemset, which also finds clumps that are only *part* of each
	signature (x, y, z inside (x, y, z, color) and (x, y, z, size)).
*/
package clumper

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

	"github.com/isocroft/tallaq/internal/itemset"
	"github.com/isocroft/tallaq/internal/langs"
	"github.com/isocroft/tallaq/internal/parse"
)

/* @HINT: `ClumpType` is the kind of clump. */
type ClumpType uint8

const (
	DataClump ClumpType = iota
	StructClump
	ClassClump
	DependencyClump
	CallClump
)

func (c ClumpType) String() string {
	switch c {
	case DataClump:
		return "data"
	case StructClump:
		return "struct"
	case ClassClump:
		return "class"
	case DependencyClump:
		return "dependency"
	case CallClump:
		return "call"
	}
	return "unknown"
}

/* @HINT: `MarshalText(...)` makes ClumpType serialise as its name in JSON. */
func (c ClumpType) MarshalText() ([]byte, error) { return []byte(c.String()), nil }

/* @NOTE: Aliases kept for compatibility with the gist's type names. */
type (
	Parameter  = parse.Param
	Field      = parse.Field
	Dependency = parse.Import
)

/* @HINT: `ClumpOccurrence` is one place where a clump was found. */
type ClumpOccurrence struct {
	File        string         `json:"file"`
	Module      string         `json:"module"`       // "pkg.utils:helpers.py"
	Container   string         `json:"container"`    // "function", "struct", "class" or "file"
	ContainerID string         `json:"container_id"` // "pkg.utils:helpers.py:Runner.run"
	Language    langs.Language `json:"language"`
	StartLine   uint32         `json:"start_line"`
	EndLine     uint32         `json:"end_line"`
	Signature   string         `json:"signature"` // the clump items, comma separated
	Hash        string         `json:"hash"`      // stable id of the clump
	Kind        ClumpType      `json:"kind"`
}

/* @HINT: `Clump` is a group of items shared by several containers. */
type Clump struct {
	Kind        ClumpType         `json:"kind"`
	Items       []string          `json:"items"`
	Hash        string            `json:"hash"`
	Occurrences []ClumpOccurrence `json:"occurrences"`
}

/* @HINT: `Options` configures a `Detector`. */
type Options struct {
	// Threshold is the minimum number of containers sharing a clump
	// (default 3).
	Threshold int
	// MinSize is the minimum number of items in a clump (default 3).
	MinSize int
	// Root is the project root, used to build module names.
	Root string
	// IncludeTests also analyses test files (default: skipped).
	IncludeTests bool
	// IgnoreCalls lists callee names ignored by call clumps; nil uses a
	// small built-in list of ubiquitous builtins/logging calls.
	IgnoreCalls map[string]struct{}
	// Workers is the number of files parsed in parallel (default NumCPU).
	Workers int
	// MaxCandidates bounds mining memory (see itemset.Options).
	MaxCandidates int
}

func (o Options) withDefaults() Options {
	if o.Threshold <= 0 {
		o.Threshold = 3
	}
	if o.MinSize <= 0 {
		o.MinSize = 3
	}
	if o.IgnoreCalls == nil {
		o.IgnoreCalls = defaultIgnoredCalls()
	}
	if o.Workers <= 0 {
		o.Workers = runtime.NumCPU()
	}
	return o
}

func defaultIgnoredCalls() map[string]struct{} {
	names := []string{
		"print", "println", "len", "range", "str", "int", "float", "bool",
		"list", "dict", "set", "tuple", "isinstance", "super", "open",
		"append", "make", "new", "panic", "recover", "copy", "cap", "delete",
		"close", "require", "parseInt", "parseFloat", "String", "Number",
		"Boolean", "Array", "Object", "isset", "empty", "count", "array",
		"fmt.Println", "fmt.Printf", "fmt.Sprintf", "fmt.Errorf",
		"console.log", "console.error", "console.warn",
	}

	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}

type transaction struct {
	occ   ClumpOccurrence
	items []string
}

/*
@HINT:

`Detector` collects item groups from source files and mines clumps.
`ProcessFile(...)/ProcessSource(...)/ProcessDir(...)` are safe
for concurrent use.
*/
type Detector struct {
	opts      Options
	mu        sync.Mutex
	db        map[ClumpType][]transaction
	truncated bool
}

func New(opts Options) *Detector {
	return &Detector{opts: opts.withDefaults(), db: make(map[ClumpType][]transaction)}
}

/* @HINT: `Truncated(...)` reports whether mining hit the candidate cap during `Analyze(...)`. */
func (d *Detector) Truncated() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.truncated
}

/* @HINT: `ProcessFile(...)` parses one file and records its item groups. */
func (d *Detector) ProcessFile(path string) error {
	pf, err := parse.ParseFile(path)
	if err != nil {
		return err
	}
	defer pf.Close()

	d.process(path, pf)
	return nil
}

/* @HINT: `ProcessSource(...)` is ProcessFile for in-memory source. */
func (d *Detector) ProcessSource(path string, src []byte) error {
	pf, err := parse.ParseSource(path, src)
	if err != nil {
		return err
	}
	defer pf.Close()

	d.process(path, pf)
	return nil
}

/*
@HINT:

`ProcessDir(...)` processes every supported file below root. Files that fail to
parse are reported in the returned (joined) error but do not stop the
scan.
*/
func (d *Detector) ProcessDir(ctx context.Context, root string) error {
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

		if _, ok := langs.FromPath(p); !ok {
			return nil
		}
		if !d.opts.IncludeTests && isTestFile(p) {
			return nil
		}

		files = append(files, p)
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

	for i := 0; i < d.opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				if err := d.ProcessFile(p); err != nil {
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

/*
@HINT:

`Analyze(...)` mines all recorded groups and returns the clumps,
most widespread first.
*/
func (d *Detector) Analyze() []Clump {
	d.mu.Lock()
	defer d.mu.Unlock()

	var out []Clump

	for kind := DataClump; kind <= CallClump; kind++ {
		txs := d.db[kind]
		if len(txs) == 0 {
			continue
		}

		/* @INFO: Deterministic input order regardless of worker scheduling. */
		sort.SliceStable(txs, func(i, j int) bool {
			a, b := txs[i].occ, txs[j].occ
			if a.File != b.File {
				return a.File < b.File
			}
			return a.StartLine < b.StartLine
		})

		rows := make([][]string, len(txs))
		for i, t := range txs {
			rows[i] = t.items
		}

		results, truncated := itemset.MineClosed(rows, itemset.Options{
			MinSupport:    d.opts.Threshold,
			MinSize:       d.opts.MinSize,
			MaxCandidates: d.opts.MaxCandidates,
		})
		if truncated {
			d.truncated = true
		}

		for _, r := range results {
			sig := strings.Join(r.Items, ", ")
			hash := hashItems(kind, r.Items)

			clump := Clump{Kind: kind, Items: r.Items, Hash: hash}
			for _, idx := range r.Members {
				occ := txs[idx].occ
				occ.Signature = sig
				occ.Hash = hash
				clump.Occurrences = append(clump.Occurrences, occ)
			}
			out = append(out, clump)
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if len(a.Occurrences) != len(b.Occurrences) {
			return len(a.Occurrences) > len(b.Occurrences)
		}
		if len(a.Items) != len(b.Items) {
			return len(a.Items) > len(b.Items)
		}
		return a.Hash < b.Hash
	})

	return out
}

func hashItems(kind ClumpType, items []string) string {
	sum := sha256.Sum256([]byte(kind.String() + "\x00" + strings.Join(items, "\x00")))
	return hex.EncodeToString(sum[:6])
}

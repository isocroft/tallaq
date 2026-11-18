/*
	@INFO:

	Package `health` measures how risky a function is to change:

	- cyclomatic complexity (Thomas McCabe): the number of independent paths
		through a function, i.e. 1 + its decision points;
	- the CRAP score (Change Risk Anti-Patterns), which combines that
		complexity with test coverage that is supplied from outside:

		CRAP(m) = comp(m)^2 * (1 - cov(m))^3 + comp(m)

		where cov is the fraction (0..1) of the method's statements covered by
		tests. Fully covered code scores its plain complexity; untested code
		scores comp^2 + comp. A score of CRAPThreshold (30) or more is
		conventionally considered risky.

	The package never runs tests itself. Coverage is provided by the caller,
	either directly (CRAP), through a CoverageFunc, or from per-line
	coverage data (LineCoverage / FileCoverage) that you have converted from
	a coverage report (go test -coverprofile, coverage.py, lcov, clover, ...).
*/
package health

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/isocroft/techdebt/internal/langs"
	"github.com/isocroft/techdebt/internal/parse"
)

/* 
	@HINT:

	`CRAPThreshold` is the score at or above which a function is considered
	risky (the value suggested by the original CRAP metric).
*/
const CRAPThreshold = 30.0

/* @HINT: `Rating` is a coarse reading of a complexity value (SEI risk bands). */
type Rating string

const (
	Simple    Rating = "simple"    // 1-10
	Moderate  Rating = "moderate"  // 11-20
	High      Rating = "high"      // 21-50
	VeryHigh  Rating = "very-high" // > 50
	NoRating  Rating = ""
)

/* @HINT: `RateComplexity` maps a cyclomatic complexity to a `Rating`. */
func RateComplexity(complexity int) Rating {
	switch {
	case complexity < 1:
		return NoRating
	case complexity <= 10:
		return Simple
	case complexity <= 20:
		return Moderate
	case complexity <= 50:
		return High
	}
	return VeryHigh
}

/*
	@HINT:

	`CRAP(...)` computes the Change Risk Anti-Patterns score. coverage is a
	fraction between 0 and 1 (use Percent to convert from 0-100).
*/
func CRAP(complexity int, coverage float64) (float64, error) {
	if complexity < 1 {
		return 0, fmt.Errorf("cyclomatic complexity must be at least 1, got %d", complexity)
	}
	if math.IsNaN(coverage) || coverage < 0 || coverage > 1 {
		return 0, fmt.Errorf("coverage must be a fraction between 0 and 1, got %v (use Percent for 0-100 values)", coverage)
	}

	c := float64(complexity)
	return c*c*math.Pow(1-coverage, 3) + c, nil
}

// Percent converts a 0-100 coverage percentage to the 0-1 fraction that
// CRAP expects.
func Percent(p float64) float64 { return p / 100 }

// CoverageFunc returns the statement-coverage fraction (0..1) of a
// function, or ok=false when nothing is known about it.
type CoverageFunc func(file string, fn *parse.Function) (coverage float64, ok bool)

// LineCoverage maps a line number to whether it was covered. Only lines
// that the coverage tool instrumented (i.e. that contain statements)
// should be present; blank lines and comments must be left out.
type LineCoverage map[int]bool

// Range returns the covered fraction of the instrumented lines within
// [start, end]. ok is false when the range contains no instrumented line.
func (lc LineCoverage) Range(start, end uint32) (float64, bool) {
	total, covered := 0, 0

	for line, hit := range lc {
		if line < int(start) || line > int(end) {
			continue
		}
		total++
		if hit {
			covered++
		}
	}

	if total == 0 {
		return 0, false
	}
	return float64(covered) / float64(total), true
}

// Func adapts the coverage of a single file to a CoverageFunc.
func (lc LineCoverage) Func() CoverageFunc {
	return func(_ string, fn *parse.Function) (float64, bool) {
		return lc.Range(fn.StartLine, fn.EndLine)
	}
}

// FileCoverage maps file paths to their line coverage. Keys may be
// absolute paths or paths relative to the project root; a key matches a
// file when it equals it or is a trailing path of it. For any other naming
// scheme (e.g. Go import paths) write a CoverageFunc yourself.
type FileCoverage map[string]LineCoverage

// Func adapts the coverage of many files to a CoverageFunc.
func (fc FileCoverage) Func() CoverageFunc {
	return func(file string, fn *parse.Function) (float64, bool) {
		lc := fc.lookup(file)
		if lc == nil {
			return 0, false
		}
		return lc.Range(fn.StartLine, fn.EndLine)
	}
}

func (fc FileCoverage) lookup(file string) LineCoverage {
	file = filepath.ToSlash(file)

	if lc, ok := fc[file]; ok {
		return lc
	}

	var (
		bestKey string
		best    LineCoverage
	)
	for key, lc := range fc {
		k := strings.TrimPrefix(filepath.ToSlash(key), "./")
		if k == "" {
			continue
		}
		if (file == k || strings.HasSuffix(file, "/"+k)) && len(k) > len(bestKey) {
			bestKey, best = k, lc
		}
	}

	return best
}

// --- cyclomatic complexity -------------------------------------------------

func set(items ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(items))
	for _, it := range items {
		m[it] = struct{}{}
	}
	return m
}

/* @HINT: `decisionNodes` are the syntax nodes that add one path each. */
var decisionNodes = map[langs.Language]map[string]struct{}{
	langs.Go: set(
		"if_statement", "for_statement",
		"expression_case", "type_case", "communication_case", // switch / type switch / select (not default)
	),
	langs.JavaScript: set(
		"if_statement", "for_statement", "for_in_statement", // for...in and for...of
		"while_statement", "do_statement", "switch_case", // not switch_default
		"catch_clause", "ternary_expression",
	),
	langs.Python: set(
		"if_statement", "elif_clause", "for_statement", "while_statement",
		"except_clause", "conditional_expression", "case_clause", // match/case
		"for_in_clause", "if_clause", // comprehensions
	),
	langs.PHP: set(
		"if_statement", "else_if_clause", "for_statement", "foreach_statement",
		"while_statement", "do_statement", "case_statement", // not default_statement
		"catch_clause", "conditional_expression", "match_conditional_expression",
	),
}

/* @HINT: `booleanOps` are the short-circuit operators; each one is a decision point. */
var booleanOps = map[langs.Language]map[string]struct{}{
	langs.Go:         set("&&", "||"),
	langs.JavaScript: set("&&", "||", "??"),
	langs.Python:     set("and", "or"),
	langs.PHP:        set("&&", "||", "and", "or", "xor", "??"),
}

func isBooleanOp(pf *parse.File, n *sitter.Node) bool {
	t := n.Type()
	if pf.Lang == langs.Python {
		if t != "boolean_operator" {
			return false
		}
	} else if t != "binary_expression" {
		return false
	}

	_, ok := booleanOps[pf.Lang][operatorText(pf, n)]
	return ok
}

func operatorText(pf *parse.File, n *sitter.Node) string {
	if op := n.ChildByFieldName("operator"); op != nil {
		return strings.ToLower(strings.TrimSpace(pf.Text(op)))
	}

	/*
		@NOTE:
	
		Some grammar versions do not name the field: the operator is the
		first anonymous child.
	*/
	for i := 0; i < int(n.ChildCount()); i++ {
		if c := n.Child(i); c != nil && !c.IsNamed() {
			return strings.ToLower(strings.TrimSpace(pf.Text(c)))
		}
	}
	return ""
}

func namedSpans(scan *parse.Scan) map[[2]uint32]struct{} {
	spans := make(map[[2]uint32]struct{}, len(scan.Functions))
	for _, fn := range scan.Functions {
		if node := fn.Node(); node != nil {
			spans[[2]uint32{node.StartByte(), node.EndByte()}] = struct{}{}
		}
	}
	return spans
}

/*
	@HINT:

	`Cyclomatic(...)` returns the cyclomatic complexity of fn: 1 plus one
	for every branch, loop, case, catch, ternary and short-circuit operator.

	Nested *named* functions are measured separately and excluded; anonymous
	closures and callbacks count towards the function that contains them.
	scan must come from pf.Scan() (the same call that produced fn) and pf must
	still be open. It returns 0 when fn has no syntax node.
*/
func Cyclomatic(pf *parse.File, scan *parse.Scan, fn *parse.Function) int {
	return cyclomatic(pf, namedSpans(scan), fn)
}

func cyclomatic(pf *parse.File, named map[[2]uint32]struct{}, fn *parse.Function) int {
	root := fn.Node()
	if root == nil {
		return 0
	}

	decisions := decisionNodes[pf.Lang]
	count := 1

	var visit func(n *sitter.Node, isRoot bool)
	visit = func(n *sitter.Node, isRoot bool) {
		if n == nil {
			return
		}

		if !isRoot {
			if _, nested := named[[2]uint32{n.StartByte(), n.EndByte()}]; nested {
				return // measured on its own
			}
		}

		if n.IsNamed() {
			if _, ok := decisions[n.Type()]; ok || isBooleanOp(pf, n) {
				count++
			}
		}

		for i := 0; i < int(n.ChildCount()); i++ {
			visit(n.Child(i), false)
		}
	}

	visit(root, true)

	return count
}

/* @HINT: Metrics is the health of one function. */
type Metrics struct {
	File      string `json:"file"`
	Name      string `json:"name"`
	Qualified string `json:"qualified"`
	Line      uint32 `json:"line"`
	EndLine   uint32 `json:"end_line"`

	Complexity int    `json:"complexity"`
	Rating     Rating `json:"rating"`

	// Coverage, CRAP and Risky are only meaningful when HasCoverage is true.
	HasCoverage bool    `json:"has_coverage"`
	Coverage    float64 `json:"coverage"` // 0..1
	CRAP        float64 `json:"crap"`
	Risky       bool    `json:"risky"` // CRAP >= CRAPThreshold
}

// Analyze measures every named function and method of an open file. cov may
// be nil, in which case only complexity is reported; functions for which
// cov reports ok=false also get no CRAP score.
func Analyze(pf *parse.File, cov CoverageFunc) ([]Metrics, error) {
	scan := pf.Scan()
	named := namedSpans(scan)

	out := make([]Metrics, 0, len(scan.Functions))

	for _, fn := range scan.Functions {
		m := Metrics{
			File: pf.Path, Name: fn.Name, Qualified: fn.Qualified,
			Line: fn.StartLine, EndLine: fn.EndLine,
			Complexity: cyclomatic(pf, named, fn),
		}
		m.Rating = RateComplexity(m.Complexity)

		if cov != nil {
			if c, ok := cov(pf.Path, fn); ok {
				score, err := CRAP(m.Complexity, c)
				if err != nil {
					return nil, fmt.Errorf("%s: %s: %w", pf.Path, fn.Qualified, err)
				}
				m.HasCoverage, m.Coverage, m.CRAP = true, c, score
				m.Risky = score >= CRAPThreshold
			}
		}

		out = append(out, m)
	}

	return out, nil
}

/* @HINT: `AnalyzePath(...)` parses a file from disk and measures it. */
func AnalyzePath(path string, cov CoverageFunc) ([]Metrics, error) {
	pf, err := parse.ParseFile(path)
	if err != nil {
		return nil, err
	}
	defer pf.Close()

	return Analyze(pf, cov)
}

/* @HINT: `AnalyzeSource(...)` measures in-memory source; path selects the language. */
func AnalyzeSource(path string, src []byte, cov CoverageFunc) ([]Metrics, error) {
	pf, err := parse.ParseSource(path, src)
	if err != nil {
		return nil, err
	}
	defer pf.Close()

	return Analyze(pf, cov)
}

/*
	@HINT:

	`SortByRisk(...)` orders metrics worst-first: functions with a CRAP score
	by descending score, then the rest by descending complexity.
*/
func SortByRisk(ms []Metrics) {
	sort.SliceStable(ms, func(i, j int) bool {
		a, b := ms[i], ms[j]

		if a.HasCoverage != b.HasCoverage {
			return a.HasCoverage
		}
		if a.HasCoverage && a.CRAP != b.CRAP {
			return a.CRAP > b.CRAP
		}
		if a.Complexity != b.Complexity {
			return a.Complexity > b.Complexity
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
}
package redundancy

import (
	"sort"
	"strings"

	"github.com/isocroft/tallaq/internal/langs"
	"github.com/isocroft/tallaq/internal/parse"
)

/* @HINT: `Confidence` says how sure we are that something really is unused. */
type Confidence string

const (
	// High: private to its module and never referenced anywhere.
	High Confidence = "high"
	// Medium: never referenced in this project, but exported/public or a
	// method, so it may be used from outside or through dynamic dispatch.
	Medium Confidence = "medium"
)

/* @HINT: `DeadKind` classifies a dead-code finding. */
type DeadKind string

const (
	UnusedFunction  DeadKind = "unused-function"
	UnusedMethod    DeadKind = "unused-method"
	UnusedType      DeadKind = "unused-type"
	UnusedImport    DeadKind = "unused-import"
	UnreachableCode DeadKind = "unreachable-code"
	DeadBranch      DeadKind = "dead-branch"
)

/* @HINT: `DeadItem` is one dead-code finding. */
type DeadItem struct {
	Kind       DeadKind       `json:"kind"`
	Confidence Confidence     `json:"confidence"`
	File       string         `json:"file"`
	Name       string         `json:"name"`
	Line       uint32         `json:"line"`
	EndLine    uint32         `json:"end_line"`
	Language   langs.Language `json:"language"`
	Reason     string         `json:"reason"`
	// Transitive is true when the item is only used by other dead code.
	Transitive bool `json:"transitive,omitempty"`
}

/*
@HINT:

`AnalyzeDead(...)` reports dead code across everything processed so far.

A symbol counts as used when its name occurs anywhere in the project
outside its own declaration (calls, callbacks, exports, member access,
even a string such as "handler"). That is deliberately generous: it can
hide some dead code when two things share a name, but it will not flag
live code that is invoked dynamically.
*/
func (a *Analyzer) AnalyzeDead() []DeadItem {
	files := a.snapshot()

	refs := make(map[string]int)
	strs := make(map[string]int)
	for _, fi := range files {
		for k, v := range fi.idents.Names {
			refs[k] += v
		}
		for k, v := range fi.idents.Strings {
			strs[k] += v
		}
	}

	/* @NOTE: Recursion: a function that only calls itself is still dead. */
	selfCalls := make(map[string]int)
	type candidate struct {
		fi *fileInfo
		fn *funcInfo
	}
	var cands []candidate

	for _, fi := range files {
		for i := range fi.funcs {
			fn := &fi.funcs[i]
			for _, c := range fn.calls {
				if c == fn.name {
					selfCalls[fn.name]++
				}
			}

			if fi.test && !a.opts.IncludeTests {
				continue
			}
			if a.implicitEntry(fi, fn) {
				continue
			}
			cands = append(cands, candidate{fi, fn})
		}
	}

	/* @INFO: Fixpoint: removing dead functions can orphan the functions they call. */
	rounds := make(map[*funcInfo]int)
	var order []candidate

	for round := 1; ; round++ {
		var newly []candidate

		for _, c := range cands {
			if _, done := rounds[c.fn]; done {
				continue
			}
			name := c.fn.name
			if refs[name]-selfCalls[name] > 0 || strs[name] > 0 {
				continue
			}
			newly = append(newly, c)
		}

		if len(newly) == 0 {
			break
		}

		for _, c := range newly {
			rounds[c.fn] = round
			order = append(order, c)

			for _, callee := range c.fn.calls {
				if callee == c.fn.name {
					continue // already accounted for by `selfCalls`
				}
				if refs[callee] > 0 {
					refs[callee]--
				}
			}
		}
	}

	var out []DeadItem

	for _, c := range order {
		kind, conf := UnusedFunction, High
		if c.fn.method {
			kind, conf = UnusedMethod, Medium
		} else if c.fn.exported {
			conf = Medium
		}
		if conf == Medium && a.opts.PrivateOnly {
			continue
		}

		reason := "never referenced anywhere in the project"
		if rounds[c.fn] > 1 {
			reason = "only referenced from code that is itself unused"
		}

		out = append(out, DeadItem{
			Kind: kind, Confidence: conf, File: c.fi.path, Name: c.fi.id(c.fn),
			Line: c.fn.line, EndLine: c.fn.endLine, Language: c.fi.lang,
			Reason: reason, Transitive: rounds[c.fn] > 1,
		})
	}

	for _, fi := range files {
		if fi.test && !a.opts.IncludeTests {
			continue
		}

		out = append(out, a.deadTypes(fi, refs, strs)...)
		out = append(out, deadImports(fi)...)

		for _, u := range fi.unreachable {
			kind := UnreachableCode
			if u.Kind == "constant-condition" {
				kind = DeadBranch
			}
			out = append(out, DeadItem{
				Kind: kind, Confidence: High, File: fi.path, Name: fi.module,
				Line: u.Line, EndLine: u.EndLine, Language: fi.lang, Reason: u.Detail,
			})
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Name < out[j].Name
	})

	return out
}

func (a *Analyzer) deadTypes(fi *fileInfo, refs, strs map[string]int) []DeadItem {
	var out []DeadItem

	for _, t := range fi.types {
		if refs[t.Name] > 0 || strs[t.Name] > 0 {
			continue
		}

		conf := High
		if t.Exported {
			conf = Medium
		}
		if conf == Medium && a.opts.PrivateOnly {
			continue
		}

		out = append(out, DeadItem{
			Kind: UnusedType, Confidence: conf, File: fi.path, Name: fi.module + ":" + t.Name,
			Line: t.Line, EndLine: t.EndLine, Language: fi.lang,
			Reason: "type is never referenced anywhere in the project",
		})
	}

	return out
}

/* @HINT: `deadImports(...)` reports imports whose local name is never used in the file. */
/* @INFO: Go is skipped (the compiler already rejects unused imports). */
func deadImports(fi *fileInfo) []DeadItem {
	if fi.lang == langs.Go || strings.HasSuffix(fi.path, "__init__.py") {
		return nil
	}

	var out []DeadItem
	seen := make(map[string]bool)

	for _, imp := range fi.imports {
		if imp.Path == "__future__" {
			continue
		}

		local := imp.Local
		if local == "" && fi.lang == langs.Python && imp.Name == "" && imp.Path != "" && !strings.HasPrefix(imp.Path, ".") {
			// `import a.b.c` binds "a".
			local = strings.SplitN(imp.Path, ".", 2)[0]
		}
		if local == "" || local == "React" {
			continue
		}

		threshold := 0
		if imp.Require {
			threshold = 1 // the `const x = require()` declaration mentions it once
		}

		if fi.idents.Names[local] > threshold || fi.idents.Strings[local] > 0 {
			continue
		}

		key := local + "\x00" + imp.Path
		if seen[key] {
			continue
		}
		seen[key] = true

		out = append(out, DeadItem{
			Kind: UnusedImport, Confidence: High, File: fi.path, Name: importLabel(imp, local),
			Line: imp.Line, EndLine: imp.Line, Language: fi.lang,
			Reason: "imported name \"" + local + "\" is never used in this file",
		})
	}

	return out
}

func importLabel(imp parse.Import, local string) string {
	if imp.Name != "" && imp.Name != "default" {
		return imp.Path + "." + imp.Name
	}
	if local != "" && local != imp.Path {
		return imp.Path + " as " + local
	}
	return imp.Path
}

/*
@HINT:

`implicitEntry(...)` reports functions that are invoked by a runtime or
framework rather than by project code, and so must never be reported.
*/
func (a *Analyzer) implicitEntry(fi *fileInfo, fn *funcInfo) bool {
	if fn.decorated {
		return true /* @INFO: routes, fixtures, properties, ... */
	}
	if _, ok := a.opts.IgnoreNames[fn.name]; ok {
		return true
	}

	name := fn.name
	switch fi.lang {
	case langs.Go:
		for _, prefix := range []string{"Test", "Benchmark", "Example", "Fuzz"} {
			if strings.HasPrefix(name, prefix) {
				return true
			}
		}
	case langs.Python:
		if strings.HasPrefix(name, "test_") || (strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__")) {
			return true
		}
	case langs.PHP:
		if strings.HasPrefix(name, "__") {
			return true
		}
	}

	return false
}

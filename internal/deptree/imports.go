package deptree

import (
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"

	"github.com/isocroft/tallaq/internal/langs"
)

/* @HINT: `importRef` is one import statement as written in source. */
type importRef struct {
	Spec  string   // module specifier exactly as written ("./a", ".pkg", "app/util")
	Names []string // Python "from X import a, b": the imported names
}

// @NOTE: regexes are a pragmatic choice for import discovery (fast, no cgo).
// Statements are anchored to line starts where possible so that commented
// out imports are not picked up.

/* @TODO: Cater to dynamic imports later on */
var (
	jsFromRe       = regexp.MustCompile(`(?m)^\s*(?:import|export)\b[^'";]*?\bfrom\s*['"]([^'"\n]+)['"]`)
	jsSideEffectRe = regexp.MustCompile(`(?m)^\s*import\s*['"]([^'"\n]+)['"]`)
	jsRequireRe    = regexp.MustCompile(`\brequire\s*\(\s*['"]([^'"\n]+)['"]\s*\)`)
	jsDynamicRe    = regexp.MustCompile(`\bimport\s*\(\s*['"]([^'"\n]+)['"]\s*\)`)

	phpIncludeRe = regexp.MustCompile(
		`\b(?:include|require)(?:_once)?\b\s*\(?\s*((?:__DIR__|dirname\s*\(\s*__FILE__\s*\))\s*\.\s*)?['"]([^'"\n]+)['"]`,
	)

	pyFromRe    = regexp.MustCompile(`(?m)^[ \t]*from[ \t]+(\.+[\w.]*|[A-Za-z_][\w.]*)[ \t]+import[ \t]+(\([^)]*\)|[^\n#]+)`)
	pyImportRe  = regexp.MustCompile(`(?m)^[ \t]*import[ \t]+([^\n#]+)`)
	pyCommentRe = regexp.MustCompile(`#[^\n]*`)
	pyNameRe    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	pyDottedRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)
)

func extractImports(lang langs.Language, content string) []importRef {
	switch lang {
	case langs.JavaScript:
		return extractJSImports(content)
	case langs.Python:
		return extractPythonImports(content)
	case langs.PHP:
		return extractPHPImports(content)
	case langs.Go:
		return extractGoImports(content)
	}
	// @SMELL: should return an empty array instead of `nil`
	// @FIXME: change to empty array
	return nil
}

func extractJSImports(content string) []importRef {
	var refs []importRef
	seen := make(map[string]struct{})

	add := func(spec string) {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			return
		}
		if _, dup := seen[spec]; dup {
			return
		}
		seen[spec] = struct{}{}
		refs = append(refs, importRef{Spec: spec})
	}

	for _, re := range []*regexp.Regexp{jsFromRe, jsSideEffectRe, jsRequireRe, jsDynamicRe} {
		for _, m := range re.FindAllStringSubmatch(content, -1) {
			add(m[1])
		}
	}

	return refs
}

func extractPythonImports(content string) []importRef {
	var refs []importRef

	for _, m := range pyFromRe.FindAllStringSubmatch(content, -1) {
		refs = append(refs, importRef{Spec: m[1], Names: parsePythonNames(m[2])})
	}

	for _, m := range pyImportRe.FindAllStringSubmatch(content, -1) {
		for _, part := range strings.Split(m[1], ",") {
			name := strings.TrimSpace(part)
			if i := strings.Index(name, " as "); i >= 0 {
				name = strings.TrimSpace(name[:i])
			}
			if pyDottedRe.MatchString(name) {
				refs = append(refs, importRef{Spec: name})
			}
		}
	}

	return refs
}

func parsePythonNames(raw string) []string {
	raw = pyCommentRe.ReplaceAllString(raw, "")
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "(")
	raw = strings.TrimSuffix(raw, ")")

	var names []string
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if i := strings.Index(name, " as "); i >= 0 {
			name = strings.TrimSpace(name[:i])
		}
		if name == "" || name == "*" || !pyNameRe.MatchString(name) {
			continue
		}
		names = append(names, name)
	}

	return names
}

func extractPHPImports(content string) []importRef {
	var refs []importRef

	for _, m := range phpIncludeRe.FindAllStringSubmatch(content, -1) {
		spec := m[2]
		if strings.Contains(spec, "://") {
			continue
		}
		/* @NOTE: `__DIR__ . '/x.php'` means "relative to this file". */
		if m[1] != "" {
			spec = "./" + strings.TrimLeft(spec, "/")
		}
		refs = append(refs, importRef{Spec: spec})
	}

	return refs
}

/* @INFO: extractGoImports uses the real Go parser (stdlib) instead of a regex. */
func extractGoImports(content string) []importRef {
	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, "", content, parser.ImportsOnly)
	if file == nil {
		_ = err
		return nil
	}

	refs := make([]importRef, 0, len(file.Imports))
	for _, imp := range file.Imports {
		spec, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		refs = append(refs, importRef{Spec: spec})
	}

	return refs
}

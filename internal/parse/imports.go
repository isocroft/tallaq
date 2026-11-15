package parse

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/isocroft/tallaq/internal/langs"
)

// Import is one imported module binding.
//
// The gist's tree-sitter query only handled Go and used the wrong node
// name for its path (`string_literal`; Go uses
// `interpreted_string_literal`). Imports are now read from the syntax tree
// for all four languages.
type Import struct {
	Path  string // module as written: "./api", "numpy", "project/service"
	Name  string // imported member ("" for whole-module, "default" for JS defaults)
	Local string // name bound in this file ("" for side-effect imports)
	Line  uint32
}

// Original returns what Local refers to: "pkg.fn" for a named import,
// otherwise the module path.
func (i Import) Original() string {
	if i.Name == "" || i.Name == "default" {
		return i.Path
	}
	return i.Path + "." + i.Name
}

// Imports lists every import in the file.
func (f *File) Imports() []Import {
	var out []Import

	walk(f.Root(), func(n *sitter.Node) bool {
		if !n.IsNamed() {
			return true
		}

		switch f.Lang {
		case langs.JavaScript:
			out = append(out, f.jsImports(n)...)
		case langs.Python:
			out = append(out, f.pyImports(n)...)
		case langs.PHP:
			out = append(out, f.phpImports(n)...)
		case langs.Go:
			out = append(out, f.goImports(n)...)
		}

		return true
	})

	return out
}

// AliasMap maps local binding names to what they stand for, e.g.
// {"np": "numpy", "svc": "project/service", "b": "./x.a"}.
// It replaces the gist's regex-based extractAliases.
func (f *File) AliasMap() map[string]string {
	m := make(map[string]string)

	for _, imp := range f.Imports() {
		if imp.Local == "" {
			continue
		}
		if orig := imp.Original(); orig != imp.Local {
			m[imp.Local] = orig
		}
	}

	return m
}

// ApplyAlias rewrites the first segment of a callee through the alias map:
// "np.array" -> "numpy.array".
func ApplyAlias(callee string, aliases map[string]string) string {
	head, rest := callee, ""
	if i := strings.Index(callee, "."); i >= 0 {
		head, rest = callee[:i], callee[i:]
	}

	if orig, ok := aliases[head]; ok {
		return orig + rest
	}
	return callee
}

func (f *File) jsImports(n *sitter.Node) []Import {
	switch n.Type() {
	case "import_statement", "export_statement":
		srcNode := n.ChildByFieldName("source")
		if srcNode == nil {
			return nil
		}

		path := unquote(f.Text(srcNode))
		line := lineOf(n)

		clause := childOfType(n, "import_clause")
		if clause == nil {
			return []Import{{Path: path, Line: line}}
		}

		var out []Import
		for _, c := range namedChildren(clause) {
			switch c.Type() {
			case "identifier":
				out = append(out, Import{Path: path, Name: "default", Local: f.Text(c), Line: line})
			case "namespace_import":
				out = append(out, Import{Path: path, Local: f.Text(firstNamed(c)), Line: line})
			case "named_imports":
				for _, spec := range namedChildren(c) {
					if spec.Type() != "import_specifier" {
						continue
					}
					name := f.Text(spec.ChildByFieldName("name"))
					local := name
					if a := spec.ChildByFieldName("alias"); a != nil {
						local = f.Text(a)
					}
					out = append(out, Import{Path: path, Name: name, Local: local, Line: line})
				}
			}
		}
		if len(out) == 0 {
			out = append(out, Import{Path: path, Line: line})
		}
		return out

	case "call_expression":
		if f.Text(n.ChildByFieldName("function")) != "require" {
			return nil
		}
		first := firstNamed(n.ChildByFieldName("arguments"))
		if first == nil || first.Type() != "string" {
			return nil
		}

		imp := Import{Path: unquote(f.Text(first)), Line: lineOf(n)}
		if p := n.Parent(); p != nil && p.Type() == "variable_declarator" {
			if nm := p.ChildByFieldName("name"); nm != nil && nm.Type() == "identifier" {
				imp.Local = f.Text(nm)
			}
		}
		return []Import{imp}
	}

	return nil
}

func (f *File) pyImports(n *sitter.Node) []Import {
	line := lineOf(n)

	switch n.Type() {
	case "import_statement":
		var out []Import
		for _, c := range namedChildren(n) {
			switch c.Type() {
			case "dotted_name":
				out = append(out, Import{Path: f.Text(c), Line: line})
			case "aliased_import":
				out = append(out, Import{
					Path:  f.Text(c.ChildByFieldName("name")),
					Local: f.Text(c.ChildByFieldName("alias")),
					Line:  line,
				})
			}
		}
		return out

	case "import_from_statement":
		modNode := n.ChildByFieldName("module_name")
		mod := f.Text(modNode)

		var out []Import
		for _, c := range namedChildren(n) {
			if sameSpan(c, modNode) {
				continue
			}
			switch c.Type() {
			case "dotted_name":
				name := f.Text(c)
				out = append(out, Import{Path: mod, Name: name, Local: name, Line: line})
			case "aliased_import":
				out = append(out, Import{
					Path:  mod,
					Name:  f.Text(c.ChildByFieldName("name")),
					Local: f.Text(c.ChildByFieldName("alias")),
					Line:  line,
				})
			}
		}
		if len(out) == 0 {
			out = append(out, Import{Path: mod, Line: line})
		}
		return out
	}

	return nil
}

func (f *File) phpImports(n *sitter.Node) []Import {
	if n.Type() != "namespace_use_clause" {
		return nil
	}

	var path, alias string
	for _, c := range namedChildren(n) {
		switch c.Type() {
		case "qualified_name", "name":
			if path == "" {
				path = f.Text(c)
			}
		case "namespace_aliasing_clause":
			alias = f.Text(firstNamed(c))
		}
	}
	if path == "" {
		return nil
	}

	local := alias
	if local == "" {
		local = path
		if i := strings.LastIndex(path, `\`); i >= 0 {
			local = path[i+1:]
		}
	}

	return []Import{{Path: path, Local: local, Line: lineOf(n)}}
}

func (f *File) goImports(n *sitter.Node) []Import {
	if n.Type() != "import_spec" {
		return nil
	}

	path := unquote(f.Text(n.ChildByFieldName("path")))
	if path == "" {
		return nil
	}

	local := f.Text(n.ChildByFieldName("name"))
	switch {
	case local == "_" || local == ".":
		local = ""
	case local == "":
		local = goDefaultName(path)
	}

	return []Import{{Path: path, Local: local, Line: lineOf(n)}}
}

// goDefaultName guesses the package name an unaliased import is bound to:
// the last path element, skipping a trailing major-version suffix (/v2).
func goDefaultName(path string) string {
	parts := strings.Split(path, "/")
	last := parts[len(parts)-1]

	if len(parts) > 1 && isMajorVersion(last) {
		return parts[len(parts)-2]
	}
	return last
}

func isMajorVersion(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	for _, r := range s[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

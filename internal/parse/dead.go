package parse

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/isocroft/tallaq/internal/langs"
)

var simpleNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

/* @HINT: `identifierLeafTypes` are the leaf node types that name something. */
var identifierLeafTypes = map[string]struct{}{
	"identifier":                            {},
	"property_identifier":                   {},
	"field_identifier":                      {},
	"type_identifier":                       {},
	"package_identifier":                    {},
	"name":                                  {},
	"shorthand_property_identifier":         {},
	"shorthand_property_identifier_pattern": {},
}

var stringNodeTypes = map[string]struct{}{
	"string": {}, "string_fragment": {}, "string_content": {}, "string_value": {},
	"interpreted_string_literal": {}, "raw_string_literal": {}, "encapsed_string": {},
}

/* @HINT: `Identifiers` counts how often names are *used* in a file. */
type Identifiers struct {
	// Names counts identifier occurrences, excluding the declaring name of
	// functions/classes/types, Go method receivers and import statements.
	Names map[string]int
	// Strings counts string literals that look like a bare name
	// ("handler"), which usually means dynamic dispatch / reflection.
	Strings map[string]int
}

func spanKey(n *sitter.Node) [2]uint32 {
	return [2]uint32{n.StartByte(), n.EndByte()}
}

func (f *File) isImportNode(tokenType string) bool {
	switch f.Lang {
	case langs.JavaScript:
		return tokenType == "import_statement"
	case langs.Python:
		return tokenType == "import_statement" || tokenType == "import_from_statement" || tokenType == "future_import_statement"
	case langs.Go:
		return tokenType == "import_declaration"
	case langs.PHP:
		return tokenType == "namespace_use_declaration"
	}
	return false
}

/*
@HINT:

`Identifiers(...)` collects name usage counts. A function whose name has a
count of zero (project-wide) is never referenced.
*/
func (f *File) Identifiers() Identifiers {
	out := Identifiers{Names: make(map[string]int), Strings: make(map[string]int)}
	skip := make(map[[2]uint32]struct{})

	walk(f.Root(), func(n *sitter.Node) bool {
		if !n.IsNamed() {
			return true
		}

		t := n.Type()
		if f.isImportNode(t) {
			return false
		}

		switch {
		case f.isFunctionNode(t), f.isClassNode(t), t == "type_spec":
			if nm := n.ChildByFieldName("name"); nm != nil {
				skip[spanKey(nm)] = struct{}{}
			}
			if f.Lang == langs.Go && t == "method_declaration" {
				walk(n.ChildByFieldName("receiver"), func(r *sitter.Node) bool {
					if r.Type() == "type_identifier" {
						skip[spanKey(r)] = struct{}{}
					}
					return true
				})
			}
		case t == "variable_declarator":
			/* @HINT: `const run = () => {}`  declares "run" as variable */
			if v := n.ChildByFieldName("value"); v != nil && f.isFunctionNode(v.Type()) {
				if nm := n.ChildByFieldName("name"); nm != nil {
					skip[spanKey(nm)] = struct{}{}
				}
			}
		}

		if _, ok := identifierLeafTypes[t]; ok && n.ChildCount() == 0 {
			if _, skipped := skip[spanKey(n)]; !skipped {
				out.Names[f.Text(n)]++
			}
			return true
		}

		if _, ok := stringNodeTypes[t]; ok {
			if s := unquote(f.Text(n)); simpleNameRe.MatchString(s) {
				out.Strings[s]++
			}
		}

		return true
	})

	return out
}

/* @HINT: `TypeDecl` is a class/struct/interface/type declaration. */
type TypeDecl struct {
	Name     string
	Kind     string // "class" or "type"
	Line     uint32
	EndLine  uint32
	Exported bool
}

/* @HINT: `Types(...)` lists every type-like declaration, including ones without fields. */
func (f *File) Types() []TypeDecl {
	var out []TypeDecl

	walk(f.Root(), func(n *sitter.Node) bool {
		if !n.IsNamed() {
			return true
		}

		t := n.Type()
		switch {
		case f.Lang == langs.Go && t == "type_spec":
			if name := f.Text(n.ChildByFieldName("name")); name != "" {
				out = append(out, TypeDecl{
					Name: name, Kind: "type", Line: lineOf(n), EndLine: endLineOf(n),
					Exported: f.isExported(n, name),
				})
			}
		case f.isClassNode(t):
			if name := f.classNameOf(n); name != "" {
				out = append(out, TypeDecl{
					Name: name, Kind: "class", Line: lineOf(n), EndLine: endLineOf(n),
					Exported: f.isExported(n, name),
				})
			}
		}

		return true
	})

	return out
}

func (f *File) isExported(n *sitter.Node, name string) bool {
	switch f.Lang {
	case langs.Go:
		r, _ := utf8.DecodeRuneInString(name)
		return unicode.IsUpper(r)

	case langs.JavaScript:
		for p := n.Parent(); p != nil; p = p.Parent() {
			switch p.Type() {
			case "export_statement":
				return true
			case "assignment_expression":
				left := f.Text(p.ChildByFieldName("left"))
				if strings.HasPrefix(left, "module.exports") || strings.HasPrefix(left, "exports.") {
					return true
				}
			}
		}
		return false

	case langs.Python:
		/*
			@NOTE:

			A module-level __all__ declares the public API explicitly: only
			listed names are exported, whatever their spelling.
		*/
		if isModuleLevel(n) {
			if all, ok := f.pythonAll(); ok {
				_, listed := all[name]
				return listed
			}
		}

		dunder := strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__")
		return dunder || !strings.HasPrefix(name, "_")

	case langs.PHP:
		if vis := childOfType(n, "visibility_modifier"); vis != nil {
			v := f.Text(vis)
			return v != "private" && v != "protected"
		}
		return true
	}

	return false
}

func (f *File) isMethod(n *sitter.Node) bool {
	switch n.Type() {
	case "method_definition", "method_declaration":
		return true
	}

	if f.Lang == langs.Python {
		p := n.Parent()
		if p != nil && p.Type() == "decorated_definition" {
			p = p.Parent()
		}
		if p != nil && p.Type() == "block" {
			if gp := p.Parent(); gp != nil && gp.Type() == "class_definition" {
				return true
			}
		}
	}

	return false
}

func isDecorated(n *sitter.Node) bool {
	p := n.Parent()
	return p != nil && p.Type() == "decorated_definition"
}

/* @HINT: `Unreachable` is a region of code that can never execute. */
type Unreachable struct {
	Kind    string // "after-terminator" or "constant-condition"
	Line    uint32
	EndLine uint32
	Detail  string
}

/*
@HINT:

`Unreachable(...)` finds statements that follow return/throw/raise/break/
continue/goto/panic in the same block, and `if` branches guarded by a
constant condition.
*/
func (f *File) Unreachable() []Unreachable {
	var out []Unreachable

	walk(f.Root(), func(n *sitter.Node) bool {
		if !n.IsNamed() {
			return true
		}

		t := n.Type()
		if f.isBlockNode(t) {
			if u, ok := f.unreachableInBlock(n); ok {
				out = append(out, u)
			}
		}
		if t == "if_statement" {
			if u, ok := f.constantCondition(n); ok {
				out = append(out, u)
			}
		}

		return true
	})

	return out
}

func (f *File) isBlockNode(tokenType string) bool {
	switch f.Lang {
	case langs.JavaScript:
		return tokenType == "statement_block"
	case langs.Python:
		return tokenType == "block"
	case langs.Go:
		return tokenType == "block" || tokenType == "statement_list"
	case langs.PHP:
		return tokenType == "compound_statement"
	}
	return false
}

func (f *File) isHoisted(tokenType string) bool {
	switch tokenType {
	case "function_declaration", "generator_function_declaration", // JS
		"function_definition", "class_declaration", "interface_declaration", // PHP
		"labeled_statement": // Go goto targets
		return true
	}
	return false
}

func (f *File) terminator(c *sitter.Node) (string, bool) {
	switch c.Type() {
	case "return_statement":
		return "return", true
	case "throw_statement":
		return "throw", true
	case "raise_statement":
		return "raise", true
	case "break_statement":
		return "break", true
	case "continue_statement":
		return "continue", true
	case "goto_statement":
		return "goto", true
	case "expression_statement":
		if f.Lang == langs.Go {
			if call := firstNamed(c); call != nil && call.Type() == "call_expression" &&
				f.Text(call.ChildByFieldName("function")) == "panic" {
				return "panic", true
			}
		}
	}
	return "", false
}

func (f *File) unreachableInBlock(block *sitter.Node) (Unreachable, bool) {
	var (
		terminated  bool
		termLine    uint32
		termKind    string
		first, last *sitter.Node
	)

	for _, c := range namedChildren(block) {
		if isComment(c.Type()) {
			continue
		}

		if terminated {
			if f.isHoisted(c.Type()) {
				continue
			}
			if first == nil {
				first = c
			}
			last = c
			continue
		}

		if kind, ok := f.terminator(c); ok {
			terminated, termLine, termKind = true, lineOf(c), kind
		}
	}

	if first == nil {
		return Unreachable{}, false
	}

	return Unreachable{
		Kind:    "after-terminator",
		Line:    lineOf(first),
		EndLine: endLineOf(last),
		Detail:  "code after " + termKind + " on line " + strconv.Itoa(int(termLine)) + " can never run",
	}, true
}

func (f *File) constantCondition(n *sitter.Node) (Unreachable, bool) {
	cond := strings.Trim(f.Normalize(n.ChildByFieldName("condition")), "( )")

	switch cond {
	case "false", "False", "FALSE", "0":
		region := n.ChildByFieldName("consequence")
		if region == nil {
			region = n.ChildByFieldName("body")
		}
		if region == nil {
			region = n
		}
		return Unreachable{
			Kind: "constant-condition", Line: lineOf(region), EndLine: endLineOf(region),
			Detail: "condition is always false; this branch never runs",
		}, true

	case "true", "True", "TRUE", "1":
		if alt := n.ChildByFieldName("alternative"); alt != nil {
			return Unreachable{
				Kind: "constant-condition", Line: lineOf(alt), EndLine: endLineOf(alt),
				Detail: "condition is always true; the else branch never runs",
			}, true
		}
	}

	return Unreachable{}, false
}

/*
@HINT:

`isModuleLevel(...)` reports whether a Python definition sits directly in the
module body (optionally under decorators).
*/
func isModuleLevel(node *sitter.Node) bool {
	parentNode := node.Parent()
	if parentNode != nil && parentNode.Type() == "decorated_definition" {
		parentNode = parentNode.Parent()
	}
	return parentNode != nil && parentNode.Type() == "module"
}

/*
@HINT:

`pythonAll(...)` returns the names listed in the module-level __all__ and
whether the module defines one. It understands `__all__ = [...]`,
`__all__ = (...)`, annotated forms and `__all__ += [...]`.
*/
func (f *File) pythonAll() (map[string]struct{}, bool) {
	if f.pyAllDone {
		return f.pyAll, f.pyAllOK
	}
	f.pyAllDone = true

	for _, stmt := range namedChildren(f.Root()) {
		if stmt.Type() != "expression_statement" {
			continue
		}

		for _, c := range namedChildren(stmt) {
			if c.Type() != "assignment" && c.Type() != "augmented_assignment" {
				continue
			}
			if f.Text(c.ChildByFieldName("left")) != "__all__" {
				continue
			}

			if f.pyAll == nil {
				f.pyAll = make(map[string]struct{})
			}
			f.pyAllOK = true

			walk(c.ChildByFieldName("right"), func(n *sitter.Node) bool {
				if n.Type() == "string" {
					if name := unquote(f.Text(n)); name != "" {
						f.pyAll[name] = struct{}{}
					}
					return false
				}
				return true
			})
		}
	}

	return f.pyAll, f.pyAllOK
}

package parse

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/isocroft/tallaq/internal/langs"
)

/* @HINT: `Param` is one function parameter. */
type Param struct {
	Name       string
	Type       string // "" for untyped (dynamic) languages
	Position   int
	IsVariadic bool
}

/* @HINT: `Call` is one call site. */
type Call struct {
	Name string // e.g. "saveUser", "db.insert", "Server.Handle"
	Line uint32
}

/* @HINT: `Function` is a named function, method or function bound to a name. */
type Function struct {
	Name      string // simple name: "run"
	Qualified string // "Runner.run", "Server.Handle", "outer.inner"

	Params     []Param
	ParamText  string // whitespace/comment-insensitive parameter list
	ResultType string // declared type, "void", or "dynamic" (returns a value, type unknown)

	StartLine uint32 // 1-based
	EndLine   uint32

	// Calls made directly by this function. Calls inside nested *named*
	// functions belong to those functions; anonymous callbacks/closures
	// count towards their enclosing function.
	Calls []Call
}

/* @HINT: `Scan` is the result of scanning one file. */
type Scan struct {
	Functions []*Function
	// ModuleCalls are calls outside of any named function (top-level
	// statements). The gist dropped these whenever a file also contained
	// at least one function.
	ModuleCalls []Call
}

// Scan walks the syntax tree once and collects functions and calls.
func (f *File) Scan() *Scan {
	s := &Scan{}
	f.scanNode(f.Root(), -1, nil, s)
	return s
}

func (f *File) scanNode(n *sitter.Node, owner int, scope []string, s *Scan) {
	if n == nil {
		return
	}

	if n.IsNamed() {
		t := n.Type()

		if f.isClassNode(t) {
			if name := f.classNameOf(n); name != "" {
				scope = appendScope(scope, name)
			}
		}

		if call := f.callName(n); call != "" {
			c := Call{Name: call, Line: lineOf(n)}
			if owner >= 0 {
				s.Functions[owner].Calls = append(s.Functions[owner].Calls, c)
			} else {
				s.ModuleCalls = append(s.ModuleCalls, c)
			}
		}

		if f.isFunctionNode(t) {
			if name, ok := f.functionName(n); ok {
				prefix := scope
				if recv := f.goReceiverType(n); recv != "" {
					prefix = appendScope(scope, recv)
				}

				fn := &Function{
					Name:      name,
					Qualified: strings.Join(appendScope(prefix, name), "."),
					StartLine: lineOf(n),
					EndLine:   endLineOf(n),
				}
				fn.Params, fn.ParamText = f.params(n)
				fn.ResultType = f.resultType(n)

				s.Functions = append(s.Functions, fn)
				owner = len(s.Functions) - 1
				scope = appendScope(prefix, name)
			}
		}
	}

	for i := 0; i < int(n.ChildCount()); i++ {
		f.scanNode(n.Child(i), owner, scope, s)
	}
}

func appendScope(scope []string, name string) []string {
	out := make([]string, len(scope), len(scope)+1)
	copy(out, scope)
	return append(out, name)
}

func (f *File) isFunctionNode(t string) bool {
	switch f.Lang {
	case langs.JavaScript:
		switch t {
		case "function_declaration", "generator_function_declaration",
			"function", "function_expression", "generator_function",
			"arrow_function", "method_definition":
			return true
		}
	case langs.Python:
		return t == "function_definition"
	case langs.PHP:
		switch t {
		case "function_definition", "method_declaration",
			"anonymous_function_creation_expression", "anonymous_function",
			"arrow_function":
			return true
		}
	case langs.Go:
		switch t {
		case "function_declaration", "method_declaration", "func_literal":
			return true
		}
	}
	return false
}

func (f *File) isClassNode(t string) bool {
	switch f.Lang {
	case langs.JavaScript:
		return t == "class_declaration" || t == "class"
	case langs.Python:
		return t == "class_definition"
	case langs.PHP:
		switch t {
		case "class_declaration", "interface_declaration", "trait_declaration", "enum_declaration":
			return true
		}
	}
	return false
}

func (f *File) classNameOf(n *sitter.Node) string {
	if name := f.Text(n.ChildByFieldName("name")); name != "" {
		return name
	}
	if p := n.Parent(); p != nil && p.Type() == "variable_declarator" {
		return f.Text(p.ChildByFieldName("name"))
	}
	return ""
}

// functionName returns the name of a function node. Anonymous functions
// that are bound to a variable, object key or assignment target get that
// name; truly anonymous ones return ok=false and are transparent (their
// calls count towards the enclosing function).
func (f *File) functionName(n *sitter.Node) (string, bool) {
	name := f.Text(n.ChildByFieldName("name"))

	if name == "" {
		if p := n.Parent(); p != nil {
			switch p.Type() {
			case "variable_declarator": // const run = () => {}
				name = f.Text(p.ChildByFieldName("name"))
			case "pair": // { run: function () {} }
				name = f.Text(p.ChildByFieldName("key"))
			case "assignment_expression": // exports.run = function () {}
				name = cleanCallee(f.Text(p.ChildByFieldName("left")))
			}
		}
	}

	if name == "" {
		return "", false
	}
	return name, true
}

// goReceiverType returns "Server" for `func (s *Server) Handle()`.
func (f *File) goReceiverType(n *sitter.Node) string {
	if f.Lang != langs.Go || n.Type() != "method_declaration" {
		return ""
	}

	for _, c := range namedChildren(n.ChildByFieldName("receiver")) {
		if c.Type() != "parameter_declaration" {
			continue
		}

		t := strings.TrimLeft(f.Text(c.ChildByFieldName("type")), "*")
		if i := strings.Index(t, "["); i >= 0 { // generics: Stack[T]
			t = t[:i]
		}
		return strings.TrimSpace(t)
	}

	return ""
}

// callName returns the callee of a call node, or "" when n is not a call.
func (f *File) callName(n *sitter.Node) string {
	switch n.Type() {
	case "call_expression", "call", "function_call_expression":
		return f.calleeText(n.ChildByFieldName("function"))

	case "new_expression":
		return f.calleeText(n.ChildByFieldName("constructor"))

	case "object_creation_expression":
		for _, c := range namedChildren(n) {
			switch c.Type() {
			case "name", "qualified_name", "variable_name", "relative_name":
				return f.calleeText(c)
			}
		}

	case "member_call_expression", "nullsafe_member_call_expression":
		return joinCallee(f.calleeText(n.ChildByFieldName("object")), f.Text(n.ChildByFieldName("name")))

	case "scoped_call_expression":
		return joinCallee(f.calleeText(n.ChildByFieldName("scope")), f.Text(n.ChildByFieldName("name")))
	}

	return ""
}

func joinCallee(object, name string) string {
	name = cleanCallee(name)
	switch {
	case object == "":
		return name
	case name == "":
		return object
	}
	return object + "." + name
}

// calleeText renders a callee expression ("db.insert", "Foo.bar"). Complex
// receivers such as `a().b` fall back to the trailing member name.
func (f *File) calleeText(n *sitter.Node) string {
	if n == nil {
		return ""
	}

	txt := f.Text(n)
	if strings.ContainsAny(txt, "(){}\n") || len(txt) > 120 {
		for _, field := range []string{"property", "attribute", "field", "name"} {
			if p := n.ChildByFieldName(field); p != nil {
				return cleanCallee(f.Text(p))
			}
		}
		return ""
	}

	return cleanCallee(txt)
}

func cleanCallee(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "?.", ".")
	s = strings.ReplaceAll(s, "->", ".")
	s = strings.ReplaceAll(s, "::", ".")
	s = strings.ReplaceAll(s, "$", "")
	return strings.Join(strings.Fields(s), "")
}

func (f *File) params(fn *sitter.Node) ([]Param, string) {
	list := fn.ChildByFieldName("parameters")

	if list == nil {
		// JavaScript: `x => x` has a single bare "parameter".
		if single := fn.ChildByFieldName("parameter"); single != nil {
			name := f.Text(single)
			return []Param{{Name: name}}, name
		}
		return nil, ""
	}

	var params []Param
	for _, c := range namedChildren(list) {
		params = f.appendParams(params, c)
	}
	for i := range params {
		params[i].Position = i
	}

	return params, f.Normalize(list)
}

func (f *File) appendParams(out []Param, c *sitter.Node) []Param {
	t := c.Type()
	if isComment(t) {
		return out
	}

	switch f.Lang {
	case langs.Go:
		switch t {
		case "parameter_declaration":
			typ := f.Normalize(c.ChildByFieldName("type"))
			named := false
			for _, id := range namedChildren(c) {
				if id.Type() == "identifier" {
					out = append(out, Param{Name: f.Text(id), Type: typ})
					named = true
				}
			}
			if !named {
				out = append(out, Param{Type: typ})
			}
		case "variadic_parameter_declaration":
			out = append(out, Param{
				Name:       f.Text(c.ChildByFieldName("name")),
				Type:       "..." + f.Normalize(c.ChildByFieldName("type")),
				IsVariadic: true,
			})
		}

	case langs.Python:
		switch t {
		case "identifier":
			if name := f.Text(c); name != "self" && name != "cls" {
				out = append(out, Param{Name: name})
			}
		case "typed_parameter":
			var name string
			variadic := false
			if first := firstNamed(c); first != nil {
				switch first.Type() {
				case "list_splat_pattern", "dictionary_splat_pattern":
					variadic = true
					name = strings.TrimLeft(f.Text(first), "*")
				default:
					name = f.Text(first)
				}
			}
			if name != "self" && name != "cls" {
				out = append(out, Param{
					Name:       name,
					Type:       f.Normalize(c.ChildByFieldName("type")),
					IsVariadic: variadic,
				})
			}
		case "default_parameter":
			out = append(out, Param{Name: f.Text(c.ChildByFieldName("name"))})
		case "typed_default_parameter":
			out = append(out, Param{
				Name: f.Text(c.ChildByFieldName("name")),
				Type: f.Normalize(c.ChildByFieldName("type")),
			})
		case "list_splat_pattern", "dictionary_splat_pattern":
			out = append(out, Param{Name: strings.TrimLeft(f.Text(c), "*"), IsVariadic: true})
		}

	case langs.JavaScript:
		switch t {
		case "identifier":
			out = append(out, Param{Name: f.Text(c)})
		case "assignment_pattern":
			out = append(out, Param{Name: f.Normalize(c.ChildByFieldName("left"))})
		case "rest_pattern":
			name := strings.TrimSpace(strings.TrimPrefix(f.Normalize(c), "..."))
			out = append(out, Param{Name: name, IsVariadic: true})
		case "object_pattern", "array_pattern":
			out = append(out, Param{Name: f.Normalize(c)})
		}

	case langs.PHP:
		switch t {
		case "simple_parameter", "property_promotion_parameter":
			out = append(out, Param{
				Name: strings.TrimPrefix(f.Text(c.ChildByFieldName("name")), "$"),
				Type: f.Normalize(c.ChildByFieldName("type")),
			})
		case "variadic_parameter":
			out = append(out, Param{
				Name:       strings.TrimPrefix(f.Text(c.ChildByFieldName("name")), "$"),
				Type:       f.Normalize(c.ChildByFieldName("type")),
				IsVariadic: true,
			})
		}
	}

	return out
}

// resultType returns the declared result type, or for languages without
// one: "dynamic" when the function returns a value and "void" otherwise.
func (f *File) resultType(fn *sitter.Node) string {
	for _, field := range []string{"result", "return_type"} {
		if r := fn.ChildByFieldName(field); r != nil {
			return f.Normalize(r)
		}
	}

	if f.Lang == langs.Go {
		return "void"
	}
	if f.returnsValue(fn) {
		return "dynamic"
	}
	return "void"
}

func (f *File) returnsValue(fn *sitter.Node) bool {
	// Arrow function with an expression body: x => x + 1
	if fn.Type() == "arrow_function" {
		if body := fn.ChildByFieldName("body"); body != nil && body.Type() != "statement_block" {
			return true
		}
	}

	found := false

	var visit func(n *sitter.Node, isRoot bool)
	visit = func(n *sitter.Node, isRoot bool) {
		if found || n == nil {
			return
		}
		if !isRoot && n.IsNamed() && f.isFunctionNode(n.Type()) {
			return // returns inside nested functions are not ours
		}
		if n.Type() == "return_statement" && n.NamedChildCount() > 0 {
			found = true
			return
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			visit(n.Child(i), false)
		}
	}

	visit(fn, true)

	return found
}

package parse

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	sitter "github.com/smacker/go-tree-sitter"
)

/* @HINT: Canon is a canonical, comparison-friendly form of a function body. */
type Canon struct {
	// Text is the canonical s-expression: variables alpha-renamed in order
	// of first use, comments/whitespace/punctuation removed, operands of
	// commutative operators sorted and `a > b` rewritten as `b < a`.
	// Literal values are kept.
	Text string
	// Shape is Text with literal values replaced by NUM / STR.
	Shape string
	// Tokens is Shape split into tokens (input for shingling).
	Tokens []string
	// Features is a behavioural bag: control-flow kinds, callees,
	// operators and numeric literals.
	Features map[string]int
	// Statements is an approximate statement count.
	Statements int
}

var numberTypes = map[string]struct{}{
	"number": {}, "integer": {}, "float": {}, "int_literal": {},
	"float_literal": {}, "imaginary_literal": {},
}

var literalStringTypes = map[string]struct{}{
	"string": {}, "interpreted_string_literal": {}, "raw_string_literal": {},
	"encapsed_string": {}, "template_string": {}, "string_literal": {},
}

var parenSpacer = strings.NewReplacer("(", "( ", ")", " )")

/*
@HINT:

`Canonicalize(...)` builds the canonical form of fn's body. fn must come from
this File's Scan and the File must not have been closed.
*/
func (f *File) Canonicalize(fn *Function) Canon {
	if fn == nil || fn.node == nil {
		return Canon{}
	}

	body := fn.node.ChildByFieldName("body")
	if body == nil {
		body = fn.node
	}

	c := &canonizer{f: f, feats: make(map[string]int)}
	text := c.run(fn, body, true, true)
	shape := c.run(fn, body, false, false)

	return Canon{
		Text:       text,
		Shape:      shape,
		Tokens:     strings.Fields(parenSpacer.Replace(shape)),
		Features:   c.feats,
		Statements: c.stmts,
	}
}

type canonizer struct {
	f       *File
	keepLit bool
	collect bool
	names   map[string]int
	feats   map[string]int
	stmts   int
}

func (c *canonizer) run(fn *Function, body *sitter.Node, keepLit, collect bool) string {
	c.keepLit, c.collect = keepLit, collect
	c.names = make(map[string]int)

	for _, p := range fn.Params {
		if p.Name != "" {
			c.rename(p.Name)
		}
	}

	return c.node(body)
}

func (c *canonizer) rename(name string) string {
	idx, ok := c.names[name]
	if !ok {
		idx = len(c.names)
		c.names[name] = idx
	}
	return "v" + strconv.Itoa(idx)
}

func (c *canonizer) node(n *sitter.Node) string {
	if n == nil {
		return ""
	}

	t := n.Type()
	if isComment(t) {
		return ""
	}

	if _, ok := numberTypes[t]; ok {
		text := c.f.Text(n)
		if c.collect {
			c.feats["lit:"+text]++
		}
		if c.keepLit {
			return "NUM:" + text
		}
		return "NUM"
	}

	if _, ok := literalStringTypes[t]; ok {
		if c.keepLit {
			return "STR:" + strings.Join(strings.Fields(c.f.Text(n)), "_")
		}
		return "STR"
	}

	if n.ChildCount() == 0 {
		text := strings.TrimSpace(c.f.Text(n))
		if text == "" {
			return ""
		}

		if !n.IsNamed() {
			if isPunct(text) {
				return ""
			}
			if c.collect && isOperatorText(text) {
				c.feats["op:"+featureOp(text)]++
			}
			return text
		}

		if _, ok := identifierLeafTypes[t]; ok {
			if c.isNameRole(n) {
				return "N:" + text
			}
			return c.rename(text)
		}

		return text
	}

	if c.collect {
		c.observe(n, t)
	}

	if strings.HasSuffix(t, "binary_expression") || strings.HasSuffix(t, "binary_operator") || t == "boolean_operator" {
		l, r, op := n.ChildByFieldName("left"), n.ChildByFieldName("right"), n.ChildByFieldName("operator")
		if l != nil && r != nil && op != nil {
			ls, rs, ops := c.node(l), c.node(r), strings.TrimSpace(c.f.Text(op))

			switch ops {
			case ">":
				ops, ls, rs = "<", rs, ls
			case ">=":
				ops, ls, rs = "<=", rs, ls
			case "+", "*", "==", "!=", "===", "!==", "&&", "||", "and", "or", "&", "|", "^":
				if rs < ls {
					ls, rs = rs, ls
				}
			}

			if c.collect {
				c.feats["op:"+featureOp(ops)]++
			}
			return "(" + t + " " + ls + " " + ops + " " + rs + ")"
		}
	}

	parts := make([]string, 0, int(n.ChildCount()))
	for i := 0; i < int(n.ChildCount()); i++ {
		if s := c.node(n.Child(i)); s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return ""
	}

	return "(" + t + " " + strings.Join(parts, " ") + ")"
}

/*
@HINT:

`isNameRole(...)` reports whether an identifier names something external to the
function (callee, member, type) and must therefore keep its text, as
opposed to a variable, which is alpha-renamed.
*/
func (c *canonizer) isNameRole(n *sitter.Node) bool {
	switch n.Type() {
	case "property_identifier", "field_identifier", "type_identifier", "package_identifier":
		return true
	}

	p := n.Parent()
	if p == nil {
		return false
	}

	for _, field := range []string{"function", "property", "attribute", "field", "constructor", "scope"} {
		if sameSpan(p.ChildByFieldName(field), n) {
			return true
		}
	}

	if strings.HasSuffix(p.Type(), "call_expression") && sameSpan(p.ChildByFieldName("name"), n) {
		return true
	}

	return false
}

func (c *canonizer) observe(n *sitter.Node, t string) {
	switch t {
	case "for_statement", "while_statement", "for_in_statement", "for_of_statement",
		"foreach_statement", "do_statement", "list_comprehension", "generator_expression",
		"dictionary_comprehension", "set_comprehension":
		c.feats["ctl:loop"]++
	case "if_statement", "conditional_expression", "ternary_expression", "switch_statement",
		"match_expression", "expression_switch_statement", "type_switch_statement",
		"match_statement", "switch_expression":
		c.feats["ctl:branch"]++
	case "return_statement":
		c.feats["ctl:return"]++
	case "throw_statement", "raise_statement":
		c.feats["ctl:throw"]++
	case "try_statement":
		c.feats["ctl:try"]++
	}

	if name := c.f.callName(n); name != "" {
		if i := strings.LastIndex(name, "."); i >= 0 {
			name = name[i+1:]
		}
		c.feats["call:"+name]++
	}

	if strings.HasSuffix(t, "_statement") || strings.HasSuffix(t, "_declaration") {
		c.stmts++
	}
}

func isPunct(s string) bool {
	switch s {
	case "(", ")", "{", "}", "[", "]", ",", ";", ":", ".":
		return true
	}
	return false
}

func isOperatorText(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
}

func featureOp(op string) string {
	switch op {
	case ">":
		return "<"
	case ">=":
		return "<="
	case "===":
		return "=="
	case "!==":
		return "!="
	}
	return op
}

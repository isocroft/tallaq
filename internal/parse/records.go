package parse

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/isocroft/tallaq/internal/langs"
)

/* @HINT: `Field` is a data member of a struct or class. */
type Field struct {
	Name string
	Type string // "" when the language does not declare one
}

/* @HINT: `Record` is a struct (Go) or class (JS/Python/PHP) with its data fields. */
type Record struct {
	Kind      string // "struct" or "class"
	Name      string
	Fields    []Field
	StartLine uint32
	EndLine   uint32
}

/* @HINT: `Records(...)` returns every struct/class that declares at least one field. */
func (f *File) Records() []Record {
	var out []Record

	walk(f.Root(), func(n *sitter.Node) bool {
		if !n.IsNamed() {
			return true
		}

		if f.Lang == langs.Go {
			if n.Type() == "type_spec" {
				if rec, ok := f.goStruct(n); ok {
					out = append(out, rec)
				}
			}
		} else if f.isClassNode(n.Type()) {
			if rec, ok := f.classRecord(n); ok {
				out = append(out, rec)
			}
		}

		return true
	})

	return out
}

func (f *File) goStruct(spec *sitter.Node) (Record, bool) {
	typ := spec.ChildByFieldName("type")
	if typ == nil || typ.Type() != "struct_type" {
		return Record{}, false
	}

	rec := Record{
		Kind:      "struct",
		Name:      f.Text(spec.ChildByFieldName("name")),
		StartLine: lineOf(spec),
		EndLine:   endLineOf(spec),
	}

	walk(typ, func(n *sitter.Node) bool {
		if n.Type() == "struct_type" && !sameSpan(n, typ) {
			return false // anonymous nested struct
		}
		if n.Type() != "field_declaration" {
			return true
		}

		fieldType := f.Normalize(n.ChildByFieldName("type"))
		named := false
		for _, c := range namedChildren(n) {
			if c.Type() == "field_identifier" {
				rec.Fields = append(rec.Fields, Field{Name: f.Text(c), Type: fieldType})
				named = true
			}
		}
		if !named && fieldType != "" { // embedded type
			rec.Fields = append(rec.Fields, Field{Name: fieldType, Type: fieldType})
		}

		return false
	})

	return rec, len(rec.Fields) > 0
}

func (f *File) classRecord(n *sitter.Node) (Record, bool) {
	rec := Record{
		Kind:      "class",
		Name:      f.classNameOf(n),
		StartLine: lineOf(n),
		EndLine:   endLineOf(n),
	}

	body := n.ChildByFieldName("body")
	if body == nil {
		return rec, false
	}

	seen := make(map[string]bool)
	add := func(fl Field) {
		if fl.Name == "" || seen[fl.Name] {
			return
		}
		seen[fl.Name] = true
		rec.Fields = append(rec.Fields, fl)
	}

	switch f.Lang {
	case langs.JavaScript:
		f.jsFields(body, add)
	case langs.Python:
		f.pyFields(body, add)
	case langs.PHP:
		f.phpFields(body, add)
	}

	return rec, len(rec.Fields) > 0
}

// jsFields: class field definitions plus `this.x = ...` in the constructor.
func (f *File) jsFields(body *sitter.Node, add func(Field)) {
	for _, m := range namedChildren(body) {
		switch m.Type() {
		case "field_definition", "public_field_definition":
			p := m.ChildByFieldName("property")
			if p == nil {
				p = m.ChildByFieldName("name")
			}
			add(Field{Name: f.Text(p)})

		case "method_definition":
			if f.Text(m.ChildByFieldName("name")) != "constructor" {
				continue
			}
			walk(m, func(n *sitter.Node) bool {
				if n.Type() == "assignment_expression" {
					left := n.ChildByFieldName("left")
					if left != nil && left.Type() == "member_expression" &&
						f.Text(left.ChildByFieldName("object")) == "this" {
						add(Field{Name: f.Text(left.ChildByFieldName("property"))})
					}
				}
				return true
			})
		}
	}
}

// pyFields: class-level (annotated) assignments plus `self.x = ...` in `__init__`.
func (f *File) pyFields(body *sitter.Node, add func(Field)) {
	for _, stmt := range namedChildren(body) {
		switch stmt.Type() {
		case "expression_statement":
			for _, c := range namedChildren(stmt) {
				if c.Type() != "assignment" {
					continue
				}
				left := c.ChildByFieldName("left")
				if left != nil && left.Type() == "identifier" {
					add(Field{Name: f.Text(left), Type: f.Normalize(c.ChildByFieldName("type"))})
				}
			}

		case "function_definition", "decorated_definition":
			fn := stmt
			if stmt.Type() == "decorated_definition" {
				fn = stmt.ChildByFieldName("definition")
			}
			if fn == nil || f.Text(fn.ChildByFieldName("name")) != "__init__" {
				continue
			}
			walk(fn, func(n *sitter.Node) bool {
				if n.Type() == "assignment" {
					left := n.ChildByFieldName("left")
					if left != nil && left.Type() == "attribute" &&
						f.Text(left.ChildByFieldName("object")) == "self" {
						add(Field{
							Name: f.Text(left.ChildByFieldName("attribute")),
							Type: f.Normalize(n.ChildByFieldName("type")),
						})
					}
				}
				return true
			})
		}
	}
}

// phpFields: property declarations plus constructor-promoted properties.
func (f *File) phpFields(body *sitter.Node, add func(Field)) {
	for _, m := range namedChildren(body) {
		switch m.Type() {
		case "property_declaration":
			typ := f.Normalize(m.ChildByFieldName("type"))
			for _, el := range namedChildren(m) {
				if el.Type() != "property_element" {
					continue
				}
				nameNode := el.ChildByFieldName("name")
				if nameNode == nil {
					nameNode = firstNamed(el)
				}
				add(Field{Name: strings.TrimPrefix(f.Text(nameNode), "$"), Type: typ})
			}

		case "method_declaration":
			if f.Text(m.ChildByFieldName("name")) != "__construct" {
				continue
			}
			for _, p := range namedChildren(m.ChildByFieldName("parameters")) {
				if p.Type() == "property_promotion_parameter" {
					add(Field{
						Name: strings.TrimPrefix(f.Text(p.ChildByFieldName("name")), "$"),
						Type: f.Normalize(p.ChildByFieldName("type")),
					})
				}
			}
		}
	}
}

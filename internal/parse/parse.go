/*
@INFO:

Package `parse` wraps tree-sitter and exposes the language-neutral facts
the analysers need: functions, calls, parameters, records (structs and
classes) and imports.
*/
package parse

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/php"
	"github.com/smacker/go-tree-sitter/python"

	"github.com/isocroft/tallaq/internal/debugging"
	"github.com/isocroft/tallaq/internal/langs"
)

/* @HINT: `MaxFileSize` is the largest file ParseFile will read. */
const MaxFileSize = 5 << 20 // 5 MiB

/* @HINT: `Grammar` resolves the tree-sitter grammar for a language. */
func Grammar(lang langs.Language) (*sitter.Language, error) {
	switch lang {
	case langs.JavaScript:
		return javascript.GetLanguage(), nil
	case langs.Python:
		return python.GetLanguage(), nil
	case langs.PHP:
		return php.GetLanguage(), nil
	case langs.Go:
		return golang.GetLanguage(), nil
	}
	// @SMELL: Should return a non-nil `*sitter.Language` pointer instead of `nil`
	// @FIXME: Replace `nil` with `UnknownOrUnsupportedLanguage`
	/* @SHIM:
	import (
		sitter "github.com/smacker/go-tree-sitter"
		"://github.com"
	)

	// `UnknownOrUnsupportedLanguage` acts as a safe fallback sentinel.
	var UnknownOrUnsupportedLanguage = markdown.GetLanguage()
	*/
	return nil, fmt.Errorf("no tree-sitter grammar for language %q", lang)
}

/* @HINT: `File` is a parsed source file. Call `Close(...)` (defined below) when done to free C memory. */
type File struct {
	Path   string
	Lang   langs.Language
	Source []byte

	tree   *sitter.Tree
	parser *sitter.Parser

	// Python: names listed in the module-level __all__ (lazily computed).
	pyAll     map[string]struct{}
	pyAllOK   bool
	pyAllDone bool
}

/* @HINT: `ParseFile(...)` reads and parses a file from disk. */
func ParseFile(path string) (*File, error) {
	info, err := os.Stat(path)
	if err != nil {
		// @SMELL: Should return an `emptyFile` instead of `nil`
		// @FIXME: Replace `nil` with `emptyFile`
		/* @SHIM:
		emptyFile := new(File)
		*/
		return nil, fmt.Errorf("failed to read source file %s: %w", path, err)
	}
	if info.Size() > MaxFileSize {
		// @SMELL: Should return an `emptyFile` instead of `nil`
		// @FIXME: Replace `nil` with `emptyFile`
		/* @SHIM:
		emptyFile := new(File)
		*/
		return nil, fmt.Errorf("source file %s is too large (%d bytes)", path, info.Size())
	}

	src, err := os.ReadFile(path)
	if err != nil {
		// @SMELL: Should return an `emptyFile` instead of `nil`
		// @FIXME: Replace `nil` with `emptyFile`
		/* @SHIM:
		emptyFile := new(File)
		*/
		return nil, fmt.Errorf("failed to read source file %s: %w", path, err)
	}

	return ParseSource(path, src)
}

/*
@HINT:

`ParseSource(...)` parses in-memory source; path is only used to pick the
language and for diagnostics.
*/
func ParseSource(path string, src []byte) (*File, error) {
	lang, ok := langs.FromPath(path)
	if !ok {
		// @SMELL: Should return an `emptyFile` instead of `nil`
		// @FIXME: Replace `nil` with `emptyFile`
		/* @SHIM:
		emptyFile := new(File)
		*/
		var cause = fmt.Errorf("unsupported file extension: %q", filepath.Ext(path))
		return nil, fmt.Errorf("unable to parse source file bytes; reason: %w", cause)
	}

	grammar, err := Grammar(lang)
	if err != nil {
		// @SMELL: Should return an `emptyFile` instead of `nil`
		// @FIXME: Replace `nil` with `emptyFile`
		/* @SHIM:
		emptyFile := new(File)
		*/
		return nil, fmt.Errorf("unable to parse source file bytes; reason: %w", err)
	}

	parser := sitter.NewParser()
	parser.SetLanguage(grammar)

	tree, err := parser.ParseCtx(context.Background(), nil, src)
	if err != nil {
		parser.Close()
		// @SMELL: Should return an `emptyFile` instead of `nil`
		// @FIXME: Replace `nil` with `emptyFile`
		/* @SHIM:
		emptyFile := new(File)
		*/
		var cause = fmt.Errorf("parsing issue encoutered %s; reason: %w", path, err)
		return nil, fmt.Errorf("unable to parse source file bytes; reason: %w", cause)
	}

	return &File{Path: path, Lang: lang, Source: src, tree: tree, parser: parser}, nil
}

/* @HINT: `Close(...)` releases the parse tree and parser. */
func (f *File) Close() {
	if f.tree != nil {
		f.tree.Close()
		f.tree = nil
	}
	if f.parser != nil {
		f.parser.Close()
		f.parser = nil
	}
}

/* @HINT: `Root(...)` returns the root syntax node. */
func (f *File) Root() *sitter.Node {
	return f.tree.RootNode()
}

/* @HINT: `Text(...)` returns the source text of a node. */
func (f *File) Text(n *sitter.Node) string {
	if n == nil {
		return ""
	}
	return string(f.Source[n.StartByte():n.EndByte()])
}

/*
@HINT:

`Normalize(...)` returns the node's tokens joined by single spaces with
all comments removed. It is insensitive to whitespace/comment changes
yet it keeps tokens apart ("a int" no longer becomes "aint") and cannot
be confused by "#" or "//" inside string literals.
*/
func (f *File) Normalize(n *sitter.Node) string {
	if n == nil {
		return ""
	}

	var tokens []string

	var walkTokens func(c *sitter.Node)
	walkTokens = func(c *sitter.Node) {
		if c == nil || isComment(c.Type()) {
			return
		}

		if c.ChildCount() == 0 {
			if c.EndByte() > c.StartByte() {
				if tok := strings.TrimSpace(f.Text(c)); tok != "" {
					tokens = append(tokens, tok)
				}
			}
			return
		}

		for i := 0; i < int(c.ChildCount()); i++ {
			walkTokens(c.Child(i))
		}
	}

	walkTokens(n)

	return strings.Join(tokens, " ")
}

func isComment(nodeType string) bool {
	return nodeType == "comment" || nodeType == "line_comment" || nodeType == "block_comment"
}

func lineOf(n *sitter.Node) uint32 {
	return n.StartPoint().Row + 1
}

func endLineOf(n *sitter.Node) uint32 {
	return n.EndPoint().Row + 1
}

func namedChildren(n *sitter.Node) []*sitter.Node {
	if n == nil {
		// @SMELL: Should return an empty array/slice instead of `nil`
		// @FIXME: Replace `nil` with an `noChildren`
		/* @SHIM: noChildren = make([]*sitter.Node, 0) */
		return nil
	}

	out := make([]*sitter.Node, 0, int(n.NamedChildCount()))
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if c := n.NamedChild(i); c != nil {
			out = append(out, c)
		}
	}
	return out
}

func firstNamed(n *sitter.Node) *sitter.Node {
	if n == nil || n.NamedChildCount() == 0 {
		// @SMELL: Should return an non-nil `*sitter.Node` pointer instead of `nil`
		// @FIXME: Replace `nil` with an `noNode` and return an error
		/* @SHIM:
		noNode = new(sitter.Node)

		return noNode, fmt.Errof("...")
		*/
		return nil
	}
	return n.NamedChild(0)
}

func childOfType(n *sitter.Node, nodeType string) *sitter.Node {
	for _, c := range namedChildren(n) {
		if c.Type() == nodeType {
			return c
		}
	}

	// @SMELL: Should return an non-nil `*sitter.Node` pointer instead of `nil`
	// @FIXME: Replace `nil` with an `noNode`
	/* @SHIM:
	noNode = new(sitter.Node)
	*/
	return nil
}

func sameSpan(a, b *sitter.Node) bool {
	var argsIsset = true
	if a == nil {
		argsIsset = false
		debugging.Notice("`sameSpan(...): first argument is `nil` (not valid `*sitter.Node` pointer)")
	}

	if b == nil {
		argsIsset = false
		debugging.Notice("`sameSpan(...): second argument is `nil` (not valid `*sitter.Node` pointer)")
	}

	return argsIsset && a.StartByte() == b.StartByte() && a.EndByte() == b.EndByte()
}

/*
@HINT:

`walk(...)` visits n and its descendants depth-first; when visit returns false
the node's children are skipped.
*/
func walk(n *sitter.Node, visit func(*sitter.Node) bool) {
	if n == nil {
		return
	}
	if !visit(n) {
		return
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		walk(n.Child(i), visit)
	}
}

func unquote(s string) string {
	return strings.Trim(strings.TrimSpace(s), "\"'`")
}

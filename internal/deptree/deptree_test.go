package deptree

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()

	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func depNames(n *Node, root string) []string {
	out := make([]string, 0, len(n.Dependencies))
	for _, d := range n.Dependencies {
		rel, _ := filepath.Rel(root, d.Path)
		out = append(out, filepath.ToSlash(rel))
	}
	return out
}

func TestBuildJavaScript(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"main.js":  "import a from './a';\nconst b = require('./lib/b');\nimport React from 'react';\nimport './missing';\n",
		"a.js":     "import main from './main';\n",
		"lib/b.js": "export * from '../a';\n",
	})

	b := NewBuilder(Options{ProjectRoot: root})
	node, err := b.Build(filepath.Join(root, "main.js"))
	if err != nil {
		t.Fatal(err)
	}

	if got, want := depNames(node, root), []string{"a.js", "lib/b.js"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deps = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(node.Unresolved, []string{"./missing"}) {
		t.Fatalf("unresolved = %v", node.Unresolved)
	}

	g := b.Graph()
	if len(g.Nodes) != 3 {
		t.Fatalf("want 3 nodes, got %d", len(g.Nodes))
	}

	cycles := g.Cycles()
	if len(cycles) != 1 || len(cycles[0]) != 3 {
		t.Fatalf("want one 3-file cycle, got %v", cycles)
	}

	var out bytes.Buffer
	Render(&out, node, root)
	for _, want := range []string{"(cycle)", "(*)", "(unresolved)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("render output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestBuildTwiceDoesNotLeakState(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"one.js": "import './two';\n",
		"two.js": "",
		"x.js":   "import './y';\n",
		"y.js":   "",
	})

	first, err := Generate(filepath.Join(root, "one.js"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate(filepath.Join(root, "x.js"))
	if err != nil {
		t.Fatal(err)
	}

	if filepath.Base(first.Path) != "one.js" || filepath.Base(second.Path) != "x.js" {
		t.Fatalf("each call must return its own root, got %s and %s", first.Path, second.Path)
	}
}

func TestBuildPython(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"pkg/__init__.py": "",
		"pkg/a.py":        "from . import b\nfrom .c import thing\nimport os\n",
		"pkg/b.py":        "",
		"pkg/c.py":        "from .. import top\n",
		"top.py":          "",
	})

	b := NewBuilder(Options{ProjectRoot: root})
	node, err := b.Build(filepath.Join(root, "pkg", "a.py"))
	if err != nil {
		t.Fatal(err)
	}

	if got, want := depNames(node, root), []string{"pkg/b.py", "pkg/c.py"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deps = %v, want %v", got, want)
	}

	c := node.Dependencies[1]
	if got, want := depNames(c, root), []string{"top.py"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("c.py deps = %v, want %v", got, want)
	}
}

func TestBuildGo(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"go.mod":            "module example.com/app\n\ngo 1.21\n",
		"main.go":           "package main\n\nimport (\n\t\"fmt\"\n\t\"example.com/app/util\"\n)\n\nfunc main() { fmt.Println(util.X) }\n",
		"util/util.go":      "package util\n\nvar X = 1\n",
		"util/util_test.go": "package util\n",
	})

	b := NewBuilder(Options{ProjectRoot: root})
	node, err := b.Build(filepath.Join(root, "main.go"))
	if err != nil {
		t.Fatal(err)
	}

	if got, want := depNames(node, root), []string{"util/util.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deps = %v, want %v", got, want)
	}
}

func TestBuildPHP(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"main.php":        "<?php\nrequire_once __DIR__ . '/lib/helpers.php';\ninclude 'config.php';\n",
		"lib/helpers.php": "<?php\n",
		"config.php":      "<?php\n",
	})

	b := NewBuilder(Options{ProjectRoot: root})
	node, err := b.Build(filepath.Join(root, "main.php"))
	if err != nil {
		t.Fatal(err)
	}

	if got, want := depNames(node, root), []string{"lib/helpers.php", "config.php"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deps = %v, want %v", got, want)
	}
}

func TestBuildDirSkipsVendoredCode(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"app.js":                    "import './util';\n",
		"util.js":                   "",
		"node_modules/pkg/index.js": "",
	})

	g, err := NewBuilder(Options{}).BuildDir(root)
	if err != nil {
		t.Fatal(err)
	}

	if len(g.Nodes) != 2 {
		t.Fatalf("want 2 nodes (node_modules skipped), got %d", len(g.Nodes))
	}

	roots := g.Roots()
	if len(roots) != 1 || filepath.Base(roots[0].Path) != "app.js" {
		t.Fatalf("unexpected roots: %v", roots)
	}
}

func TestBuildRejectsMissingAndUnsupportedEntry(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"notes.txt": "hi"})

	if _, err := Generate(filepath.Join(root, "nope.js")); err == nil {
		t.Error("expected an error for a missing entry file")
	}
	if _, err := Generate(filepath.Join(root, "notes.txt")); err == nil {
		t.Error("expected an error for an unsupported extension")
	}
}

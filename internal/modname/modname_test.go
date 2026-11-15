package modname

import (
	"testing"

	"github.com/isocroft/tallaq/internal/langs"
)

func TestFor(t *testing.T) {
	cases := []struct {
		rel  string
		src  string
		lang langs.Language
		want string
	}{
		{"main.py", "", langs.Python, "main.py"},
		{"pkg/utils/helpers.py", "", langs.Python, "pkg.utils:helpers.py"},
		{"index.js", "", langs.JavaScript, "index.js"},
		{"src/index.js", "", langs.JavaScript, "src:index.js"},
		{"root.go", "package system\n", langs.Go, "system:root.go"},
		{"cmd/a/main.go", "package main\n", langs.Go, "cmd.a:main.go"},
		{"index.php", "<?php\n", langs.PHP, "root:index.php"},
		{"x/index.php", "<?php\nnamespace Vendor\\Module;\n", langs.PHP, "Vendor.Module:index.php"},
	}
	for _, c := range cases {
		if got := For(c.rel, []byte(c.src), c.lang); got != c.want {
			t.Errorf("For(%q) = %q, want %q", c.rel, got, c.want)
		}
	}
}

func TestRel(t *testing.T) {
	if got := Rel("/proj", "/proj/a/b.go"); got != "a/b.go" {
		t.Errorf("got %q", got)
	}
	if got := Rel("/proj", "/other/b.go"); got != "b.go" {
		t.Errorf("outside root should fall back to base name, got %q", got)
	}
	if got := Rel("", "/other/b.go"); got != "b.go" {
		t.Errorf("empty root should fall back to base name, got %q", got)
	}
}

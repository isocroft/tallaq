package langs

import "testing"

func TestFromPath(t *testing.T) {
	cases := map[string]Language{
		"a/b/c.js":  JavaScript,
		"x.MJS":     JavaScript,
		"main.py":   Python,
		"index.php": PHP,
		"main.go":   Go,
	}
	for path, want := range cases {
		got, ok := FromPath(path)
		if !ok || got != want {
			t.Errorf("FromPath(%q) = %q, %v; want %q", path, got, ok, want)
		}
	}
	if _, ok := FromPath("README.md"); ok {
		t.Error("README.md should not be supported")
	}
}

func TestShouldSkip(t *testing.T) {
	if !ShouldSkipDir("node_modules") || !ShouldSkipDir(".git") {
		t.Error("expected node_modules and .git to be skipped")
	}
	if ShouldSkipDir("src") || ShouldSkipDir(".") {
		t.Error("src and . must not be skipped")
	}
	if !ShouldSkipPath("a/vendor/b/c.go") {
		t.Error("vendor component should be skipped")
	}
	if ShouldSkipPath("a/b/vendor") {
		t.Error("the final component is a file name and is never considered")
	}
}

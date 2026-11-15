/*
!

	@INFO:

	Package langs is the single source of truth for which source languages
	the tool understands and which directories it should never descend into.

	It has no third-party dependencies so that it can be used everywhere.
*/
package langs

import (
	"path/filepath"
	"sort"
	"strings"
)

/* @HINT: `Language` identifies a supported source language. */
type Language string

const (
	JavaScript Language = "javascript"
	Python     Language = "python"
	PHP        Language = "php"
	Go         Language = "go"
)

var extensions = map[Language][]string{
	JavaScript: {".js", ".jsx", ".mjs", ".cjs"},
	Python:     {".py"},
	PHP:        {".php"},
	Go:         {".go"},
}

var byExtension = func() map[string]Language {
	m := make(map[string]Language)
	for lang, exts := range extensions {
		for _, ext := range exts {
			m[ext] = lang
		}
	}
	return m
}()

/* @HINT: `FromExt(...)` maps a file extension (with leading dot) to a Language. */
func FromExt(ext string) (Language, bool) {
	lang, ok := byExtension[strings.ToLower(ext)]
	return lang, ok
}

/* @HINT: `FromPath(...)` maps a file path to a Language using its extension. */
func FromPath(p string) (Language, bool) {
	return FromExt(filepath.Ext(p))
}

/*
@HINT:

`Extensions(...)` returns the file extensions used by the language, in
resolution-priority order.
*/
func (l Language) Extensions() []string {
	return append([]string(nil), extensions[l]...)
}

/* @HINT: `SupportedExtensions(...)` returns every supported extension, sorted. */
func SupportedExtensions() []string {
	out := make([]string, 0, len(byExtension))
	for ext := range byExtension {
		out = append(out, ext)
	}
	sort.Strings(out)
	return out
}

var ignoredDirs = map[string]struct{}{
	"node_modules":     {},
	"vendor":           {},
	"__pycache__":      {},
	"venv":             {},
	".github":          {},
	".next":            {},
	"dist":             {},
	"build":            {},
	"target":           {},
	"bower_components": {},
}

/*
@HINT:

`ShouldSkipDir(...)` reports whether a directory name should never be scanned
(hidden directories, dependency caches, build output).
*/
func ShouldSkipDir(name string) bool {
	if name == "." || name == ".." || name == "" {
		return false
	}
	if strings.HasPrefix(name, ".") {
		return true
	}
	_, skip := ignoredDirs[name]
	return skip
}

/*
@HINT:

`ShouldSkipPath(...)` reports whether any directory component of a
slash-separated, root-relative path should be skipped. The final
component (the file name) is not considered.
*/
func ShouldSkipPath(slashPath string) bool {
	parts := strings.Split(slashPath, "/")
	for i := 0; i < len(parts)-1; i++ {
		if ShouldSkipDir(parts[i]) {
			return true
		}
	}
	return false
}

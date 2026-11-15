/*
!

	@INFO:

	Package modname builds the stable "module:file" prefixes used in
	function IDs, e.g.

	-	main.py                       (file in the project root)
	-	pkg.utils:helpers.py          (Python/JS: dotted directory)
	-	system:root.go                (Go file in the root: package name)
	-	Vendor.Module:index.php       (PHP: namespace, default "root")

	And also converted into hashes.
*/
package modname

import (
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/isocroft/tallaq/internal/langs"
)

var (
	goPackageRe = regexp.MustCompile(`(?m)^\s*package\s+([A-Za-z0-9_]+)`)
	phpNSRe     = regexp.MustCompile(`(?m)^\s*namespace\s+([A-Za-z0-9_\\]+)\s*;`)
)

/*
@HINT:

`Rel(...)` returns file relative to root, slash separated.
When file is outside root (or root is empty) only the base
name is returned.
*/
func Rel(root, file string) string {
	if root == "" {
		return filepath.Base(file)
	}
	rel, err := filepath.Rel(root, file)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.Base(file)
	}
	return filepath.ToSlash(rel)
}

/*
@NOTE:

For returns "<module>:<file>" for a root-relative path, or just "<file>"
when there is no module component.

Python/JS modules are derived from the project-relative directory,
and JS files get a module too so that two index.js files in different
folders no longer collide.
*/
func For(relPath string, src []byte, lang langs.Language) string {
	rel := path.Clean(filepath.ToSlash(relPath))
	base := path.Base(rel)
	dir := path.Dir(rel)

	var module string

	switch lang {
	case langs.Go:
		if dir == "." {
			module = "main"
			if m := goPackageRe.FindSubmatch(src); m != nil {
				module = string(m[1])
			}
		} else {
			module = dotted(dir)
		}
	case langs.PHP:
		module = "root"
		if m := phpNSRe.FindSubmatch(src); m != nil {
			module = strings.Trim(strings.ReplaceAll(string(m[1]), `\`, "."), ".")
		}
	default:
		module = dotted(dir)
	}

	if module == "" {
		return base
	}
	return module + ":" + base
}

func dotted(dir string) string {
	if dir == "." || dir == "/" {
		return ""
	}
	return strings.ReplaceAll(strings.Trim(dir, "/"), "/", ".")
}

package deptree

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/isocroft/tallaq/internal/langs"
)

/* @HINT: `resolution` is the outcome of mapping an import to files. */
type resolution struct {
	paths []string
	// local is true when the specifier clearly points inside the project
	// (relative path, project package, ...), so failing to resolve it is
	// worth reporting.
	local bool
}

func (b *Builder) resolve(lang langs.Language, from string, ref importRef) resolution {
	var res resolution

	switch lang {
	case langs.JavaScript:
		res = b.resolveJS(from, ref.Spec)
	case langs.Python:
		res = b.resolvePython(from, ref)
	case langs.PHP:
		res = b.resolvePHP(from, ref.Spec)
	case langs.Go:
		res = b.resolveGo(from, ref.Spec)
	}

	kept := make([]string, 0, len(res.paths))
	for _, p := range res.paths {
		if !b.ignored(p) {
			kept = append(kept, p)
		}
	}
	res.paths = kept

	return res
}

/*
@HINT:

`ignored(...)` reports whether p lives inside a vendored/hidden directory of
the project (node_modules, vendor, venv, ...).
*/
func (b *Builder) ignored(p string) bool {
	if b.root == "" {
		return false
	}

	rel, err := filepath.Rel(b.root, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return false
	}

	return langs.ShouldSkipPath(filepath.ToSlash(rel))
}

func firstFile(candidates ...string) (string, bool) {
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.Mode().IsRegular() {
			return c, true
		}
	}
	return "", false
}

func isRelativeSpec(spec string) bool {
	return spec == "." || spec == ".." ||
		strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../")
}

// resolveJS maps "./utils" to utils.js, utils.jsx, ..., utils/index.js.
// Bare specifiers ("react") are packages and are ignored.
func (b *Builder) resolveJS(from, spec string) resolution {
	if !isRelativeSpec(spec) && !filepath.IsAbs(spec) {
		return resolution{}
	}

	base := spec
	if !filepath.IsAbs(base) {
		base = filepath.Join(filepath.Dir(from), spec)
	}
	base = filepath.Clean(base)

	exts := langs.JavaScript.Extensions()

	candidates := []string{base}
	for _, ext := range exts {
		candidates = append(candidates, base+ext)
	}
	for _, ext := range exts {
		candidates = append(candidates, filepath.Join(base, "index"+ext))
	}

	if p, ok := firstFile(candidates...); ok {
		return resolution{paths: []string{p}, local: true}
	}

	return resolution{local: true}
}

// resolvePython handles relative imports ("from ..pkg import x", where each
// leading dot goes one package up) and absolute imports of modules that
// exist inside the project ("import utils"). Third-party and stdlib modules
// simply do not resolve to files and are ignored.
func (b *Builder) resolvePython(from string, ref importRef) resolution {
	spec := ref.Spec
	dots := len(spec) - len(strings.TrimLeft(spec, "."))
	rest := spec[dots:]

	if rest == "" && len(ref.Names) == 0 {
		return resolution{}
	}

	local := dots > 0

	var bases []string
	if local {
		base := filepath.Dir(from)
		for i := 1; i < dots; i++ {
			base = filepath.Dir(base)
		}
		bases = []string{base}
	} else {
		bases = []string{filepath.Dir(from)}
		if b.root != "" && b.root != filepath.Dir(from) {
			bases = append(bases, b.root)
		}
	}

	for _, base := range bases {
		modDir := base
		if rest != "" {
			modDir = filepath.Join(base, filepath.FromSlash(strings.ReplaceAll(rest, ".", "/")))
		}

		var candidates []string
		if rest != "" {
			candidates = append(candidates, modDir+".py", filepath.Join(modDir, "__init__.py"))
		}
		for _, name := range ref.Names {
			candidates = append(candidates,
				filepath.Join(modDir, name+".py"),
				filepath.Join(modDir, name, "__init__.py"),
			)
		}

		found := existingFiles(candidates)

		// "from . import helper" where helper is defined in the package's
		// __init__.py rather than a submodule.
		if len(found) == 0 && rest == "" {
			found = existingFiles([]string{filepath.Join(modDir, "__init__.py")})
		}

		if len(found) > 0 {
			return resolution{paths: found, local: local}
		}
	}

	return resolution{local: local}
}

func existingFiles(candidates []string) []string {
	var found []string
	seen := make(map[string]struct{}, len(candidates))

	for _, c := range candidates {
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}

		if info, err := os.Stat(c); err == nil && info.Mode().IsRegular() {
			found = append(found, c)
		}
	}

	return found
}

// --- PHP ----------------------------------------------------------------

// resolvePHP resolves include/require paths against the including file's
// directory, then against the project root.
func (b *Builder) resolvePHP(from, spec string) resolution {
	var candidates []string

	if filepath.IsAbs(spec) {
		candidates = append(candidates, spec)
	} else {
		candidates = append(candidates, filepath.Join(filepath.Dir(from), spec))
		if b.root != "" {
			candidates = append(candidates, filepath.Join(b.root, spec))
		}
	}

	if p, ok := firstFile(candidates...); ok {
		return resolution{paths: []string{p}, local: true}
	}

	return resolution{local: true}
}

// --- Go -----------------------------------------------------------------

type goModule struct {
	dir  string // directory containing go.mod
	path string // module path
}

var goModuleRe = regexp.MustCompile(`(?m)^module\s+"?([^\s"]+)"?`)

// goModuleFor finds the go.mod governing a file (nil when there is none).
func (b *Builder) goModuleFor(file string) *goModule {
	dir := filepath.Dir(file)

	for {
		if m, ok := b.goMods[dir]; ok {
			return m
		}

		if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
			var m *goModule
			if match := goModuleRe.FindSubmatch(data); match != nil {
				m = &goModule{dir: dir, path: string(match[1])}
			}
			b.goMods[dir] = m
			return m
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

// resolveGo maps an import of a package inside the current module to every
// non-test .go file of that package directory. Standard-library and
// third-party imports are ignored.
func (b *Builder) resolveGo(from, spec string) resolution {
	mod := b.goModuleFor(from)
	if mod == nil {
		return resolution{}
	}

	var rel string
	switch {
	case spec == mod.path:
		rel = ""
	case strings.HasPrefix(spec, mod.path+"/"):
		rel = strings.TrimPrefix(spec, mod.path+"/")
	default:
		return resolution{}
	}

	dir := filepath.Join(mod.dir, filepath.FromSlash(rel))

	entries, err := os.ReadDir(dir)
	if err != nil {
		return resolution{local: true}
	}

	var files []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}

	return resolution{paths: files, local: true}
}

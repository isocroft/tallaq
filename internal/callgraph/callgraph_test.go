package callgraph

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/isocroft/tallaq/internal/deptree"
	"github.com/isocroft/tallaq/internal/parse"
)

func writeProject(t *testing.T, root string, files map[string]string) {
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

func byID(fns []FunctionNode) map[string]FunctionNode {
	out := make(map[string]FunctionNode, len(fns))
	for _, fn := range fns {
		out[fn.ID] = fn
	}
	return out
}

// goProject is a tiny two-package Go module used by the integration tests.
//
// Line numbers matter: main is on line 5 and run on line 9 of main.go.
var goProject = map[string]string{
	"go.mod": "module example.com/app\n\ngo 1.21\n",
	"main.go": `package main
 
import "example.com/app/util"
 
func main() {
	run()
}
 
func run() {
	util.Helper()
	println("done")
}
 
var started = util.Boot()
`,
	"util/util.go": `package util
 
func Helper() {
	inner()
}
 
func inner() {}
 
func Boot() int {
	return 1
}
`,
}

func TestUniqueID(t *testing.T) {
	seen := make(map[string]int)

	got := []string{
		uniqueID(seen, "m:f"),
		uniqueID(seen, "m:f"),
		uniqueID(seen, "m:f"),
		uniqueID(seen, "m:g"),
	}
	want := []string{"m:f", "m:f#2", "m:f#3", "m:g"}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNormalizeCallees(t *testing.T) {
	calls := []parse.Call{
		{Name: "np.array"},
		{Name: "print"},
		{Name: "print"}, // duplicate
		{Name: ""},      // dropped
		{Name: "fn"},
		{Name: "np.array"}, // duplicate after aliasing
	}
	aliases := map[string]string{"np": "numpy", "fn": "pkg.fn"}

	got := normalizeCallees(calls, aliases)
	want := []string{"numpy.array", "print", "pkg.fn"}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	if got := normalizeCallees(nil, nil); len(got) != 0 {
		t.Errorf("no calls must give no callees, got %v", got)
	}
}

func TestBuildSignature(t *testing.T) {
	a := buildSignature("m:f", "(x int)")

	if a != buildSignature("m:f", "(x int)") {
		t.Error("signature must be deterministic")
	}
	if !strings.HasPrefix(a, "sig_") || len(a) != len("sig_")+16 {
		t.Errorf("unexpected format: %q", a)
	}
	if a == buildSignature("m:g", "(x int)") {
		t.Error("a different name must change the signature")
	}
	if a == buildSignature("m:f", "(x string)") {
		t.Error("different parameters must change the signature")
	}
}

// --- argument validation -------------------------------------------------------

func TestGenerateRejectsEmptyInput(t *testing.T) {
	if _, err := Generate(nil, Options{}); err == nil {
		t.Error("Generate(nil) must fail")
	}
	if _, err := GenerateAll(nil); err == nil {
		t.Error("GenerateAll(nil) must fail")
	}
	if _, err := GenerateFromGraph(nil, Options{}); err == nil {
		t.Error("GenerateFromGraph(nil) must fail")
	}
	if _, err := GenerateFromGraph(&deptree.Graph{}, Options{}); err == nil {
		t.Error("an empty graph must fail")
	}
}

func TestScanFileSkipsUnsupportedAndRejectsMissing(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, map[string]string{"notes.txt": "not code"})

	fns, err := scanFile(filepath.Join(root, "notes.txt"), root)
	if err != nil || fns != nil {
		t.Errorf("unsupported files are skipped silently, got %v, %v", fns, err)
	}

	if _, err := scanFile(filepath.Join(root, "missing.go"), root); err == nil {
		t.Error("a missing supported file must be an error")
	}
}

func TestGenerateOnGoProject(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, goProject)

	mainPath := filepath.Join(root, "main.go")
	utilPath := filepath.Join(root, "util", "util.go")

	node, err := deptree.NewBuilder(deptree.Options{ProjectRoot: root}).Build(mainPath)
	if err != nil {
		t.Fatal(err)
	}

	res, err := Generate(node, Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Calls) != 2 {
		t.Fatalf("want 2 scanned files, got %d", len(res.Calls))
	}
	if !reflect.DeepEqual(res.Deps[mainPath], []string{utilPath}) {
		t.Errorf("deps = %v", res.Deps)
	}

	mainFns := byID(res.Calls[mainPath])

	for _, id := range []string{"main:main.go:main", "main:main.go:run", "main:main.go:<global>"} {
		if _, ok := mainFns[id]; !ok {
			t.Fatalf("missing %s in %v", id, mainFns)
		}
	}

	if got := mainFns["main:main.go:main"]; got.Line != 5 || !reflect.DeepEqual(got.Callees, []string{"run"}) {
		t.Errorf("main: %+v", got)
	}

	/* @HINT: Import aliases are expanded: 'util.Helper' -> 'example.com/app/util.Helper'. */
	wantRun := []string{"example.com/app/util.Helper", "println"}
	if got := mainFns["main:main.go:run"]; got.Line != 9 || !reflect.DeepEqual(got.Callees, wantRun) {
		t.Errorf("run: %+v", got)
	}

	/* @NOTE: Loose, top-level calls are kept. */
	wantGlobal := []string{"example.com/app/util.Boot"}
	if got := mainFns["main:main.go:<global>"]; !reflect.DeepEqual(got.Callees, wantGlobal) {
		t.Errorf("<global>: %+v", got)
	}

	utilFns := byID(res.Calls[utilPath])
	for _, id := range []string{"util:util.go:Helper", "util:util.go:inner", "util:util.go:Boot"} {
		if _, ok := utilFns[id]; !ok {
			t.Errorf("missing %s in %v", id, utilFns)
		}
	}

	seen := make(map[string]bool)
	for _, fns := range res.Calls {
		for _, fn := range fns {
			if !strings.HasPrefix(fn.Signature, "sig_") {
				t.Errorf("%s: bad signature %q", fn.ID, fn.Signature)
			}
			if seen[fn.Signature] {
				t.Errorf("%s: signature collides with another function", fn.ID)
			}
			seen[fn.Signature] = true
		}
	}
}

func TestGenerateDetectsRootWhenNotGiven(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, goProject)

	node, err := deptree.NewBuilder(deptree.Options{ProjectRoot: root}).Build(filepath.Join(root, "main.go"))
	if err != nil {
		t.Fatal(err)
	}

	res, err := Generate(node, Options{}) /* @OTE: Root detected from 'go.mod' */
	if err != nil {
		t.Fatal(err)
	}

	utilFns := byID(res.Calls[filepath.Join(root, "util", "util.go")])
	if _, ok := utilFns["util:util.go:Helper"]; !ok {
		t.Errorf("module names should be relative to the detected root, got %v", utilFns)
	}
}

func TestGenerateAllReturnsTheCallMap(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, goProject)

	node, err := deptree.NewBuilder(deptree.Options{ProjectRoot: root}).Build(filepath.Join(root, "main.go"))
	if err != nil {
		t.Fatal(err)
	}

	calls, err := GenerateAll(node)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Errorf("want 2 files in the call map, got %d", len(calls))
	}
}

func TestGenerateFromGraphScansEveryFile(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, goProject)

	g, err := deptree.NewBuilder(deptree.Options{}).BuildDir(root)
	if err != nil {
		t.Fatal(err)
	}

	res, err := GenerateFromGraph(g, Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Calls) != 2 {
		t.Fatalf("want 2 files, got %d", len(res.Calls))
	}
	if len(res.Deps[filepath.Join(root, "main.go")]) != 1 {
		t.Errorf("main.go should depend on util.go, deps = %v", res.Deps)
	}
}

func TestGenerateHandlesDependencyCycles(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		"a.js": "import './b';\nexport function a() { b(); }\n",
		"b.js": "import './a';\nexport function b() { a(); }\n",
	})

	node, err := deptree.NewBuilder(deptree.Options{ProjectRoot: root}).Build(filepath.Join(root, "a.js"))
	if err != nil {
		t.Fatal(err)
	}

	res, err := Generate(node, Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Calls) != 2 {
		t.Fatalf("each file of a cycle is scanned exactly once, got %d", len(res.Calls))
	}

	/* @INFO: The two functions call each other, so the resolved graph has a cycle. */
	cycles := res.Graph().Cycles()
	if len(cycles) != 1 || len(cycles[0]) != 2 {
		t.Errorf("want one mutual-recursion cycle, got %v", cycles)
	}
}

func TestScanFileDisambiguatesRedefinitionsAndQualifiesMethods(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		"dup.go": "package main\n\nfunc f() {}\n\nfunc f() {}\n",
		"srv.go": "package main\n\ntype Server struct{}\n\n" +
			"func (s *Server) Handle() { s.log() }\n\n" +
			"func (s *Server) log() {}\n",
	})

	dup, err := scanFile(filepath.Join(root, "dup.go"), root)
	if err != nil {
		t.Fatal(err)
	}
	ids := byID(dup)
	if _, ok := ids["main:dup.go:f"]; !ok {
		t.Errorf("first definition keeps the plain ID: %v", ids)
	}
	if _, ok := ids["main:dup.go:f#2"]; !ok {
		t.Errorf("second definition must be suffixed: %v", ids)
	}

	srv, err := scanFile(filepath.Join(root, "srv.go"), root)
	if err != nil {
		t.Fatal(err)
	}
	methods := byID(srv)

	handle, ok := methods["main:srv.go:Server.Handle"]
	if !ok {
		t.Fatalf("methods are qualified by receiver type: %v", methods)
	}
	if handle.Name != "Handle" || handle.Qualified != "Server.Handle" {
		t.Errorf("handle: %+v", handle)
	}
	if !reflect.DeepEqual(handle.Callees, []string{"s.log"}) {
		t.Errorf("callees = %v", handle.Callees)
	}

	/* @NOTE: `s.log()` resolves to the `Server.log()` method of the same file. */
	res := &Result{Calls: CallMap{"srv.go": srv}, Deps: map[string][]string{}}
	got := res.Graph().GetSuccessorNodes("main:srv.go:Server.Handle")
	if !reflect.DeepEqual(got, []string{"main:srv.go:Server.log"}) {
		t.Errorf("successors = %v", got)
	}
}

func TestGraphResolutionRules(t *testing.T) {
	res := &Result{
		Calls: CallMap{
			"a": {
				{ID: "a:caller", Name: "caller", Qualified: "caller",
					Callees: []string{"helper", "x.run", "shared", "external.Thing"}},
				{ID: "a:T.helper", Name: "helper", Qualified: "T.helper"},
				{ID: "a:shared", Name: "shared", Qualified: "shared"},
			},
			"b": {
				{ID: "b:run", Name: "run", Qualified: "run"},
				{ID: "b:shared", Name: "shared", Qualified: "shared"},
			},
		},
		Deps: map[string][]string{"a": {"b"}},
	}

	got := res.Graph().GetSuccessorNodes("a:caller")

	want := []string{
		"a:shared",       // the file's own definition wins over the dependency's
		"b:run",          // qualified callee resolved through the import
		"external.Thing", // unknown: kept as an external node
		"helper",         // a bare call never resolves to a method
	}
	sortStrings(want)

	if !reflect.DeepEqual(got, want) {
		t.Errorf("successors = %v, want %v", got, want)
	}
}

func TestGraphDoesNotResolveThroughFilesThatAreNotImported(t *testing.T) {
	res := &Result{
		Calls: CallMap{
			"a": {{ID: "a:caller", Name: "caller", Qualified: "caller", Callees: []string{"run"}}},
			"b": {{ID: "b:run", Name: "run", Qualified: "run"}},
		},
		Deps: map[string][]string{}, // a does not import b
	}

	got := res.Graph().GetSuccessorNodes("a:caller")
	if !reflect.DeepEqual(got, []string{"run"}) {
		t.Errorf("an unimported file must not be linked, got %v", got)
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

package redundancy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, root string, files map[string]string) {
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

// --- pure helpers (no tree-sitter needed at runtime) ---------------------

func TestOptionsDefaults(t *testing.T) {
	o := Options{}.withDefaults()

	if o.MinLines != 5 || o.MinTokens != 25 || o.MinFeatures != 6 {
		t.Errorf("unexpected size defaults: %+v", o)
	}
	if o.NearThreshold != 0.80 || o.SemanticThreshold != 0.85 {
		t.Errorf("unexpected threshold defaults: %+v", o)
	}
	if o.Workers <= 0 {
		t.Errorf("Workers should default to NumCPU, got %d", o.Workers)
	}
	if _, ok := o.IgnoreNames["main"]; !ok {
		t.Error("default ignore list should contain main")
	}
}

func TestOptionsKeepExplicitValues(t *testing.T) {
	o := Options{
		MinLines:      9,
		MinTokens:     40,
		NearThreshold: 0.5,
		Workers:       2,
		IgnoreNames:   map[string]struct{}{},
	}.withDefaults()

	if o.MinLines != 9 || o.MinTokens != 40 || o.NearThreshold != 0.5 || o.Workers != 2 {
		t.Errorf("explicit values were overwritten: %+v", o)
	}
	if len(o.IgnoreNames) != 0 {
		t.Error("an explicit (empty) IgnoreNames must replace the built-in list")
	}
}

func TestIsTestFile(t *testing.T) {
	cases := map[string]bool{
		"pkg/server_test.go":    true,
		"tests/test_utils.py":   true,
		"utils_test.py":         true,
		"src/app.test.js":       true,
		"src/app.spec.js":       true,
		"tests/UserTest.php":    true,
		"pkg/server.go":         false,
		"utils.py":              false,
		"src/app.js":            false,
		"src/testimony.go":      false,
		"contest.php":           false,
		"src/latest_results.py": false,
	}

	for path, want := range cases {
		if got := isTestFile(path); got != want {
			t.Errorf("isTestFile(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestLastSegment(t *testing.T) {
	cases := map[string]string{
		"run":             "run",
		"db.insert":       "insert",
		"a.b.c":           "c",
		"Server.Handle":   "Handle",
		"numpy.array":     "array",
		"./x.fetch":       "fetch",
		"":                "",
		"trailing.":       "",
		"project/svc.Foo": "Foo",
	}

	for in, want := range cases {
		if got := lastSegment(in); got != want {
			t.Errorf("lastSegment(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHashHex(t *testing.T) {
	a, b := hashHex("(block (return v0))"), hashHex("(block (return v0))")
	if a != b {
		t.Error("hashing must be deterministic")
	}
	if len(a) != 16 {
		t.Errorf("want 16 hex chars, got %d (%s)", len(a), a)
	}
	if a == hashHex("(block (return v1))") {
		t.Error("different input must give a different hash")
	}
}

func TestFileInfoID(t *testing.T) {
	fi := &fileInfo{module: "pkg.utils:helpers.py"}
	fn := &funcInfo{qualified: "Runner.run"}

	if got := fi.id(fn); got != "pkg.utils:helpers.py:Runner.run" {
		t.Errorf("id = %q", got)
	}
}

func TestSnapshotIsSortedCopy(t *testing.T) {
	a := New(Options{})
	a.files = []*fileInfo{{path: "b.go"}, {path: "a.go"}, {path: "c.go"}}

	snap := a.snapshot()

	want := []string{"a.go", "b.go", "c.go"}
	for i, fi := range snap {
		if fi.path != want[i] {
			t.Fatalf("snapshot order = %v, want %v", paths(snap), want)
		}
	}
	if a.files[0].path != "b.go" {
		t.Error("snapshot must not reorder the analyzer's own slice")
	}
}

func paths(files []*fileInfo) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.path
	}
	return out
}

func TestAnalyzeOnEmptyAnalyzerReturnsEmptyNotNil(t *testing.T) {
	r := New(Options{}).Analyze()

	if r.Dead == nil || r.Clones == nil {
		t.Fatalf("Analyze must return non-nil slices, got %+v", r)
	}
	if len(r.Dead) != 0 || len(r.Clones) != 0 {
		t.Errorf("nothing was processed, got %+v", r)
	}
}

func TestAnalyzeCombinesBothAnalyses(t *testing.T) {
	a := New(Options{})
	a.files = []*fileInfo{
		goFile("a.go", []funcInfo{
			{name: "orphan", qualified: "orphan", line: 1, endLine: 2},
		}, map[string]int{}),
	}

	r := a.Analyze()
	if len(r.Dead) != 1 || r.Dead[0].Kind != UnusedFunction {
		t.Fatalf("dead findings: %+v", r.Dead)
	}
	if len(r.Clones) != 0 {
		t.Errorf("no clones expected: %+v", r.Clones)
	}
}

// --- processing (requires tree-sitter) -----------------------------------

func TestProcessErrors(t *testing.T) {
	a := New(Options{})

	if err := a.ProcessSource("notes.txt", []byte("hello")); err == nil {
		t.Error("an unsupported extension must be an error")
	}
	if err := a.ProcessFile(filepath.Join(t.TempDir(), "missing.go")); err == nil {
		t.Error("a missing file must be an error")
	}
	if n := len(a.snapshot()); n != 0 {
		t.Errorf("failed files must not be recorded, got %d", n)
	}
}

const computeSource = `package sample
 
import "strings"
 
func Compute(a int, b int) int {
	total := 0
	name := strings.TrimSpace(" x ")
	for i := 0; i < a; i++ {
		total += b * i
	}
	return total + len(name)
}
 
func tiny() {}
`

func TestProcessSourceRecordsFunctionFacts(t *testing.T) {
	a := New(Options{MinLines: 3, MinTokens: 5})
	if err := a.ProcessSource("sample.go", []byte(computeSource)); err != nil {
		t.Fatal(err)
	}

	files := a.snapshot()
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}

	fi := files[0]
	if fi.test {
		t.Error("sample.go is not a test file")
	}
	if fi.module != "sample:sample.go" {
		t.Errorf("module = %q", fi.module)
	}

	byName := make(map[string]*funcInfo)
	for i := range fi.funcs {
		byName[fi.funcs[i].name] = &fi.funcs[i]
	}

	compute, ok := byName["Compute"]
	if !ok {
		t.Fatalf("Compute not found in %v", byName)
	}
	if !compute.exported || compute.method {
		t.Errorf("Compute should be an exported plain function: %+v", compute)
	}
	if compute.arity != 2 {
		t.Errorf("arity = %d, want 2", compute.arity)
	}
	if !contains(compute.calls, "TrimSpace") {
		t.Errorf("calls should hold the last segment of strings.TrimSpace: %v", compute.calls)
	}
	if !compute.eligible || compute.shapeHash == "" || compute.textHash == "" ||
		len(compute.shingles) == 0 || len(compute.features) == 0 {
		t.Errorf("Compute should carry similarity data: %+v", compute)
	}

	tiny, ok := byName["tiny"]
	if !ok {
		t.Fatal("tiny not found")
	}
	if tiny.exported {
		t.Error("tiny is unexported")
	}
	if tiny.eligible {
		t.Error("a one-line function is below MinLines and must not be compared")
	}
}

func TestTestFilesAreScannedButNotCompared(t *testing.T) {
	skip := New(Options{MinLines: 3, MinTokens: 5})
	if err := skip.ProcessSource("sample_test.go", []byte(computeSource)); err != nil {
		t.Fatal(err)
	}

	fi := skip.snapshot()[0]
	if !fi.test {
		t.Fatal("sample_test.go must be flagged as a test file")
	}
	for _, fn := range fi.funcs {
		if fn.eligible {
			t.Errorf("%s: functions in test files are not compared by default", fn.name)
		}
	}

	include := New(Options{MinLines: 3, MinTokens: 5, IncludeTests: true})
	if err := include.ProcessSource("sample_test.go", []byte(computeSource)); err != nil {
		t.Fatal(err)
	}

	found := false
	for _, fn := range include.snapshot()[0].funcs {
		found = found || fn.eligible
	}
	if !found {
		t.Error("IncludeTests should make test-file functions comparable")
	}
}

func TestProcessDirFindsDeadCodeAndSkipsVendored(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"main.go": "package main\n\nfunc main() { used() }\n\nfunc used() int {\n\treturn 1\n}\n\nfunc orphan() int {\n\treturn 2\n}\n",
		"lib.go":  "package main\n\nfunc onlyInTests() {}\n",
		"lib_test.go": "package main\n\nimport \"testing\"\n\n" +
			"func TestX(t *testing.T) { onlyInTests() }\n",
		"vendor/dep.go": "package dep\n\nfunc vendored() {}\n",
		"notes.txt":     "not source code",
	})

	a := New(Options{Root: root})
	if err := a.ProcessDir(context.Background(), root); err != nil {
		t.Fatal(err)
	}

	for _, fi := range a.snapshot() {
		if strings.Contains(filepath.ToSlash(fi.path), "/vendor/") {
			t.Errorf("vendored file was scanned: %s", fi.path)
		}
	}
	if n := len(a.snapshot()); n != 3 {
		t.Fatalf("want 3 scanned files (main.go, lib.go, lib_test.go), got %d", n)
	}

	items := a.AnalyzeDead()
	if len(items) != 1 {
		t.Fatalf("want exactly the orphan function, got %+v", items)
	}

	got := items[0]
	if got.Kind != UnusedFunction || got.Confidence != High || !strings.HasSuffix(got.Name, ":orphan") {
		t.Errorf("unexpected finding: %+v", got)
	}
}

func TestProcessDirHonoursCancellation(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"a.go": "package a\n\nfunc F() {}\n"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := New(Options{}).ProcessDir(ctx, root)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestRenamedCopiesAreIdenticalLogic(t *testing.T) {
	const a = `package sample
 
func Sum(values []int) int {
	total := 0
	for _, v := range values {
		total += v
	}
	return total
}
`
	const b = `package sample
 
func Add(items []int) int {
	acc := 0
	for _, x := range items {
		acc += x
	}
	return acc
}
`

	an := New(Options{MinLines: 3, MinTokens: 10})
	if err := an.ProcessSource("a.go", []byte(a)); err != nil {
		t.Fatal(err)
	}
	if err := an.ProcessSource("b.go", []byte(b)); err != nil {
		t.Fatal(err)
	}

	groups := findKind(an.AnalyzeClones(), IdenticalLogic)
	if len(groups) != 1 || len(groups[0].Members) != 2 {
		t.Fatalf("renamed copies should form one identical-logic group, got %+v", an.AnalyzeClones())
	}

	m := groups[0].Members
	if !strings.HasSuffix(m[0].Function, ":Sum") || !strings.HasSuffix(m[1].Function, ":Add") {
		t.Errorf("unexpected members: %+v", m)
	}
}

func TestConcurrentProcessingIsSafe(t *testing.T) {
	root := t.TempDir()
	files := make(map[string]string)
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		files[name+".go"] = "package p\n\nfunc " + strings.ToUpper(name) + "() int {\n\treturn 1\n}\n"
	}
	writeTree(t, root, files)

	a := New(Options{Workers: 4})
	if err := a.ProcessDir(context.Background(), root); err != nil {
		t.Fatal(err)
	}

	if n := len(a.snapshot()); n != len(files) {
		t.Fatalf("want %d files, got %d", len(files), n)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

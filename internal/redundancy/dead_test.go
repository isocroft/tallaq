package redundancy

import (
	"testing"

	"github.com/isocroft/tallaq/internal/langs"
	"github.com/isocroft/tallaq/internal/parse"
)

func names(items []DeadItem, kind DeadKind) map[string]DeadItem {
	out := make(map[string]DeadItem)
	for _, it := range items {
		if it.Kind == kind {
			out[it.Name] = it
		}
	}
	return out
}

func goFile(path string, funcs []funcInfo, names map[string]int) *fileInfo {
	return &fileInfo{
		path:   path,
		module: "app:" + path,
		lang:   langs.Go,
		funcs:  funcs,
		idents: parse.Identifiers{Names: names, Strings: map[string]int{}},
	}
}

func TestDeadFunctionsAndTransitiveChains(t *testing.T) {
	a := New(Options{})
	a.files = []*fileInfo{
		goFile("main.go", []funcInfo{
			{name: "main", qualified: "main", line: 1, endLine: 3, calls: []string{"run"}},
			{name: "run", qualified: "run", line: 5, endLine: 9, calls: []string{"helper"}},
			{name: "helper", qualified: "helper", line: 11, endLine: 12},
			{name: "orphan", qualified: "orphan", line: 14, endLine: 15},
			{name: "dead1", qualified: "dead1", line: 17, endLine: 18, calls: []string{"dead2"}},
			{name: "dead2", qualified: "dead2", line: 20, endLine: 21},
			{name: "loop", qualified: "loop", line: 23, endLine: 25, calls: []string{"loop"}},
			{name: "Public", qualified: "Public", line: 27, endLine: 28, exported: true},
		}, map[string]int{
			// main is an entry point and calls run; run calls helper...
			"run": 1, "helper": 1, "dead2": 1, "loop": 1,
		}),
	}

	got := names(a.AnalyzeDead(), UnusedFunction)

	for _, want := range []string{"app:main.go:orphan", "app:main.go:dead1", "app:main.go:dead2", "app:main.go:loop", "app:main.go:Public"} {
		if _, ok := got[want]; !ok {
			t.Errorf("expected %s to be reported, got %v", want, keys(got))
		}
	}
	for _, notWant := range []string{"app:main.go:main", "app:main.go:run", "app:main.go:helper"} {
		if _, ok := got[notWant]; ok {
			t.Errorf("%s is live and must not be reported", notWant)
		}
	}

	if !got["app:main.go:dead2"].Transitive || got["app:main.go:dead1"].Transitive {
		t.Error("dead2 should be transitive, dead1 should not")
	}
	if got["app:main.go:orphan"].Confidence != High {
		t.Error("private unreferenced function must be high confidence")
	}
	if got["app:main.go:Public"].Confidence != Medium {
		t.Error("exported unreferenced function must be medium confidence")
	}
}

func TestPrivateOnlyDropsMediumFindings(t *testing.T) {
	a := New(Options{PrivateOnly: true})
	a.files = []*fileInfo{
		goFile("a.go", []funcInfo{
			{name: "Public", qualified: "Public", exported: true},
			{name: "method", qualified: "T.method", method: true},
			{name: "hidden", qualified: "hidden"},
		}, map[string]int{}),
	}

	got := names(a.AnalyzeDead(), UnusedFunction)
	if len(got) != 1 {
		t.Fatalf("want only the private function, got %v", keys(got))
	}
	if _, ok := got["app:a.go:hidden"]; !ok {
		t.Errorf("got %v", keys(got))
	}
}

func TestStringReferencesKeepFunctionsAlive(t *testing.T) {
	fi := goFile("a.go", []funcInfo{{name: "dispatchMe", qualified: "dispatchMe"}}, map[string]int{})
	fi.idents.Strings["dispatchMe"] = 1

	a := New(Options{})
	a.files = []*fileInfo{fi}

	if got := a.AnalyzeDead(); len(got) != 0 {
		t.Fatalf("a name used as a string is dynamically referenced, got %v", got)
	}
}

func TestTestFilesCountAsReferencesButAreNotReported(t *testing.T) {
	prod := goFile("lib.go", []funcInfo{{name: "onlyTested", qualified: "onlyTested"}}, map[string]int{})
	tests := goFile("lib_test.go", []funcInfo{{name: "helperInTest", qualified: "helperInTest"}}, map[string]int{"onlyTested": 1})
	tests.test = true

	a := New(Options{})
	a.files = []*fileInfo{prod, tests}

	if got := a.AnalyzeDead(); len(got) != 0 {
		t.Fatalf("code used by tests is live, and test files are not reported: %v", got)
	}
}

func TestEntryPointsAndFrameworkHooksAreIgnored(t *testing.T) {
	py := &fileInfo{
		path: "app.py", module: "app.py", lang: langs.Python,
		funcs: []funcInfo{
			{name: "__init__", qualified: "C.__init__", method: true},
			{name: "route", qualified: "route", decorated: true},
			{name: "test_it", qualified: "test_it"},
			{name: "unused_helper", qualified: "unused_helper"},
		},
		idents: parse.Identifiers{Names: map[string]int{}, Strings: map[string]int{}},
	}

	a := New(Options{})
	a.files = []*fileInfo{py}

	got := names(a.AnalyzeDead(), UnusedFunction)
	if len(got) != 1 {
		t.Fatalf("only unused_helper should be reported, got %v", keys(got))
	}
}

func TestUnusedImports(t *testing.T) {
	py := &fileInfo{
		path: "m.py", module: "m.py", lang: langs.Python,
		imports: []parse.Import{
			{Path: "os", Line: 1},
			{Path: "sys", Line: 2},
			{Path: "numpy", Local: "np", Line: 3},
			{Path: "pkg", Name: "thing", Local: "thing", Line: 4},
		},
		idents: parse.Identifiers{
			Names:   map[string]int{"sys": 2, "np": 1},
			Strings: map[string]int{},
		},
	}

	js := &fileInfo{
		path: "a.js", module: "a.js", lang: langs.JavaScript,
		imports: []parse.Import{
			{Path: "./x", Local: "x", Line: 1, Require: true},
			{Path: "./y", Local: "y", Line: 2, Require: true},
		},
		// require declarations mention the local once; only y is used again.
		idents: parse.Identifiers{Names: map[string]int{"x": 1, "y": 2}, Strings: map[string]int{}},
	}

	a := New(Options{})
	a.files = []*fileInfo{py, js}

	got := names(a.AnalyzeDead(), UnusedImport)
	for _, want := range []string{"os", "pkg.thing", "./x as x"} {
		if _, ok := got[want]; !ok {
			t.Errorf("expected unused import %q, got %v", want, keys(got))
		}
	}
	if len(got) != 3 {
		t.Errorf("want exactly 3 unused imports, got %v", keys(got))
	}
}

func TestUnreachableFindingsAreForwarded(t *testing.T) {
	fi := goFile("a.go", nil, map[string]int{})
	fi.unreachable = []parse.Unreachable{
		{Kind: "after-terminator", Line: 10, EndLine: 12, Detail: "code after return on line 9 can never run"},
		{Kind: "constant-condition", Line: 20, EndLine: 22, Detail: "condition is always false"},
	}

	a := New(Options{})
	a.files = []*fileInfo{fi}

	items := a.AnalyzeDead()
	if len(items) != 2 || items[0].Kind != UnreachableCode || items[1].Kind != DeadBranch {
		t.Fatalf("got %+v", items)
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

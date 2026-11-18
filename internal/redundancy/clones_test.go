package redundancy

import (
	"testing"

	"github.com/isocroft/tallaq/internal/langs"
)

func seq(from, to uint64) []uint64 {
	var out []uint64
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

func cloneFile(path string, funcs ...funcInfo) *fileInfo {
	return &fileInfo{path: path, module: path, lang: langs.Python, funcs: funcs}
}

func eligible(name, shape, text string, tokens int, shingles []uint64, feats map[string]int, arity int) funcInfo {
	return funcInfo{
		name: name, qualified: name, line: 1, endLine: 20, arity: arity,
		eligible: true, shapeHash: shape, textHash: text,
		tokens: tokens, shingles: shingles, features: feats,
	}
}

func findKind(groups []CloneGroup, kind CloneKind) []CloneGroup {
	var out []CloneGroup
	for _, g := range groups {
		if g.Kind == kind {
			out = append(out, g)
		}
	}
	return out
}

func TestIdenticalAndSameStructureGroups(t *testing.T) {
	a := New(Options{})
	a.files = []*fileInfo{
		cloneFile("a.py",
			eligible("f", "S1", "T1", 40, seq(1, 36), nil, 2),
			eligible("g", "S1", "T1", 40, seq(1, 36), nil, 2),
		),
		cloneFile("b.py",
			eligible("h", "S2", "T2", 40, seq(1000, 1036), nil, 2),
			eligible("i", "S2", "T3", 40, seq(1000, 1036), nil, 2), // same shape, different literals
		),
	}

	groups := a.AnalyzeClones()

	if got := findKind(groups, IdenticalLogic); len(got) != 1 || len(got[0].Members) != 2 {
		t.Fatalf("identical groups: %+v", got)
	}
	if got := findKind(groups, SameStructure); len(got) != 1 || len(got[0].Members) != 2 {
		t.Fatalf("same-structure groups: %+v", got)
	}
}

func TestNearMissClones(t *testing.T) {
	// 19 of 21 distinct shingles in common -> Jaccard ~0.905.
	setA := seq(1, 20)
	setB := append(seq(1, 19), 500)

	a := New(Options{})
	a.files = []*fileInfo{
		cloneFile("a.py", eligible("f", "SA", "TA", 30, setA, nil, 1)),
		cloneFile("b.py", eligible("g", "SB", "TB", 31, setB, nil, 1)),
		cloneFile("c.py", eligible("unrelated", "SC", "TC", 30, seq(9000, 9020), nil, 1)),
	}

	got := findKind(a.AnalyzeClones(), NearMiss)
	if len(got) != 1 || len(got[0].Members) != 2 {
		t.Fatalf("want one near-miss pair, got %+v", got)
	}
	if got[0].Similarity < 0.8 || got[0].Similarity > 1 {
		t.Errorf("similarity out of range: %v", got[0].Similarity)
	}
}

func TestSameBehaviorWithDifferentCode(t *testing.T) {
	featsA := map[string]int{"call:sorted": 1, "call:len": 1, "lit:2": 1, "ctl:loop": 1, "op:+": 2, "ctl:return": 1}
	featsB := map[string]int{"call:sorted": 1, "call:len": 1, "lit:2": 1, "ctl:loop": 1, "op:+": 2, "ctl:return": 1, "op:-": 0}

	a := New(Options{})
	a.files = []*fileInfo{
		// completely different token shingles: not a near-miss
		cloneFile("a.py", eligible("median_a", "SA", "TA", 40, seq(1, 30), featsA, 1)),
		cloneFile("b.py", eligible("median_b", "SB", "TB", 40, seq(5000, 5030), featsB, 1)),
		// same features but a different arity must not match
		cloneFile("c.py", eligible("median_c", "SC", "TC", 40, seq(7000, 7030), featsA, 3)),
	}

	groups := a.AnalyzeClones()

	if len(findKind(groups, NearMiss)) != 0 {
		t.Fatalf("no near-miss expected: %+v", groups)
	}

	got := findKind(groups, SameBehavior)
	if len(got) != 1 {
		t.Fatalf("want one same-behavior group, got %+v", groups)
	}
	if len(got[0].Members) != 2 {
		t.Errorf("arity mismatch must be excluded, members: %+v", got[0].Members)
	}

	hasEvidence := false
	for _, e := range got[0].Evidence {
		if e == "call:sorted" {
			hasEvidence = true
		}
	}
	if !hasEvidence {
		t.Errorf("evidence should mention the shared callee, got %v", got[0].Evidence)
	}
}

func TestDifferentLanguagesAreNeverCompared(t *testing.T) {
	py := cloneFile("a.py", eligible("f", "S", "T", 40, seq(1, 30), nil, 1))
	js := &fileInfo{
		path: "a.js", module: "a.js", lang: langs.JavaScript,
		funcs: []funcInfo{eligible("g", "S", "T", 40, seq(1, 30), nil, 1)},
	}

	a := New(Options{})
	a.files = []*fileInfo{py, js}

	if got := a.AnalyzeClones(); len(got) != 0 {
		t.Fatalf("cross-language comparison must not happen, got %+v", got)
	}
}

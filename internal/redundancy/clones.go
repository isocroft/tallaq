package redundancy

import (
	"sort"
	"strings"

	"github.com/isocroft/tallaq/internal/langs"
	"github.com/isocroft/tallaq/internal/redundancy/fingerprint"
)

/* @HINT: `CloneKind` says how two functions are alike, from strictest to loosest. */
type CloneKind string

const (
	// IdenticalLogic: same canonical form. Differences are limited to
	// formatting, comments, variable names and operand order of
	// commutative operators (a+b vs b+a, x>y vs y<x).
	IdenticalLogic CloneKind = "identical-logic"
	// SameStructure: identical except for literal values (e.g. a tax rate
	// of 0.2 vs 0.3), i.e. a parameterisable copy.
	SameStructure CloneKind = "same-structure"
	// NearMiss: mostly the same code with statements added, removed or
	// changed (shingle similarity >= NearThreshold).
	NearMiss CloneKind = "near-miss"
	// SameBehavior: different code that looks like it computes the same
	// thing: same arity, same callees/literals/operators and control-flow
	// profile. This is a heuristic, not a proof of equivalence.
	SameBehavior CloneKind = "same-behavior"
)

/* @HINT: `CloneMember` is one function in a clone group. */
type CloneMember struct {
	File     string `json:"file"`
	Function string `json:"function"` // "pkg.utils:helpers.py:Runner.run"
	Line     uint32 `json:"line"`
	EndLine  uint32 `json:"end_line"`
	Lines    int    `json:"lines"`
}

/* @HINT: `CloneGroup` is a set of functions that are alike. */
type CloneGroup struct {
	Kind       CloneKind     `json:"kind"`
	Similarity float64       `json:"similarity"`
	Members    []CloneMember `json:"members"`
	// Evidence lists the behavioural features all members share
	// (same-behavior only), e.g. "call:sorted", "lit:2", "ctl:loop".
	Evidence []string `json:"evidence,omitempty"`
}

const (
	shingleSize = 5
	maxDF       = 60 // ignore shingles/features present in more functions than this
)

func shingle(tokens []string) []uint64 {
	return fingerprint.Shingles(tokens, shingleSize)
}

type member struct {
	fi *fileInfo
	fn *funcInfo
}

type edge struct {
	a, b  int
	score float64
}

/*
@HINT:

`AnalyzeClones(...)` finds similar functions. Functions are only compared
with others written in the same language.
*/
func (a *Analyzer) AnalyzeClones() []CloneGroup {
	byLang := make(map[langs.Language][]member)

	for _, fi := range a.snapshot() {
		for i := range fi.funcs {
			if fi.funcs[i].eligible {
				byLang[fi.lang] = append(byLang[fi.lang], member{fi, &fi.funcs[i]})
			}
		}
	}

	names := make([]string, 0, len(byLang))
	for l := range byLang {
		names = append(names, string(l))
	}
	sort.Strings(names)

	var out []CloneGroup
	for _, n := range names {
		out = append(out, a.clonesFor(byLang[langs.Language(n)])...)
	}

	order := map[CloneKind]int{IdenticalLogic: 0, SameStructure: 1, NearMiss: 2, SameBehavior: 3}
	sort.SliceStable(out, func(i, j int) bool {
		x, y := out[i], out[j]
		if order[x.Kind] != order[y.Kind] {
			return order[x.Kind] < order[y.Kind]
		}
		if x.Similarity != y.Similarity {
			return x.Similarity > y.Similarity
		}
		if len(x.Members) != len(y.Members) {
			return len(x.Members) > len(y.Members)
		}
		return x.Members[0].File < y.Members[0].File
	})

	return out
}

func (a *Analyzer) clonesFor(ms []member) []CloneGroup {
	if len(ms) < 2 {
		return nil
	}

	/* @HINT: Units: functions with the same canonical shape are one unit. */
	unitOf := make(map[string]int)
	var units [][]int

	for i, m := range ms {
		u, ok := unitOf[m.fn.shapeHash]
		if !ok {
			u = len(units)
			unitOf[m.fn.shapeHash] = u
			units = append(units, nil)
		}
		units[u] = append(units[u], i)
	}

	var out []CloneGroup

	for _, u := range units {
		if len(u) < 2 {
			continue
		}

		kind := IdenticalLogic
		for _, i := range u[1:] {
			if ms[i].fn.textHash != ms[u[0]].fn.textHash {
				kind = SameStructure
				break
			}
		}

		out = append(out, CloneGroup{Kind: kind, Similarity: 1, Members: toMembers(ms, u)})
	}

	/* @HINT: From here on compare one representative (`reps[u]`) per unit. */
	reps := make([]int, len(units))
	for u, idxs := range units {
		reps[u] = idxs[0]
	}

	/* @HINT: Record Near-miss clones: shingle similarity via an inverted index. */
	shingleSets := make([][]uint64, len(units))
	for u := range units {
		shingleSets[u] = ms[reps[u]].fn.shingles
	}

	nearUF := fingerprint.NewUnionFind(len(units))
	var nearEdges []edge

	for _, p := range fingerprint.Candidates(shingleSets, maxDF, 3) {
		if sizeRatio(ms[reps[p.A]].fn.tokens, ms[reps[p.B]].fn.tokens) < 0.5 {
			continue
		}

		score := fingerprint.JaccardSorted(shingleSets[p.A], shingleSets[p.B])
		if score < a.opts.NearThreshold {
			continue
		}

		nearUF.Union(p.A, p.B)
		nearEdges = append(nearEdges, edge{p.A, p.B, score})
	}

	out = append(out, buildClusters(nearUF, nearEdges, units, ms, NearMiss, nil)...)

	/* @HINT: Same behaviour: compare behavioural feature bags. */
	featureSets := make([][]uint64, len(units))
	for u := range units {
		featureSets[u] = fingerprint.HashStrings(distinguishing(ms[reps[u]].fn.features))
	}

	semUF := fingerprint.NewUnionFind(len(units))
	var semEdges []edge

	for _, p := range fingerprint.Candidates(featureSets, maxDF, 2) {
		fa, fb := ms[reps[p.A]].fn, ms[reps[p.B]].fn

		if fa.arity != fb.arity {
			continue
		}
		if nearUF.Find(p.A) == nearUF.Find(p.B) {
			continue // already reported as a near-miss clone
		}
		if total(fa.features) < a.opts.MinFeatures || total(fb.features) < a.opts.MinFeatures {
			continue
		}

		score := fingerprint.WeightedJaccard(fa.features, fb.features)
		if score < a.opts.SemanticThreshold {
			continue
		}

		semUF.Union(p.A, p.B)
		semEdges = append(semEdges, edge{p.A, p.B, score})
	}

	evidence := func(us []int) []string {
		var common map[string]struct{}

		for _, u := range us {
			cur := make(map[string]struct{}, len(ms[reps[u]].fn.features))
			for k := range ms[reps[u]].fn.features {
				cur[k] = struct{}{}
			}

			if common == nil {
				common = cur
				continue
			}
			for k := range common {
				if _, ok := cur[k]; !ok {
					delete(common, k)
				}
			}
		}

		keys := make([]string, 0, len(common))
		for k := range common {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) > 10 {
			keys = keys[:10]
		}
		return keys
	}

	out = append(out, buildClusters(semUF, semEdges, units, ms, SameBehavior, evidence)...)

	return out
}

/* @HINT: `buildClusters(...)` turns union-find components of units into clone groups. */
func buildClusters(
	uf *fingerprint.UnionFind,
	edges []edge,
	units [][]int,
	ms []member,
	kind CloneKind,
	evidence func(unitIdx []int) []string,
) []CloneGroup {
	type agg struct {
		units []int
		sum   float64
		n     int
	}

	groups := make(map[int]*agg)

	for _, e := range edges {
		root := uf.Find(e.a)
		g := groups[root]
		if g == nil {
			g = &agg{}
			groups[root] = g
		}
		g.sum += e.score
		g.n++
	}

	for u := range units {
		if g, ok := groups[uf.Find(u)]; ok {
			g.units = append(g.units, u)
		}
	}

	roots := make([]int, 0, len(groups))
	for r := range groups {
		roots = append(roots, r)
	}
	sort.Ints(roots)

	var out []CloneGroup
	for _, r := range roots {
		g := groups[r]

		var idxs []int
		for _, u := range g.units {
			idxs = append(idxs, units[u]...)
		}

		cg := CloneGroup{
			Kind:       kind,
			Similarity: g.sum / float64(g.n),
			Members:    toMembers(ms, idxs),
		}
		if evidence != nil {
			cg.Evidence = evidence(g.units)
		}
		out = append(out, cg)
	}

	return out
}

func toMembers(ms []member, idxs []int) []CloneMember {
	out := make([]CloneMember, 0, len(idxs))
	for _, i := range idxs {
		m := ms[i]
		out = append(out, CloneMember{
			File:     m.fi.path,
			Function: m.fi.id(m.fn),
			Line:     m.fn.line,
			EndLine:  m.fn.endLine,
			Lines:    int(m.fn.endLine-m.fn.line) + 1,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})

	return out
}

/*
@HINT:

`distinguishing(...)` keeps the features that say *what* a function does
(callees and literals) rather than generic structure; they drive
candidate selection.
*/
func distinguishing(features map[string]int) []string {
	out := make([]string, 0, len(features))
	for k := range features {
		if strings.HasPrefix(k, "call:") || strings.HasPrefix(k, "lit:") {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func total(features map[string]int) int {
	n := 0
	for _, v := range features {
		n += v
	}
	return n
}

func sizeRatio(a, b int) float64 {
	lo, hi := min(a, b), max(a, b)
	if hi == 0 {
		return 0
	}
	return float64(lo) / float64(hi)
}

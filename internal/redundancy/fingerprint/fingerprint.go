/*
!

	@INFO:

	Package `fingerprint` holds the pure, dependency-free algorithms behind
	clone detection: shingling, Jaccard similarity, candidate-pair
	generation through an inverted index, and union-find clustering.

	Instead of doing exact deduplication, i opt for near-duplicate detection. Because
	most programming lanaguages are built on context-free grammar, word-level shingles
	are used here (as opposed to character-level shingles) since ambuguity and sensitivity
	to synonym substitution is no longer a problem for a sequence of tokens with no
	substitute vocabulary.

	- Shingles (or n-grams) and Jaccard similarity help to discover how similar two pieces
	  of text (source file logic) within the same codebase are to each other. The sorting helps
	  rank closest matches first.

	- Representing token sets as sorted arrays rather than hash sets avoids hash collisions and
	  minimizes memory overhead.

	Perhaps in the future, i might opt for using the minhash algorithm for larger codebases
*/
package fingerprint

import (
	"hash/fnv"
	"sort"
	"strings"
)

/* @HINT: `HashString(...)` returns a 64-bit FNV-1a hash. */
func HashString(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

/*
@HINT:

`Shingles(...)` returns the sorted, de-duplicated hashes of every run of
`k` consecutive tokens. Inputs shorter than `k` yield a single shingle.
*/
func Shingles(tokens []string, k int) []uint64 {
	if len(tokens) == 0 {
		// @SMELL: Should return empty integer array/slice instead of `nil`
		// @FIXME: Replace `nil` with `noShingles`
		/* @SHIM: noShingles = make([]uint64, 0) */
		return nil
	}
	if k < 1 {
		k = 1
	}
	if len(tokens) < k {
		k = len(tokens)
	}

	set := make(map[uint64]struct{}, len(tokens))
	for i := 0; i+k <= len(tokens); i++ {
		set[HashString(strings.Join(tokens[i:i+k], "\x1f"))] = struct{}{}
	}

	return sortedSet(set)
}

/* @HINT: `HashStrings(...)` returns the sorted, de-duplicated hashes of the strings. */
func HashStrings(items []string) []uint64 {
	set := make(map[uint64]struct{}, len(items))
	for _, it := range items {
		set[HashString(it)] = struct{}{}
	}
	return sortedSet(set)
}

func sortedSet(set map[uint64]struct{}) []uint64 {
	out := make([]uint64, 0, len(set))
	for h := range set {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

/*
	@HINT:

	`JaccardSorted(...)` is |A ∩ B| / |A ∪ B| for two ascending, duplicate-free
	slices.
*/
/*
	@INFO:

	The Jaccard index measures the similarity between two sets by dividing
	the size of their intersection by the size of their union.
*/
func JaccardSorted(a, b []uint64) float64 {
	inter := 0
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			inter++
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}

	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

/* @HINT: `WeightedJaccard(...)` is `Σ min(a,b) / Σ max(a,b)` over the union of keys. */
func WeightedJaccard(a, b map[string]int) float64 {
	var minSum, maxSum int

	for k, av := range a {
		bv := b[k]
		minSum += min(av, bv)
		maxSum += max(av, bv)
	}
	for k, bv := range b {
		if _, ok := a[k]; !ok {
			maxSum += bv
		}
	}

	if maxSum == 0 {
		return 0
	}
	return float64(minSum) / float64(maxSum)
}

/*
@HINT:

`Pair` is a candidate pair of set indices (A < B) and how many (non-hot)
hashes they share.
*/
type Pair struct {
	A, B   int
	Shared int
}

/*
@HINT:

`Candidates(...)` finds pairs of sets sharing at least `minShared` hashes using
an inverted index, which avoids comparing every pair. Hashes that occur in
more than `maxDF` sets (hot, uninformative ones) are ignored; `maxDF <= 0`
disables that cut-off. Output is sorted for determinism.
*/
func Candidates(sets [][]uint64, maxDF, minShared int) []Pair {
	postings := make(map[uint64][]int)
	for i, set := range sets {
		for _, h := range set {
			postings[h] = append(postings[h], i)
		}
	}

	counts := make(map[[2]int]int)
	for _, ids := range postings {
		if len(ids) < 2 || (maxDF > 0 && len(ids) > maxDF) {
			continue
		}
		for x := 0; x < len(ids); x++ {
			for y := x + 1; y < len(ids); y++ {
				counts[[2]int{ids[x], ids[y]}]++
			}
		}
	}

	out := make([]Pair, 0, len(counts))
	for k, n := range counts {
		if n >= minShared {
			out = append(out, Pair{A: k[0], B: k[1], Shared: n})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].A != out[j].A {
			return out[i].A < out[j].A
		}
		return out[i].B < out[j].B
	})

	return out
}

/* @HINT: `UnionFind` is a disjoint-set forest with path compression. */
type UnionFind struct {
	parent []int
}

/* @HINT: `NewUnionFind(...)` creates n singleton sets. */
func NewUnionFind(n int) *UnionFind {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	return &UnionFind{parent: p}
}

/* @HINT: `Find(...)` returns the representative of x's set. */
func (u *UnionFind) Find(x int) int {
	for u.parent[x] != x {
		u.parent[x] = u.parent[u.parent[x]]
		x = u.parent[x]
	}
	return x
}

/* @HINT: `Union(...)` merges the sets containing a and b. */
func (u *UnionFind) Union(a, b int) {
	ra, rb := u.Find(a), u.Find(b)
	if ra == rb {
		return
	}
	if ra < rb {
		u.parent[rb] = ra
	} else {
		u.parent[ra] = rb
	}
}

/*
!

	@INFO:

	Package `itemset` mines "clumps": groups of items (parameters, fields,
	imports, calls) that occur together in several containers.

	It computes *closed* frequent itemsets: an itemset is reported only if no
	larger itemset is shared by exactly the same containers. That reports the
	largest clump for each distinct set of occurrences without enumerating
	every sub-combination (which would explode for big sets).

	The package is pure Go (no tree-sitter) so it is easy to test.
*/
package itemset

import (
	"sort"
	"strings"
)

/* @HINT: Options controls mining. */
type Options struct {
	// MinSupport is the minimum number of containers that must share an
	// itemset (the "threshold").
	MinSupport int
	// MinSize is the minimum number of items in a reported itemset.
	MinSize int
	// MaxCandidates caps the number of candidate itemsets held in memory.
	// When hit, mining continues with what it has and reports truncation.
	MaxCandidates int
}

func (o Options) withDefaults() Options {
	if o.MinSupport < 2 {
		o.MinSupport = 2
	}
	if o.MinSize < 1 {
		o.MinSize = 1
	}
	if o.MaxCandidates <= 0 {
		o.MaxCandidates = 500 //50000
	}
	return o
}

/* @HINT: Result is one closed frequent itemset. */
type Result struct {
	Items   []string // sorted
	Support int      // len(Members)
	Members []int    // indices (into the input) of containers holding Items
}

/*
	   @HINT:

		`MineClosed(...)` finds closed itemsets with at least `opt.MinSize` items that
		are shared by at least `opt.MinSupport` transactions. The boolean result is
		true when the candidate cap was hit and the output may be incomplete.

		Results are sorted by support (desc), size (desc), then lexicographically.
*/
func MineClosed(txs [][]string, opt Options) ([]Result, bool) {
	opt = opt.withDefaults()

	/* @HINT: Normalise transactions and count global itemset seen at least once. */
	support := make(map[string]int)
	norm := make([][]string, len(txs))

	for i, tx := range txs {
		set := uniqueSorted(tx)
		norm[i] = set
		for _, item := range set {
			support[item]++
		}
	}

	/* @HINT: Keep only globally frequent items; give them sorted integer ids. */
	names := make([]string, 0, len(support))

	for item, n := range support {
		if n >= opt.MinSupport {
			names = append(names, item)
		}
	}
	sort.Strings(names)
	ids := make(map[string]int, len(names))
	for i, name := range names {
		ids[name] = i
	}

	/* @HINT:

	   Encode transactions (ids ascending because names are sorted) and
	   build posting lists item -> transactions.
	*/
	rows := make([][]int, len(txs))
	posting := make([][]int, len(names))

	for i, set := range norm {
		row := make([]int, 0, len(set))
		for _, item := range set {
			if id, ok := ids[item]; ok {
				row = append(row, id)
			}
		}
		if len(row) < opt.MinSize {
			continue
		}
		rows[i] = row
		for _, id := range row {
			posting[id] = append(posting[id], i)
		}
	}

	/*
		@HINT:

		Build the family of candidate sets closed under intersection:
		every transaction plus every intersection of transactions. Every set
		in this family is a closed itemset.
	*/
	var family [][]int
	seen := make(map[string]struct{})
	truncated := false

	for _, row := range rows {
		if row == nil {
			continue
		}

		existing := len(family)
		for i := 0; i < existing; i++ {
			inter := intersect(family[i], row)
			if len(inter) < opt.MinSize {
				continue
			}
			key := keyOf(inter)
			if _, dup := seen[key]; dup {
				continue
			}
			if len(family) >= opt.MaxCandidates {
				truncated = true
				continue
			}
			seen[key] = struct{}{}
			family = append(family, inter)
		}

		key := keyOf(row)
		if _, dup := seen[key]; dup {
			continue
		}
		if len(family) >= opt.MaxCandidates {
			truncated = true
			continue
		}
		seen[key] = struct{}{}
		family = append(family, row)
	}

	/* @HINT: Compute the real support of each candidate from posting lists. */
	results := make([]Result, 0)
	for _, cand := range family {
		members := membersOf(cand, posting)
		if len(members) < opt.MinSupport {
			continue
		}
		items := make([]string, len(cand))
		for i, id := range cand {
			items[i] = names[id]
		}
		results = append(results, Result{Items: items, Support: len(members), Members: members})
	}

	sort.Slice(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if a.Support != b.Support {
			return a.Support > b.Support
		}
		if len(a.Items) != len(b.Items) {
			return len(a.Items) > len(b.Items)
		}
		return strings.Join(a.Items, "\x00") < strings.Join(b.Items, "\x00")
	})

	return results, truncated
}

func uniqueSorted(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

/* @HINT: `intersect(...)` returns the intersection of two ascending int slices. */
func intersect(a, b []int) []int {
	out := make([]int, 0, min(len(a), len(b)))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return out
}

func membersOf(cand []int, posting [][]int) []int {
	/* @NOTE: Start from the shortest posting list to keep intersections cheap. */
	best := cand[0]
	for _, id := range cand[1:] {
		if len(posting[id]) < len(posting[best]) {
			best = id
		}
	}
	members := posting[best]
	for _, id := range cand {
		if id == best {
			continue
		}
		members = intersect(members, posting[id])
		if len(members) == 0 {
			break
		}
	}
	return members
}

func keyOf(ids []int) string {
	buf := make([]byte, 0, len(ids)*3)
	for _, id := range ids {
		buf = append(buf, byte(id), byte(id>>8), byte(id>>16))
	}
	return string(buf)
}

package fingerprint

import (
	"math"
	"reflect"
	"testing"
)

func TestShingles(t *testing.T) {
	tokens := []string{"a", "b", "c", "d", "e"}

	if got := Shingles(tokens, 3); len(got) != 3 {
		t.Fatalf("want 3 shingles, got %d", len(got))
	}
	if got := Shingles([]string{"a", "b"}, 5); len(got) != 1 {
		t.Fatalf("short input should give a single shingle, got %d", len(got))
	}
	if Shingles(nil, 3) != nil {
		t.Error("nil tokens should give nil")
	}

	a, b := Shingles(tokens, 3), Shingles(tokens, 3)
	if !reflect.DeepEqual(a, b) {
		t.Error("shingling must be deterministic")
	}
}

func TestJaccardSorted(t *testing.T) {
	a := []uint64{1, 2, 3, 4}
	b := []uint64{3, 4, 5, 6}

	if got := JaccardSorted(a, b); math.Abs(got-1.0/3.0) > 1e-9 {
		t.Errorf("got %v want 1/3", got)
	}
	if JaccardSorted(a, a) != 1 {
		t.Error("identical sets must score 1")
	}
	if JaccardSorted(nil, nil) != 0 {
		t.Error("two empty sets score 0")
	}
}

func TestWeightedJaccard(t *testing.T) {
	a := map[string]int{"x": 2, "y": 1}
	b := map[string]int{"x": 1, "z": 1}

	// min: x=1 ; max: x=2, y=1, z=1 -> 1/4
	if got := WeightedJaccard(a, b); math.Abs(got-0.25) > 1e-9 {
		t.Errorf("got %v want 0.25", got)
	}
	if WeightedJaccard(a, a) != 1 {
		t.Error("identical bags must score 1")
	}
	if WeightedJaccard(nil, nil) != 0 {
		t.Error("empty bags score 0")
	}
}

func TestCandidates(t *testing.T) {
	sets := [][]uint64{
		{1, 2, 3},
		{2, 3, 4},
		{9, 10, 11},
		{3, 100},
	}

	got := Candidates(sets, 0, 2)
	want := []Pair{{A: 0, B: 1, Shared: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}

	// Hash 3 appears in three sets; with maxDF=2 it is ignored, leaving
	// only one shared hash for (0,1).
	if got := Candidates(sets, 2, 2); len(got) != 0 {
		t.Fatalf("hot hashes must be ignored, got %+v", got)
	}
}

func TestUnionFind(t *testing.T) {
	u := NewUnionFind(5)
	u.Union(0, 1)
	u.Union(3, 4)
	u.Union(1, 4)

	if u.Find(0) != u.Find(3) {
		t.Error("0 and 3 should be connected")
	}
	if u.Find(2) == u.Find(0) {
		t.Error("2 should be isolated")
	}
}

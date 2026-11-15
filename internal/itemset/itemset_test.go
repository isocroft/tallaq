package itemset

import (
	"reflect"
	"testing"
)

func TestMineClosedBasic(t *testing.T) {
	txs := [][]string{
		{"a", "b", "c", "d"},
		{"a", "b", "c", "e"},
		{"a", "b", "c", "f"},
		{"x", "y", "z"},
	}

	got, truncated := MineClosed(txs, Options{MinSupport: 3, MinSize: 3})
	if truncated {
		t.Fatal("unexpected truncation")
	}
	if len(got) != 1 {
		t.Fatalf("want 1 clump, got %d: %+v", len(got), got)
	}
	if !reflect.DeepEqual(got[0].Items, []string{"a", "b", "c"}) {
		t.Errorf("items = %v", got[0].Items)
	}
	if !reflect.DeepEqual(got[0].Members, []int{0, 1, 2}) || got[0].Support != 3 {
		t.Errorf("members = %v support = %d", got[0].Members, got[0].Support)
	}
}

func TestMineClosedReportsBothClosedSets(t *testing.T) {
	txs := [][]string{
		{"a", "b", "c", "d"},
		{"a", "b", "c", "d"},
		{"a", "b", "c"},
	}

	got, _ := MineClosed(txs, Options{MinSupport: 2, MinSize: 3})
	if len(got) != 2 {
		t.Fatalf("want 2 closed sets, got %d: %+v", len(got), got)
	}

	// Highest support first: {a,b,c} is in all three, {a,b,c,d} in two.
	if got[0].Support != 3 || len(got[0].Items) != 3 {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Support != 2 || len(got[1].Items) != 4 {
		t.Errorf("second = %+v", got[1])
	}
}

func TestMineClosedBelowThresholds(t *testing.T) {
	txs := [][]string{{"a", "b"}, {"a", "b"}, {"a", "b"}}

	if got, _ := MineClosed(txs, Options{MinSupport: 3, MinSize: 3}); len(got) != 0 {
		t.Errorf("sets smaller than MinSize must not be reported: %+v", got)
	}
	if got, _ := MineClosed(txs, Options{MinSupport: 4, MinSize: 2}); len(got) != 0 {
		t.Errorf("support below threshold must not be reported: %+v", got)
	}
}

func TestMineClosedIgnoresDuplicateItems(t *testing.T) {
	txs := [][]string{
		{"a", "a", "b", "c", ""},
		{"a", "b", "c"},
	}

	got, _ := MineClosed(txs, Options{MinSupport: 2, MinSize: 3})
	if len(got) != 1 || len(got[0].Items) != 3 {
		t.Fatalf("got %+v", got)
	}
}

func TestMineClosedTruncation(t *testing.T) {
	txs := [][]string{
		{"a", "b", "c", "d"},
		{"a", "b", "c", "e"},
		{"a", "b", "d", "e"},
		{"a", "c", "d", "e"},
	}

	_, truncated := MineClosed(txs, Options{MinSupport: 2, MinSize: 2, MaxCandidates: 2})
	if !truncated {
		t.Error("expected truncation flag with a tiny candidate cap")
	}
}

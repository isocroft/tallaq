package callgraph

import (
	"reflect"
	"testing"
)

func TestCallGraphBasics(t *testing.T) {
	cg := NewCallGraph()
	cg.AddCall("a", "b")
	cg.AddCall("b", "c")
	cg.AddCall("c", "a")
	cg.AddCall("a", "print")
	cg.AddCall("d", "d")
	cg.AddNode("lonely")

	if got, want := cg.GetSuccessorNodes("a"), []string{"b", "print"}; !reflect.DeepEqual(got, want) {
		t.Errorf("successors = %v, want %v", got, want)
	}
	if got, want := cg.GetPredecessorNodes("a"), []string{"c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("predecessors = %v, want %v", got, want)
	}
	if got := cg.GetSuccessorNodes("missing"); len(got) != 0 {
		t.Errorf("unknown node must have no successors, got %v", got)
	}
	if !cg.HasNode("lonely") || cg.HasNode("nope") {
		t.Error("HasNode misbehaves")
	}
	if cg.FanOut("a") != 2 || cg.FanIn("a") != 1 {
		t.Errorf("fan-out/fan-in wrong: %d/%d", cg.FanOut("a"), cg.FanIn("a"))
	}
	if got, want := cg.Reachable("b"), []string{"a", "b", "c", "print"}; !reflect.DeepEqual(got, want) {
		t.Errorf("reachable = %v, want %v", got, want)
	}

	want := [][]string{{"a", "b", "c"}, {"d"}}
	if got := cg.Cycles(); !reflect.DeepEqual(got, want) {
		t.Errorf("cycles = %v, want %v", got, want)
	}
}

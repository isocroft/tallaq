package debugging

import (
	"strings"
	"testing"
)

func TestAssertPasses(t *testing.T) {
	Assert(true, "never fires")
	Assertf(true, "never %s", "fires")
}

func TestAssertPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected a panic")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "boom") {
			t.Fatalf("unexpected panic value: %v", r)
		}
	}()

	Assert(false, "boom")
}

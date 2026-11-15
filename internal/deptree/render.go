package deptree

import (
	"fmt"
	"io"

	"github.com/isocroft/tallaq/internal/modname"
)

/*
@HINT:

`Render(...)` writes an ASCII tree for root. Paths are shown relative to
baseDir. Files already printed are marked "(*)" and imports that lead back
to an ancestor are marked "(cycle)", so output stays finite for cyclic
and diamond-shaped graphs.
*/
func Render(w io.Writer, root *Node, baseDir string) {
	if root == nil {
		return
	}

	r := &renderer{
		w:       w,
		base:    baseDir,
		printed: make(map[string]bool),
		onPath:  make(map[string]bool),
	}

	fmt.Fprintln(w, r.rel(root.Path))
	r.walk(root, "")
}

type renderer struct {
	w       io.Writer
	base    string
	printed map[string]bool
	onPath  map[string]bool
}

func (r *renderer) rel(p string) string {
	return modname.Rel(r.base, p)
}

func (r *renderer) walk(n *Node, prefix string) {
	r.onPath[n.Path] = true
	r.printed[n.Path] = true

	total := len(n.Dependencies) + len(n.Unresolved)
	index := 0

	connector := func() (string, string) {
		index++
		if index == total {
			return "└── ", prefix + "    "
		}
		return "├── ", prefix + "│   "
	}

	for _, d := range n.Dependencies {
		conn, childPrefix := connector()

		switch {
		case r.onPath[d.Path]:
			fmt.Fprintf(r.w, "%s%s%s (cycle)\n", prefix, conn, r.rel(d.Path))
		case r.printed[d.Path]:
			fmt.Fprintf(r.w, "%s%s%s (*)\n", prefix, conn, r.rel(d.Path))
		default:
			fmt.Fprintf(r.w, "%s%s%s\n", prefix, conn, r.rel(d.Path))
			r.walk(d, childPrefix)
		}
	}

	for _, u := range n.Unresolved {
		conn, _ := connector()
		fmt.Fprintf(r.w, "%s%s%s (unresolved)\n", prefix, conn, u)
	}

	delete(r.onPath, n.Path)
}

package clumper

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"

	"github.com/isocroft/tallaq/internal/modname"
)

/*
@HINT:

`WriteText(...)` prints clumps in a human-readable form.
File paths are shown relative to root.
*/
func WriteText(w io.Writer, clumps []Clump, root string) error {
	bw := bufio.NewWriter(w)

	if len(clumps) == 0 {
		fmt.Fprintln(bw, "no clumps found")
		return bw.Flush()
	}

	for _, c := range clumps {
		fmt.Fprintf(bw, "[%s clump] %d occurrences, %d items (id %s)\n",
			c.Kind, len(c.Occurrences), len(c.Items), c.Hash)

		for _, item := range c.Items {
			fmt.Fprintf(bw, "    - %s\n", item)
		}
		fmt.Fprintln(bw, "  found in:")

		for _, o := range c.Occurrences {
			fmt.Fprintf(bw, "    %s:%d  %s\n", modname.Rel(root, o.File), o.StartLine, o.ContainerID)
		}
		fmt.Fprintln(bw)
	}

	return bw.Flush()
}

/* @HINT: `WriteJSON(...)` prints clumps as indented JSON. */
func WriteJSON(w io.Writer, clumps []Clump) error {
	if clumps == nil {
		clumps = []Clump{}
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(clumps)
}

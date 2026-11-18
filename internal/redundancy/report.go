package redundancy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"

	"github.com/isocroft/tallaq/internal/modname"
)

/* @HINT: `WriteText(...)` prints a human-readable report. Paths are relative to root. */
func WriteText(w io.Writer, r *Report, root string) error {
	bw := bufio.NewWriter(w)

	if r.Dead != nil {
		fmt.Fprintf(bw, "== Dead code (%d) ==\n", len(r.Dead))
		for _, d := range r.Dead {
			tag := ""
			if d.Transitive {
				tag = " [transitive]"
			}
			fmt.Fprintf(bw, "[%-6s] %-16s %s:%d-%d  %s%s\n    %s\n",
				d.Confidence, d.Kind, modname.Rel(root, d.File), d.Line, d.EndLine, d.Name, tag, d.Reason)
		}
		fmt.Fprintln(bw)
	}

	if r.Clones != nil {
		fmt.Fprintf(bw, "== Similar code (%d groups) ==\n", len(r.Clones))
		for _, g := range r.Clones {
			fmt.Fprintf(bw, "[%s] %d functions, similarity %.2f\n", g.Kind, len(g.Members), g.Similarity)
			for _, m := range g.Members {
				fmt.Fprintf(bw, "    %s:%d-%d (%d lines)  %s\n",
					modname.Rel(root, m.File), m.Line, m.EndLine, m.Lines, m.Function)
			}
			if len(g.Evidence) > 0 {
				fmt.Fprintf(bw, "    shared: %v\n", g.Evidence)
			}
			fmt.Fprintln(bw)
		}
	}

	return bw.Flush()
}

/* @HINT: `WriteJSON(...)` prints the report as indented JSON. */
func WriteJSON(w io.Writer, r *Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

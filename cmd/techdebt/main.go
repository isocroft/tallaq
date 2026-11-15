/*
!

	@INFO:

	Command techdebt runs the technical-debt analysers:

		tallaq deptree   [-root DIR] [-format text|json] <file|dir>
		tallaq callgraph [-root DIR] [-format text|json] <file|dir>
		tallaq churn     [-top N] [-max-commits N] [-since YYYY-MM-DD] [-format text|json] <repo>
		tallaq clumps    [-threshold N] [-min-size N] [-include-tests] [-format text|json] <dir>
*/
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/isocroft/tallaq/internal/callgraph"
	"github.com/isocroft/tallaq/internal/clumper"
	"github.com/isocroft/tallaq/internal/deptree"
	"github.com/isocroft/tallaq/internal/gitsifter"
	"github.com/isocroft/tallaq/internal/modname"
)

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)

	var err error
	switch os.Args[1] {
	case "deptree":
		err = runDeptree(os.Args[2:])
	case "callgraph":
		err = runCallgraph(os.Args[2:])
	case "churn":
		err = runChurn(ctx, os.Args[2:])
	case "clumps":
		err = runClumps(ctx, os.Args[2:])
	case "help", "-h", "--help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage(os.Stderr)
		stop()
		os.Exit(2)
	}

	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `tallaq - technical debt detection

Commands:
  deptree   <file|dir>   file dependency tree/graph and circular imports
  callgraph <file|dir>   functions, their callees and recursion
  churn     <repo>       functions whose interface changes most in git history
  clumps    <dir>        data/struct/class/dependency/call clumps

Run "tallaq <command> -h" for flags.
`)
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

/*
@HINT:

`loadGraph(...)` builds the dependency graph for a file or directory and
returns it together with the project root.
*/
func loadGraph(target, root string) (*deptree.Graph, string, error) {
	info, err := os.Stat(target)
	if err != nil {
		return nil, "", err
	}

	abs, err := filepath.Abs(target)
	if err != nil {
		return nil, "", err
	}

	b := deptree.NewBuilder(deptree.Options{ProjectRoot: root})

	if info.IsDir() {
		g, err := b.BuildDir(abs)
		if err != nil {
			return nil, "", err
		}
		if root == "" {
			root = abs
		}
		return g, root, nil
	}

	if _, err := b.Build(abs); err != nil {
		return nil, "", err
	}
	if root == "" {
		root = b.Root()
	}
	return b.Graph(), root, nil
}

func relJoin(root string, paths []string, sep string) string {
	rel := make([]string, len(paths))
	for i, p := range paths {
		rel[i] = modname.Rel(root, p)
	}
	return strings.Join(rel, sep)
}

func runDeptree(args []string) error {
	fs := flag.NewFlagSet("deptree", flag.ExitOnError)
	root := fs.String("root", "", "project root (default: detected)")
	format := fs.String("format", "text", "output format: text or json")
	_ = fs.Parse(args)

	if fs.NArg() != 1 {
		return fmt.Errorf("usage: tallaq deptree [flags] <file|dir>")
	}

	g, projectRoot, err := loadGraph(fs.Arg(0), *root)
	if err != nil {
		return err
	}

	if *format == "json" {
		return writeJSON(os.Stdout, g)
	}

	for _, r := range g.Roots() {
		deptree.Render(os.Stdout, r, projectRoot)
		fmt.Println()
	}

	if cycles := g.Cycles(); len(cycles) > 0 {
		fmt.Printf("circular dependencies (%d):\n", len(cycles))
		for _, c := range cycles {
			fmt.Printf("  - %s\n", relJoin(projectRoot, c, " <-> "))
		}
	}

	for _, e := range g.Errors {
		fmt.Fprintln(os.Stderr, "warning:", e)
	}

	return nil
}

func runCallgraph(args []string) error {
	fs := flag.NewFlagSet("callgraph", flag.ExitOnError)
	root := fs.String("root", "", "project root (default: detected)")
	format := fs.String("format", "text", "output format: text or json")
	_ = fs.Parse(args)

	if fs.NArg() != 1 {
		return fmt.Errorf("usage: tallaq callgraph [flags] <file|dir>")
	}

	g, projectRoot, err := loadGraph(fs.Arg(0), *root)
	if err != nil {
		return err
	}

	res, err := callgraph.GenerateFromGraph(g, callgraph.Options{Root: projectRoot})
	if err != nil {
		return err
	}
	cg := res.Graph()

	if *format == "json" {
		return writeJSON(os.Stdout, map[string]any{
			"functions": res.Calls,
			"edges":     cg.Edges(),
			"cycles":    cg.Cycles(),
		})
	}

	fmt.Print(cg.String())

	if cycles := cg.Cycles(); len(cycles) > 0 {
		fmt.Printf("\nrecursion (%d):\n", len(cycles))
		for _, c := range cycles {
			fmt.Printf("  - %s\n", strings.Join(c, " <-> "))
		}
	}

	return nil
}

func runChurn(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("churn", flag.ExitOnError)
	top := fs.Int("top", 20, "number of functions to list (0 = all)")
	maxCommits := fs.Int("max-commits", 0, "only the most recent N commits (0 = all)")
	since := fs.String("since", "", "ignore commits before this date (YYYY-MM-DD)")
	format := fs.String("format", "text", "output format: text or json")
	_ = fs.Parse(args)

	if fs.NArg() != 1 {
		return fmt.Errorf("usage: tallaq churn [flags] <repo>")
	}

	opts := gitsifter.Options{MaxCommits: *maxCommits}
	if *since != "" {
		t, err := time.Parse("2006-01-02", *since)
		if err != nil {
			return fmt.Errorf("invalid -since date: %w", err)
		}
		opts.Since = t
	}

	report, err := gitsifter.TrackChanges(ctx, fs.Arg(0), opts)
	if err != nil {
		return err
	}

	if *format == "json" {
		return writeJSON(os.Stdout, report)
	}

	fmt.Printf("%d commits scanned, %d files parsed, %d parse errors\n\n",
		report.CommitsScanned, report.FilesParsed, report.ParseErrors)

	for _, r := range report.Top(*top) {
		fmt.Printf("%5d  %s\n", r.Changes, r.FunctionID)
	}

	return nil
}

func runClumps(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("clumps", flag.ExitOnError)
	threshold := fs.Int("threshold", 3, "minimum number of containers sharing a clump")
	minSize := fs.Int("min-size", 3, "minimum number of items in a clump")
	includeTests := fs.Bool("include-tests", false, "also analyse test files")
	root := fs.String("root", "", "project root for relative paths (default: the scanned dir)")
	format := fs.String("format", "text", "output format: text or json")
	_ = fs.Parse(args)

	if fs.NArg() != 1 {
		return fmt.Errorf("usage: tallaq clumps [flags] <dir>")
	}

	dir, err := filepath.Abs(fs.Arg(0))
	if err != nil {
		return err
	}
	if *root == "" {
		*root = dir
	}

	d := clumper.New(clumper.Options{
		Threshold:    *threshold,
		MinSize:      *minSize,
		Root:         *root,
		IncludeTests: *includeTests,
	})

	if err := d.ProcessDir(ctx, dir); err != nil {
		if ctx.Err() != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "warning:", err)
	}

	clumps := d.Analyze()
	if d.Truncated() {
		fmt.Fprintln(os.Stderr, "warning: candidate limit reached; results may be incomplete")
	}

	if *format == "json" {
		return clumper.WriteJSON(os.Stdout, clumps)
	}
	return clumper.WriteText(os.Stdout, clumps, *root)
}

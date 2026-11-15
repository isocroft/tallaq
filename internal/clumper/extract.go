package clumper

import (
	"strings"

	"github.com/isocroft/tallaq/internal/modname"
	"github.com/isocroft/tallaq/internal/parse"
)

/* @HINT: `process(...)` extracts every transaction of a file and stores them. */
func (d *Detector) process(path string, pf *parse.File) {
	module := modname.For(modname.Rel(d.opts.Root, path), pf.Source, pf.Lang)
	scan := pf.Scan()

	type entry struct {
		kind ClumpType
		tx   transaction
	}
	var entries []entry

	base := func(container, id string, start, end uint32, kind ClumpType) ClumpOccurrence {
		return ClumpOccurrence{
			File: path, Module: module, Container: container, ContainerID: id,
			Language: pf.Lang, StartLine: start, EndLine: end, Kind: kind,
		}
	}

	/* @HINT: Data clumps (parameters) and call clumps (callees) per function. */
	for _, fn := range scan.Functions {
		id := module + ":" + fn.Qualified

		if items := d.paramItems(fn.Params); len(items) >= d.opts.MinSize {
			entries = append(entries, entry{DataClump, transaction{
				occ: base("function", id, fn.StartLine, fn.EndLine, DataClump), items: items,
			}})
		}

		if items := d.callItems(fn.Calls); len(items) >= d.opts.MinSize {
			entries = append(entries, entry{CallClump, transaction{
				occ: base("function", id, fn.StartLine, fn.EndLine, CallClump), items: items,
			}})
		}
	}

	/* @HINT: Struct / class clumps (fields). */
	for _, rec := range pf.Records() {
		kind := ClassClump
		if rec.Kind == "struct" {
			kind = StructClump
		}

		if items := fieldItems(rec.Fields); len(items) >= d.opts.MinSize {
			entries = append(entries, entry{kind, transaction{
				occ:   base(rec.Kind, module+":"+rec.Name, rec.StartLine, rec.EndLine, kind),
				items: items,
			}})
		}
	}

	/* @HINT: Dependency clumps (imports) per file. */
	imports := pf.Imports()
	seen := make(map[string]struct{}, len(imports))
	var deps []string
	var first, last uint32

	for _, imp := range imports {
		if imp.Path == "" {
			continue
		}
		if _, dup := seen[imp.Path]; dup {
			continue
		}
		seen[imp.Path] = struct{}{}
		deps = append(deps, imp.Path)

		if first == 0 || imp.Line < first {
			first = imp.Line
		}
		if imp.Line > last {
			last = imp.Line
		}
	}
	if len(deps) >= d.opts.MinSize {
		entries = append(entries, entry{DependencyClump, transaction{
			occ: base("file", module, first, last, DependencyClump), items: deps,
		}})
	}

	if len(entries) == 0 {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	for _, e := range entries {
		d.db[e.kind] = append(d.db[e.kind], e.tx)
	}
}

/*
@NOTE:

`paramItems(...)` builds "name type" items. Unnamed parameters carry no
identity and are skipped.
*/
func (d *Detector) paramItems(params []parse.Param) []string {
	items := make([]string, 0, len(params))
	for _, p := range params {
		if p.Name == "" {
			continue
		}
		items = append(items, strings.TrimSpace(p.Name+" "+p.Type))
	}
	return items
}

func fieldItems(fields []parse.Field) []string {
	items := make([]string, 0, len(fields))
	for _, f := range fields {
		if f.Name == "" {
			continue
		}
		items = append(items, strings.TrimSpace(f.Name+" "+f.Type))
	}
	return items
}

func (d *Detector) callItems(calls []parse.Call) []string {
	items := make([]string, 0, len(calls))
	for _, c := range calls {
		if c.Name == "" {
			continue
		}
		if _, skip := d.opts.IgnoreCalls[c.Name]; skip {
			continue
		}
		items = append(items, c.Name)
	}
	return items
}

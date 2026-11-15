# tallaq

CLI application for analysing early warning signs of technical debt and detecting code quality issues for **JavaScript, Python, PHP and Go** code bases.


| Command | What it finds |
|---|---|
| `deptree`   | File dependency tree / graph, circular imports, unresolved local imports |
| `callgraph` | Functions, their callees (resolved across imported files), recursion, fan-in/out |
| `churn`     | Functions whose *interface* (name + params + result) changes most in git history |
| `clumps`    | Data, struct, class, dependency and call clumps (groups of items that always travel together) |

## Layout

```
tallaq/
├── cmd/techdebt/main.go        CLI (deptree | callgraph | churn | clumps)
├── internal/
│   ├── debugging/              Assert helpers
│   ├── langs/                  supported languages, extensions, ignored dirs   (stdlib only)
│   ├── modname/                "module:file" ID prefixes                       (stdlib only)
│   ├── itemset/                closed-itemset mining used by clumper           (stdlib only)
│   ├── deptree/                dependency tree/graph builder + resolvers       (stdlib only)
│   ├── parse/                  tree-sitter wrapper: functions, calls, params,
│   │                           structs/classes, imports (shared by the below)
│   ├── callgraph/              call map + resolved CallGraph
│   ├── gitsifter/              interface-churn over git history (go-git)
│   └── clumper/                clump detector
├── Makefile
├── LICENSE
├── go.sum
└── go.mod
```

## Build & Run

```sh
make tidy            # go mod tidy: fetches (needs network, cgo)
make test            # go test ~:
make build           # spits out bin/tallaq

bin/tallaq deptree   ./path/to/project
bin/tallaq deptree   -format json ./src/index.js
bin/tallaq callgraph ./path/to/project
bin/tallaq churn     -top 20 -since 2025-01-01 .
bin/tallaq clumps    -threshold 3 -min-size 3 ./path/to/project
```

**tree-sitter** uses **cgo**, so a C compiler (gcc/clang) must be installed.


## Improvements

- Shared `parse` package replaces four copies of the same helpers.
- Dependency graph is a real graph (one node per file, cycles via Tarjan SCC), ASCII tree and JSON output.
- Go imports use `go/parser`; Go package imports resolve through `go.mod`.
- Call graph resolves callees into imported files, expands import aliases, adds fan-in/out, reachability and recursion detection.
- Churn: first-parent history, blob-hash cache, bounded workers, per-change event log with before/after interfaces.
- Clumps: subset clumps (not just identical signatures) via closed-itemset mining, five clump kinds, text/JSON reports.
- Deterministic (sorted) output everywhere.

## Known limitations

- Resolution is static and name-based: dynamic imports with variables, TS path aliases, PHP PSR-4 `use` autoloading and Python `sys.path` tricks are not followed.
- Call resolution matches by function name inside the file and its direct imports, so it can over-link same-named functions.
- Go packages are mapped to all their non-test files (build tags are ignored).
- Grammar node names (tree-sitter) vary between grammar versions; if a language yields no functions/records, check the node type names in `internal/parse`.

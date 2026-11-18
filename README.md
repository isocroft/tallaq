# tallaq

CLI application for analysing early warning signs of technical debt and detecting code quality issues for **JavaScript, Python, PHP and Go** code bases.

This project is the result of a 3-year personal research journey that focused on the early warning signns of technical debt in addition to the late warning signs (which we formally call [code smells](https://refactoring.guru/refactoring/smells) by the way).

The 3 early warning signs are as follows:

1. **Logic Fragmentation** (dependency clumps + similar code with same logical outcome)
2. **Unstable Public Signature** (churn + lack of factoring and/or lockdown -- related to git commit history)
3. **Incongruent Composition** (callgraph fan-in/fan-out bloat).

>NOTE: This project does not dabble into extracting metrics like the [cyclomatic complexity](https://en.wikipedia.org/wiki/Cyclomatic_complexity), [halstead effort and volume](https://en.wikipedia.org/wiki/Halstead_complexity_measures) and [maintaiability index](https://www.sourcery.ai/blog/maintainability-index) as this relates more to readability and ease of comprehesion which is outside the scope of this reesearch project. Code base readability depends on two factors: **clarity** and **familiarity**. Even when **clarity** is low, as long as **familiarity** is high, readability and ease of comprehension would be high. But, when **familiarity** is low, even if **clarity** is high, readability and ease of comprehension can still suffer. This highlights an important imbalance. High familiarity can act as a crutch that lets a software engineer navigate messy code, whereas high clarity cannot instantly compensate for a total lack of context or domain knowledge. Hence, the encouragement to lean into idiomatic code styles when possible.


>**Analogy**: a lawyer who understands a contract drafted for an employee even though the employee knows the language the contract was written (English) in yet cannot understand said contract.

### Improving Readability

• **Optimize for Low Familiarity**: Since team members change and memories fade, code should be written for a reader with low familiarity.
• **Bridge the Familiarity Gap**: Use documentation: e.g. architectural diagrams, ontologies, onboarding guides, and domain glossaries to build familiarity faster, rather than relying solely on clean syntax.
• **Refactor Legacy Code Gradually**: When dealing with low-clarity, high-familiarity systems, veteran developers should document their mental models before those models are lost.

>**Familiarity** is subjective while **Clarity** is objective. Therefore, there are no formulars to calculate a score for Familiarity**. However, **Clarity** get a score based on metrics like **Cyclomatic Complexity** and **Halstead Effort &amp; Volume**.

| Command | What it finds |
|---|---|
| `deptree`   | File dependency tree / graph, circular imports, unresolved local imports |
| `callgraph` | Functions, their callees (resolved across imported files), recursion, fan-in/out |
| `churn`     | Functions whose *interface* (name + params + result) changes most in git history |
| `clumps`    | Data, struct, class, dependency and call clumps (groups of items that always travel together) |
| `redundancy`| Dead code (unused functions/methods/types/imports, unreachable code, dead branches) and similar code (identical, same-structure, near-miss and same-behaviour functions) |

## Layout

```
tallaq/
├── cmd/techdebt/main.go        CLI (deptree | callgraph | churn | clumps | redundancy)
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
|   ├── clumper/                clump detector
│   └── redundancy/             dead code + similar-code detection
|       └── fingerprint/        shingling, Jaccard index, candidate pairs, union-fi
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
bin/tallaq callgraph -format json ./path/to/project
bin/tallaq churn     -top 20 -since 2025-01-01 .
bin/tallaq clumps    -threshold 3 -min-size 3 ./path/to/project
bin/tallaq redundancy -mode dead -private-only ./path/to/project
bin/tallaq redundancy -mode dead -format json ./path/to/project
bin/tallaq redundancy -mode similar -near 0.8 -semantic 0.85 ./path/to/project
```

>NOTE: **tree-sitter** uses **cgo**, so a C compiler (gcc/clang) must be installed.

## About Dead code and Similar code
 
**Dead code** (each finding has a confidence): functions/methods/types whose
name is never referenced anywhere in the project (calls, callbacks, exports,
member access and even string literals such as `"handler"` all count as use,
including from test files); functions only used by other dead code
(transitive, reported iteratively); self-recursive functions nobody calls;
unused imports (JS/Python/PHP; Go's compiler already rejects them);
statements after `return`/`throw`/`raise`/`break`/`continue`/`goto`/`panic`;
and `if` branches guarded by a constant (`if (false)`, `if False:`, ...).
Entry points and framework hooks (`main`, `init`, `__dunder__`, decorated
Python functions, `Test*`, React lifecycle methods, ...) are never reported;
extend the list through `Options.IgnoreNames`. *High* = private and
unreferenced; *medium* = exported/public or a method (may be used from outside
the project or through interfaces; hide with `-private-only`).
 
**Similar code**, from strictest to loosest (functions are only compared
within one language):
 
| Kind | Meaning |
|---|---|
| `identical-logic` | same canonical form: formatting, comments, variable names and operand order (`a+b` vs `b+a`, `x>y` vs `y<x`) ignored |
| `same-structure` | identical except literal values (a parameterisable copy) |
| `near-miss` | mostly the same code with statements added/removed/changed (5-token shingle Jaccard >= `-near`) |
| `same-behavior` | *different* code with the same arity, callees, literals, operators and control-flow profile (weighted Jaccard >= `-semantic`); lists the shared evidence |
 
`same-behavior` is a heuristic for "same logical outcome": proving two
functions equivalent is undecidable, so treat these as strong review
candidates, not verdicts. Candidate pairs come from inverted indexes, so large
code bases do not need an all-pairs comparison.

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

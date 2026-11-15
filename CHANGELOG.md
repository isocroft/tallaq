## Bugs fixed in v0.0.0

**deptree**: unresolvable import path `"debugging"`; undefined `sourceFilePath`;
undeclared `matchedImportPathString`; invalid returns (`struct{}`, `nill`), `:=`
re-declaring `err`, `%w` given `err` twice; package-level `rootNode/visited`
state made a second call return a stale tree; `currentNodeVisited` was never
restored, so siblings became a chain; Python `.utils` was treated as already
having an extension, so relative imports never resolved; JS regex was greedy
and missed multi-line imports, `require()` and `import()`; one missing file
aborted everything.

**callgraph**: duplicated `Node` type; missing `regexp` import; syntax error in
`buildSignature`; `.go` missing from the language switch; only `call_expression`
was searched (Python's `call` and PHP's call nodes produced nothing; Go's
`selector_expression` callee failed); calls in nested functions were
attributed to the outer function; top-level calls were dropped when a file
also had functions; Python module IDs used the absolute path and had a trailing
dot; global `callMap`; nil context in `ParseCtx`; parsers never closed; the
`CallGraph` type was never populated.

**gitsifter**: unterminated `".go:` string; `go.GetLanguage()` (package is
`golang`); unused imports; functions keyed by bare name (cross-file
collisions); worker/error-channel layout could deadlock; history compared in
the wrong direction and every file re-parsed on every commit; whitespace
stripping merged tokens (`a int` -> `aint`) and comment regexes mangled
strings; alias resolution applied to *definitions* (meaningless).

**clumper**: stubbed (`...`) bodies, `[]bytes` return type, undefined variables;
hash included the function name so nothing could ever clump; parameter lists
were sorted with their parentheses attached; Go-only queries using the wrong
node name (`string_literal` instead of `interpreted_string_literal`).
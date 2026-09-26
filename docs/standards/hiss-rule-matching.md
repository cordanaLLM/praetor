# How the HISS rule matchers read source

The Go rules use the AST. The rules for C, C++, Rust and Python match text, and this document
records what that means, because the difference is load-bearing.

## Whitespace does not silence a rule

The non-Go matchers previously compared exact strings, so a single space defeated them. All of these
passed a gate that claims to catch them:

```text
while ( 1 )        for ( ;; )        loop{
'outer: loop {     x. unwrap()       while True :
```

A reformatter could erase a finding without changing behaviour. The patterns now tolerate arbitrary
internal spacing.

## Literals and comments are stripped before matching

A rule keyword inside a string or a comment is not a finding. Stripping runs across lines, so a
block comment or a docstring containing `while True` does not fire.

## CRLF does not silence a rule

Measured across thirteen cases in four languages: LF and CRLF produce identical reports. The
property holds because `TrimSpace` treats the carriage return as whitespace and no pattern anchors
at end of line — by accident rather than by design, which is why four CRLF fixtures in the HISS-20
corpus assert it, with `.gitattributes` marking them `-text whitespace=cr-at-eol` so a checkout
cannot normalise away the bytes they exist to carry.

## What a text matcher cannot do

It cannot resolve a name, follow a call, or know a type. A rule whose axiom needs any of those
cannot be decided this way, and the coverage catalog records such a rule as `unsupported` with its
gap fixtures rather than claiming an enforcement that does not exist.

## Rust: scopes follow braces

Test code and function bodies are both brace-delimited items, and one tracker (`braceTracker` in
`internal/hiss/rules.go`) follows both:

- A `#[cfg(test)]`, `#[test]` or path-qualified test attribute such as `#[tokio::test]` makes test
  code of exactly the item it annotates, up to the brace that closes it. Code after a closed test
  module is production code again. `#![cfg(test)]` and the Cargo test paths (`tests/`, `benches/`,
  `*_test.rs`, `tests.rs`, `test.rs`) still cover the whole file.
- A function header is any visibility (`pub`, `pub(crate)`, `pub(super)`, `pub(in path)`) followed
  by any run of `const`, `async`, `unsafe`, `safe`, `default` and `extern` qualifiers before `fn`.
  The grammar matches the line after literals are stripped, so a `fn` in a string or comment is not
  a header. Outer attributes may precede the header on the same line (`#[inline] pub fn`,
  `#[tokio::main] async fn main() {`).
- The abort policy exempts the body of the unindented `fn main`; rustfmt indents a method of the
  same name inside its `impl` block, so the method stays library code. The header line belongs to
  `fn main` too, so a one-line `fn main() { std::process::exit(run()) }` is exempt.
- An abort macro is a call only with its delimiter after the bang (`panic!(`, `todo![`,
  `unreachable!{`), so an identifier compared with `!=` is not reported.

`internal/hiss/rust_scope_test.go` and `internal/hiss/abort_policy_test.go` pin each case.

## Python: the entry point follows logical lines

The abort policy allows `sys.exit` inside the top-level `if __name__ == "__main__":` block and the
top-level `def main`. The scope (`pythonAbortScope` in `internal/hiss/rules.go`) opens on the
column-zero line that starts either one and closes on the next column-zero code line.

Python ignores indentation inside open brackets and after a trailing backslash, so a column-zero
line there continues the line above it. black wraps a long signature and puts its closing
`) -> int:` at column zero. `pythonLineJoiner` counts brackets in the literal-stripped code and
treats such a line as a continuation, which neither closes nor opens the scope. An unbalanced
closer leaves the count at zero rather than below it.

`TestAbortPolicy_Boundary_PythonWrappedSignatureKeepsTheEntryOpen` in
`internal/hiss/abort_policy_test.go` and the fixture
`.config/hiss/testdata/HISS-07/python/negative/wrapped-entry-signature.py` pin the wrapped case.

## Go: one pass reads a package, not a file

Most matchers decide a line. HISS-01 cannot be decided that way in full: a function that calls
itself is visible in one file, but a cycle through two functions is not, because no single file's
AST shows the loop closing. `internal/hiss/go_callgraph.go` therefore runs after the walk, builds
each package's call graph from the Go files the scan read, and reports every strongly connected
component of two or more functions, naming the path.

Package scope is complete here rather than convenient. A call cycle spanning two packages would
need each package to import the other, and the Go compiler rejects that outright, so every call
cycle a buildable program can contain is inside one package.

A name the function binds itself shadows the package-level name of the same spelling for the whole
body: its receiver, a parameter, a named result, a local, a range variable or a closure parameter.
A call through such a name adds no edge, a bare call is not direct recursion, and `os.Exit` on a
binding named `os` is not the process exit (`funcDeclares` in `internal/hiss/go_ast.go`). A method
is called through its receiver, so a local named like the method does not hide its recursion.

Two limits are deliberate and recorded as gap fixtures rather than left implicit:

- **Methods are not in the graph.** Resolving `x.foo()` needs the receiver's type, and guessing it
  would invent edges that do not exist. `HISS-01/go/gap/method-cycle.go` records this.
- **A call through a function value or interface is undecidable statically**, for the same reason
  the section above gives.

Direct recursion keeps its own finding from the per-file scanner; the graph pass skips
single-node components so one defect is not reported twice.

The check is verified by planting cycles rather than by watching it pass — the failure mode
[#90](https://github.com/cordanaLLM/praetor/issues/90) recorded, where `dedupe scan` reported
100% cleanliness having read no files. The approach is backported from
[golusoris/sveltesentio#252](https://github.com/golusoris/sveltesentio/pull/252), which built the
equivalent import-graph check for TypeScript.

## HISS-20: claims are replayed

Every enforcement claim in `.config/hiss/coverage.yaml` is replayed against the fixture corpus by
`praetorctl hiss coverage --verify`, which runs inside `make verify-all`. The check runs in both
directions: a claim of enforcement must report each of its positive fixtures, and a claim of absence
must leave its gap fixtures undetected. A rule that silently *gains* coverage fails the gate exactly
as one that silently loses it, so the catalog cannot drift in either direction.

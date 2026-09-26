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

It cannot follow a call into another function or know a type, and it resolves a name only as far
as one function's own text allows (see the Rust and Python section below). A rule whose axiom needs
more than that cannot be decided this way, and the coverage catalog records such a rule as
`unsupported` or `partial` with its gap fixtures rather than claiming an enforcement that does not
exist.

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

## Rust and Python: a function calling itself

Without a parser, the Rust and Python scanners decide the one HISS-01 shape a single function's
text shows: its body calling it by a name that actually resolves to it. Matching the name anywhere
would report every delegation, so `internal/hiss/selfcall.go` follows each language's resolution
rule:

| Function | Reported spelling | Not reported |
| :--- | :--- | :--- |
| Python plain function | `f()`, including from a nested def | a parameter, assignment, loop target, `as` target, import or nested def named `f` |
| Python method | `self.f()`, `cls.f()`, `Owner.f()` | bare `f()` (reaches the module-level `f`), `self.inner.f()`, `super().f()` |
| Rust free function | `f()`, including from a closure and after a local `f` goes out of scope | `other::f()`; `f()` while a `let`, `for`, `if let`, match-arm or closure-parameter binding named `f` is in scope, or anywhere in a body that declares a nested `fn` or `use` of `f` |
| Rust method or associated function in `impl T` or `trait T` | `self.f()`, `Self::f()` | bare `f()` inside the `impl` or `trait` body (reaches a free function) |
| Rust function in `impl Trait for T` | nothing | `self.f()` and `Self::f()`, which reach an inherent `T::f` first, or another impl when `Self::f` is picked by argument type (`Self::from(b)` inside `From<A>` reaches `From<B>`) |

A finding is decided when the function closes rather than at the call, because a Python binding
later in the body makes the name local for all of it. That binding belongs to the function that
makes it: a nested def's parameter or assignment shadows the name for the nested def's calls and
never for the enclosing function's own, and a class body binds nothing a function reads.

Rust scopes are lexical, so `internal/hiss/rustscope.go` walks the body byte by byte and tracks
brace and parenthesis depth. A `let` shadows from the semicolon ending it to the end of its
block, so `let f = f(n - 1);` still calls the function. A `for`, `if let` or `while let` pattern
shadows inside the block it heads, a match arm's pattern from its `=>` to the end of the arm, and
a closure parameter from the closing `|` to the end of the closure. An item (`fn`, `use`, `const`,
`static`) covers the whole body. A match arm's pattern names a path and calls nothing, so
`Variant(x) =>` is not a call site, while the arm's guard and body are.

A macro may rewrite its input: `syscall!(recv(fd, buf))` expands to `libc::recv`, and tracing's
`debug!(x = debug(&v))` never calls a function named `debug`. A call inside the input of any macro
except the standard expression macros (`assert!`, `format!`, `println!`, `vec!`, `write!` and the
rest of `rustExpressionMacros`) is therefore not decided. The cost is a real self-call inside a
user-defined wrapper macro, such as `ok!(self.parse_expr())`, which
`HISS-01/rust/gap/user-macro-self-call.rs` records.

`TestPythonSelfRecursionScopesBindings`, `TestRustSelfRecursionShadowsLexically` and
`TestRustMacroInputIsUndecided` in `internal/hiss/recursion_test.go` pin these rules. The fixtures
`HISS-01/python/positive/nested-scope-binding.py`, `HISS-01/rust/positive/binding-after-call.rs`,
`HISS-01/rust/negative/scoped-binding.rs` and `HISS-01/rust/negative/macro-input.rs` replay them.

A trait impl is left undecided because one function's text cannot tell forwarding from recursion
there: the inherent method that `self.f()` would reach may sit in any file of the crate. Deciding
it anyway reported idiomatic forwarding, a trait method calling the inherent method of the same
name or `Self::from` reaching a different `From` impl, which rustc compiles clean with
`-D unconditional_recursion`. `HISS-01/rust/negative/trait-impl-forwarding.rs` holds both shapes
and `HISS-01/rust/gap/trait-impl-self-call.rs` the real recursion this leaves unseen.
`rustHeaderKind` in `internal/hiss/selfcall.go` classifies each `impl` header, including one
rustfmt wraps across lines or one with an attribute on the same line. A brace inside the header's
brackets is a const generic argument (`impl Tr for W<{ N + 1 }>`) and a semicolon there an array
length (`impl Tr for [u8; N]`); a semicolon outside them ends an item that opens no body
(`trait Alias = A + B;`). `TestRustHeaderKind` and `TestRustTraitImplHeaderForms` pin it, and
`HISS-01/rust/negative/trait-impl-header-forms.rs` replays it.

The Python scanner also treats a line that starts inside an open bracket or a string as a
continuation of the statement above it. Reading its indentation as a dedent ended a
black-formatted function at its `) -> T:` line, and a function holding a column-0 string at that
string, so neither body was measured for HISS-04 or scanned for recursion.
`TestPythonContinuationLinesStayInTheirFunction` in `internal/hiss/recursion_test.go` pins it.

That tracking depends on the literal stripper reading strings the way Python does: a trailing
backslash carries a quoted string onto the next line, and `\"""` does not close a triple-quoted
string. When the stripper still misreads one, as with a Python 3.12 f-string field that reuses its
quote, the carried bracket depth resets at the next line that starts with a statement-only keyword
(`def`, `class`, `return`, `import` and the like), which no bracket can hold. A misread then costs
the lines up to that statement, not every function below it.
`TestLiteralStripperCarriesEscapedLineBreaks` and `TestPythonStringAndBracketRecovery` pin both.

Mutual and indirect recursion need a call graph across functions and stay undecided for both
languages, as do nested-fn recursion, recursion inside a trait impl, turbofish or path-qualified
self-calls in Rust, and lambda recursion in Python. `HISS-01/rust/gap/` and `HISS-01/python/gap/`
record each one. So does `HISS-01/rust/gap/unrecognised-header.rs`: a function behind a header the
Rust scanner does not recognise yet (`pub(super)`, `const`, `unsafe` or `extern fn`) is never
opened, so its self-call goes unseen.

## HISS-20: claims are replayed

Every enforcement claim in `.config/hiss/coverage.yaml` is replayed against the fixture corpus by
`praetorctl hiss coverage --verify`, which runs inside `make verify-all`. The check runs in both
directions: a claim of enforcement must report each of its positive fixtures, and a claim of absence
must leave its gap fixtures undetected. A rule that silently *gains* coverage fails the gate exactly
as one that silently loses it, so the catalog cannot drift in either direction.

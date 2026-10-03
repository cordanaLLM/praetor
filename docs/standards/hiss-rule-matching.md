# How the HISS rule matchers read source

The Go rules use the AST. The rules for C, C++, Rust, Python, JavaScript, TypeScript, Svelte and
shell match text, and the shell rules also read the `run:` blocks of GitHub Actions workflows. The
systemd rules read a unit's settings, and the Ansible rules read a playbook's YAML tree. This
document records what that means, because the difference is load-bearing.

## One engine, one scanner per language

`hiss.Scan` owns the walk, the git scope, the ignore policy, the byte-bounded read and the coverage
record. Each language is one `languageScanner` in the `languageScanners` table of
[`internal/hiss/engine.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/hiss/engine.go):
Go, C and C++ (with CUDA and HIP), Python, Rust, the script scanner for JavaScript, TypeScript
and Svelte, and the shell, GitHub Actions workflow, systemd and Ansible scanners.
`hiss.SupportsExtension`, the walk's scope test and `ci filter`'s code classification read that
table, so a language cannot be reported as supported where it is not scanned. A scanner may decline
a file, which is then counted as unscanned rather than read; a scanner that needs every file of a
package after the walk (the Go call graph) is a `packageScanner`
(`TestLanguageScanners_ClaimDisjointExtensions` in `internal/hiss/engine_test.go`). A scanner that
reads at most a fixed number of files in one scan is a `boundedScanner`: a file past its bound is
unscanned source of its language, never clean.

Some files are only recognised from their contents: a script without an extension names its shell
on its `#!` line, a YAML file is a workflow only directly in `.github/workflows` and a playbook only
if it holds a play, and a `.service` or `.socket` file is a unit only if it opens a unit section. A
scanner that reads such files is a `contentScanner`: the walk reads each candidate (an
extensionless file, a `.yml` or `.yaml` file, a `.service` or `.socket` file) and asks the scanner
whether it claims the bytes. A candidate nobody
claims is recorded exactly as it was before these scanners existed, so `SupportsExtension` widens
only by `.sh` and `.bash`. A candidate that is a symlink, a special file or larger than the read
bound is never opened, and one the walk cannot read (permission denied, say) stays unscanned instead
of failing the scan, while an unreadable `.sh` file fails it like any other scanned source. A
claimed file the scanner then declines is unscanned source of its language
(`TestContentScanners_ClaimByBytes`, `TestContentScanners_DeclinedClaimKeepsItsLanguage`,
`TestContentScanners_UnreadableCandidateStaysUnscanned`,
`TestScan_UnreadableScannedExtensionIsAnError` in `internal/hiss/engine_test.go`,
`TestScan_ContentCandidateFIFOIsNeverOpened` in
`internal/hiss/scan_irregular_unix_test.go`). `util.SourceLanguage` names `.zsh` and `.ksh` files as
`zsh` and `ksh`, which no scanner reads, so the audit lists them on its `[UNSCANNED]` line instead of
under the shell it verified.

The coverage record names the languages it read (`languages_read`), the source languages no
scanner examined (`unscanned_languages`, named by `util.SourceLanguage`) and, per shell, the
workflow `run:` blocks no rule read (`unscanned_run_blocks`, see
[GitHub Actions run: blocks](#github-actions-run-blocks)). `praetorctl audit` prints its PASS line
for the languages it read (`verified for go, typescript:`) and reports every other source language
and every unscanned block on an `[UNSCANNED]` line; a tree whose only source is unscanned gets no
PASS line (`TestAudit_InvariantVerdictNamesItsLanguages` in `cmd/standardsctl/audit_cmd_test.go`).

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

The script scanner holds the property by design: its rules look at what a line ends with (a
parameter list left open, a quote that does not close), so it reads a CRLF file as LF before any
rule runs (`sourceFile.lfLines` in `internal/hiss/engine.go`). Under `* text=auto` every script is
a CRLF checkout on Windows. `TestScriptScanner_CRLFReadsLikeLF` in `internal/hiss/script_test.go`
replays its cases in both forms, and two more pinned fixtures,
`HISS-01/typescript/positive/crlf-wrapped-signature.ts` and
`HISS-04/typescript/positive/crlf-wrapped-props.tsx`, keep a wrapped signature tracked on Linux too.

The shell and systemd scanners read lines the same way: a trailing backslash continues a shell
command or a unit setting, and a `#!` line ending in a carriage return still names its shell, so
both read a CRLF file as LF (`TestShellScanner_CRLFReadsLikeLF` in `internal/hiss/shell_test.go`,
`TestSystemdScanner_Boundaries` in `internal/hiss/systemd_test.go`). `.gitattributes` pins `.sh`
and `.yml` files to LF anyway, because shellcheck and yamllint reject a carriage return.

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

## Rust: where a SAFETY proof attaches

HISS-09 follows the two clippy lints a Rust workspace already runs, `undocumented_unsafe_blocks`
and `missing_safety_doc`, so code both accept is not reported. `checkRustUnsafe` in
`internal/hiss/rust_safety.go` decides each line:

- **An unsafe block** is proven by a `SAFETY:` comment on its line, in the comment block directly
  above it, or in the comment block directly above the first line of its statement. rustfmt wraps a
  long `let v = unsafe { .. };` after the `=` and leaves the comment above the `let`; clippy accepts
  that by default (`accept-comment-above-statement`). A line continues the statement above it while
  a parenthesis or bracket is open, after a line ending in an operator such as `=`, or when it starts
  a method chain (`.map(..)`) or a binary operator.
- **A statement-level proof stops** where clippy's does. It does not reach the next match arm, a
  block in the head of an `if`, `match`, `while` or `for` statement, a struct literal field, a block
  inside a brace opened earlier on its line, or the next statement.
- **An unsafe fn** is a declaration, not a block. It is proven by a rustdoc `# Safety` section (any
  heading level; also `SAFETY` and `Implementation safety`, from `///`, `/** */` or `#[doc = ".."]`,
  with attributes between the docs and the header) or by a `SAFETY:` comment above it. Every
  visibility and qualifier is held to that alike, and so is a method declared in a trait. A method
  implementing a trait is exempt, because the trait declaration documents its contract. A local
  `unsafe fn` inside such a method is not. A pointer type such as `unsafe fn(u8)` names no function.

Three spellings are stricter than clippy on purpose: the marker must be the upper-case `SAFETY:`,
a blank line detaches a comment from the line below it, and the comment block searched is at most
eight lines. Two shapes clippy accepts are still reported: a block in a statement that only
continues after a closing brace (`}, unsafe { .. });`), because the statement walk restarts inside
every brace a line leaves open, and an undocumented foreign function declared `unsafe fn` inside an
`extern` block, because the scanner does not track `extern` blocks.

`internal/hiss/rust_safety_test.go` pins each case, and the fixtures under
`.config/hiss/testdata/HISS-09/rust/` replay them in both directions.

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

A name bound inside a function shadows the package-level name of the same spelling only where Go
puts the binding in scope (`goScope` in `internal/hiss/go_scope.go`):

- a receiver, type parameter, parameter or named result for the whole body, and a closure
  parameter for the closure's body;
- a local declared by `:=`, `var` or `const` from the end of its declaration to the end of the
  innermost block holding it, a local type from its name on, and a range variable in the loop body;
- a variable an `if`, `for`, `switch` or type switch header declares, or a `select` case receives
  into, until that statement or clause ends.

At a call where such a binding is in scope, the call adds no call-graph edge, a bare call is not
direct recursion, and `os.Exit` on a binding named `os` is not the process exit. A local of another
block, or one declared after the call, hides nothing, so the call still reaches the function. Each
function is walked once, so the cost grows with its size, not with its size times its calls. A
method is called through its receiver, so a local named like the method does not hide its
recursion. `TestGoScope_*` in `internal/hiss/go_scope_test.go` and
`TestCallGraphSeesACycleBehindAnOutOfScopeLocal` in `internal/hiss/go_callgraph_test.go` pin
these rules.

### Go: the net/http abort sentinel

`net/http` documents `panic(http.ErrAbortHandler)` as the way a handler aborts its response: the
server recovers it, drops the connection or resets the stream, and logs no stack trace. Recovery
middleware needs it once a response is committed, because completing the truncated response would
hand the client a partial body as a finished one. HISS-07 therefore accepts a panic whose single
argument is that sentinel (`AbortsHTTPResponse` in `internal/hiss/go_ast.go`):

- The argument is resolved through the file's imports, so `http.ErrAbortHandler`, an alias such as
  `web.ErrAbortHandler`, and `ErrAbortHandler` under a dot import of `net/http` are the sentinel.
- A local or package variable named `ErrAbortHandler`, another package's `ErrAbortHandler`, a
  receiver, parameter or local of the enclosing declared function that shadows the package name
  where the panic is (scoped as above), a wrapped sentinel (`fmt.Errorf("%w", ...)`), a recovered
  value re-panicked as is, and a second argument are still reported. A function literal assigned
  at package level has no enclosing declared function, so a parameter of it that shadows the
  package name is not seen.
- `standards-lsp` asks the same function, so the editor and `praetorctl audit` agree
  (`refusedPanic` in `cmd/standards-lsp/server.go`).

One limit is recorded as a gap fixture: the server recovers the sentinel only on the goroutine it
runs the handler on, and which goroutine runs a function is not visible in syntax.
`HISS-07/go/gap/abort-handler-goroutine.go` panics with it on a goroutine the code started itself,
which ends the process unreported. `internal/hiss/go_abort_handler_test.go`,
`cmd/standards-lsp/hiss07_test.go` and the `abort-handler-*` fixtures under
`.config/hiss/testdata/HISS-07/go/` pin each case.

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
a downstream adopter, which built the equivalent import-graph check for TypeScript.

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
shadows inside the block it heads, a match arm's pattern from its guard's `if` (or its `=>` when
the arm has no guard) to the end of the arm, and a closure parameter from the closing `|` to the
end of the closure. A struct pattern's braces belong to the pattern, so `let S { f, .. } = s;`
and `S { f, .. } => f()` bind `f` like any other pattern. An item (`fn`, `use`, `const`,
`static`) covers the whole body. A match arm's pattern names a path and calls nothing, so
`Variant(x) =>` is not a call site. The arm's guard and body are call sites, but the arm's own
bindings are already in scope there: in `Some(f) if f() => 1` the guard calls the local, while a
guard that calls `f` in an arm whose pattern does not bind it reaches the function. A later arm
on the match's line binds as a first arm does (`match s { None => 0, Some(f) => f() }`).

An arm and a closure end where rustc's grammar ends them, not at the next closing brace. An arm
ends at its comma, or, when its body starts with a block-like expression (`{`, `if`, `match`,
`loop`, `while`, `for`, `unsafe`, `const`), at the brace closing that expression, unless the next
token (on the same line or a later one) is `else`, a method call or `?`. rustc ends such a body
before a binary operator, so a `-`, `&`, `|` or `<` there opens the next arm's pattern (a
negative literal, a reference, a leading vert, a qualified path). A body that starts on the line
after `=>` is read at its first token. So in `Some(f) => if c { 1 } else { f() }` the brace after
`1` ends nothing and `f()` reaches the local, while a comma-less block arm still ends before the
next arm's pattern. A closure's body is a
whole expression (`|f| { 0 } + f()` is one body), so it ends only at a comma, a semicolon or the
bracket around it. The opening pipe of a closure's parameters is the last pipe before the name
when what precedes it cannot be an operand (`(|`, `= |`, `move |`), so the pipe of an or-pattern
(`A | B => v.map(|f| f())`) or of a bitwise or is not taken for it.

A binding is read from one line: a `let` pattern or closure parameter list wrapped across lines,
or a match arm whose guard or `=>` sits on a later line than the name, is not recognised as
binding the name, so a call of that local is reported. Keep such a pattern on one line, or rename
the local.

A macro may rewrite its input: `syscall!(recv(fd, buf))` expands to `libc::recv`, and tracing's
`debug!(x = debug(&v))` never calls a function named `debug`. A call inside the input of any macro
except the standard expression macros (`assert!`, `format!`, `println!`, `vec!`, `write!` and the
rest of `rustExpressionMacros`) is therefore not decided. The cost is a real self-call inside a
user-defined wrapper macro, such as `ok!(self.parse_expr())`, which
`HISS-01/rust/gap/user-macro-self-call.rs` records.

A function written in a `macro_rules!` template, whose signature holds a `$` metavariable, is not
decided either. Its body may be a fragment such as `$body`, so the brace its header seems to open
belongs to other code, and the template's expansion is not visible.
`HISS-01/rust/negative/macro-template.rs` and `HISS-01/rust/gap/macro-template-self-call.rs`
record both sides.

`TestPythonSelfRecursionScopesBindings`, `TestRustSelfRecursionShadowsLexically` and
`TestRustMacroInputIsUndecided` in `internal/hiss/recursion_test.go` pin these rules. The fixtures
`HISS-01/python/positive/nested-scope-binding.py`, `HISS-01/rust/positive/binding-after-call.rs`,
`HISS-01/rust/positive/after-block-arm.rs`, `HISS-01/rust/negative/scoped-binding.rs`,
`HISS-01/rust/negative/block-in-arm-or-closure.rs` and `HISS-01/rust/negative/macro-input.rs`
replay them.

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
record each one. A Rust function behind any header form that "Rust: scopes follow braces" lists
(`pub(super)`, `const`, `unsafe`, `extern "C"`) is decided like a plain `fn`
(`HISS-01/rust/positive/qualified-header.rs`).

## C and C++: `goto` and the declared cleanup exception

The native scan reports HISS-01 for a line of literal-stripped code that opens with `goto` and a space.
Without a declaration every such line is a finding, as it always was. A repository that declares
`hiss.exceptions.c_goto_cleanup` in `.standards.yaml`, and carries the document it names, scans
with `hiss.CleanupGoto` enabled ([declared HISS exceptions](../adoption.md#what-adoption-reads-before-it-writes)).
`nativeGotos` in `internal/hiss/cleanup_goto.go` then holds each `goto` inside a function body
until the brace tracker closes that body, since the label usually follows the jump, and reports it
unless all five conditions hold:

1. The `goto` line comes before its label.
2. The label is in the same function body.
3. That function defines exactly one label.
4. The label sits at body depth 1, outside every nested block. A label after closing braces on its
   own line (`} out:`) counts at the depth those braces leave.
5. The label is `cleanup`, `out`, `err` or `fail`, or is listed in
   `hiss.exceptions.c_goto_cleanup_labels`.

A label is an identifier and one colon opening a line. `case`, `default`, the C++ access
specifiers and a `::` qualified name are not labels. A `goto` outside any function body, and every
`goto` past 1024 held in one function, is reported at once, so the bound fails closed. A body the
file never closes is decided at end of file. The adopted HISS-01 clause renders the same rule text
(`hiss.CleanupGotoRule`), so the harness and the scan cannot disagree. `internal/hiss/cleanup_goto_test.go`
replays each condition in both directions, the label shapes and the bound.

The limits of a line matcher still apply: a `goto` or a label that does not open its line, such as
`if (rc) goto out;`, is not seen, with or without the exception.

## HISS-04: Go complexity is measured, not enforced

For Go, the scanner enforces one HISS-04 bound, function length, and *measures* the other three:
cyclomatic complexity, cognitive complexity and statement count. A value over its limit is printed
as a report line and changes nothing else: it never becomes a violation, never enters
`.standards-baseline.json`, and never fails an audit, a gate stage or a dogfood run. A report line
has this shape:

```text
[REPORT] HISS-04 pkg/parse/parse.go:24 Function 'Parse' cyclomatic complexity 12 exceeds 10 (report: measured, not enforced)
```

One visitor takes every measurement: `hiss.MeasureFunc` in `internal/hiss/complexity.go`. Its
counts follow the linters that enforce the same caps. Cyclomatic follows gocyclo, cognitive follows
gocognit and statements follow funlen. `TestMeasureFunc_MatchesReferenceTools` pins one function per
counting rule to the values those tools report. The one known difference is gocognit's recursion
increment: gocognit also adds it when a method calls an unrelated identifier of the same name,
which is not recursion, so the scanner does not.

What is measured:

- Every production function declaration. Test files are exempt, as they are from the length rule
  and from the linters in `.golangci.yml`.
- A function literal bound to a package-level variable, or otherwise outside any function, counted
  as a function of its own.
- A literal inside a function counts toward that function, as gocyclo and gocognit count it. It is
  never measured twice.

The limits come from the resolved policy through `config.ComplexityPolicy.ScanOptions`
(`internal/config/repository_policy.go`), the same limits the audit resolves. Unset limits fall
back to `hiss.DefaultMaxCyclomatic`, `hiss.DefaultMaxCognitive` and `hiss.DefaultMaxStatements`
(10, 15, 50). The dogfood self audit and remote clones scan with those defaults.

The measurements travel in one structured field, `ScanReport.Complexity`. Each entry carries its
`kind` (`cyclomatic`, `cognitive`, `statements`), `value`, `limit` and `severity` (always
`report`). Every entry point renders them with `ComplexityReport.Lines`:

| Entry point | Where the lines appear |
| :--- | :--- |
| `praetorctl audit` | after the scan-scope line |
| `praetorctl gate` | after the stage list; kept out of the signed stage output |
| MCP `standards_audit` | after the baseline verdict |
| `praetorctl dogfood`, MCP `standards_dogfood` | under the self audit and under each remote |
| public dogfood | JSON: `original_scan.complexity` and each verification's `scan.complexity` |
| MCP `standards_inspect_symbols` | under each function, verdict `HISS-04 REPORT: <kinds>` |
| `standards-lsp` | one diagnostic per measurement, severity Information |

Enforcement stays with gocyclo, gocognit and funlen at the thresholds `.golangci.yml` sets. The
corpus pins both sides of each limit. Every fixture in
`.config/hiss/testdata/HISS-04/go/measured/` sits over a limit and must yield a measurement and
no violation. The `negative/` fixtures sit exactly at cyclomatic 10, cognitive 15 and statements
50, and must yield neither.

## JavaScript, TypeScript and Svelte: one line scanner

[`internal/hiss/script.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/hiss/script.go)
reads `.js`, `.jsx`, `.mjs`, `.cjs`, `.ts`, `.tsx`, `.mts` and `.cts` files, and the `<script>` blocks
of a `.svelte` component that open at the start of a line, with every other component line blanked
so line numbers still match. The literal stripper's JavaScript mode removes strings, comments,
template text and regular expression literals but keeps the code of a template's `${...}`
substitutions; a slash starts a regular expression only where an expression may, and never in a
JSX `</` or `/>`. JSX text is not string or comment syntax: a quote that does not close on its line
(the apostrophe of `<p>Don't</p>`) is text, since a JavaScript string cannot span lines, and the `//`
of a URL written in JSX text (`https://example.com`) opens no comment
(`TestLiteralStripperScriptSyntax`, `TestScriptScanner_JSXTextKeepsBracesBalanced`).

- HISS-01: a declared or bound function calling its bare name, and a method or class field calling
  `this.name`. A local binding of the name (a parameter, a declarator, a nested function, a catch or
  arrow parameter) shadows it. Mutual recursion is not decided.
- HISS-02: `while (true)`, `while (1)` and `for (;;)`, the pattern C shares. The I/O-timeout half
  is not decided.
- HISS-04: the length of every named function, from the line its body opens on, as for C and Rust.
  Anonymous callbacks and the complexity caps are not measured
  (`script_header.go` recognises the headers).
- HISS-07: an empty catch block, a `.catch` with an empty handler, and `process.exit` outside a test
  file, module scope and a top-level `main`.
- HISS-08: `eval`, the `Function` constructor and a string passed to `setTimeout` or `setInterval`.

A parameter list may hold a destructuring pattern, an object type or a default value, on one line
or wrapped one name per line, as a React component's props usually are: `closeParen` in
`script_header.go` nests braces and brackets inside the list, so the function is tracked for all
of the rules above (`TestScriptScanner_PatternParameters`). A list still waiting for its closing
parenthesis never hides a function that opens on one of its lines, so the methods of an object
passed to a call such as `Page({` are measured too.

A file with a line longer than 1024 bytes is minified output and is declined, so it is reported as
unscanned source. A file whose braces the scanner misread, so that they do not balance, is declined
the same way, and none of its findings is reported: its function boundaries are unknown. Neither
makes the scan incomplete, so neither rejects `gate run`; both show on the audit's `[UNSCANNED]`
line and in `unscanned_languages`, never as clean
(`TestScriptScanner_MinifiedIsUnscannedNotClean`, `TestScriptScanner_MisreadFileIsDeclined` in
`internal/hiss/script_test.go`). `.config/hiss/coverage.yaml` lists each claim and its gaps per
language (`javascript`, `typescript`, `svelte`).

## Shell, systemd and Ansible

These scanners cover the files an operations repository keeps its logic in: shell scripts, the
`run:` blocks of GitHub Actions workflows, systemd units and Ansible playbooks. Each states which
HISS rules apply to its language and why; a rule left out has no analogue there or is held as a gap
in `.config/hiss/coverage.yaml` (languages `shell`, `github-actions`, `systemd`, `ansible`).

| Rule | Shell | Workflow `run:` block | systemd unit | Ansible |
| :--- | :--- | :--- | :--- | :--- |
| HISS-01 | direct recursion | as shell | no functions | no functions |
| HISS-02 | unbounded loops, curl without a deadline | as shell | oneshot start timeout, disabled timeouts, restart loops | not decided |
| HISS-04 | function length | as shell | no functions | no functions |
| HISS-07 | strict mode, `\|\| true` | `\|\| true` (no strict mode) | `-` command prefix | discarded failures, shell pipes |
| HISS-08 | `eval`, text piped into a shell | as shell | not decided | change not determined by state |

### Shell

[`internal/hiss/shell.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/hiss/shell.go)
reads `.sh` and `.bash` files and files without an extension whose `#!` line names `sh`, `bash`,
`dash` or `ash`, directly or through `env`. A `.sh` file whose `#!` line names another program
(`zsh`, `python`) is declined. Its lexer (`shell_lex.go`) blanks quotes, comments, parameter and
arithmetic expansions and here-document bodies, keeps the code of command and process
substitutions, even inside double quotes, and carries an open quote or here-document across lines
(`TestShellLexer_KeepsSubstitutionCode`). `shell_commands.go` cuts each logical line, joined across
a trailing backslash, pipe or `&&`, into simple commands and reads each command's name past
reserved words, assignments, redirections and wrappers such as `sudo`, `env` and `timeout`. The
patterns of a `case` statement, including one opened on the same line as an outer pattern
(`x) case $b in`) and one after Bash's fall-through `;&` or `;;&`, and the inside of a `[[ ... ]]`
test name no command, so `*/sh | */bash)` and `[[ $f =~ \.(sh|bash)$ ]]` pipe nothing into a shell
(`TestShellScanner_CasePatternsAreNotCommands`, `TestShellScanner_NestedCasePatternsAreNotCommands`,
`TestShellScanner_CaseFallThroughEndsAnItem`, `TestShellScanner_TestExpressionsAreNotPipes`,
`.config/hiss/testdata/HISS-08/shell/negative/nested-case.sh`,
`.config/hiss/testdata/HISS-08/shell/negative/case-fall-through.bash`).

- HISS-01: a function whose body is a brace group runs its own name as a command. A call through
  `command`, `builtin` or `exec` runs a program, never the function, so it is not reported.
- HISS-02: `while true`, `while :`, `until false` and `for ((;;))` carry no bound. A `curl`
  transfer with neither `--max-time` nor `-m`, and no `timeout` command around it, has no overall
  deadline, which is the I/O half of the rule; `curl --version` and `curl --help` transfer nothing.
- HISS-04: function length, from the line the body brace opens on to the line it closes on, as the
  spec measures it.
- HISS-07: a script with a `#!` line that never enables `errexit` and `nounset` (`set -eu`, the
  long names, or options on the `#!` line), and under Bash `pipefail`, carries on past a failed
  command, an unset variable or a failure on the left of a pipe; it is reported once at line 1.
  POSIX `sh` is not required to set `pipefail`, which not every shell still deployed supports. A
  file without a `#!` line is a library its caller sources and runs under the caller's options.
  `|| true` and `|| :` discard the failure they follow.
- HISS-08: `eval`, text piped into a shell that reads its script from standard input
  (`curl ... | sh`, through `sudo` too), and a process substitution sourced or run as a script.

Not decided: mutual recursion, a loop whose condition always succeeds without being a constant
(`while sleep 5`), `read` without `-t`, a subshell function body `f() ( ... )`, `set +e` around an
unchecked command, a failure discarded through a brace group, and a command string given to
`sh -c`; each is a gap fixture. A file whose quotes, substitutions, here-documents or braces the
scanner misread is declined and shows on the `[UNSCANNED]` line
(`TestShellScanner_DeclinedFilesAreUnscanned` in `internal/hiss/shell_test.go`). The `run:` blocks
of a GitHub Actions workflow are read with these rules too, as the next section describes.

### GitHub Actions run: blocks

[`internal/hiss/workflow.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/hiss/workflow.go)
reads every YAML document directly in `.github/workflows`, the files GitHub runs as workflows,
through the workflow model the forge audits read too
([`internal/ghworkflow`](https://github.com/cordanaLLM/praetor/blob/main/internal/ghworkflow/ghworkflow.go)),
and hands each `run:` block to the shell scanner when its shell is one that scanner reads. The
coverage record names a workflow file `github-actions`.

The shell is resolved as GitHub's workflow syntax reference documents it (`ghworkflow.StepShell`,
`TestStepShell_Precedence` in `internal/ghworkflow/shell_test.go`):

| Source, most specific first | Shell |
| :--- | :--- |
| the step's `shell:` | a built-in keyword (`bash`, `sh`, `pwsh`, `powershell`, `cmd`, `python`), or the first word of a custom template such as `perl {0}` |
| the job's `defaults.run.shell` | the same |
| the workflow's `defaults.run.shell` | the same |
| a job with a `container:` | `sh` |
| a runner whose labels name Windows (`windows-*`, `windows`) | `pwsh` |
| a runner whose labels name Linux or macOS (`ubuntu-*`, `macos-*`, `linux`, `macOS`) | `bash` |

A block under `sh`, `bash`, `dash` or `ash` is read with the rules of the Shell section above. Its
`${{ ... }}` expressions are blanked first, line breaks kept, because GitHub substitutes them
before the shell starts: `|| true` or `eval` inside one is not shell
(`TestWorkflowScanner_BlanksExpressions` in `internal/hiss/workflow_test.go`). A finding names the
workflow file. In a literal block (`run: |`) it names the line the command sits on; in any other
style it names the line the value starts on, because folding and escapes lose the mapping
(`TestWorkflowScanner_ReportsEachShellRule`). Baseline and ratchet then treat it like any other
finding.

Every other block is counted in `unscanned_run_blocks` under its shell and listed on the audit's
`[UNSCANNED]` line as `GitHub Actions run: blocks (2 pwsh, 1 unresolved)`, never read as clean
(`TestWorkflowScanner_ResolvesTheShell`, `TestWorkflowScanner_UnresolvedAndContainerShells`):

- a shell the shell scanner does not read: `pwsh`, the Windows default, `powershell`, `cmd`,
  `python`, or a custom command;
- `unresolved`: a shell an expression names, or a default on a runner whose labels name no single
  operating system, such as `runs-on: ${{ matrix.os }}` or a self-hosted runner without an OS
  label, because a Windows leg would run the block under `pwsh`;
- a block the shell scanner declines, and a block past a bound.

The bounds (HISS-02) are 64 workflow files per scan (`ghworkflow.MaxFiles`, the bound the forge
audits read under), 512 blocks per file and 64 KiB per block. The 64 KiB block bound is praetor's
own, not a GitHub limit. GitHub's 21,000-character limit (`Exceeded max expression length 21000`)
is the maximum length of an expression; it reaches a `run:` value only through the `${{ }}`
expressions the value holds. A file past the file bound is unscanned `github-actions` source; a
document the model refuses, malformed or past its job or step bounds, is declined whole
(`TestWorkflowScanner_FileBound`, `TestWorkflowScanner_BlockBounds`,
`TestWorkflowScanner_DeclinedBlocksAndDocuments`). The coverage record keeps at most 64 shell
keys: past 62 named shells a further one is counted under `custom`, which, like `unresolved`,
always keeps its own key, so no block goes uncounted (`TestWorkflowScanner_RunBlockLabelBound`).

A block has no interpreter line: GitHub starts its shell itself, as `bash -e {0}` when no shell is
named, `bash --noprofile --norc -eo pipefail {0}` for `shell: bash` and `sh -e {0}` for `shell: sh`.
The HISS-07 strict-mode rule, which holds a script with an interpreter line to `set -eu`, therefore
does not apply to a block, and a pipeline failure under the default shell, which runs without
`pipefail`, is a gap fixture (`HISS-07/github-actions/gap/default-shell-pipe.yml`). Composite
actions (`action.yml` under `.github/actions`) are not read. The corpus replay stages each
`github-actions` fixture under `.github/workflows` (`hiss.FixturePath`), where the scan reads
workflows.

### systemd units

[`internal/hiss/systemd.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/hiss/systemd.go)
reads `.service` and `.socket` files that open a `[Unit]`, `[Service]`, `[Socket]` or `[Install]`
section, following `systemd.syntax(7)`: a trailing backslash continues a setting, `#` and `;` start
a comment line, the last assignment of a setting wins and an empty one resets it.

- HISS-02: a `Type=oneshot` service without `TimeoutStartSec=` or `TimeoutSec=`, because systemd
  disables the start timeout for oneshot by default (`systemd.service(5)`); a start, stop, abort or
  socket timeout set to `infinity`, or any of them but the abort timeout set to `0`, which systemd
  also reads as no timeout (`TestSystemdScanner_Boundaries`); and a service with
  `Restart=` other than `no` whose `StartLimitIntervalSec=` is `0`, which turns off the rate limit
  that stops a restart loop (`systemd.unit(5)`).
- HISS-07: a command setting whose prefix holds `-`, since systemd records its failure and then
  treats it as success. The command settings are the keys systemd parses as command lines:
  `ExecCondition=`, `ExecStartPre=`, `ExecStart=`, `ExecStartPost=`, `ExecReload=`,
  `ExecReloadPost=` (systemd 259), `ExecStop=` and `ExecStopPost=` of a service
  (`systemd.service(5)`), and `ExecStartPre=`, `ExecStartPost=`, `ExecStopPre=` and
  `ExecStopPost=` of a socket (`systemd.socket(5)`). `ExecPaths=`, `NoExecPaths=` and
  `ExecSearchPath=` are path lists, whose `-` ignores a missing path (`systemd.exec(5)`), so they
  are not read (`TestSystemdScanner_OnlyCommandSettingsAreExec`,
  `.config/hiss/testdata/HISS-07/systemd/negative/exec-path-lists.service`).

Not decided: a drop-in (`.conf`) that overrides a unit, which is a separate file, and shell inside a
command setting. A unit has no functions and evaluates nothing itself, so HISS-01, HISS-04 and
HISS-08 have no analogue (`internal/hiss/systemd_test.go`).

### Ansible

[`internal/hiss/ansible.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/hiss/ansible.go)
reads a YAML file that is a playbook (a list holding a play with `hosts:` or an `import_playbook:`)
or a role's task or handler file (`roles/<role>/tasks/` or `roles/<role>/handlers/`). It walks a
play's `pre_tasks`, `tasks`, `post_tasks` and `handlers` and every `block`, `rescue` and `always`
section with an explicit stack. The rules follow the matching
[ansible-lint](https://ansible.readthedocs.io/projects/lint/rules/) rules.

- HISS-07: `ignore_errors: true` or `failed_when: false` on a task or block that does not
  `register` its result (ignore-errors), and a `shell` task that pipes without `set -o pipefail`,
  whose status is then the last command's (risky-shell-pipe). The command is the module's
  free-form value or, when the module holds a mapping, nothing or an empty string, its `cmd`
  argument, from the module or from the task's `args`, as Ansible reads it
  (`TestAnsibleScanner_ShellCommandFromArgs`). A pipe inside quotes or Jinja is text.
- HISS-08: a `command`, `shell`, `raw` or `script` task, under its short, `ansible.builtin` or
  `ansible.legacy` name, with no `changed_when`, `creates` or `removes` reports a change on every
  run whatever the host's state, so the play's outcome is not determined by the state it converges
  (no-changed-when).

Not decided: a task file included by path from outside a role's directories, which its contents
alone do not mark as Ansible, the Windows command modules, and HISS-02, whose loops run over lists
and whose I/O deadlines are module arguments this scanner does not read
(`internal/hiss/ansible_test.go`).

## HISS-20: claims are replayed

Every enforcement claim in `.config/hiss/coverage.yaml` is replayed against the fixture corpus by
`praetorctl hiss coverage --verify`, which runs inside `make verify-all`. The check runs in both
directions: a claim of enforcement must report each of its positive fixtures, and a claim of absence
must leave its gap fixtures undetected. A rule that silently *gains* coverage fails the gate exactly
as one that silently loses it, so the catalog cannot drift in either direction. A `measured/`
bucket holds shapes the scanner reports without enforcing them. Each must yield a measurement and
no violation, and a negative fixture must yield no measurement. So a measurement can neither
disappear nor start to enforce unnoticed.

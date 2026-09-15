# How the HISS rule matchers read source

The Go rules use the AST. The rules for C, C++, Rust and Python match text, and this document
records what that means, because the difference is load-bearing.

## Whitespace does not silence a rule

The non-Go matchers previously compared exact strings, so a single space defeated them. All of these
passed a gate that claims to catch them:

```
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

## HISS-20: claims are replayed

Every enforcement claim in `.config/hiss/coverage.yaml` is replayed against the fixture corpus by
`praetorctl hiss coverage --verify`, which runs inside `make verify-all`. The check runs in both
directions: a claim of enforcement must report each of its positive fixtures, and a claim of absence
must leave its gap fixtures undetected. A rule that silently *gains* coverage fails the gate exactly
as one that silently loses it, so the catalog cannot drift in either direction.

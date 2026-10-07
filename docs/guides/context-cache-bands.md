# Context cache bands

A client keeps the longest unchanged prefix of an instruction file in its prompt cache. One
edit near the top of a compiled file invalidates every byte after it. Cache bands order
`AGENTS.md` so the text that rarely changes comes first.

## Start here

Add three marker lines to `AGENTS.md`, each on a line of its own:

```markdown
<!-- praetor:head -->
<!-- praetor:config -->
<!-- praetor:tail -->
```

A marker opens its band and the band runs to the next marker. Text before the first marker is
head. Then run `praetorctl compile-context`.

| Band | Put here | Changes when |
| :--- | :--- | :--- |
| head | title, invariants, operational rules | never in a normal run |
| config | the text register block, `## <Client>` sections | a manifest or client edit |
| tail | verification commands, repository-state text | the repository changes |

`compile-context` writes the static generated header first, then head, config and tail, for
all six vendor files, whatever order the source wrote the bands in. The markers do not appear
in the compiled files. Blank lines at a band edge are trimmed and the bands are joined by one
blank line.

## Reference

- A `## <Client>` section (`## Claude Code`, `## Cursor`, ...) always joins the config band, and
  a marker ends the section.
- A marker inside a fenced code block is text, not a marker.
- An `AGENTS.md` without markers compiles in source order. `compile-context --verify`,
  `compile-context --verify-stable` and `praetorctl audit` print a warning that names the markers
  to add. The next release turns the warning into a failure.
- This repository's `AGENTS.md` places the markers after the title, before
  `## Text Register` and before `## Primary Verification Commands`.

## Stability gate

`praetorctl compile-context --verify-stable` checks two things:

1. Two renders of `AGENTS.md` under different injected clocks and shuffled target visit orders
   (`agentcontext.RenderEnv`) produce identical bytes in every vendor file. Today the renderer
   reads neither the clock nor the order, so this half guards later changes: a render that starts
   to read `RenderEnv.Now` or the visit order is refused (`TestVerifyRenderTwice_Negative_ClockReadingRenderRefused`).
2. The head band holds no volatile text: an ISO timestamp, a `sha256:` digest or a 40 to 64 digit
   hex string, an absolute path (`/home/...`, `C:\...`) or a run counter (`run #7`, `count=3`).

`compile-context --verify` and `praetorctl audit` run the same check, so a drifting or
volatile head fails the gate that already guards the compiled files. The tests are
`internal/compiler/stable_test.go` (a planted timestamp, digest, path and counter in the head are
refused) and `internal/agentcontext/bands_test.go` (band order for every vendor file, render
identity, and a head edit leaving the bytes before it and after it untouched).

## Scope

This page covers the compiled context files. The MCP tool list and tool output are not changed by
cache bands.

## Adopted repositories

`praetorctl adopt` writes the harness with the three markers (`internal/adopt/harness.go`), so a fresh
adoption compiles layered and a `--force` refresh of the harness region keeps them
(`TestHarnessCarriesTheCacheBandMarkers`). Only an `AGENTS.md` written by hand needs the markers added.

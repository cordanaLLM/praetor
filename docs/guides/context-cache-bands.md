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
  to add. A later release turns the warning into a failure (follow-up tracked in #853).
- This repository's `AGENTS.md` places the markers after the title, before
  `## Text Register` and before `## Primary Verification Commands`.

## Stability gate

`praetorctl compile-context --verify-stable` checks two things:

1. Two renders of `AGENTS.md` under different injected clocks and shuffled target visit orders
   (`agentcontext.RenderEnv`) produce identical bytes in every vendor file. Today the renderer
   reads neither the clock nor the order, so this half guards later changes: a render that starts
   to read `RenderEnv.Now` or the visit order is refused (`TestVerifyRenderTwice_Negative_ClockReadingRenderRefused`).
2. The head band holds no volatile text. The scanner matches exact shapes only, so ordinary prose
   passes (`retry count: 3`, `Run: 2 passes`, a static 40-digit commit pin, `/var/tmp` named in a
   sentence):
   - an RFC 3339 timestamp (`2026-10-07T12:30:00Z`);
   - `sha256:` followed by all 64 hex digits;
   - an absolute path: `/home/<name>/...`, `/Users/<name>/...` or `/Volumes/<disk>/...`, a path
     with two components under `/root /tmp /var /mnt /srv /opt /etc /usr /private /workspace /nix`
     (`/etc/ssl/certs`), a drive path (`C:\work`, `D:/work`) or a UNC path (`\\host\share`);
   - a run or build counter: `run #7`, `run_id: <value>`, `build number 12`, `build #12`.

`compile-context --verify` and `praetorctl audit` run the same check, so a drifting or
volatile head fails the gate that already guards the compiled files. The tests are
`internal/compiler/stable_test.go` (a planted timestamp, digest, path and counter in the head are
refused) and `internal/agentcontext/bands_test.go` (band order for every vendor file, render
identity, final newline, and a head edit leaving the bytes before it and after it untouched).
`cmd/standardsctl/audit_context_stable_test.go` proves that `compile-context --verify` and the
audit refuse a planted head timestamp and print the unlayered warning for an unmarked source.

`praetorctl compile-context` also writes the text register block into the config band when a
layered `AGENTS.md` has no register block yet (`TestSpliceRegisterBlock_LayeredSourceGetsTheSectionInTheConfigBand`).

## Scope

This page covers the compiled context files. The MCP tool list and tool output are not changed by
cache bands.

## Adopted repositories

`praetorctl adopt` writes the harness with the three markers (`internal/adopt/harness.go`), so a
fresh adoption compiles layered (`TestHarnessCarriesTheCacheBandMarkers`).

An already adopted repository keeps its existing `AGENTS.md`: a plain `praetorctl adopt` leaves the
harness as it is and adds no markers. Run `praetorctl adopt --force` to refresh the harness region
with the markers, or add the three markers by hand as shown above. Until then `compile-context`
compiles in source order and warns.

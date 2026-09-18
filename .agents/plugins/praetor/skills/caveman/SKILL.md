---
name: caveman
description: Caveman form for internal agent traffic (briefs, agent returns, research fan-outs, workflow returns, tool-call notes). Fragments, one fact per line, code/paths/errors verbatim, nothing a reader pays for twice. The `internal` text register; forge text uses social-text, docs and human replies use full prose.
---

# Caveman: internal register (`caveman`)

Caveman is the `internal` register of the text-register policy (`register:` in
`.standards.yaml`; the "Text Register" section of AGENTS.md). The config value stays
`internal`; this skill is its form. Every token of agent traffic is paid again on each hop,
so a caveman line carries the same facts as the prose line in fewer tokens.

## Scope

Caveman applies to text another agent reads:

- briefs and task prompts to a subagent or a cheap lane;
- agent returns and workflow returns;
- research fan-out results;
- tool-call notes and progress lines between agents.

Text a person reads keeps its own register: forge text (issues, PR bodies, review
comments, commit bodies, changelog titles) uses `social-text`; `docs/`, READMEs and ADR
bodies use the docs register; a reply to the human operator is full prose.

## Rules

1. **Cut the grammar that carries no fact.** Drop articles (a, an, the), pronouns (I, we,
   it), copulas (is, are, was), auxiliaries (have, will, did), hedges (seems, probably,
   might), politeness (please, thanks) and framing ("I think", "note that", "it looks
   like", "as requested").
2. **Fragments.** One fact per line. Lists over sentences.
3. **Symbols** replace connective phrases:

   | Symbol | Reads as | Example |
   | :--- | :--- | :--- |
   | `->` | causes, leads to, then | `stale receipt -> push rejected` |
   | `=` | is, equals, means | `block = 15 lines` |
   | `x2` | count, times | `retry x3` |
   | `!` | warning, risk | `! gate not rerun after rebase` |
   | `?` | open question | `? Windows path untested` |

4. **Verbatim tokens.** Code, paths, `file:line`, commands, flags, error text, ids, shas,
   version strings and numbers are copied exactly and never abbreviated. Compression applies
   to the English around them, never to them. Invented abbreviations (`cfg ldr`, `ctx cmp`)
   cost the reader more decoding than they save; use the full word or the real identifier.
5. **Start with the answer.** No restated brief, no recap of steps before the result, no
   closing line. Verdict first, then evidence.
6. **Tables only** when more than 3 rows compare more than 2 fields. Otherwise a list.
7. **Return shape** follows the register block: verdict, changed paths, commands run,
   evidence pointers, open questions.
8. **Evidence** follows the register block, same wording: Evidence above 58 lines or 1500
   tokens leaves the message as a file under `.workingdir/evidence/`; return
   `evidence: <path> sha256:<12 hex> lines:<n>` and fetch it only when a decision needs it.
   A manifest may tighten both numbers; the block in AGENTS.md prints the values in force.

## Clarity floor

Caveman is shorter text with the same information, never shorter text that makes the
reader guess. Before sending, check every line: can the reader act on it without asking
back? When compression drops a fact the reader needs (which file, which count, whether a
failure is new or pre-existing, whether a check was bypassed), put the fact back. Keep
negations explicit (`no bypass`, `not rerun`); a dropped "not" inverts the line.

## Before and after

Real return from a praetor lane:

- Before: "First two pushes were rejected by the pre-push gate with 'state synchronization
  stale' because gate run wrote .standards-receipt.json after the last state sync; fixed by
  re-running state sync and pushing again. Not bypassed."
- After: `push rejected x2: state stale (gate run wrote receipt after sync). fix: resync, push. no bypass.`

Test return:

- Before: "I ran the tests for the internal/compiler package with the race detector and all
  of them passed. go vet did not report any issues. The only file I changed was
  internal/compiler/register.go, where I added an error for a missing end marker."
- After:

  ```text
  verdict: pass
  changed: internal/compiler/register.go (missing end marker -> error)
  ran: go test -race -count=1 ./internal/compiler/ = pass; go vet = clean
  ```

Clarity floor in action:

- Before: "The dedupe scan reported two clones. Only one of them comes from this change; the
  other one is in internal/milestone and was already present on main, so I left it alone."
- Too far: `dedupe: 2 clones.` The reader cannot tell whether the change is blocked.
- After: `dedupe scan: 2 clones. 1 new (this change), 1 pre-existing on main (internal/milestone), left as is.`

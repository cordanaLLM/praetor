---
name: social-text
description: Write forge-facing text (issues, pull-request bodies, review comments, commit bodies, changelog titles) as scannable human prose derived from adhd-format, while keeping the PR template, the receipt fence, conventional commits and Keep-a-Changelog intact.
---

# Social register for the forge (`social-text`)

Use this skill for anything a person reads on the forge: an issue, a pull-request body, a review comment, a commit body, a changelog fragment title. It is the `social` register of the text-register policy (`register:` in `.standards.yaml`; see the "Text Register" section of AGENTS.md). Agent-to-agent text uses the `internal` register, documentation the `docs` register; neither uses this skill.

## Inherited from adhd-format

This skill inherits three principles of `adhd-format` by reference and does not restate them:

1. **Bottom line first**: the first sentence is the decision, the defect or the ask.
2. **Scannable structure**: paragraphs of at most three sentences; bold the operative words of an action bullet.
3. **Progressive disclosure**: summary first, commands second, detail linked or collapsed below.

It overrides the rest of adhd-format for humans: at most one GitHub alert per text; no Mermaid unless a flow is the point of the change; tables only for three or more rows; no emoji headings; no anchor bolding in running prose.

## Voice

- Full sentences and plain words. A maintainer must be able to act on the text without opening the diff.
- One idea per paragraph or bullet. Name the file and line (`internal/config/register.go:42`) when the reader will look there.
- Say four things, then stop: what changed, why, how it was verified, what the reviewer must decide. A pull-request summary stays around 250 words before its evidence links.
- No meta-commentary ("as requested", "based on my analysis"), no hedging filler, no restatement of code the diff already shows.
- Quote the operator paraphrased in neutral English; never a verbatim colloquial line.
- No tool attribution anywhere: no `Co-Authored-By` naming a model or tool, no "generated with" footer (AGENTS.md rule 12). Strip any such footer when editing an existing body.

## Evidence

Link or attach evidence; never paste it beyond the inline bound (`register.evidence`, default 58 lines / 1500 tokens). A test log, a SARIF file or a transcript is referenced as `evidence: <path> sha256:<12 hex> lines:<n>` or as an attached file. The signed receipt is the one exception and is pasted where the template asks for it.

## Standards this register does not change

**Pull-request body.** Fill `.github/pull_request_template.md` exactly as shipped. The social register governs only the prose under `## Summary of Changes`. Sections 1 to 4 (the HISS-16 checklist, the 3D-testing verification, the context-integrity checklist and the receipt) are filled, never rewritten, reordered or removed. The receipt goes inside the fence labelled `receipt`; an unlabelled fence is not read by the validator.

**Commit message.** The subject stays a conventional commit, `type(scope): subject`, with `type` one of `feat | fix | docs | style | refactor | perf | test | build | ci | chore | revert`. The commit-msg hook (`.config/lefthook/scripts/hooks.py`) rejects any other shape and requires a `Signed-off-by` trailer. Keep the subject near 72 characters; the hook does not measure it, a reader's terminal does. A breaking change carries `!` in the subject and a `Migration:` footer (HISS-14). Only the body is written in this register: why first, then what, wrapped at 72 columns.

**Changelog.** Never edit `CHANGELOG.md` directly. Write one fragment `changelog.d/<date>-<slug>.yaml` with `type` from `added | changed | deprecated | removed | fixed | security`, a one-sentence imperative `title`, optional `issue`, and `breaking: true` when applicable. The register shapes the title only.

**Issue.** Title is the defect in one sentence. Body: observed, expected, reproduction commands, evidence pointers. Search open issues and pull requests before filing (AGENTS.md rule 5).

**Review comment.** One finding per comment; severity word first, then file:line, then the fix. Address the code, not the author.

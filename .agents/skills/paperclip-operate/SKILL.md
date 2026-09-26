---
name: paperclip-operate
description: Operate within Paperclip agent orchestration harness, enforcing Rule 0 terminal disposition, AGit change submission, and verified shipping contracts.
---

# Paperclip Autonomous Agent Operations (`paperclip-operate`)

Operate within Paperclip orchestration harness (`apps/ai/paperclip*` / `paperclipai/paperclip`), adhering strictly to Rule 0 terminal disposition, AGit change submission, and high-integrity invariant validation.

## Core Rules & Invariants

1. **Rule 0 — Terminal Disposition Mandate (ADR-0087)**:
   - Every Paperclip agent run MUST terminate with unambiguous, structured disposition: `in_review` or `blocked`.
   - Never emit `done` directly: LLM self-completion claims are non-authoritative, automatically re-mapped to `in_review`.
   - Blocked runs must name human or team recovery owner.

2. **Operating Contract — "Pushing is NOT Shipping"**:
   - Pushing branch or AGit topic = change submission, not change delivery.
   - Code shipped only when target branch merged with authoritative Ed25519 Exit-0 receipt attached.

3. **AGit Submission Protocol**:
   - Changes are pushed to Gerrit/Paperclip review refs, then to review branch `paperclip/<issue-id>`:
     ```bash
     git push origin HEAD:refs/for/main -o topic=<issue-id> && git push origin HEAD:refs/heads/paperclip/<issue-id>
     ```
   - AGit push opens review, records no local ref. Second push records `refs/remotes/origin/paperclip/<issue-id>` = local proof for `paperclip verify`. Explicit destination -> never pushes local `main` to remote `main`.

---

## 4-Step Operational Workflow

### Step 1: Initialize or Verify Paperclip Harness
Check and synthesize repo-level Paperclip configuration and rules:
```bash
praetorctl paperclip harness --path=.
```
Verifies `.paperclip/harness.json` and `.paperclip/rules.md`.

### Step 2: Implement & Run Invariant Gates
Implement requested changes and execute local verification gate before change submission:
```bash
make verify-all
```

### Step 3: Submit Changes via AGit
Push commits using Paperclip AGit topic format plus review branch (`agit_push_format` in `.paperclip/harness.json`):
```bash
git push origin HEAD:refs/for/main -o topic=<issue-id> && git push origin HEAD:refs/heads/paperclip/<issue-id>
```

### Step 4: Record Rule 0 Disposition & Verify Contract
Generate cryptographically verifiable terminal disposition record:

- **For Successful Runs (`in_review`)**:
  ```bash
  praetorctl paperclip disposition \
    --issue=<issue-id> \
    --status=in_review \
    --note="Implemented feature with 3D test suite passing" \
    --proof="https://github.com/cordanaLLM/praetor/pull/<pr-number>" \
    --output=.paperclip/disposition.json
  ```

- **For Blocked Runs (`blocked`)**:
  ```bash
  praetorctl paperclip disposition \
    --issue=<issue-id> \
    --status=blocked \
    --note="Blocked on upstream API credential access" \
    --recovery-owner="security-team" \
    --output=.paperclip/disposition.json
  ```

- **Verify Contract Compliance**:
  ```bash
  praetorctl paperclip verify --path=.
  ```
  - `in_review` fails unless: working tree clean (disposition file itself exempt); some ref under `refs/remotes/` contains HEAD. Local refs only, no network. Step 3 second push satisfies it on any branch or detached HEAD; upstream config not needed.
  - AGit `refs/for/*` push alone records no local ref -> fails. No branch-push permission -> fetch forge review head into `refs/remotes/` instead, e.g. forges exposing `refs/pull/<n>/head`: `git fetch origin +refs/pull/<n>/head:refs/remotes/origin/pull/<n>`.
  - Status: whitespace + case ignored, `done` -> `in_review`. Whitespace-only issue, note, proof, recovery owner rejected.
  - Attached `receipt` = envelope carrying `gate_output`; verified against `receipt.public_key` pinned in `.standards.yaml` (`--config=<path>` names another manifest), never key embedded in receipt. Receipt without pin fails.
  - Receipt `commit_sha` must equal HEAD, any status (same binding as `praetorctl gate verify`) -> receipt signed for earlier commit cannot be replayed. Mint receipt after final commit.

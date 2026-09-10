---
name: paperclip-operate
description: Operate within the Paperclip agent orchestration harness, enforcing Rule 0 terminal disposition, AGit change submission, and verified shipping contracts.
---

# Paperclip Autonomous Agent Operations (`paperclip-operate`)

Operate within the Paperclip orchestration harness (`apps/ai/paperclip*` / `paperclipai/paperclip`), adhering strictly to Rule 0 terminal disposition, AGit change submission, and high-integrity invariant validation.

## Core Rules & Invariants

1. **Rule 0 — Terminal Disposition Mandate (ADR-0087)**:
   - Every Paperclip agent run MUST terminate with an unambiguous, structured disposition: `in_review` or `blocked`.
   - Never emit `done` directly: LLM self-completion claims are non-authoritative and automatically re-mapped to `in_review`.
   - Blocked runs must name a human or team recovery owner.

2. **Operating Contract — "Pushing is NOT Shipping"**:
   - Pushing a branch or AGit topic is change submission, not change delivery.
   - Code is only shipped when the target branch is merged with an authoritative Ed25519 Exit-0 receipt attached.

3. **AGit Submission Protocol**:
   - Changes are pushed to Gerrit/Paperclip review refs:
     ```bash
     git push origin HEAD:refs/for/main -o topic=<issue-id>
     ```

---

## 4-Step Operational Workflow

### Step 1: Initialize or Verify Paperclip Harness
Check and synthesize the repo-level Paperclip configuration and rules:
```bash
praetorctl paperclip harness --path=.
```
Verifies `.paperclip/harness.json` and `.paperclip/rules.md`.

### Step 2: Implement & Run Invariant Gates
Implement the requested changes and execute the local verification gate before change submission:
```bash
make verify-all
```

### Step 3: Submit Changes via AGit
Push commits using the Paperclip AGit topic format:
```bash
git push origin HEAD:refs/for/main -o topic=<issue-id>
```

### Step 4: Record Rule 0 Disposition & Verify Contract
Generate the cryptographically verifiable terminal disposition record:

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

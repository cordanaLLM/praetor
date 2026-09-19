---
name: breaking-change-checker
description: Inspect and verify public API changes, schema evolutions, and CLI contracts against HISS-14 append-only rules, enforcing breaking indicators and mandatory Migration footers.
---

# Breaking Change Checker (`breaking-change-checker`)

Enforce HISS-14 append-only contract evolution across Go interfaces, CLI surfaces, declarative schemas, and agent protocols.

## HISS-14 Core Directives

1. **Append-Only Invariant**:
   - Public APIs, exported types, protocol interfaces, and declarative configuration schemas are append-only.
   - Backward-compatible extensions (new optional fields, additive methods) do not require breaking markers.

2. **Breaking Change Criteria**:
   Change classified as **Breaking** when it:
   - Removes or renames exported Go symbol (`type`, `func`, `var`, `const`, `interface`).
   - Changes parameter list, return types, or error behavior of existing exported function.
   - Modifies or removes fields in `.standards.yaml` or `.config/` schemas.
   - Alters CLI subcommands, flags, or default behaviors in `cmd/standardsctl`.
   - Modifies input argument schemas or return structures of registered MCP tools.

## Verification Workflow

Running breaking change check on branch or PR:

1. **Diff against Base**:
   ```bash
   git diff origin/main...HEAD
   ```
2. **Inspect Exported Symbols**:
   - Verify whether public declaration modified or deleted.
   - Breaking modification discovered ->
     - Check commit title for conventional-commit breaking indicator (`!`):
       `feat(!): replace Manifest.Profiles with ProfileObjects`
     - Check commit body for mandatory `Migration:` footer:
       ```text
       Migration:
       Existing configurations using `profiles: ["framework"]` must migrate
       to `profiles: [{ id: "framework" }]` before running `standardsctl sync`.
       ```

3. **Rejection Criteria**:
   - Exported contract broken WITHOUT `!` indicator or WITHOUT `Migration:` footer -> reject change immediately.
   - Append-only alternative exists (example: add new method with `V2` suffix or optional struct field) -> require developer to adopt additive pattern instead.

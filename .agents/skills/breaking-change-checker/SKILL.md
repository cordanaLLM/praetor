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
   A change is classified as **Breaking** if it:
   - Removes or renames any exported Go symbol (`type`, `func`, `var`, `const`, `interface`).
   - Changes the parameter list, return types, or error behavior of an existing exported function.
   - Modifies or removes fields in `.standards.yaml` or `.config/` schemas.
   - Alters CLI subcommands, flags, or default behaviors in `cmd/standardsctl`.
   - Modifies input argument schemas or return structures of registered MCP tools.

## Verification Workflow

When running a breaking change check on a branch or PR:

1. **Diff against Base**:
   ```bash
   git diff origin/main...HEAD
   ```
2. **Inspect Exported Symbols**:
   - Verify if any public declaration was modified or deleted.
   - If a breaking modification is discovered:
     - Check the commit title for the conventional commit breaking indicator (`!`):
       `feat(!): replace Manifest.Profiles with ProfileObjects`
     - Check the commit body for the mandatory `Migration:` footer:
       ```text
       Migration:
       Existing configurations using `profiles: ["framework"]` must migrate
       to `profiles: [{ id: "framework" }]` before running `standardsctl sync`.
       ```

3. **Rejection Criteria**:
   - If an exported contract was broken WITHOUT the `!` indicator or WITHOUT the `Migration:` footer, reject the change immediately.
   - If an append-only alternative exists (e.g. adding a new method with a `V2` suffix or optional struct field), require the developer to adopt the additive pattern instead.

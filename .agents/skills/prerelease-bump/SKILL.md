---
name: prerelease-bump
description: Plan or execute dependency canaries in ephemeral worktrees, inspect failures, and apply reviewed dependency updates and supplied patches.
---

# Proactive Prerelease Bump Train (`prerelease-bump`)

Speculatively test dependency upgrades against upstream `alpha`, `beta`, `rc`, and `nightly` channels inside ephemeral git worktrees before official GA releases.

## 4-Step Bump Train Workflow

1. **Scan Upstream Dependency Candidates**:
   - Inspect supported manifests (`go.mod`, `package.json`) for candidate stable and prerelease versions:
     ```bash
     praetorctl bump scan --prerelease
     ```

2. **Run Ephemeral Worktree Canary Test**:
   - Test candidate version in complete isolation without dirtying working tree:
     ```bash
     praetorctl bump canary <package-name> --target=<prerelease-version>
     ```
   - Passing result = configured command exited zero; no certification issued. CLI currently uses `go test -v ./...`.
   - On failure, inspect private SARIF diagnostics under `.workingdir/evidence/canary/`: command output, not adaptation patches.

3. **Execute Automated Bump Train**:
   - Run speculative testing across all eligible dependencies:
     ```bash
     praetorctl bump train --dry-run
     praetorctl bump train
     ```
   - Dry run only plans work. Execution failures produce nonzero result.

4. **Apply and Verify Reviewed Update**:
   - Verify intended stable version and any supplied patch, then apply and run repository gates:
     ```bash
     praetorctl bump apply <package-name> --version=<stable-version> --patch=<reviewed-patch-file>
     make verify-all
     ```
   - Patch optional. Syntax checked before updating; applicability checked afterward. Failed application may leave dependency updated, does not roll back.

For status fields, diagnostic privacy, patch bounds, and migration from older false certification flags, read [canary evidence guide](../../../docs/guides/canary-evidence.md).

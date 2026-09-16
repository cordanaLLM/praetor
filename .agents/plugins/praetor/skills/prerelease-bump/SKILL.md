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
   - Test a candidate version in complete isolation without dirtying the working tree:
     ```bash
     praetorctl bump canary <package-name> --target=<prerelease-version>
     ```
   - A passing result means the configured command exited zero; no certification is issued. The CLI currently uses `go test -v ./...`.
   - On failure, inspect private SARIF diagnostics under `.workingdir/evidence/canary/`. These contain command output and are not adaptation patches.

3. **Execute Automated Bump Train**:
   - Run speculative testing across all eligible dependencies:
     ```bash
     praetorctl bump train --dry-run
     praetorctl bump train
     ```
   - A dry run only plans work. Execution failures produce a nonzero result.

4. **Apply and Verify a Reviewed Update**:
   - Verify the intended stable version and any supplied patch, then apply and run the repository gates:
     ```bash
     praetorctl bump apply <package-name> --version=<stable-version> --patch=<reviewed-patch-file>
     make verify-all
     ```
   - The patch is optional. Its syntax is checked before updating; applicability is checked afterward. A failed application may leave the dependency updated and does not roll back.

For status fields, diagnostic privacy, patch bounds, and migration from older false certification flags, read [the canary evidence guide](../../../docs/guides/canary-evidence.md).

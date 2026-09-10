---
name: prerelease-bump
description: Execute proactive prerelease bump trains, speculative ephemeral worktree tests, and adaptation patch synthesis to prevent upgrade fire-drills on stable release day.
---

# Proactive Prerelease Bump Train (`prerelease-bump`)

Speculatively test dependency upgrades against upstream `alpha`, `beta`, `rc`, and `nightly` channels inside ephemeral git worktrees before official GA releases.

## 4-Step Bump Train Workflow

1. **Scan Upstream Dependency Candidates**:
   - Inspect manifests (`go.mod`, `package.json`, `Cargo.toml`) for available stable and prerelease updates:
     ```bash
     praetorctl bump scan --prerelease
     ```

2. **Run Ephemeral Worktree Canary Test**:
   - Test a candidate version in complete isolation without dirtying the working tree:
     ```bash
     praetorctl bump canary <package-name> --target=<prerelease-version>
     ```
   - If tests pass cleanly, the candidate is **Canary Certified**.
   - If tests fail, SARIF distillation pinpoints the exact breaking symbol change and stages an adaptation patch in `.standards/patches/`.

3. **Execute Automated Bump Train**:
   - Run speculative testing across all eligible dependencies:
     ```bash
     praetorctl bump train --dry-run
     praetorctl bump train
     ```

4. **Instant Landing on Stable Release**:
   - When upstream releases GA/stable, apply the pre-tested bump and adaptation patch in minutes:
     ```bash
     praetorctl bump apply <package-name> --version=<stable-version> --patch=.standards/patches/<patch-file>
     make verify-all
     ```

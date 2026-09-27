# Praetor Architecture: C4 Models & System Lattice

This document specifies the architectural topology of the Praetor Governance Engine using C4 modeling and interactive figures.

---

## 1. Level 1: System Context Diagram

The System Context diagram details Praetor's boundaries, autonomous agent interactions, git providers, and downstream cluster infrastructure.

```figure
c4-system-context
```

Edges cross into infrastructure this repository does not ship. The ARC scale sets and the
ArgoCD Application are operator data: `deploy/arc/` and `deploy/k8s/` are owner-only paths
(`ownerOnlyPrefixes` in `internal/operationalsync/overlay.go`) that exist only in the operational
fork, and the engine's `.gitignore` ignores them (`TestOwnerOnlyPrefixesAreIgnoredByTheEngine` in
`internal/operationalsync/owner_only_test.go`). Praetor does not dispatch jobs: `praetorctl audit`
checks that the runner policy resolves (`auditRunnerMatrix` in `cmd/standardsctl/audit.go`), and
this repository's workflows run on GitHub-hosted runners.

---

## 2. Level 2: Container Diagram

The Container diagram illustrates the runtime modules within the Praetor binary, daemon services, and configuration inputs.

```figure
c4-containers
```

The HISS scanner returns a `ScanReport` to the gate and to `praetorctl audit`; it emits no SARIF.
SARIF enters through `lockdown.DistillSARIF`, which condenses a SARIF log (today the bump canary's
wrapped test failure, `internal/bump/canary.go`) into a bounded summary and writes the full report
under `.workingdir/evidence/`. The gate reads `.standards.yaml` twice: the prefetch stage requires it
and `.standards.lock` to exist, and the HISS stage scans with the function-length limit its policy
resolves to. `.github/workflows/release-binaries.yml` builds the image from the released
`praetorctl` binaries, pushes it and the Helm chart to GHCR and signs both; a published chart pulls
the image tagged with its `appVersion` (ADR-0013).

---

## 3. Level 3: Component Diagram (Gating Engine)

The Component diagram details the internal workflow of the Anti-Direct-Merge Gating Pipeline (`internal/gating/pipeline.go`).
Its tabs play four runs: a clean run that signs the receipt, a HISS violation rejected at stage 2, a dry run,
and a missing signing key that fails stage 6 closed.

```figure
gating-pipeline
```

Stage order and names come from `executeStages` in `internal/gating/pipeline.go`. Each stage records
one `StageStatus`: `passed`, `failed`, `skipped` or `not_applicable`. Only `failed` stops the
pipeline and leads to the rejected disposition; a stage that ran nothing is never recorded as
`passed`, in the CLI report, in `--json`, or in the signed `praetor-gate-output/v2` stage output.

A dry run (`gate run --dry-run`) changes nothing and reaches no network. It verifies the lockfiles
and runs stages 2 and 4. In a Go repository it records the module prefetch in stage 1, stage 3,
stage 5 and the receipt as `skipped`; without a `go.mod`, stages 1 and 3 are `not_applicable`
instead. It mints no receipt (`TestExecuteStages_DryRunInvokesNoCommand`). `gate verify`,
`forge validate-pr` and `paperclip verify` refuse a receipt whose gate output is not
`praetor-gate-output/v2` (`lockdown.VerifyPinnedReceiptFile`, `lockdown.VerifyUnpinnedReceiptFile`). The [adoption verification guide](../guides/adoption-verification.md#stages-that-do-not-apply-are-skipped-not-failed)
covers each verdict. ADR-0012 (decision 3) records the pipeline; ADR-0004's four-stage description
is superseded.

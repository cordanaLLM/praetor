# Adoption and onboarding

These guides cover bringing an existing or new repository under Praetor governance:
scaffolding the manifest and agent harness, choosing archetypes and facets, and checking
what adoption declared.

Read [Fast adoption](../../adoption.md) first for the one-step path, then
[Repository onboarding](../onboarding.md) for the staged workflow behind it.

- [Fast adoption](../../adoption.md): one-step adoption through `praetorctl adopt`, the
  `standards_adopt` MCP tool or the GitHub Action, and what each scaffolds.
- [Repository onboarding](../onboarding.md): flavor detection, the manifest, the agent
  harness, the technical-debt baseline and verification, step by step.
- [Adoption verification commands](../adoption-verification.md): what the adoption report's
  `verification` object declares, and why adoption never runs a project's own scripts.
- [Archetype and facet authoring](../archetype-authoring.md): defining profiles and facets,
  and how the lattice joins them.
- [Go API compatibility gate](../api-compatibility.md): the hosted check the
  `api:public-contract` facet adds where git tracks a `go.mod`, which compares every Go module's
  exported API with a base.
- [clang-tidy coverage gate](../clang-tidy-coverage.md): the audit check the `clang-tidy` linter
  of `native-gpu-systems` enables, which names every tracked C/C++ translation unit no declared
  clang-tidy lane reads, and the top-level `exceptions` list that excuses one.
- [Supply-chain gate](../releasing.md#how-the-audit-measures-the-slsa-level): how the audit
  measures the SLSA level, cosign signing and SBOM generation the release workflows reach, and
  the HISS-11 `exceptions` entry adoption records when the declared supply chain exceeds them.
- [Generated artefacts](../generated-artefacts.md): declaring the files a repository renders
  from other files, why a pull request leaves them alone, and the one regeneration change per
  batch that renders them.
- [Effective audit policy](../effective-policy.md): how the CLI, MCP and adoption loops
  resolve complexity limits from one implementation.
- [Independent review and single-maintainer operation](../review-policy.md): the
  branch-protection `review_mode` and reviewer minimums.
- [Live Actions checks](../actions-live-checks.md): how the audit compares the declared Actions
  workflow permissions with the forge and reports each workflow's recent runs.
- [Planning-artifact onboarding](../planning-onboarding.md): the `planning-artifacts`
  archetype for research, concept and preparation repositories.
- [Framework capability evidence](../needs-capability-evidence.md): what
  `praetorctl needs report` measures, which is dependency mapping availability, not
  migration readiness.

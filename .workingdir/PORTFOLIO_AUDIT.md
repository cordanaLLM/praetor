# Portfolio dogfood: gaps and corrective acceptance

This audit covers the path from repository discovery through generated
verification commands, capability reports, recorded evidence and staged
execution. It is not a claim that every portfolio application was tested.
Live remote identity, pinned source reads, actual Praetor CLI/MCP calls and
negative fixtures support the findings below. Original checkouts and unpublished
work remain intact. Private retained evidence: `repo-interconnect-20260912/`.

| Finding | Verified failure | Correction / acceptance |
| --- | --- | --- |
| BUG-717: capability readiness | A pgx consumer reports 100% with both the built-in catalog and an explicitly empty framework. | Reconcile every consumer against its selected index; distinguish catalog declarations, observed source and executed verification. A directory name cannot prove a capability. Isolated fix under review. |
| BUG-718: verification generation | C#, Python and Node receive Go commands; Cargo/CMake receive Meson commands. | One bounded build-system verification plan must feed both agent instructions and Makefile generation. Explicit unavailable results for absent/ambiguous commands. Isolated fix in progress. |
| BUG-005 / BUG-042: legacy no-op gate | Existing `verify-all` which only echoes is accepted by target-name presence. | Recognize and repair the exact old Praetor stub; preserve arbitrary user recipes as unverified until executed. Covered by the verification-plan correction. |
| BUG-719: missing analysis scope | A C# source yields zero violations without indicating it was unscanned. | Fixed in `4168620`: bounded file and extension evidence, explicit historical unknown, shared CLI/MCP scope. Full gate passed; no C# analyzer added. |
| Imago backend success | Non-dry-run dispatch returns fabricated IDs/URLs without invoking any backend. | Every unimplemented backend must return a typed failure through CLI and direct/staged MCP. Dry-run is only a plan. Separate Imago correction in progress. |
| Enrollment gap | Public dogfood accepts eight hardcoded repository URLs; the new portfolio cannot yet be selected. | Add an explicit bounded enrollment contract with immutable pins and retained policy provenance. Keep private settings separate from generic engine data. Not implemented. |
| Application-stage gap | Existing public loops verify governance adoption and repeat stability, not native applications. | Reuse each project's real tests with pinned toolchains and failure controls, and retain exact tested scope. A governance pass cannot promote an application-execution stage. Not implemented. |
| Interconnect gap | Notebook sources, requirements, state tasks, forge references and run evidence lack shared stable typed links and lifecycle admission. | Add provenance links and staged orchestration through shared services; test the portfolio before promoting private operational configuration. Not implemented. |

## Portfolio bindings

- `20-watts-was-enough` has the approved destination `cordanaLLM/ingenium`.
  The existing repository ID is `1323961722`. Preserve its unpublished commit,
  staged CLRS runner, PR heads and published artifact digests during migration.
- `imago` and `nucleus` are distinct image-assembly and kernel-artifact jobs.
  Their local renamed checkouts each contain one unpublished migration commit
  atop the old live repositories; the new remote names were absent at inventory.
  This is incomplete migration, not duplicate engines to delete.
- Jellysin's initial application pin is
  `jellysin/plugin-lastfm@37f446e7d7def05891ef2f4878f9512be3668ec7`.
  It pins `jellysin/release-helper@41f9ccffa1093d7dda8c3f7f634fad24d08cdc7a`.
  Legacy local Last.fm/release-helper adoption branches are different projects
  and must not substitute for these exact inputs.

The machine-readable migration plan and source bindings are retained privately.
No transfer, release, production plugin installation or bot-stage activation is
established by this inventory. The owner Praetor synchronization contract still
permits four identity overlays; portfolio enrollment/stage data needs an explicit
supported configuration contract.

## Required staged evidence

1. **Discovery:** canonical repository ID, immutable source/dependency pins,
   selected policy/catalog and original tree identity. Preview must leave the
   original unchanged; old aliases must resolve to the same repository lineage.
2. **Governance:** actual adoption plus repeat-apply, policy identity and exact
   original baseline. Preserve unsupported languages, failed cases and truncation.
3. **Native verification:** repository-owned build/lint/test commands with their
   pinned toolchains. Injected compiler/test failures and missing executors must
   produce nonzero/error outcomes through every adapter.
4. **Integration:** synthetic isolated host/backend, actual acknowledgement and
   artifact readback, cancellation/failure/recovery and replay boundaries. Link
   every result to its exact source, configuration and checker version.
5. **Promotion:** enforce explicitly selected repository-stage requirements.
   Planning, observed source, governance verification and application execution
   are different evidence states. Private operational settings follow verified
   portfolio runs; no generated percentage advances a stage by itself.

Jellysin provides concrete native checks to reuse: frontend-before-.NET builds,
Roslyn/Sonar policy self-tests, xUnit/Python/browser suites and synthetic Jellyfin
restart/isolation/load checks. Its pinned SDK is .NET 10.0.400; the inspected
workstation only had 9.0.120. Provision an isolated pinned environment before
claiming a native replay. Historical hosted green checks are not a fresh local run.

## Evidence and limits

- `repository-inventory.md`, `repository-migration-plan.json`: three repository
  lineages, unpublished-work preservation, Pages/registry and dispatch impact.
- `praetor-map.md`: existing module seams and real empty-framework MCP reproduction.
- `jellysin-map.md`, `jellysin-source-bindings.json`: exact current application
  contracts, legacy local state and native test/toolchain differences.
- `hiss-coverage-red.log`, `hiss-coverage-verify-all.log`,
  `hiss-coverage-live-mcp.json`: red fixture, full passing gate and fresh consumer.
- `public-policy-fix-20260912/`: the prior policy/baseline correction, pinned
  Cobra/Flask replay and successful repair import. Those inputs remain useful
  regression controls while portfolio application stages are added.

Do not bulk-close the historical bug ledger from this audit. Resolve a finding
only when its specific failure is covered by retained, passing regression evidence.

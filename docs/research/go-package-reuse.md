# Go package reuse and maintenance audit

## Decision context

Praetor should concentrate custom code on repository policy, provenance, staged authorization and evaluation contracts. Mature implementations of wire protocols, provider APIs and language tooling are stronger reuse candidates than another generic application framework. Existing Go standard-library improvements deserve the first comparison because this checkout already targets Go 1.27 and they do not enlarge the module graph.

The current runtime module contains only gopkg.in/yaml.v3 v3.0.1. ADR-0009 retains explicit constructor dependencies and a single root module; ADR-0008 permits an isolated OpenAPI tooling module. See [go.mod](https://github.com/CordanaLLM/praetor/blob/main/go.mod), [ADR-0008](../adr/0008-spec-driven-provider-integration.md) and [ADR-0009](../adr/0009-structural-unification.md). Those constraints are current architecture decisions, not proof that a custom implementation is cheaper. A targeted dependency-policy amendment should be considered when an actual differential prototype shows that a maintained package removes more risk and maintenance than it introduces. No dependency change follows merely from a shortlist.

The gap audit provides concrete workloads: loss-prone Markdown state; duplicated CLI/LSP HISS analysis; custom MCP transport; missing forge providers and webhook lifecycle; provider-specific model execution; hand-written JSON decoding; native Git worktrees and patch acceptance; generated editor files; and bounded repair scheduling. The source-bound capability audit is retained in the private local ledger. Source facts refer to checkpoint ebd5184, and package metadata was checked on 2026-09-12. Candidate versions are review pins, not evergreen recommendations.

## Recommended sequence

| Priority | Workload | Direction | Why this order |
| --- | --- | --- | --- |
| 1 | State preservation and strict artifacts | Repair lossless state writes; evaluate Go 1.27 json/v2 for the shared decoder | Reproduced data loss and ambiguous evidence undermine every automated loop. This slice can stay within the existing dependency policy. |
| 2 | MCP protocol maintenance | Compare the official Go SDK behind Praetor's existing tool/policy functions | Protocol framing, capabilities and transport evolution are expensive custom responsibilities; a thin adapter can preserve domain logic. |
| 3 | Shared source analysis | Unify in-memory analysis and policy, using x/tools only for capabilities requiring typed Go analysis | Four real parity failures show that duplicated engines are already diverging. A generic analyzer framework cannot decide HISS semantics by itself. |
| 4 | Forge coverage | Isolated SDK/spec-generator differential comparison, then one provider adapter | Provider implementations and caller wiring are incomplete, and the generic runtime proposal also creates maintenance. Compare exact required operations before building either broadly. |
| 5 | Provider model execution | One official SDK adapter under existing execution limits | Reuse can reduce wire-format drift, but default retries, response fields and error handling must be made explicit. Evaluation/promotion remains Praetor-owned. |
| 6 | Configuration maintenance | Evaluate maintained YAML paths and schema checks; retain explicit policy resolution | Parsing upgrades do not resolve precedence, strictness joins, migration or preservation of user values. |
| 7 | Persistence and distributed work | Keep bounded files/systemd now; evaluate a transactional store or shared queue only for a measured multi-process need | A database or workflow engine would introduce migration and operational work before current state and status semantics are trustworthy. |
| 8 | Git and filesystem | Retain native Git and expand existing os.Root-based boundaries | The candidate Git library lacks required commands; standard-library file confinement already fits this toolchain. |

Each row is a proposed engineering decision with the supporting package evidence below. None represents an installed package or a completed migration. In particular, a protocol SDK comparison requires a concrete dependency-policy proposal if it is selected for the shipped root module.

## Evaluation criteria

A recommendation must identify the existing code it replaces, preserve externally visible contracts and error behavior, and name the acceptance cases that justify deletion. The priority order is correctness and evidence preservation, maintenance ownership, protocol/API coverage, operational burden, and then measured performance. Stars, ecosystem familiarity and total download counts are not substitutes for these criteria.

Evidence distinguishes declared module requirements from selected transitive modules, linked packages, binary size and reachable vulnerabilities. No build-size or latency claim is made without a representative import graph and workload. License names below are an inventory; redistribution notices and transitive-license review belong to the actual pinned dependency proposal.

Three outcomes are useful: adopt a standard-library improvement within existing constraints; prototype a bounded library adapter in isolated tooling; or defer a dependency because the contract is missing or its operational burden exceeds the current problem. Libraries cannot supply Praetor's definitions of verified, authorized, owned, fresh or complete. Those domain decisions must remain shared and tested regardless of transport or storage choice.

## Migration discipline

1. Fix ledger preservation and false-success inputs before collecting optimization measurements; corrupted evidence cannot rank alternatives reliably.
2. Freeze representative positive, negative and boundary fixtures from the current adapters, including the failing cases retained by the gap audit. Preserve the desired behavior, not tests that currently certify simulated success.
3. Introduce an explicit service boundary and inject the implementation. A library must sit behind the same policy and provenance contracts used by CLI, MCP, IDE and worker consumers.
4. Run both implementations against identical fixtures in an isolated module. Record exact versions, checksums, Go toolchain, module graph, artifact size, cancellations, bounded I/O and failures. Use native transport emulators before any owner-repository write.
5. Migrate one vertical slice, remove the replaced implementation and update all consumers together. If the old implementation remains necessary for ordinary requests, count the result as additional complexity until that duplication is resolved.
6. Change architecture/dependency policy only for the concrete selected import graph. Revalidate source identity, gates and one real installed consumer before advertising availability. No automatic fleet expansion follows from unit-test success.

## State, document formats, configuration and Go analysis

### Verified versions and measured import cost

**Count definition:** `go list -mod=mod -deps -json <explicit packages>` ran under Go 1.27.1, Linux/amd64, `GOTOOLCHAIN=local`, `GOWORK=off`, `CGO_ENABLED=1`, in separate temporary modules pinned to one candidate version. “Extra modules” means unique non-stdlib `Package.Module.Path` values in that selected **package import closure**, excluding the candidate's own module. It is not `go list -m all`, not the full go.mod graph, not a deployed binary measurement, and not a vulnerability count. “Packages” includes the candidate's own non-stdlib packages. Package files were resolved, not linked into Praetor. No inference about binary size, latency, build time, OS coverage or transitive security follows from these counts.

| Candidate | Verified latest stable/tag time | Go minimum | Project license | Extra modules / non-stdlib packages in selected import closure |
|---|---|---:|---|---|
| goldmark | v1.8.6[^1], 2026-09-03 | 1.22 | MIT | **0 / 9**, root + extension |
| jsonschema/v6 | v6.0.3[^2], 2026-06-28 | 1.21 | Apache-2.0 | **1 / 14**, root; extra x/text |
| maintained yaml/v3 | v3.0.5[^3], 2026-07-26 | 1.16 | MIT and Apache-2.0 by file; not simply Apache | **0 / 1**, root |
| goccy/go-yaml | v1.19.2[^4], 2026-01-08 | 1.21 | MIT | **0 / 9**, root |
| bbolt | v1.5.0[^5], 2026-06-03 | 1.25; module toolchain 1.25.11 | MIT | **1 / 5**, root; extra x/sys |
| modernc SQLite | v1.58.0[^6], 2026-09-01 | 1.25 | BSD-3-Clause | **7 / 12**, root |
| mattn SQLite alternative | v1.14.52[^7], 2026-09-05 | 1.21 | MIT | **0 / 1**, root; C compiler/CGO cost is outside Go module count |
| x/tools | v0.50.0[^8], 2026-09-08 | 1.26 | BSD-3-Clause | **2 / 34**, analysis + packages + analysistest; extra x/mod and x/sync |
| go-cmp | v0.7.0[^9], 2025-01-14 | 1.21 | BSD-3-Clause | **0 / 5**, cmp; test-only proposed |
| rapid | v1.3.0[^10], 2026-03-30 | 1.23 | MPL-2.0 | **0 / 1**, root; test-only proposed |

The measured modernc extras are `go-humanize`, `google/uuid`, `bigfft`, `x/sys`, `modernc/libc`, `modernc/mathutil`, `modernc/memory`. bbolt's go.mod also lists CLI/testing/tool dependencies which were absent from the root-library import closure; claiming all of Cobra/testify/etc are runtime dependencies would overstate this measurement. Conversely zero external Go modules does not imply tiny code or a negligible dependency burden.

### 1. Standard library first: json/v2, jsontext, typed state and native fuzzing

The Go 1.27 release notes[^11] introduce json/v2 and jsontext as available packages, with stricter UTF-8/duplicate defaults. The package's security discussion[^12] explains exact-case matching and unknown-field policy. The installed `go doc` and executed program agree; the source's experiment build tag is default-on in this release, and the smoke explicitly emptied GOEXPERIMENT.

**Actual smoke, exit 0:** using `json.Unmarshal(raw, &value, json.RejectUnknownMembers(true))`, exact duplicate keys, cost_usd/COST_USD aliases, uppercase-only keys, unknown members, trailing documents, invalid UTF-8 and a lone surrogate all reject. Explicit numeric zero succeeds. Missing cost, null cost and null root still decode syntactically; required pointer/root validation rejects them. A plain numeric field collapses absent/null/zero to 0. Therefore a new parser alone cannot establish observation completeness. The comparison v1 Decoder accepted case aliases, invalid UTF-8 and lone surrogate; its first decode also accepted trailing content, which the current Praetor wrapper separately checks.

**Keep:** byte limit, depth32 limit, cancellation, source identity, pointers or presence-aware wire structs, bounded document counts, domain validation, and atomic persistence contract. jsontext's Decoder.StackDepth[^13] can replace the custom syntax stack in a bounded token preflight. Do not drop depth32 just because the standard parser accepts deeper data. Do not canonicalize/re-marshal source bytes before checking notebook bundle hashes; v1/v2 encode nil containers and other details differently.

**Prototype:** replace only the pure decoder in a temporary copy, then run all existing Prepare/Select positive/negative/boundary tests plus case-alias, null-root and nested custom-UnmarshalJSON cases. Existing Experiment/Observation custom unmarshallers must use the new common decoder internally; leaving a nested v1 call restores permissive behavior. Delete the old token/key-stack implementation only after parity tests pass.

For state, keep a versioned typed canonical record and deterministic Markdown projection as a candidate design, or preserve exact Markdown source spans if human-authored Markdown remains canonical. Bounded read/validate/compare-old-hash/atomic-replace remains app logic. A one-file rename is not a two-file transaction: moving OPEN tasks to BACKLOG needs a recoverable transaction or one canonical file with generated views. First repair the current parser without forcing a format migration.

### 2. goldmark: strong Markdown parser, not a lossless ledger database

Goldmark v1 has a CommonMark-oriented AST, source positions, and built-in GFM table/task extensions. Its pinned README[^14] and go.mod[^15] support the fit and zero-extra-module measurement. Repository default branch is now v2; use v1.8.6 documentation/source for the prototype, not whatever the default branch later exposes.

**Good fit:** interpret source tables/headings and distinguish code fences, escaped pipes and ordinary text. Use read-only import/validation and raw byte spans. **Not sufficient:** an unescaped pipe is a real table delimiter in GFM, so a full Markdown parser cannot infer the intended title from arbitrary damaged rows or recover the four records already absent. Rendering an AST also does not promise byte-for-byte preservation of human prose, layout and comments.

**Prototype:** run 716 retained finding titles through explicit escaping and inverse parsing; require stable IDs, unchanged untouched spans, refusal on malformed row shapes, multiline/unicode/code-span cases. Compare against a small lossless line-span parser before accepting dependency cost. Prefer goldmark when more than ledger tables need Markdown semantics; do not add it just to split one constrained format.

### 3. santhosh-tekuri/jsonschema/v6: schemas shared by CLI, MCP, bot and IDE

The v6.0.3 source[^16] supports JSON Schema dialects through 2020-12. The compiler API[^17] supplies resource registration and format configuration. Pin an explicit `$schema` / draft; defaults can change. Format assertion is not automatic for modern drafts. It does not replace strict raw JSON decoding; validation after permissive decoding cannot discover keys already overwritten.

**Good fit:** one artifact/config schema consumed by all transports, required metrics, enums, item limits, and versioned contracts. It does not establish source truth, auth scopes, monotonic policy joins, cross-file atomicity or cited semantic correctness. Keep those explicit Go checks.

**Important default:** v6.0.3 `roots.go:21-23` installs a FileLoader; unknown refs can read local files. Supply a deny-by-default resolver and only embedded/pinned resources. Network loaders are opt-in examples, not a reason to assume default HTTP fetch. Check compile/validation CPU bounds for nested/combinatorial schemas before using untrusted schemas.

**Prototype:** one canonical schema for notebook bundles and prompt experiment wire data, with offline resource tests, rejected external file/HTTP refs, missing/null fields and version mismatches. Measure whether it removes more hand-written contract code than it adds. Keep schema tooling in an isolated module until dependency policy changes; avoid executing a heavyweight schema subprocess on every local parse by accident.

### 4. YAML organization's maintained v3: least disruptive replacement candidate

The pinned v3.0.5 README[^18] identifies the YAML organization maintained continuation and stable v3 API. KnownFields[^19] exists in v3; inspected decoder source enables duplicate-key checking. Its go.mod[^20] retracts v3.0.0-v3.0.1 for the new module path: do not mechanically rename the old path while keeping version3.0.1.

**Immediate repair without a replacement:** centralize the existing yaml.v3 decoder, enable known struct fields, validate one document, reject invalid/absent governance values, enforce bounds and preserve node metadata when editing. Unknown-field strictness only helps typed fields; unmarshalling into broad maps still needs key validation.

**Prototype:** replay every manifest, archetype, facet, owner overlay and generated lock through current v3.0.1 versus maintained v3.0.5; compare accepted values, rejected duplicates, tags, merges, anchors, quoting and output hashes. Choose this minimal migration if actual behavior is compatible. The source LICENSE combines MIT and Apache coverage; GitHub's single license label is incomplete.

### 5. goccy/go-yaml: use when source-preserving YAML edits justify another parser

The pinned README[^21] exposes token/parser APIs, YAML Path edits and comment/anchor-aware transformations. It is independently implemented; no dependency graph beyond its own module was observed. Its README's historical claims about the old go-yaml project should not be applied to the maintained YAML organization fork.

**Good fit:** owner overlays and generated config edits that require understandable diagnostics and preserved comments/anchors. **Risk:** parser/type/tag/merge differences are a migration problem; a larger API cannot fix inert config consumers. Do not carry both YAML engines indefinitely or claim exact formatting preservation without differential tests.

**Prototype:** the same corpus as maintained v3 plus golden source-preservation checks and failing edits that must leave original bytes unchanged. Prefer maintained v3 if decoding alone suffices; prefer goccy only after a demonstrated edit/diagnostic benefit. Do not use filesystem anchor resolution on untrusted input without explicit confinement and bounds.

### 6. bbolt: a compact transactional local store, conditional on ownership

The v1.5.0 README[^22] documents atomic transactions, many readers/one writer and file locking with an open timeout. It offers a practical single-file key/value store; `go list` found only x/sys outside bbolt for the library root. CLI/tool/test dependencies in its module file do not all enter that library closure.

**Good fit:** one workstation daemon owns durable task IDs, receipt/claim indexes and repair dedupe records. A transaction can combine task transition and archive projection metadata. **Mismatch:** many independent CLI processes holding the same DB open, cross-workstation sync, relational reports or Git-readable canonical state. Those require ownership/locking and import/export design, not merely a DB field in a constructor.

**Prototype:** behind a minimal state-store interface, compare file-store and bbolt behavior for cancel, concurrent readers/writers, crash before/after commit, replay idempotency, corrupt DB and export/rebuild. Explicit open timeout and capacity bounds remain necessary. No persistence migration until recovery and portability are demonstrated.

### 7. SQLite: choose only if relational state and multi-process access are needed

modernc SQLite v1.58.0[^6] is CGo-free and has database/sql integration. It adds seven runtime-import modules in the measured Linux closure, including generated C-runtime support; pure Go is not zero complexity. Its documentation calls out libc dependency alignment. mattn v1.14.52[^23] requires CGO and GCC: zero extra Go modules comes with a real C toolchain and cross-compilation cost. Neither driver was executed against a DB in this research.

**Good fit:** transactional multi-entity state, joins for failures/tasks/evidence, constraints on unique IDs, and many short-lived local clients. **Costs:** schema migrations, lock contention/busy policy, transaction retries, recovery, WAL/backup behavior and platform packaging. A local SQLite file is not a safe multi-workstation synchronization protocol or a Git merge strategy.

**Prototype:** choose one driver according to current release CGO constraints; exercise two processes racing the same task, a killed writer, rollback and clean export/rebuild. Compare request latency, file growth, build artifact size and packaging on every supported OS against the current store. Do not add an ORM or a second database abstraction layer before this proves value.

### 8. golang.org/x/tools: shared analysis adapters, not a ready-made HISS engine

The official analysis API[^24] separates checks from drivers and carries typed diagnostics/facts. packages[^25] handles package loading and analysistest[^26] supports fixture expectations. Pin v0.50.0; it requires Go1.26 and follows Go tooling's v0 release stream. The combined requested import closure measured x/mod and x/sync plus x/tools; narrower imports can be smaller.

**Good fit:** identical rule IDs/spans in CLI, LSP and CI, semantic call identity through types, reusable analysis test fixtures. **Not supplied:** Praetor's HISS policy definitions, dynamic-call completeness, budgets, timeout semantics or recursion acceptance policy. Start by sharing the existing stdlib go/ast + go/types engine; an adapter can follow.

**Prototype:** one existing HISS rule receives source buffers and resolved limits, then exposes analysis and LSP adapters. Assert equivalent diagnostics for disk/buffer source and below/at/above thresholds. Preserve partial syntax and timeout distinctions. packages.Load can invoke the Go command and consult module/workspace state; load under explicit context/environment/network policy when scanning non-owned repos. Do not run or trust imported application code to produce an audit.

### 9. go-cmp: clearer contract failures, test-only

cmp's own source[^27] says it is intended for tests and can panic; it recursively compares data and permits custom semantics. This makes it a useful diagnostic tool for CLI/LSP parity, canonical projections and configuration joins, not production policy equivalence. The project had a June2026 push despite its last stable release being January2025; age alone is not proof of abandonment.

**Prototype:** replace one opaque reflect.DeepEqual assertion with a meaningful diff. Do not ignore unexported state broadly, equate nil/empty indiscriminately, or use float tolerances where an exact cost/policy contract is required. Straight field/byte assertions remain sufficient for the immediate repair. Adding it to root tests still changes root go.mod, even if no production binary imports it.

### 10. rapid plus native fuzzing: test invariants the loops currently miss

rapid v1.3.0[^28] generates structured inputs, shrinks failures, persists minimized cases and supports state-machine testing. It lacks native coverage feedback but provides integration with Go fuzz targets. Its root library has no extra modules; its license is MPL-2.0, which differs from the otherwise mostly permissive shortlist.

**Good fit:** random sequences of add/resolve/archive/replay/cancel with invariants: stable unique IDs, no silent record loss, original bytes unchanged on failure, idempotent replay, and governance joins commutative/associative/idempotent/monotone. Native Go fuzzing[^29] is already available and should start with the actual F392 pipe case, null/case-alias JSON inputs and malformed state fixtures.

**Prototype:** retain those regressions in testdata/fuzz; run bounded normal seed checks on every relevant change and a time-limited fuzz job periodically. Use rapid only if explicit state-machine generators materially improve coverage. “No panic” is not an adequate property. Never equate a green finite fuzz budget with exhaustive correctness.

### Rejected overengineering and sequence

- A DI framework would not resolve ownership; inject narrow interfaces/values through constructors per ADR-0009. Do not construct a generic service locator or a package for every conceptual noun.
- A universal config merger would obscure the required strictness join. Keep operational overrides and governance min/max/OR/union rules separate, with source provenance and absent-versus-invalid semantics.
- An ORM, distributed database, general Markdown editor or alternate high-speed JSON implementation is unjustified by the current small-state failures. No benchmark yet shows serialization speed as the bottleneck.
- Do not replace functioning stdlib Go parsing with tree-sitter just to advertise multi-language analysis, or interpret package imports as proof a rule executes.

Recommended order: (1) lossless state fix and read-error propagation; (2) stdlib strict JSON proof and existing contract regressions; (3) canonical versioned schemas/config resolver; (4) source-preserving Markdown/YAML prototypes where necessary; (5) shared analysis/driver parity; (6) transaction-store choice only after multi-process/crash tests establish the need. Each package adoption must identify code removed, surviving domain checks, import/OS/license impact, consumer integration tests and rollback. This research measures metadata/import closures and one stdlib behavior seam; it does not claim the larger package migrations have been implemented or benchmarked.

## MCP, LSP and forge protocols

### Local seams and architecture constraints

Verified current source:

| Seam | Exact local evidence | Consequence |
|---|---|---|
| Root dependency policy | `go.mod:5`; `docs/adr/0008-spec-driven-provider-integration.md:34-42`; `docs/adr/0009-structural-unification.md:44-48` | Root currently has only YAML. ADR-0008 is labeled Proposed but includes ratified owner decisions; ADR-0009 accepts one root module, explicit constructors and the isolated parser constraint. A runtime SDK must be an explicit policy change, not hidden behind a subprocess to evade the decision. |
| Custom MCP | `cmd/standards-mcp/server.go:649` returns literal `2024-11-05`; `cmd/standards-mcp/transport.go` is approximately 650 lines; `internal/mcp/tool.go` and `bridge.go` own tool and remote bridge contracts | SDK can replace framing, negotiation and transport state; tool validation, authority, root confinement and Praetor handler behavior remain local. Existing tests that assert the literal version are insufficient compatibility tests. |
| Custom forge transport | `internal/forge/github.go` approximately 749 lines; `internal/milestone/forge.go` approximately 329 lines | GitHub HTTP is real, but request/error/pagination behavior is duplicated. Preserve existing HTTP tests as an oracle before replacing either implementation. |
| Provider wiring | `internal/forge/forge.go:64`; direct GitHub constructors at `cmd/standardsctl/sync.go:119`, `issue.go:169,205`; explicit unsupported errors at `internal/forge/gitea.go:54`, `gitlab.go:52` | A common factory exists but CLI callers bypass it. Libraries cannot fix provider selection or credentials automatically. Gitea/GitLab must continue failing explicitly until operations are implemented and verified. |
| LSP | `cmd/standards-lsp/server.go` approximately 974 lines mixes protocol and rule analysis | Extract the shared analysis service before changing protocol serialization. Earlier retained fixture audit found core/LSP differences for recursion, conditional loops, goto and function length. Changing LSP types does not resolve those differences. |

ADR-0008's opening historical claims about successful fake-token/Gitea behavior are not current runtime evidence: current GitHub methods perform HTTP, and unsupported provider methods return errors. Its `libopenapi` rationale also needs precision: upstream still exposes a Swagger v2 model, but explicitly says it is unmaintained and will be removed. The concern is supported format longevity, not that v2 has never been readable. [ADR-0008 local source](../adr/0008-spec-driven-provider-integration.md), maintainer Swagger warning[^30].

### Candidate inventory

Versions below were resolved on 2026-09-12 from maintainer releases or Go module distribution metadata, then inspected at that exact version. Dates mean release publication or module version time, not proof of production stability. GitHub repository-metadata refreshes returned unauthenticated API 403 rate-limit errors; archive status and latest commit activity are therefore not claimed verified. Release and pinned-source retrieval succeeded independently. Direct/indirect counts are declared `require` entries in the pinned module's `go.mod`; they include test/tool dependencies. **They are not a resolved transitive production graph, SBOM, binary-size estimate, or count of imported runtime packages.** In particular, LSP's large indirect declaration count is substantially tooling-related.

| Candidate and exact module pin | Release/module date | Required Go | Declared direct / indirect | License inspected | Fit judgment |
|---|---:|---:|---:|---|---|
| `github.com/modelcontextprotocol/go-sdk v1.7.0` | 2026-07-28 | 1.25.0 | 8 / 3 | Transition from MIT to Apache-2.0; docs CC BY 4.0 | Preferred MCP migration candidate; root policy amendment needed |
| `github.com/mark3labs/mcp-go v1.0.0` | 2026-09-02 | 1.25.5 | 7 / 6 | MIT | Credible alternate, protocol-version coverage differs |
| `github.com/google/go-github/v91 v91.0.0` | 2026-09-03 | 1.26.0 | 2 / 0 | BSD-3-Clause | Strongest scoped GitHub typed client/oracle |
| `github.com/getkin/kin-openapi v0.149.0` | 2026-08-28 | 1.25 | 6 / 7 | MIT | Approved offline parser, not a runtime SDK |
| `github.com/ogen-go/ogen v1.24.0` | 2026-08-07 | 1.25.0 | 25 / 14 | Apache-2.0 | Typed generation challenger; measure generated runtime |
| `github.com/oapi-codegen/oapi-codegen/v2 v2.8.0` | 2026-07-17 | 1.25.0 | 8 / 11 | Apache-2.0 | Strong scoped-codegen fallback; explicit ADR escape hatch |
| `code.gitea.io/sdk/gitea v0.25.1` | 2026-05-12 | 1.26 | 6 / 4 | MIT | Real provider SDK; needs strict client-policy wrapper |
| `code.forgejo.org/f3/gof3/v3 v3.11.52`, package `forges/forgejo/sdk` | 2026-07-17 | 1.25.0; toolchain 1.26.5 | 10 / 10 | MIT at module root | Actual available Forgejo SDK seam, broad containing module |
| `gitlab.com/gitlab-org/api/client-go/v3 v3.6.0` | 2026-09-11 | 1.26.0 | 7 / 3 | Apache-2.0 | Maintained GitLab client; qualify retry and endpoint coverage |
| `go.lsp.dev/protocol v1.0.1` | 2026-06-27 | 1.26 | 4 / 221 | BSD-3-Clause | Generated protocol reuse after analysis-core extraction |

Pin, module and license sources appear with each candidate below. No dependency's license label substitutes for a review of the actual linked module graph and license notices.

### MCP: official SDK versus mcp-go

**Official Go SDK v1.7.0.** The maintainer documents client/server support and compatibility with MCP `2026-07-28`, `2025-11-25`, `2025-06-18`, `2025-03-26`, and `2024-11-05`. The July 2026 revision changes discovery, request metadata and stream/subscription behavior. Its HTTP support for the new revision requires stateless mode; legacy negotiation remains available. Praetor should first preserve existing client interoperability, then intentionally enable newer behavior. Tagged release[^31], tagged README[^32].

The SDK provides protocol, JSON-RPC, stdio and HTTP machinery, context-aware run APIs and client/server transport configuration. That is a strong replacement boundary for custom framing and negotiation. It does not establish Praetor's repository allowlist, tool authorization, strict tool-argument semantics, request/body budgets, promotion evidence or process lifecycle. Verify disconnect-to-handler cancellation, injected HTTP transport, retry behavior and limits against the pinned implementation rather than assuming a `context.Context` parameter solves all cancellation cases. Transport implementation[^33], package API[^34].

The pinned license file describes a transition: new contributions are Apache-2.0, while unconsented older contributions remain MIT; documentation is CC BY 4.0. Calling the release simply MIT would be inaccurate. Its declared module requirements include JSON schema, OAuth, JWT, encoding and URI-template support, plus test/tool dependencies. Pinned module[^35], pinned license[^36].

**mcp-go v1.0.0.** Its pinned documentation advertises MCP `2025-11-25`, with older-version compatibility, and stdio, SSE and Streamable HTTP transports. Do not infer support for the July 2026 revision solely because its release date is newer. It supplies useful OAuth protected-resource metadata and localhost/DNS-rebinding controls; CORS is configurable. Task concurrency defaults to unlimited unless a maximum is configured, so resource policy still belongs to the application. Release[^37], pinned behavior/options[^38], module[^39], MIT license[^40].

**Judgment:** choose one runtime SDK after a compatibility matrix, not both. Prefer the official SDK for current protocol coverage and maintainer alignment. Use mcp-go as an independent client in the isolated conformance harness where useful. Neither choice is authorized as a new shipped root dependency by the existing ADRs.

### GitHub: typed SDK versus specification tooling

**go-github/v91 v91.0.0.** This is a typed REST client with service methods for repository rules, issues, pull requests, checks and milestones, contexts, pagination response metadata and typed rate-limit errors. It does not implement the GitHub GraphQL API; the README points to a separate client. GitHub App authentication is also separate from worker orchestration and may use additional helper packages. Do not interpret the package name as complete API or full bot coverage. Release[^41], README[^42], repository rules source[^43].

It accepts a custom HTTP client and uses per-call contexts. Typed quota/abuse errors and optional waiting behavior improve error handling, but Praetor still must enforce deadlines, redirect/host policy, bounded pages/bodies and mutation retry rules. The declared two requirements include `go-cmp` used by tests and `go-querystring`; this is evidence of a small declaration graph, not a measured shipping footprint. Client implementation[^44], module[^45], license[^46].

**Judgment:** best candidate to retire hand-maintained typed GitHub transport if the architecture decision amends the root dependency rule. Under current constraints, use it outside the shipped graph as an oracle for request binding, error classification and pagination; keep real GitHub implementation tests. It cannot replace the missing webhook/App/worker lifecycle. Avoid adding auth helpers before the required installation-token and repository-scope model is defined.

**kin-openapi v0.149.0.** The exact ADR pin remains current in the verified release inventory. It supplies Swagger 2 conversion and OpenAPI 3 parsing/validation, including OpenAPI 3.1 work. Its loader can resolve references; external reference access must be explicitly confined to locked, approved inputs, rather than making builds depend on arbitrary remote URLs. A sub-v1 parser with documented breaking changes needs pinned fixtures and deterministic regeneration. Release[^47], README and loader examples[^48], module[^49], license[^50].

**Judgment:** highest-confidence immediate tooling reuse, because it matches the approved module boundary. It does not execute APIs. The proposed compiler must reject unsupported union, nullability, parameter-style and content-type semantics explicitly; silently flattening them into an incomplete table recreates stubs in data form. Specification completeness must be verified per operation/provider/server version.

**ogen v1.24.0.** Maintainer documentation describes typed client/server generation, context-based APIs, generated encoders and union/optional/nullability support; SSE generation is specifically client-side. This can remove hand-written serialization and parameter binding, but generated output and its runtime imports are the relevant shipping cost, not the generator's own declaration graph. Generated features may import ogen-related runtime packages, encoding, logging or telemetry support. Release[^51], README[^52], module[^53], license[^54].

**oapi-codegen/v2 v2.8.0.** The current release includes initial OpenAPI 3.1 support; an old blanket claim that it supports only 3.0 is stale. Its configuration schema exposes `include-operation-ids` and tag-based subsetting, fitting a scoped seven-operation canary. The release requires runtime v1.6.0 or newer for its new generated features. Generated code can require `github.com/oapi-codegen/runtime`; putting the generator in a separate module does not by itself preserve the root's dependency rule. Release[^55], README[^56], configuration schema[^57], module[^58], license[^59].

**Judgment:** benchmark one scoped typed output against one flat-table vertical slice before extending the generic interpreter. oapi-codegen is the first alternative because ADR-0008 already defines its amendment path; ogen is a useful second challenger. Measure imported graph, generated/source lines, conformance failures, incremental build cost and actual operations supported. Do not reuse the ADR's historical approximately 23 MB GitHub generation figure as a freshly measured result.

### Gitea, Forgejo and GitLab

**Gitea SDK v0.25.1.** The actual module is `code.gitea.io/sdk/gitea`; distribution metadata resolves to the Gitea maintainer repository and tag `gitea/v0.25.1`. API documentation exposes branch-protection, label, issue, PR and status methods, making it a real implementation candidate for explicit current stubs. This is not proof that every method matches every deployed Gitea version. Pinned module metadata[^60], module requirements[^61], versioned API[^62].

The inspected client initializes `http.Client{}` and `context.Background()`. `SetHTTPClient` permits injection; `SetContext` is shared client state used by `NewRequestWithContext`, rather than a context argument on every operation. Several response/error paths use `io.ReadAll`. A concurrent shared client whose context is changed per request can couple unrelated operations even if locks prevent a data race. Use an explicitly constructed client policy, bounded response reader and safe operation-scoped context strategy. These observations come from the exact archive's `client.go` and MIT license, retained locally. Pinned archive[^63], source commit[^64].

**Forgejo SDK in gof3/v3 v3.11.52.** The verified package is `code.forgejo.org/f3/gof3/v3/forges/forgejo/sdk`, not an assumed standalone `code.forgejo.org/forgejo/go-sdk`. Its containing module is a broader forge migration/federation library and declares dependencies including another forge client. The embedded client's default context/client and body-read patterns resemble the Gitea SDK. Its availability does not justify importing the whole F3 domain layer into Praetor. Package documentation[^65], exact version metadata[^66], module requirements[^67], archive with SDK and root MIT license[^68].

**Judgment:** useful Forgejo-specific oracle/reference; reject wholesale F3 adoption for this bounded problem. Gitea compatibility ancestry is not evidence of current Forgejo endpoint parity. Compare the deployed instance's API description and a seven-operation capability matrix before selecting a shared adapter or separate descriptors.

**GitLab official client v3.6.0.** The current maintained module is `gitlab.com/gitlab-org/api/client-go/v3`, under the GitLab organization. The older `github.com/xanzy/go-gitlab` name is not the target to add. The module proxy reports v3.6.0 at 2026-09-11, newer than search snippets that still described v3.4.0. Exact metadata[^69], maintainer README[^70], module[^71].

Its request options include `WithContext`, it supports injected HTTP clients, and its client initialization sets retry maximum 5, minimum retry wait 100 ms and maximum 400 ms. These defaults are not Praetor's agreed global request/retry budget. Mutation replay, idempotency, rate-limit handling and nested retries must be qualified before use. API/service breadth is substantial, but exact coverage of the required operations and target server remains unverified. Pinned client source[^72], request options[^73], Apache-2.0 license[^74], distribution archive inspected[^75].

**Judgment:** a credible route from explicit GitLab stubs to real methods, after provider/config wiring and the dependency decision. Reuse transport and types rather than copying the SDK into the root to cosmetically keep dependency count at one.

### LSP reuse

**go.lsp.dev/protocol v1.0.1**, hosted in `go-language-server/protocol`, documents generated LSP 3.18 model types, union handling and client/server RPC APIs over `go.lsp.dev/jsonrpc2`. The pinned module uses `go-json-experiment/json`; its large indirect module declaration set includes development tools. This requires a concrete codec and linked-dependency audit, not an automatic rejection based on the raw count or an assumption it is stdlib-only. Release[^76], README[^77], module[^78], BSD license[^79].

**Judgment:** useful type/framing replacement after extracting one analysis service used by CLI, MCP and LSP. Preserve unsaved-buffer/version semantics, UTF-16 position handling, cancellation, diagnostic ordering and shutdown behavior in adapter tests. An LSP SDK implements the protocol, not HISS checks. Importing or copying gopls internal packages is not an appropriate shortcut to a maintained public API.

### Discovery and alternatives not promoted

Discovery collections include Awesome Go[^80] and Awesome MCP servers[^81]. Entries are leads, not maintenance or conformance certification. The candidate claims use pinned maintainer sources.

- **libopenapi:** not selected for the Swagger 2 normalization boundary because its maintainer explicitly advises against a new v2-model dependency and plans its removal. It remains a separate candidate for OpenAPI 3-only work. This corrects the historical ADR's overly broad format assertion. Maintainer warning[^30].
- **GitHub's official MCP server:** worth evaluating as an optional external connector, but not a universal REST/GraphQL replacement or the missing Praetor bot runtime. Toolsets do not establish full operation coverage or Praetor repository authority. Adding a hosted/process dependency has its own lifecycle and credential cost. Maintainer project[^82].
- **MCP-to-LSP bridges discovered on awesome lists:** useful interoperability leads, not evidence that Praetor's analysis engine can be deleted. No maintenance/coverage ranking was assigned without equivalent source verification.
- **Old deepmap/xanzy module names or an imagined standalone Forgejo SDK:** do not promote discovery names directly into dependencies; use the verified current module origins above.

### Required migration and differential tests

These are proposed bounded tests, not work claimed completed by this report.

1. **MCP conformance harness in a separate module:** launch the real server with temporary roots; use official SDK and one independent client. Exercise negotiated legacy/current revisions, stdio and supported HTTP modes, real `tools/list` plus `tools/call`, malformed/oversized messages, concurrent calls, cancellation/disconnect, missing and denied roots, shutdown/reconnect, explicit handler errors and mutation write/readback. Require a failure when a supposed real operation returns fabricated success.
2. **Shared forge contract:** run identical safe `httptest` fixtures against the existing driver and candidate adapter/table executor. Compare method/path/query/headers/body, API version, typed errors, pagination caps, deadline/cancellation, oversized responses, 401/403/404/409/422/429/5xx, redirect/host confinement, token isolation and idempotent versus unsafe retries. Test every required operation, not just constructor/authentication success.
3. **Spec coverage and compiler gate:** lock input URL/version/hash; disable uncontrolled references; test Swagger 2 and OpenAPI 3.0/3.1, unions/nullability, body formats and parameter styles. Deliberately unsupported constructs must fail compilation with operation pointers. Verify deterministic output, operation inventory/diffs and provider-specific capability matrix. A parsed spec is not proof of complete API coverage.
4. **LSP parity:** common fixtures through CLI, MCP and unsaved-buffer LSP must produce equivalent rule IDs/severities/locations under explicit config. Retain the already demonstrated rule mismatches as failing parity cases, then add UTF-16 positions, document version ordering, cancellation and protocol shutdown tests.
5. **Measure dependency cost before adoption:** in an approved isolated comparison module, pin versions and run `go list -deps -json`, license/SBOM checks and representative builds for the actual imports/generated subset. Record compiled graph, binary/build cost, generated LOC, race results and conformance gaps. These protocol/forge candidates have not yet had their selected import closures or representative builds measured, so transitive shipping weight remains an explicit unknown.

The first useful implementation sequence is shared service/config boundaries, an isolated protocol/forge oracle, one real seven-operation provider slice, then a measured choice between flat tables and scoped typed clients. Bot App/webhook ingress, durable forge delivery scheduling and deduplication, staged repository authority and observability remain independent missing components; a package migration must not mark those capabilities complete. Existing local repair scheduling does not supply this webhook lifecycle.

## Model execution, commands and durable work

### Existing behavior that a replacement must preserve

Praetor already has meaningful execution policy. `repairrun.Generate` binds an exact endpoint/model and a hashed credential helper, imposes a 120-second deadline, and performs one HTTP request. Its transport rejects redirects, disables proxy inheritance, limits response headers/body, suppresses provider error bodies, and checks credential leakage. Proposal decoding, file allowlists, original-content hashes, isolated tests, attempt consumption and publication stages remain separate from transport. These are application requirements that an SDK or agent framework does not replace. [Provider boundary](https://github.com/CordanaLLM/praetor/blob/main/internal/repairrun/provider.go#L17), [HTTP transport](https://github.com/CordanaLLM/praetor/blob/main/internal/repairrun/provider_http.go#L20).

The local repair timer inspects bounded retained reports, runs one eligible attempt per tick, and preserves a durable execution key tied to source/input/policy. Repeated timestamps do not grant another request. Its systemd unit supplies process and resource bounds; there is no Redis or PostgreSQL service in this local queue design. [Timer contract](../guides/dogfood-repair-timer.md).

`internal/router.SelectForTask` is a deterministic selection from declared capabilities, task labels and rates. It does not provide live catalog verification, measured model quality, concurrency reservation or dispatch. `internal/runner` only resolves CI runner labels and platform constraints; it is not a workflow executor. Replacing either with a workflow framework based on its package name would target the wrong responsibility. [Task selection](https://github.com/CordanaLLM/praetor/blob/main/internal/router/task.go#L28), [runner resolution](https://github.com/CordanaLLM/praetor/blob/main/internal/runner/matrix.go#L10).

### Candidate comparison

The dependency column describes **declared module requirements**, not the packages linked into a production binary. Test dependencies, optional platform integrations and lazy module loading affect the eventual footprint. No compiled size, latency or memory advantage has been measured.

| Candidate / observed version | License; Go directive | What it could replace | Operational and dependency cost | Decision |
| --- | --- | --- | --- | --- |
| `openai/openai-go/v3` **v3.61.0**, released 2026-09-10 | Apache-2.0; Go 1.25.0 | Handwritten Responses request/response types and API evolution handling | No extra service. Manifest includes Azure/AWS integration modules and JSON helpers; actual selected-import footprint needs measurement. | **Prototype first** behind current policy/transport. |
| `anthropics/anthropic-sdk-go` **v1.72.0**, released 2026-09-10 | MIT; Go 1.24 | Native Claude Messages adapter, typed content blocks, usage and errors | No extra service. Manifest spans AWS/Google integrations, JSON/schema helpers, MCP and test tooling; do not assume all are linked. | **Conditional native-provider adapter** when native Claude capability is needed. |
| `google.golang.org/genai` **v1.71.0**, released 2026-08-31 | Apache-2.0; Go 1.24 | Native Gemini API generation, structured output and model metadata transport | No extra service beyond the selected API. Module includes Google auth, websocket and tokenization dependencies. | **Conditional native-provider adapter**; does not supply personal NotebookLM access. |
| `github.com/firebase/genkit/go` **v1.13.1**, Go tag dated 2026-09-03 | Apache-2.0; Go 1.25.0 | Common generation/evaluator interfaces, prompt templates, evaluation run comparison | Broad manifest includes provider SDKs, OpenTelemetry, Google Cloud, database/vector clients and schema tooling. Developer evaluation UI/CLI adds tooling; core use does not inherently require a hosted Firebase deployment. | **Prototype evaluation only**, retaining Praetor execution ownership. |
| `golang.org/x/sync` **v0.23.0** and `golang.org/x/time` **v0.16.0** | BSD-3-Clause; both Go 1.26.0 | Bounded in-process fan-out and request-admission helpers | Their inspected manifests have no external requirements. They add no durable queue, cross-process coordination or provider telemetry. | **Keep stdlib now; reuse when bounded parallel execution is introduced.** |
| `github.com/cenkalti/backoff/v7` **v7.0.0**, tagged 2026-06-30 | MIT; Go 1.23 | Repeated exponential-backoff mechanics for explicitly retryable operations | No external requirements in manifest. Still needs a total context deadline and an attempt budget; it cannot decide idempotence. | **Defer for generation; consider for bounded reads** if retry duplication appears. |
| CLI comparison: `spf13/cobra` **v1.10.2** (2025-12-04) versus `urfave/cli/v3` **v3.11.0** (2026-08-16) | Cobra Apache-2.0, Go 1.15; urfave MIT, Go 1.22 | Manual subcommand/help and interspersed-argument parsing | No service. Cobra declares pflag, mousetrap, documentation/YAML tooling. urfave's manifest declares testify plus indirect test-support packages; determine runtime imports separately. | **Prototype one command family against current parser**; do not rewrite everything. |
| `riverqueue/river` **v0.47.0**, released 2026-08-31 | MPL-2.0; Go 1.26.0 | Shared persisted queue, transactional enqueueing, worker limits, recovery and job inspection | Requires PostgreSQL and migrations for this use. Manifest includes pgx/puddle, River submodules, cron and JSON helpers. A distributed worker fleet needs DB ownership, backups and upgrades. | **Conditional future fleet queue**; retain systemd for workstation mode. |
| `go.temporal.io/sdk` **v1.48.0**, released 2026-08-18 | MIT; Go 1.25.4 | Durable multistage workflows, waiting for approvals, replay and coordinated recovery | Requires a Temporal service/cloud plus workers, storage/service operations and workflow versioning. SDK includes gRPC/protobuf, Temporal API and Nexus components. | **Defer** until cross-service, long-lived workflow recovery warrants it. |
| `hibiken/asynq` **v0.26.0**, released 2026-02-03 | MIT; Go 1.24.0 | Redis-backed distributed task dispatch and retry scheduling | Requires Redis, persistence/availability operations, workers and upgrade policy; module includes go-redis, cron, protobuf and rate helpers. | **Defer** unless Redis is the chosen shared queue substrate. |

Version, license and manifest evidence is linked in the source ledger below. For these execution candidates, all inspected GitHub repository metadata reported `archived:false`; that is a maintenance signal, not a compatibility or security guarantee. The protocol inventory above has a separate metadata-refresh limitation.

### Provider SDKs: retain policy, replace protocol maintenance

**OpenAI Go is the closest fit to the existing Responses transport.** It exposes typed Responses operations, custom HTTP clients and raw response/header access. That makes it a plausible replacement for repetitive protocol plumbing while preserving the gateway cost header and local proposal schema. It also retries selected connection/status failures twice by default; disabling SDK retries is necessary to retain the present client-side single-attempt boundary. Keep the explicit context deadline and no-tools request. Official SDK documentation[^83], request options[^84].

The prototype must use an injected local test server or transport and prove that 429, 500, connection interruption and cancellation produce the same allowed request count as the current implementation. It must also preserve response caps, redirect/proxy behavior, credential suppression, incomplete-output rejection and gateway cost readback. Generated SDK response types are not a substitute for validating untrusted repair edits. A model name constant is not a current capability catalog or a successful provider observation.

**Anthropic's SDK is useful for a native Messages contract, not as another generic agent loop.** Its API supports typed content and context/options control. The official documentation says it retries selected failures twice by default and uses a ten-minute default timeout for non-streaming Messages; neither matches the current repair contract. The adapter must configure its own deadline and retry policy, map refusal/tool blocks deliberately, and retain Praetor's error redaction. Do not copy the documentation's request/response dump examples into private logging. Official Go documentation[^85], pinned options implementation[^86].

**Google's SDK provides Gemini API transport, not a NotebookLM connector.** The inspected client lists Models, Chats, Files, Operations and related services, with explicit backend and HTTP-client configuration. Its `HTTPRetryOptions` documents five attempts when unspecified, and zero or one as no retries. Confirm this behavior in adapter tests before adopting it; documentation and type declarations alone are not an executed request-count result. Do not let ambient Google environment variables select a different backend or credentials than the reviewed policy. Pinned client[^87], retry option contract[^88], request path[^89].

Using three native SDKs provides fuller native contracts but creates three adapters and three compatibility matrices. Keeping one reviewed gateway contract is cheaper when its supported features are enough. Neither arrangement resolves the current static catalog's missing provenance or prompt-quality evidence. Add a native adapter for a demonstrated capability gap, not merely to claim provider coverage.

### Genkit: a credible evaluation comparison with a limited adoption surface

Genkit's Go documentation describes generation, structured output, multiple provider plugins and evaluators. Its evaluation workflow can run flows on datasets and compare runs, addressing a real gap: Praetor's new `promptopt` currently selects from caller-reported metrics but does not run providers or evaluators. A bounded prototype should export a captured evaluation run into Praetor's versioned experiment format, with the exact prompt, provider/model revision, evaluator contract and dataset digests attached. Go README at v1.13.1[^90], evaluation documentation[^91].

There is a meaningful separation between deterministic tests and model-quality evaluation: Genkit itself recommends normal Go tests with fake models for the former and evaluation for the latter. Use that distinction to avoid turning successful mocked flows into evidence that a prompt is better. No framework proves universal prompt optimality; the contract, data distribution, provider settings, trials and observed costs determine the result. Testing guidance[^92].

The current Go README places agent constructors/session machinery under experimental packages and marks reconnectable durable streaming as preview. A session-owning agent loop would overlap Praetor's attempt ledger, source identity, cancellation and policy. Keep it outside the proposed first slice. If the Firebase telemetry plugin is enabled, it captures inputs/outputs by default unless configured otherwise; that is a plugin behavior, not a claim that every Genkit process exports to Google. Private notebook and workstation sources need an explicit telemetry/storage policy. Experimental agent surface[^93], telemetry configuration[^94].

The version boundary matters: the repository's overall latest release was a Python release, whereas the Go module resolves to **v1.13.1**. The Go manifest retracts v1.13.0 because a preview package was shipped at the wrong path. Use the module-specific tag, not a monorepo-wide "latest" assumption. Go module manifest[^95].

### Process, concurrency, retries and commands

**Keep the standard-library process foundation.** Go's `exec.CommandContext` provides cancellation, and `Cmd.WaitDelay` bounds process/pipe shutdown delays. Praetor already wraps those mechanisms with bounded output, deadlines and Unix process-group cleanup. A general shell-command library would not remove the need for environment control, executable provenance, no-shell argv, descendant cleanup and sandbox restrictions. Prefer consolidating active callers onto the existing audited wrapper. Go exec documentation[^96], WaitDelay[^97], [Praetor wrapper](https://github.com/CordanaLLM/praetor/blob/main/internal/util/command_bytes.go#L32).

`errgroup.WithContext` and `SetLimit` are a good fit for future bounded concurrent read/evaluation batches; the zero group has no concurrency limit. `rate.Limiter` controls in-process token-bucket admission and supports cancellation while waiting. Neither is durable, shared across workstations, or proof of remote remaining quota. Current inspected execution is largely serialized, so introducing these modules today without an active fan-out consumer would produce another unused abstraction. errgroup[^98], rate[^99].

Backoff v7 is small and provides explicit context, maximum tries and elapsed-time controls. Its elapsed-time setting governs scheduling between attempts; it does not interrupt a running operation, so the operation must observe its context. Its inspected default elapsed-time window is fifteen minutes. This is useful plumbing for approved retryable reads, but placing it around `repairrun.Generate` would change the attempt contract. Classify permanent errors and cap retries centrally; avoid multiplying SDK, gateway, worker and outer-loop retry budgets. Pinned README[^100], v7 API[^101].

**CLI reuse has a concrete reason to investigate.** `flagargs.go` exists because standard flag parsing stopped at the first positional and silently dropped later documented flags. Its tests cover unknown flags, `--`, boolean flags, space-separated values and argument-count limits. This is demonstrated recurring parsing surface, not merely a preference for a popular package. [Current parser](https://github.com/CordanaLLM/praetor/blob/main/cmd/standardsctl/flagargs.go#L10), [fixtures](https://github.com/CordanaLLM/praetor/blob/main/cmd/standardsctl/flagargs_test.go#L19).

Cobra offers nested commands, inherited/local flags, argument validation, completion and generated help. urfave/cli v3 offers an alternative command model with a lighter-looking manifest, although the actual imported dependency graph still needs measurement. Compare one nested command family against the existing fixtures and documented exit/help behavior; retain root context propagation, `maxCLIArgs`, aliases, strict unknown arguments and mutation authorization. Changing the parser will not fix stale model metadata or missing bot deployment. Cobra documentation[^102], urfave v3[^103].

### Queue and workflow choices

**River is the best candidate if fleet coordination belongs in an already-operated PostgreSQL database.** Transactional enqueueing can couple job publication to application state, while typed jobs, unique-job constraints and worker controls replace custom shared queue mechanics. It does not make an external provider request atomic with a database transaction. A crash after a provider charged the request but before completion still needs an application idempotency/attempt strategy. Preserve Praetor's source/input/policy execution identity and external evidence artifacts. Transactions[^104], unique jobs[^105].

River's default retry policy differs from a single provider attempt and must be explicitly configured. Its regular periodic schedule lives in the elected leader's memory; persisted periodic scheduling is documented as a **River Pro** feature. Therefore the open-source scheduler is not automatically a more durable substitute for the current systemd tick. PostgreSQL migrations, queue retention, cleanup, privilege boundaries and recovery tests are additional work, not eliminated work. Retry behavior[^106], periodic-job caveats[^107].

**Temporal fits a later multistage control plane**, for example separate harvest/evaluate/propose/review/apply/replay stages that wait across worker or service restarts. Workflow code must be deterministic; provider calls and Git operations belong in Activities. Activities retry by default with an unlimited attempt count unless bounded, and setting one attempt disables retry. Consequently adopting the SDK without an explicit policy adapter would reverse the current one-attempt guarantee. The SDK/service, durable history, worker versioning, payload storage and infrastructure are a large migration. Retry and determinism requirements[^108], Go SDK[^109].

**Asynq is a simpler distributed job queue when Redis is already the desired substrate.** It advertises at-least-once execution, retries, crash recovery, timeouts and uniqueness options. These semantics still require idempotent external effects and do not replace the content/policy attempt ledger. Its README warns that the v0 API can change and some Lua scripts may not support Redis Cluster. Adding Redis solely to replace a local fifteen-minute timer is unlikely to reduce maintenance; choose it only for a demonstrated shared-queue requirement. Pinned README[^110].

### Grouped alternatives and adoption limits

Eino **v0.9.19** is an alternative Go component/graph framework; the repository also published a **v0.10.0-alpha.29** prerelease, which should not be mistaken for the selected stable module version. It introduces its own orchestration abstractions, JSON/schema and template dependencies, and provider integrations would still need inspection. Defer a second framework prototype until Genkit's evaluator comparison shows a concrete missing capability. Stable release[^111], manifest[^112].

The inspected Awesome lists surface broad Python/TypeScript agent and evaluation stacks, including LangChain and DSPy. They are useful architectural references, but introducing another runtime to replace Go transport/process mechanics adds packaging and supervision boundaries. That is not justified by list inclusion. The discovery lists were **Awesome Go**, **Awesome Agents**, and **Awesome-LLM**; candidate claims above use the projects' own releases, source and documentation. Awesome Go[^80], Awesome Agents[^113], Awesome-LLM[^114].

No candidate automatically repairs the static catalog, proves task competence, enforces the repository's stages, discovers missing IDE/bot consumers, or guarantees optimized prompts across all models. Those remain domain contracts and integration tests. Package replacement should remove a demonstrated maintenance burden while preserving the active policy surface.

### Three isolated comparisons and their acceptance evidence

1. **OpenAI Go adapter versus existing HTTP implementation.** Reuse the current fake-provider corpus. Compare request bytes/semantics, exact attempt count, cancellation, truncation/refusal/tool-block handling, body/header caps, leaked credentials, redirects/proxies and cost headers. Measure selected-import module graph, build size and fixture execution time. Retain the current implementation until differences are explained and reviewed.
2. **Genkit evaluator to Praetor experiment bridge.** Use synthetic public fixtures first, one provider contract, fixed candidate prompts and unchanged evaluators. Retain evaluator versions, dataset/case hashes, full model settings, raw-response hashes, latency and observed usage. Test failed/missing metrics and partial runs. The experiment may recommend a candidate; it must not automatically rewrite policy or promote code.
3. **One CLI family under Cobra and urfave/cli.** Run the existing positive/negative/boundary parser fixtures and compare help, aliases, `--`, flag-after-positional behavior, limits and exit codes. Count removed parser/help duplication and new integration code. Choose a framework only if that measured tradeoff is favorable; retaining the corrected stdlib parser is a valid result.

Estimated migration effort is qualitative: SDK adapters are a bounded protocol exercise with substantial policy-regression testing; Genkit evaluation and CLI migrations span several adapters/fixtures; shared queues add operational ownership; Temporal adds the largest conceptual and operational change. No wall-clock savings or cost reductions are claimed without these measurements.

### Pinned primary-source ledger

| Project | Version / source of date | Module requirements | License source |
| --- | --- | --- | --- |
| OpenAI Go | v3.61.0 release[^115] | go.mod[^116] | Apache-2.0[^117] |
| Anthropic Go | v1.72.0 release[^118] | go.mod[^119] | MIT[^120] |
| Google Gen AI | v1.71.0 release[^121] | go.mod[^122] | Apache-2.0[^123] |
| Genkit Go | Go v1.13.1 tag[^124], module version[^125] | go/go.mod[^95] | Apache-2.0[^126] |
| x/sync | v0.23.0[^127] | go.mod[^128] | BSD-3-Clause[^129] |
| x/time | v0.16.0[^130] | go.mod[^131] | BSD-3-Clause[^132] |
| Backoff | v7.0.0[^101] | go.mod[^133] | MIT[^134] |
| Cobra | v1.10.2 release[^135] | go.mod[^136] | Apache-2.0[^137] |
| urfave/cli | v3.11.0 release[^138] | go.mod[^139] | MIT[^140] |
| River | v0.47.0 release[^141] | go.mod[^142] | MPL-2.0[^143] |
| Temporal SDK | v1.48.0 release[^144] | go.mod[^145] | MIT[^146] |
| Asynq | v0.26.0 release[^147] | go.mod[^148] | MIT[^149] |


## Git and filesystem boundaries


Praetor should retain native Git behind its bounded execution adapter for the current worktree/repair paths. go-git is a maintained pure-Go implementation, and release v5.19.2 is current stable in the inspected release feed, with Apache-2.0 license. Its pinned compatibility document explicitly omits worktree management, apply, rebase and cherry-pick and restricts merge to fast-forward. Those omissions directly intersect internal/worktree, internal/operationalsync and internal/repairrun, making it unsuitable as a wholesale replacement. This is a workload-fit judgment, not a claim that the library is low quality. The v6 release stream is prerelease and was not selected as a production candidate. Release[^150], pinned compatibility[^151], license[^152].

The pinned module requires Go 1.25 and declares 23 direct and 12 indirect requirement lines, including test requirements. These are manifest counts, not 35 necessarily-linked runtime dependencies, binary-size measurements or vulnerability findings. A measured graph must be derived from the actual imported packages before any adoption decision. For this repository, adding a second Git implementation would preserve the native Git dependency while creating a second set of repository semantics to maintain. A narrow object-reader use case could be evaluated separately if one arises. Pinned module[^153].

Filesystem reuse should start with the installed Go 1.27.1 os.Root APIs already used in contextopt. They retain a directory handle, confine relative operations, and expose ReadFile/WriteFile/Rename and related operations without a new module. They are not a sandbox against bind mounts or device files, allow symlinks that remain inside the root, and carry documented platform caveats. Praetor still needs its explicit no-symlink policy where required, bounded content, regular-file checks, temporary-file/rename durability, permissions and conflict handling. Migrating existing ad hoc path checks to one shared implementation is more valuable than adding a generic filesystem facade. Current os.Root reference[^154], design rationale[^155].

Afero is a useful filesystem abstraction when several storage backends or in-memory filesystem substitution are real requirements. Its API abstraction is not evidence of os.Root-style confinement. The inspected repository instead relies heavily on real temporary-directory fixtures, symlink attacks and process/Git behavior, which an in-memory backend cannot establish. Recommendation: do not add Afero just to make tests easier; use io/fs for read-only interfaces and narrowly injected functions for failures, retaining real filesystem adversarial tests. Maintainer project[^156].

A library selection gate should record exact module path/tag/commit, supported Go versions, license/notice files, required APIs, security policy, release activity, and the module graph for the actual adapter. A fresh govulncheck run is necessary once a real import graph exists; package popularity and a release badge do not establish absence of reachable vulnerabilities. No candidate in this study has passed an integrated Praetor vulnerability or performance acceptance merely because its metadata was retrieved. Go vulnerability management[^157].

For each candidate, measure the existing wrapper and the alternative on identical requests/fixtures: correctness and errors, timeout/cancellation, response/AST/file bounds, allocations and p50/p95 latency where relevant, cold build time, artifact size, runtime service requirements and maintenance ownership. Remove the old implementation only after every consumer has moved and differential cases are retained. Source-lines removed can inform expected maintenance savings but are not a performance measurement.

Discovery reference: Awesome Go[^80]. Maintainer sources and the actual operation requirements determine the decision.

## Sources

Version and source observations were checked on 2026-09-12. Linked release, module and license documents specify the relevant pin; unversioned conceptual documentation can change. Selected-import measurements apply only to their stated imports and environment. No integrated dependency vulnerability clearance or runtime performance benchmark is implied.

[^1]: yuin/goldmark. [v1.8.6](https://github.com/yuin/goldmark/releases/tag/v1.8.6). Accessed 2026-09-12.
[^2]: santhosh-tekuri/jsonschema. [v6.0.3](https://github.com/santhosh-tekuri/jsonschema/releases/tag/v6.0.3). Accessed 2026-09-12.
[^3]: yaml/go-yaml. [v3.0.5](https://github.com/yaml/go-yaml/releases/tag/v3.0.5). Accessed 2026-09-12.
[^4]: goccy/go-yaml. [v1.19.2](https://github.com/goccy/go-yaml/releases/tag/v1.19.2). Accessed 2026-09-12.
[^5]: etcd-io/bbolt. [v1.5.0](https://github.com/etcd-io/bbolt/releases/tag/v1.5.0). Accessed 2026-09-12.
[^6]: pkg.go.dev. [v1.58.0](https://pkg.go.dev/modernc.org/sqlite@v1.58.0). Accessed 2026-09-12.
[^7]: mattn/go-sqlite3. [v1.14.52](https://github.com/mattn/go-sqlite3/releases/tag/v1.14.52). Accessed 2026-09-12.
[^8]: pkg.go.dev. [v0.50.0](https://pkg.go.dev/golang.org/x/tools@v0.50.0). Accessed 2026-09-12.
[^9]: google/go-cmp. [v0.7.0](https://github.com/google/go-cmp/releases/tag/v0.7.0). Accessed 2026-09-12.
[^10]: flyingmutant/rapid. [v1.3.0](https://github.com/flyingmutant/rapid/releases/tag/v1.3.0). Accessed 2026-09-12.
[^11]: go.dev. [Go 1.27 release notes](https://go.dev/doc/go1.27#stdlib). Accessed 2026-09-12.
[^12]: pkg.go.dev. [security discussion](https://pkg.go.dev/encoding/json/v2#hdr-Security_Considerations). Accessed 2026-09-12.
[^13]: pkg.go.dev. [Decoder.StackDepth](https://pkg.go.dev/encoding/json/jsontext#Decoder.StackDepth). Accessed 2026-09-12.
[^14]: yuin/goldmark. [README](https://github.com/yuin/goldmark/blob/v1.8.6/README.md). Accessed 2026-09-12.
[^15]: yuin/goldmark. [go.mod](https://github.com/yuin/goldmark/blob/v1.8.6/go.mod). Accessed 2026-09-12.
[^16]: santhosh-tekuri/jsonschema. [v6.0.3 source](https://github.com/santhosh-tekuri/jsonschema/tree/v6.0.3). Accessed 2026-09-12.
[^17]: pkg.go.dev. [compiler API](https://pkg.go.dev/github.com/santhosh-tekuri/jsonschema/v6#Compiler). Accessed 2026-09-12.
[^18]: yaml/go-yaml. [v3.0.5 README](https://github.com/yaml/go-yaml/blob/v3.0.5/README.md). Accessed 2026-09-12.
[^19]: pkg.go.dev. [KnownFields](https://pkg.go.dev/go.yaml.in/yaml/v3#Decoder.KnownFields). Accessed 2026-09-12.
[^20]: yaml/go-yaml. [go.mod](https://github.com/yaml/go-yaml/blob/v3.0.5/go.mod). Accessed 2026-09-12.
[^21]: goccy/go-yaml. [pinned README](https://github.com/goccy/go-yaml/blob/v1.19.2/README.md). Accessed 2026-09-12.
[^22]: etcd-io/bbolt. [v1.5.0 README](https://github.com/etcd-io/bbolt/blob/v1.5.0/README.md). Accessed 2026-09-12.
[^23]: mattn/go-sqlite3. [mattn v1.14.52](https://github.com/mattn/go-sqlite3/blob/v1.14.52/README.md). Accessed 2026-09-12.
[^24]: pkg.go.dev. [analysis API](https://pkg.go.dev/golang.org/x/tools/go/analysis). Accessed 2026-09-12.
[^25]: pkg.go.dev. [packages](https://pkg.go.dev/golang.org/x/tools/go/packages). Accessed 2026-09-12.
[^26]: pkg.go.dev. [analysistest](https://pkg.go.dev/golang.org/x/tools/go/analysis/analysistest). Accessed 2026-09-12.
[^27]: google/go-cmp. [cmp's own source](https://github.com/google/go-cmp/blob/v0.7.0/cmp/compare.go). Accessed 2026-09-12.
[^28]: flyingmutant/rapid. [rapid v1.3.0](https://github.com/flyingmutant/rapid/blob/v1.3.0/README.md). Accessed 2026-09-12.
[^29]: go.dev. [Go fuzzing](https://go.dev/doc/security/fuzz/). Accessed 2026-09-12.
[^30]: pb33f.io. [maintainer Swagger warning](https://pb33f.io/libopenapi/swagger/). Accessed 2026-09-12.
[^31]: modelcontextprotocol/go-sdk. [Tagged release](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.7.0). Accessed 2026-09-12.
[^32]: modelcontextprotocol/go-sdk. [tagged README](https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/README.md). Accessed 2026-09-12.
[^33]: modelcontextprotocol/go-sdk. [Transport implementation](https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/mcp/streamable.go). Accessed 2026-09-12.
[^34]: pkg.go.dev. [package API](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp). Accessed 2026-09-12.
[^35]: modelcontextprotocol/go-sdk. [Pinned module](https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/go.mod). Accessed 2026-09-12.
[^36]: modelcontextprotocol/go-sdk. [pinned license](https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/LICENSE). Accessed 2026-09-12.
[^37]: mark3labs/mcp-go. [Release](https://github.com/mark3labs/mcp-go/releases/tag/v1.0.0). Accessed 2026-09-12.
[^38]: mark3labs/mcp-go. [pinned behavior/options](https://github.com/mark3labs/mcp-go/blob/v1.0.0/README.md). Accessed 2026-09-12.
[^39]: mark3labs/mcp-go. [module](https://github.com/mark3labs/mcp-go/blob/v1.0.0/go.mod). Accessed 2026-09-12.
[^40]: mark3labs/mcp-go. [MIT license](https://github.com/mark3labs/mcp-go/blob/v1.0.0/LICENSE). Accessed 2026-09-12.
[^41]: google/go-github. [Release](https://github.com/google/go-github/releases/tag/v91.0.0). Accessed 2026-09-12.
[^42]: google/go-github. [README](https://github.com/google/go-github/blob/v91.0.0/README.md). Accessed 2026-09-12.
[^43]: google/go-github. [repository rules source](https://github.com/google/go-github/blob/v91.0.0/github/repos_rules.go). Accessed 2026-09-12.
[^44]: google/go-github. [Client implementation](https://github.com/google/go-github/blob/v91.0.0/github/github.go). Accessed 2026-09-12.
[^45]: google/go-github. [module](https://github.com/google/go-github/blob/v91.0.0/go.mod). Accessed 2026-09-12.
[^46]: google/go-github. [license](https://github.com/google/go-github/blob/v91.0.0/LICENSE). Accessed 2026-09-12.
[^47]: getkin/kin-openapi. [Release](https://github.com/getkin/kin-openapi/releases/tag/v0.149.0). Accessed 2026-09-12.
[^48]: getkin/kin-openapi. [README and loader examples](https://github.com/getkin/kin-openapi/blob/v0.149.0/README.md). Accessed 2026-09-12.
[^49]: getkin/kin-openapi. [module](https://github.com/getkin/kin-openapi/blob/v0.149.0/go.mod). Accessed 2026-09-12.
[^50]: getkin/kin-openapi. [license](https://github.com/getkin/kin-openapi/blob/v0.149.0/LICENSE). Accessed 2026-09-12.
[^51]: ogen-go/ogen. [Release](https://github.com/ogen-go/ogen/releases/tag/v1.24.0). Accessed 2026-09-12.
[^52]: ogen-go/ogen. [README](https://github.com/ogen-go/ogen/blob/v1.24.0/README.md). Accessed 2026-09-12.
[^53]: ogen-go/ogen. [module](https://github.com/ogen-go/ogen/blob/v1.24.0/go.mod). Accessed 2026-09-12.
[^54]: ogen-go/ogen. [license](https://github.com/ogen-go/ogen/blob/v1.24.0/LICENSE). Accessed 2026-09-12.
[^55]: oapi-codegen/oapi-codegen. [Release](https://github.com/oapi-codegen/oapi-codegen/releases/tag/v2.8.0). Accessed 2026-09-12.
[^56]: oapi-codegen/oapi-codegen. [README](https://github.com/oapi-codegen/oapi-codegen/blob/v2.8.0/README.md). Accessed 2026-09-12.
[^57]: oapi-codegen/oapi-codegen. [configuration schema](https://github.com/oapi-codegen/oapi-codegen/blob/v2.8.0/configuration-schema.json). Accessed 2026-09-12.
[^58]: oapi-codegen/oapi-codegen. [module](https://github.com/oapi-codegen/oapi-codegen/blob/v2.8.0/go.mod). Accessed 2026-09-12.
[^59]: oapi-codegen/oapi-codegen. [license](https://github.com/oapi-codegen/oapi-codegen/blob/v2.8.0/LICENSE). Accessed 2026-09-12.
[^60]: proxy.golang.org. [Pinned module metadata](https://proxy.golang.org/code.gitea.io/sdk/gitea/@v/v0.25.1.info). Accessed 2026-09-12.
[^61]: proxy.golang.org. [module requirements](https://proxy.golang.org/code.gitea.io/sdk/gitea/@v/v0.25.1.mod). Accessed 2026-09-12.
[^62]: pkg.go.dev. [versioned API](https://pkg.go.dev/code.gitea.io/sdk/gitea@v0.25.1). Accessed 2026-09-12.
[^63]: proxy.golang.org. [Pinned archive](https://proxy.golang.org/code.gitea.io/sdk/gitea/@v/v0.25.1.zip). Accessed 2026-09-12.
[^64]: gitea.com. [source commit](https://gitea.com/gitea/go-sdk/src/commit/cc735c2bdee14e53367913cbdbd219d3f4f4d80c/gitea/client.go). Accessed 2026-09-12.
[^65]: pkg.go.dev. [Package documentation](https://pkg.go.dev/code.forgejo.org/f3/gof3/v3/forges/forgejo/sdk). Accessed 2026-09-12.
[^66]: proxy.golang.org. [exact version metadata](https://proxy.golang.org/code.forgejo.org/f3/gof3/v3/@v/v3.11.52.info). Accessed 2026-09-12.
[^67]: proxy.golang.org. [module requirements](https://proxy.golang.org/code.forgejo.org/f3/gof3/v3/@v/v3.11.52.mod). Accessed 2026-09-12.
[^68]: proxy.golang.org. [archive with SDK and root MIT license](https://proxy.golang.org/code.forgejo.org/f3/gof3/v3/@v/v3.11.52.zip). Accessed 2026-09-12.
[^69]: proxy.golang.org. [Exact metadata](https://proxy.golang.org/gitlab.com/gitlab-org/api/client-go/v3/@v/v3.6.0.info). Accessed 2026-09-12.
[^70]: gitlab-org/api. [maintainer README](https://gitlab.com/gitlab-org/api/client-go/-/blob/v3.6.0/README.md). Accessed 2026-09-12.
[^71]: proxy.golang.org. [module](https://proxy.golang.org/gitlab.com/gitlab-org/api/client-go/v3/@v/v3.6.0.mod). Accessed 2026-09-12.
[^72]: gitlab-org/api. [Pinned client source](https://gitlab.com/gitlab-org/api/client-go/-/blob/v3.6.0/gitlab.go). Accessed 2026-09-12.
[^73]: gitlab-org/api. [request options](https://gitlab.com/gitlab-org/api/client-go/-/blob/v3.6.0/request_options.go). Accessed 2026-09-12.
[^74]: gitlab-org/api. [Apache-2.0 license](https://gitlab.com/gitlab-org/api/client-go/-/blob/v3.6.0/LICENSE). Accessed 2026-09-12.
[^75]: proxy.golang.org. [distribution archive inspected](https://proxy.golang.org/gitlab.com/gitlab-org/api/client-go/v3/@v/v3.6.0.zip). Accessed 2026-09-12.
[^76]: go-language-server/protocol. [Release](https://github.com/go-language-server/protocol/releases/tag/v1.0.1). Accessed 2026-09-12.
[^77]: go-language-server/protocol. [README](https://github.com/go-language-server/protocol/blob/v1.0.1/README.md). Accessed 2026-09-12.
[^78]: go-language-server/protocol. [module](https://github.com/go-language-server/protocol/blob/v1.0.1/go.mod). Accessed 2026-09-12.
[^79]: go-language-server/protocol. [BSD license](https://github.com/go-language-server/protocol/blob/v1.0.1/LICENSE). Accessed 2026-09-12.
[^80]: avelino/awesome-go. [Awesome Go](https://github.com/avelino/awesome-go). Accessed 2026-09-12.
[^81]: punkpeye/awesome-mcp-servers. [Awesome MCP servers](https://github.com/punkpeye/awesome-mcp-servers). Accessed 2026-09-12.
[^82]: github/github-mcp-server. [Maintainer project](https://github.com/github/github-mcp-server). Accessed 2026-09-12.
[^83]: openai/openai-go. [Official SDK documentation](https://github.com/openai/openai-go/tree/v3.61.0). Accessed 2026-09-12.
[^84]: openai/openai-go. [request options](https://github.com/openai/openai-go/blob/v3.61.0/option/requestoption.go). Accessed 2026-09-12.
[^85]: platform.claude.com. [Official Go documentation](https://platform.claude.com/docs/en/cli-sdks-libraries/sdks/go). Accessed 2026-09-12.
[^86]: anthropics/anthropic-sdk-go. [pinned options implementation](https://github.com/anthropics/anthropic-sdk-go/blob/v1.72.0/option/requestoption.go). Accessed 2026-09-12.
[^87]: googleapis/go-genai. [Pinned client](https://github.com/googleapis/go-genai/blob/v1.71.0/client.go). Accessed 2026-09-12.
[^88]: googleapis/go-genai. [retry option contract](https://github.com/googleapis/go-genai/blob/v1.71.0/types.go#L1866). Accessed 2026-09-12.
[^89]: googleapis/go-genai. [request path](https://github.com/googleapis/go-genai/blob/v1.71.0/api_client.go). Accessed 2026-09-12.
[^90]: genkit-ai/genkit. [Go README at v1.13.1](https://github.com/genkit-ai/genkit/blob/go/v1.13.1/go/README.md). Accessed 2026-09-12.
[^91]: genkit.dev. [evaluation documentation](https://genkit.dev/docs/go/evaluation/). Accessed 2026-09-12.
[^92]: genkit.dev. [Testing guidance](https://genkit.dev/docs/go/testing/). Accessed 2026-09-12.
[^93]: genkit-ai/genkit. [Experimental agent surface](https://github.com/genkit-ai/genkit/blob/go/v1.13.1/go/README.md#agents). Accessed 2026-09-12.
[^94]: genkit.dev. [telemetry configuration](https://genkit.dev/docs/go/observability/advanced-configuration/). Accessed 2026-09-12.
[^95]: genkit-ai/genkit. [Go module manifest](https://github.com/genkit-ai/genkit/blob/go/v1.13.1/go/go.mod). Accessed 2026-09-12.
[^96]: pkg.go.dev. [Go exec documentation](https://pkg.go.dev/os/exec#CommandContext). Accessed 2026-09-12.
[^97]: pkg.go.dev. [WaitDelay](https://pkg.go.dev/os/exec#Cmd). Accessed 2026-09-12.
[^98]: pkg.go.dev. [errgroup](https://pkg.go.dev/golang.org/x/sync@v0.23.0/errgroup). Accessed 2026-09-12.
[^99]: pkg.go.dev. [rate](https://pkg.go.dev/golang.org/x/time@v0.16.0/rate). Accessed 2026-09-12.
[^100]: cenkalti/backoff. [Pinned README](https://github.com/cenkalti/backoff/tree/v7.0.0). Accessed 2026-09-12.
[^101]: pkg.go.dev. [v7 API](https://pkg.go.dev/github.com/cenkalti/backoff/v7@v7.0.0). Accessed 2026-09-12.
[^102]: spf13/cobra. [Cobra documentation](https://github.com/spf13/cobra/tree/v1.10.2). Accessed 2026-09-12.
[^103]: urfave/cli. [urfave v3](https://github.com/urfave/cli/tree/v3.11.0). Accessed 2026-09-12.
[^104]: riverqueue.com. [Transactions](https://riverqueue.com/docs/transactional-enqueueing). Accessed 2026-09-12.
[^105]: riverqueue.com. [unique jobs](https://riverqueue.com/docs/unique-jobs). Accessed 2026-09-12.
[^106]: riverqueue.com. [Retry behavior](https://riverqueue.com/docs/job-retries). Accessed 2026-09-12.
[^107]: riverqueue.com. [periodic-job caveats](https://riverqueue.com/docs/periodic-jobs). Accessed 2026-09-12.
[^108]: docs.temporal.io. [Retry and determinism requirements](https://docs.temporal.io/encyclopedia/retry-policies). Accessed 2026-09-12.
[^109]: temporalio/sdk-go. [Go SDK](https://github.com/temporalio/sdk-go/tree/v1.48.0). Accessed 2026-09-12.
[^110]: hibiken/asynq. [Pinned README](https://github.com/hibiken/asynq/tree/v0.26.0). Accessed 2026-09-12.
[^111]: cloudwego/eino. [Stable release](https://github.com/cloudwego/eino/tree/v0.9.19). Accessed 2026-09-12.
[^112]: cloudwego/eino. [manifest](https://github.com/cloudwego/eino/blob/v0.9.19/go.mod). Accessed 2026-09-12.
[^113]: kyrolabs/awesome-agents. [Awesome Agents](https://github.com/kyrolabs/awesome-agents). Accessed 2026-09-12.
[^114]: Hannibal046/Awesome-LLM. [Awesome-LLM](https://github.com/Hannibal046/Awesome-LLM). Accessed 2026-09-12.
[^115]: openai/openai-go. [v3.61.0 release](https://github.com/openai/openai-go/releases/tag/v3.61.0). Accessed 2026-09-12.
[^116]: openai/openai-go. [go.mod](https://github.com/openai/openai-go/blob/v3.61.0/go.mod). Accessed 2026-09-12.
[^117]: openai/openai-go. [Apache-2.0](https://github.com/openai/openai-go/blob/v3.61.0/LICENSE). Accessed 2026-09-12.
[^118]: anthropics/anthropic-sdk-go. [v1.72.0 release](https://github.com/anthropics/anthropic-sdk-go/releases/tag/v1.72.0). Accessed 2026-09-12.
[^119]: anthropics/anthropic-sdk-go. [go.mod](https://github.com/anthropics/anthropic-sdk-go/blob/v1.72.0/go.mod). Accessed 2026-09-12.
[^120]: anthropics/anthropic-sdk-go. [MIT](https://github.com/anthropics/anthropic-sdk-go/blob/v1.72.0/LICENSE). Accessed 2026-09-12.
[^121]: googleapis/go-genai. [v1.71.0 release](https://github.com/googleapis/go-genai/releases/tag/v1.71.0). Accessed 2026-09-12.
[^122]: googleapis/go-genai. [go.mod](https://github.com/googleapis/go-genai/blob/v1.71.0/go.mod). Accessed 2026-09-12.
[^123]: googleapis/go-genai. [Apache-2.0](https://github.com/googleapis/go-genai/blob/v1.71.0/LICENSE). Accessed 2026-09-12.
[^124]: genkit-ai/genkit. [Go v1.13.1 tag](https://github.com/genkit-ai/genkit/tree/go/v1.13.1). Accessed 2026-09-12.
[^125]: pkg.go.dev. [module version](https://pkg.go.dev/github.com/firebase/genkit/go@v1.13.1). Accessed 2026-09-12.
[^126]: genkit-ai/genkit. [Apache-2.0](https://github.com/genkit-ai/genkit/blob/go/v1.13.1/LICENSE). Accessed 2026-09-12.
[^127]: pkg.go.dev. [v0.23.0](https://pkg.go.dev/golang.org/x/sync@v0.23.0). Accessed 2026-09-12.
[^128]: golang/sync. [go.mod](https://github.com/golang/sync/blob/v0.23.0/go.mod). Accessed 2026-09-12.
[^129]: golang/sync. [BSD-3-Clause](https://github.com/golang/sync/blob/v0.23.0/LICENSE). Accessed 2026-09-12.
[^130]: pkg.go.dev. [v0.16.0](https://pkg.go.dev/golang.org/x/time@v0.16.0). Accessed 2026-09-12.
[^131]: golang/time. [go.mod](https://github.com/golang/time/blob/v0.16.0/go.mod). Accessed 2026-09-12.
[^132]: golang/time. [BSD-3-Clause](https://github.com/golang/time/blob/v0.16.0/LICENSE). Accessed 2026-09-12.
[^133]: cenkalti/backoff. [go.mod](https://github.com/cenkalti/backoff/blob/v7.0.0/go.mod). Accessed 2026-09-12.
[^134]: cenkalti/backoff. [MIT](https://github.com/cenkalti/backoff/blob/v7.0.0/LICENSE). Accessed 2026-09-12.
[^135]: spf13/cobra. [v1.10.2 release](https://github.com/spf13/cobra/releases/tag/v1.10.2). Accessed 2026-09-12.
[^136]: spf13/cobra. [go.mod](https://github.com/spf13/cobra/blob/v1.10.2/go.mod). Accessed 2026-09-12.
[^137]: spf13/cobra. [Apache-2.0](https://github.com/spf13/cobra/blob/v1.10.2/LICENSE.txt). Accessed 2026-09-12.
[^138]: urfave/cli. [v3.11.0 release](https://github.com/urfave/cli/releases/tag/v3.11.0). Accessed 2026-09-12.
[^139]: urfave/cli. [go.mod](https://github.com/urfave/cli/blob/v3.11.0/go.mod). Accessed 2026-09-12.
[^140]: urfave/cli. [MIT](https://github.com/urfave/cli/blob/v3.11.0/LICENSE). Accessed 2026-09-12.
[^141]: riverqueue/river. [v0.47.0 release](https://github.com/riverqueue/river/releases/tag/v0.47.0). Accessed 2026-09-12.
[^142]: riverqueue/river. [go.mod](https://github.com/riverqueue/river/blob/v0.47.0/go.mod). Accessed 2026-09-12.
[^143]: riverqueue/river. [MPL-2.0](https://github.com/riverqueue/river/blob/v0.47.0/LICENSE). Accessed 2026-09-12.
[^144]: temporalio/sdk-go. [v1.48.0 release](https://github.com/temporalio/sdk-go/releases/tag/v1.48.0). Accessed 2026-09-12.
[^145]: temporalio/sdk-go. [go.mod](https://github.com/temporalio/sdk-go/blob/v1.48.0/go.mod). Accessed 2026-09-12.
[^146]: temporalio/sdk-go. [MIT](https://github.com/temporalio/sdk-go/blob/v1.48.0/LICENSE). Accessed 2026-09-12.
[^147]: hibiken/asynq. [v0.26.0 release](https://github.com/hibiken/asynq/releases/tag/v0.26.0). Accessed 2026-09-12.
[^148]: hibiken/asynq. [go.mod](https://github.com/hibiken/asynq/blob/v0.26.0/go.mod). Accessed 2026-09-12.
[^149]: hibiken/asynq. [MIT](https://github.com/hibiken/asynq/blob/v0.26.0/LICENSE). Accessed 2026-09-12.
[^150]: go-git/go-git. [Release](https://github.com/go-git/go-git/releases/tag/v5.19.2). Accessed 2026-09-12.
[^151]: go-git/go-git. [pinned compatibility](https://github.com/go-git/go-git/blob/v5.19.2/COMPATIBILITY.md). Accessed 2026-09-12.
[^152]: go-git/go-git. [license](https://github.com/go-git/go-git/blob/v5.19.2/LICENSE). Accessed 2026-09-12.
[^153]: go-git/go-git. [Pinned module](https://github.com/go-git/go-git/blob/v5.19.2/go.mod). Accessed 2026-09-12.
[^154]: pkg.go.dev. [Current os.Root reference](https://pkg.go.dev/os@go1.27.1#Root). Accessed 2026-09-12.
[^155]: go.dev. [design rationale](https://go.dev/blog/osroot). Accessed 2026-09-12.
[^156]: spf13/afero. [Maintainer project](https://github.com/spf13/afero). Accessed 2026-09-12.
[^157]: go.dev. [Go vulnerability management](https://go.dev/doc/security/vuln/). Accessed 2026-09-12.

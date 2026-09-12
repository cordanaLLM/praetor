# Bug Ledger

> Discoveries made while coding that should not distract from the active task.

| ID | Title | Severity | Status | Location | Resolution |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `BUG-001` | [F212] SyncToBacklog deletes every '###' ledger block following the Active Milestones section (live data loss in .workingdir/BACKLOG.md) | p0 | resolved | internal/milestone/milestone.go:182 | Verified F212 fixed by c9d4d28; TestMilestone_Boundary_SyncPreservesTrailingLedgerBlocks passes with race, plus 15 fresh-CLI fixture commands preserving historical and newly archived headings (closure-F212 evidence). |
| `BUG-002` | [F10] adopt --force silently discards every AGENTS.md without a '\n---\n' separator (including praetor's own) | p1 | open | internal/adopt/adopt.go:877 |  |
| `BUG-003` | [F100] issue reconcile --dry-run=false strips every label from each unblocked issue | p1 | open | cmd/standardsctl/issue.go:126 |  |
| `BUG-004` | [F12] reconcileEditors overwrites user IDE configs on every live adopt run, ignoring Force | p1 | open | internal/adopt/adopt.go:1002 |  |
| `BUG-005` | [F13] Scaffolded Makefile 'verify-all' target is an echo no-op | p1 | open | internal/adopt/adopt.go:1039 |  |
| `BUG-006` | [F132] ScanRepo fallback fabricates a Go/golusoris manifest (go_version 1.27, score 100) for non-Go repos and masks the primary analyzer error | p1 | open | internal/needs/extract.go:24 |  |
| `BUG-007` | [F134] shouldSkipDir skips the walk root when repoPath is "." so default CLI invocations never scan Go sources | p1 | open | internal/needs/extract.go:118 |  |
| `BUG-008` | [F165] UpdateIssue replaces the issue's entire label set; unblock transition wipes all other labels | p1 | open | internal/forge/github.go:414 |  |
| `BUG-009` | [F169] PR checklist gate: 'Ed25519 Exit-0 Receipt' is satisfied by any fenced code block or the literal phrase; no signature is verified | p1 | open | internal/forge/pr.go:77 |  |
| `BUG-010` | [F182] ReconcileProtection POSTs a ruleset that omits required_status_checks and never updates an existing one; sync reports 'synchronized' while C | p1 | open | internal/forge/github.go:152 |  |
| `BUG-011` | [F192] project add fabricates a local success record and prints [PASS] when the remote gh call fails | p1 | open | internal/forge/project.go:92 |  |
| `BUG-012` | [F218] project_test.go resolves a real GitHub token from env/`gh` and can mutate a live Projects board | p1 | open | internal/forge/project_test.go:24 |  |
| `BUG-013` | [F22] Scaffolded anti-evasion hook is a no-op: lefthook invokes it with no arguments, so no pattern is ever checked | p1 | open | internal/adopt/adopt.go:591 |  |
| `BUG-014` | [F229] SSE transport does not implement the MCP SSE protocol: responses go to the POST body, never onto the event stream | p1 | open | cmd/standards-mcp/server.go:802 |  |
| `BUG-015` | [F23] Adopt follows repo-committed symlinks on read-modify-write of AGENTS.md/README.md/Makefile, corrupting arbitrary user-writable files | p1 | open | internal/adopt/adopt.go:865 |  |
| `BUG-016` | [F233] HTTP/SSE JSON-RPC endpoints accept unauthenticated cross-origin POSTs (CSRF into mutating tools) | p1 | open | cmd/standards-mcp/server.go:735 |  |
| `BUG-017` | [F263] HISS-04 cyclomatic/cognitive caps are documented but no gate computes them; the repo's own code breaches them widely (scanner's ShouldIgnore | p1 | open | internal/hiss/hiss.go:99 |  |
| `BUG-018` | [F27] Every gosec invocation excludes G104, so HISS-07 "all errors handled" has zero enforcement anywhere in the gate chain | p1 | open | Makefile:62 |  |
| `BUG-019` | [F280] dogfood --verify-only never fails on a failed audit; exit status ignores ContextSync/SelfAudit results | p1 | open | internal/dogfood/dogfood.go:168 |  |
| `BUG-020` | [F297] DeduplicateSkills RemoveAll deletes any non-gemini-config duplicate (repo-local, ~/.claude, ~/.codex, ~/.copilot, ~/.agents), not just gemin | p1 | open | internal/harvester/skills.go:169 |  |
| `BUG-021` | [F302] Modernization score is inverted by network state: online scan counts only outdated modules as TotalScanned | p1 | open | internal/bump/audit.go:49 |  |
| `BUG-022` | [F319] Fallback pivot on zero candidates turns every dependency into a phantom upgrade candidate for scan/update/train | p1 | open | internal/bump/scan_go.go:262 |  |
| `BUG-023` | [F325] fallbackGoModEdit corrupts go.mod when CurrentVersion is empty and swallows the go get failure | p1 | open | internal/bump/update.go:248 |  |
| `BUG-024` | [F340] adopt unconditionally overwrites existing IDE configuration, including JetBrains .idea/workspace.xml | p1 | open | internal/editor/editor.go:820 |  |
| `BUG-025` | [F350] devcontainer.Verify is blind to every JSON key outside its 7-field struct, so "100% in sync" passes on an injected initializeCommand/runArgs | p1 | open | internal/devcontainer/devcontainer.go:300 |  |
| `BUG-026` | [F357] Unsanitized dependency version in distilled-doc filename escapes the output directory (or aborts the whole sync) | p1 | open | internal/docdistill/cache.go:134 |  |
| `BUG-027` | [F384] `flavor apply --force` overwrites .workingdir ledgers and .standards.yaml with a one-line stub | p1 | open | internal/flavor/scaffold.go:55 |  |
| `BUG-028` | [F386] Flavor scaffolding writes comment-only placeholders for workflows, manifests and agent harnesses, and the flavor audit then scores them 100% | p1 | open | internal/flavor/scaffold.go:100 |  |
| `BUG-029` | [F388] flavor apply scaffolds CI, security and standards files as one-line comment stubs that the flavor audit then scores as compliant | p1 | open | internal/flavor/scaffold.go:100 |  |
| `BUG-030` | [F394] `state task complete <n>` marks the wrong task done once OPEN.md contains a completed entry | p1 | open | internal/state/tasks.go:112 |  |
| `BUG-031` | [F4] adopt silently overwrites an existing .git/hooks/pre-commit when `lefthook install` fails | p1 | open | internal/adopt/adopt.go:518 |  |
| `BUG-032` | [F40] HISS-13 debt ratchet is not monotonic: touched-file rule never evaluated, --record overwrites freely, no CI guard on baseline growth | p1 | open | cmd/standardsctl/audit.go:113 |  |
| `BUG-033` | [F422] WriteHarness follows symlinks in the target repo: hostile repo + manifest overwrites arbitrary user files with attacker-controlled lines | p1 | open | internal/paperclip/harness.go:106 |  |
| `BUG-034` | [F467] Transpiler never reads AGENTS.md content: all six vendor files are Go string constants, so compile-context --verify cannot detect context dr | p1 | open | internal/compiler/transpiler.go:140 |  |
| `BUG-035` | [F480] gc deletes worktrees by top-level directory mtime, destroying actively edited worktrees with uncommitted work | p1 | open | internal/gc/gc.go:170 |  |
| `BUG-036` | [F483] sentinel fabricates 16 GB/8 GB host memory and zero load with nil error when /proc is unavailable, so HEALTHY/APPROVED verdicts are fictiona | p1 | open | internal/sentinel/sentinel.go:211 |  |
| `BUG-037` | [F541] UniversalBuilder compiles nothing; Success is hardcoded and the CLI prints "[PASS] Successfully compiled N target(s)" | p1 | open | internal/builder/builder.go:133 |  |
| `BUG-038` | [F544] `flavors sync` force-moves every flavor tag (including lts) to HEAD because PlanTransitions ignores tag_pattern and source_ref | p1 | open | internal/flavors/flavors.go:52 |  |
| `BUG-039` | [F573] Ed25519 Exit-0 receipt requirement is satisfied by the PR template's own placeholder text | p1 | open | .github/pull_request_template.md:58 |  |
| `BUG-040` | [F61] Exit-0 receipt is signed with an ephemeral per-run keypair and is unverifiable | p1 | open | internal/gating/pipeline.go:191 |  |
| `BUG-041` | [F628] Declared branch protection is never verified against the committed ruleset, which allows 0 approvals and no signature rule | p1 | open | .standards.yaml:36 |  |
| `BUG-042` | [F635] Scaffolded Makefile's `verify-all` is `@echo` — the gate every other emitted artifact points at does nothing | p1 | open | internal/adopt/adopt.go:1039 |  |
| `BUG-043` | [F646] Ed25519 Exit-0 receipt gating is unimplemented: the receipt is gitignored, never read, and self-signed with a per-run ephemeral key | p1 | open | <agent-copies>/praetor-gatekeeper.md:25 |  |
| `BUG-044` | [F649] compile-context does not read AGENTS.md: all six vendor targets are hard-coded string literals | p1 | open | internal/compiler/transpiler.go:140 |  |
| `BUG-045` | [F655] Documented HISS-16 'Lattice Engine' archetype/facet resolution is dead code | p1 | open | docs/guides/archetype-authoring.md:10 |  |
| `BUG-046` | [F660] ADR's 'Ed25519 Exit-0 receipt' PR-governance requirement is enforced only by literal substring matching, no signature verification | p1 | open | docs/adr/README.md:25 |  |
| `BUG-047` | [F666] HISS compliance matrix advertises gocyclo/gocognit/gitleaks/semgrep/heap-alloc-sweep enforcement that is not wired into the repo anywhere | p1 | open | docs/wiki/HISS-Matrix.md:10 |  |
| `BUG-048` | [F67] Fleet epic publish passes a repo NAME where a path is expected, resolving owner/repo from cwd-relative heuristics | p1 | open | cmd/standardsctl/needs.go:293 |  |
| `BUG-049` | [F675] Archetype/facet lattice is never loaded; config.Join is unreachable and `plan` silently uses the lax DefaultPolicy | p1 | open | cmd/standardsctl/plan.go:68 |  |
| `BUG-050` | [F73] gc --worktrees-dir RemoveAll's any worktree whose top-level dir mtime is older than --max-age, with no git-status check and dry-run off by d | p1 | open | cmd/standardsctl/gc.go:14 |  |
| `BUG-051` | [F74] needs epic --publish resolves the forge owner/repo from untrusted repo content (go.mod module path, .standards.yaml) and, in fleet mode, fro | p1 | open | cmd/standardsctl/needs.go:293 |  |
| `BUG-052` | [F78] harvest ingest: manifest RelativePath traverses out of the skills dir and overwrites ~/.gemini/config/hooks.json / mcp_config.json / GEMINI. | p1 | open | internal/harvester/bundle.go:615 |  |
| `BUG-053` | [F81] harvest skills --dedupe deletes same-named skills from Claude/Copilot/Codex roots and the repo-local .agents/skills, not just shadowed Gemin | p1 | open | internal/harvester/skills.go:166 |  |
| `BUG-054` | [F85] updateGoMod substring-drops go.mod lines using dependency names from every ecosystem; can delete the module directive and rewrite unrelated | p1 | open | internal/needs/migrate.go:171 |  |
| `BUG-055` | [F87] topology clean removes the .git directory (and docs/, .github/, .config/ ...) of an org container that is itself a valid repo | p1 | open | internal/topology/topology.go:270 |  |
| `BUG-056` | [F88] topology clean deletes a live org-container .git (valid HEAD) whenever sibling child repos exist | p1 | open | internal/topology/topology.go:276 |  |
| `BUG-057` | [F104] HealthServer.Start() reports success when the listener never binds | p2 | open | internal/container/health.go:91 |  |
| `BUG-058` | [F108] `bump canary`'s flag-reordering loop mis-binds space-separated flag values, swapping package and version | p2 | open | cmd/standardsctl/bump.go:346 |  |
| `BUG-059` | [F11] Context-compilation and workingdir/flavor failures are downgraded to report.Errors that no caller reads; CLI prints success | p2 | open | internal/adopt/adopt.go:923 |  |
| `BUG-060` | [F110] Five I/O paths in cmd/standardsctl run on a deadline-free context, defeating util.RunCommand's own nil-ctx fallback | p2 | open | cmd/standardsctl/docs.go:81 |  |
| `BUG-061` | [F111] Documented `[dir] [--flag=...]` subcommand forms silently ignore their flags | p2 | open | cmd/standardsctl/flavor.go:88 |  |
| `BUG-062` | [F112] `hindsight sync --offline` reports N facts "Synced ... successfully" although no request is made | p2 | open | cmd/standardsctl/hindsight.go:310 |  |
| `BUG-063` | [F114] Fleet coverage ignores the framework repo entirely; --framework is inert and report.Framework is a constant | p2 | open | internal/needs/aggregate.go:26 |  |
| `BUG-064` | [F115] filepath.Walk callbacks ignore context cancellation (return nil, keep walking) | p2 | open | internal/needs/aggregate.go:70 |  |
| `BUG-065` | [F117] Fleet demand report prints "Zero gaps / 100% coverage" when every repo scan failed | p2 | open | internal/needs/aggregate.go:122 |  |
| `BUG-066` | [F119] MatchPackage prefix match has no path-boundary check, so unrelated modules inherit a catalog mapping | p2 | open | internal/needs/catalog.go:305 |  |
| `BUG-067` | [F123] RegenerateFleetEpics swallows every per-repo error; the CLI reports [PASS] with exit 0 after writing nothing | p2 | open | internal/needs/epic.go:234 |  |
| `BUG-068` | [F131] Repos discovered only via .standards.yaml get a fabricated "go, 100% ready" leaderboard entry | p2 | open | internal/needs/extract.go:19 |  |
| `BUG-069` | [F133] Cancelled/expired context makes the AST walk silently return an empty, successful result (100% readiness) | p2 | open | internal/needs/extract.go:87 |  |
| `BUG-070` | [F136] loadExistingDeclarations overwrites freshly computed Capabilities with the stale .needs.yaml, so Required never updates after the first --wr | p2 | open | internal/needs/extract.go:140 |  |
| `BUG-071` | [F137] Go dependency count double-counts a module when a subpackage is imported; fixture is crafted to coincide module and import paths | p2 | open | internal/needs/extract.go:167 |  |
| `BUG-072` | [F138] Unmatched dependencies are keyed by the last path segment, merging unrelated modules into "custom.vN" | p2 | open | internal/needs/extract.go:186 |  |
| `BUG-073` | [F140] Manifest line-parsers miss common syntax forms (single-line pyproject list, Cargo workspace/dev/inline tables, capitalised CMake packages, s | p2 | open | internal/needs/analyzer_python.go:123 |  |
| `BUG-074` | [F144] HISS-07 breaches in internal/needs are invisible to the repo's own scanner and CI (no golangci-lint step; HISS-07 rule matches only "_ = " a | p2 | open | internal/needs/migrate.go:107 |  |
| `BUG-075` | [F145] TestScanRepoManifestPersistence cannot fail: the asserted score is recomputed from go.mod and is independent of the manifest it claims to te | p2 | open | internal/needs/needs_test.go:122 |  |
| `BUG-076` | [F146] TestPlanAndApplyMigration shells out to git and `go mod tidy` (network in CI) and swallows their outcome | p2 | open | internal/needs/needs_test.go:179 |  |
| `BUG-077` | [F149] ApplyMigration runs `git checkout -B`, silently resetting an existing adoption branch and orphaning its commits | p2 | open | internal/needs/migrate.go:106 |  |
| `BUG-078` | [F15] Documented HISS-02/04/07/10 breaches in internal/adopt are invisible to the repo's own scanner and gates | p2 | open | internal/adopt/adopt.go:138 |  |
| `BUG-079` | [F150] Import rewrite follows file symlinks and writes outside the target repository | p2 | open | internal/needs/migrate.go:147 |  |
| `BUG-080` | [F151] HISS-10 gate runs only go vet; configured staticcheck/errcheck never execute so this unit's warnings persist | p2 | open | .golangci.yml:3 |  |
| `BUG-081` | [F152] CMake native dependency lookup is case-sensitive so canonical find_package(CUDA)/find_package(Vulkan) are classified as gaps; parseCMakeList | p2 | open | internal/needs/analyzer_native.go:108 |  |
| `BUG-082` | [F154] CI skips the PR checklist gate entirely when the PR body is empty | p2 | open | .github/workflows/ci.yml:80 |  |
| `BUG-083` | [F155] HISS-15 is advertised as a 'CI coverage gate' but nothing in CI or the HISS scanner measures coverage or 3D test presence | p2 | open | .github/workflows/ci.yml:97 |  |
| `BUG-084` | [F157] standardsctl sync creates branch-protection rulesets on hard-coded cordanaLLM/praetor when the manifest lacks repository coordinates | p2 | open | internal/forge/github.go:56 |  |
| `BUG-085` | [F158] repoPath silently targets cordanaLLM/praetor when the manifest omits repository owner/name and GITHUB_REPOSITORY is unset | p2 | open | internal/forge/github.go:65 |  |
| `BUG-086` | [F159] ReconcileProtection is create-only: every sync POSTs a new ruleset and ignores linear-history/signed-commit policy fields | p2 | open | internal/forge/github.go:179 |  |
| `BUG-087` | [F164] ListIssues reads only the first 100 issues (no pagination) and includes pull requests; reconciler treats missing prerequisites as open | p2 | open | internal/forge/github.go:351 |  |
| `BUG-088` | [F17] Any repository whose directory is named 'dev' is refused as the workstation root | p2 | open | internal/adopt/validate.go:64 |  |
| `BUG-089` | [F170] HISS-14 Migration-footer enforcement exists only as unreachable library code (AnalyzeCommit) | p2 | open | internal/forge/pr.go:181 |  |
| `BUG-090` | [F174] PR governance checklist gate is skipped entirely when the PR body is empty | p2 | open | .github/workflows/ci.yml:80 |  |
| `BUG-091` | [F176] Documentation Integrity Audit step silently passes without auditing anything | p2 | open | .github/workflows/ci.yml:95 |  |
| `BUG-092` | [F18] ValidateAdoptionTarget rejects leaf repos that contain git submodules | p2 | open | internal/adopt/validate.go:117 |  |
| `BUG-093` | [F181] repoPath silently targets hard-coded cordanaLLM/praetor for mutating API calls when owner/repo are unresolved | p2 | open | internal/forge/github.go:65 |  |
| `BUG-094` | [F184] Docs advertise `standardsctl sync` reconciles GitHub label taxonomies; ReconcileLabels has no callers and sync only writes a local YAML file | p2 | open | internal/forge/github.go:190 |  |
| `BUG-095` | [F185] Ed25519 'Exit-0 receipt' PR gate is satisfied by any fenced code block or the literal words 'Receipt Signature' | p2 | open | internal/forge/pr.go:68 |  |
| `BUG-096` | [F186] HISS-15 coverage gaps in internal/forge (this unit's files): most exported driver methods have no positive, negative, or boundary test | p2 | open | internal/forge/github.go:136 |  |
| `BUG-097` | [F187] GitHub driver ships a 'test-' token-prefix stub mode in production; tests exercise only that branch, real HTTP paths are 0% covered | p2 | open | internal/forge/github.go:148 |  |
| `BUG-098` | [F188] Shipped PR template checkboxes can never satisfy the HISS-15 grep; gate is failed-by-default or bypassed with custom bodies | p2 | open | internal/forge/pr.go:92 |  |
| `BUG-099` | [F191] ListProjects remote path overwrites project.json with item-less projects, wiping every locally tracked item | p2 | open | internal/forge/project.go:76 |  |
| `BUG-100` | [F193] Remote project failures are swallowed and reported as success with a fabricated item ID | p2 | open | internal/forge/project.go:99 |  |
| `BUG-101` | [F196] appendItemToCache silently discards a corrupt or unreadable project cache and overwrites it | p2 | open | internal/forge/project.go:207 |  |
| `BUG-102` | [F2] Caller context is discarded for all subprocess and scan I/O: git identity lookups run with context.Background() (no timeout) and MCP/CLI dea | p2 | open | internal/adopt/adopt.go:174 |  |
| `BUG-103` | [F200] Dependency owner is discarded and any '*/repo' suffix match is accepted, so a cross-org dependency resolves against the wrong repository's i | p2 | open | internal/forge/reconciler.go:182 |  |
| `BUG-104` | [F201] parseSingleDepStr strips 'Depends-On:' case-sensitively while the producer regex is case-insensitive; lowercase tags are never resolved and | p2 | open | internal/forge/reconciler.go:199 |  |
| `BUG-105` | [F206] `gh auth token` is executed without any timeout, bypassing the 2-minute CLI context | p2 | open | internal/milestone/forge.go:30 |  |
| `BUG-106` | [F208] Milestone numbering diverges between local store and GitHub: PublishMilestone's Number update is never persisted and mergeRemoteMilestones c | p2 | open | internal/milestone/forge.go:106 |  |
| `BUG-107` | [F21] Adopt activates repo-controlled hook execution: pre-existing lefthook.yml is installed unreviewed and the fallback hook executes ./bin/stand | p2 | open | internal/adopt/adopt.go:466 |  |
| `BUG-108` | [F211] CloseMilestone numeric selector falls through to substring match and closes the wrong milestone | p2 | open | internal/milestone/milestone.go:124 |  |
| `BUG-109` | [F215] Security gates that would catch this unit's issues are excluded or not executed | p2 | open | .gosec.json:3 |  |
| `BUG-110` | [F217] Ledger and doc writes follow symlinks shipped by a hostile repository | p2 | open | internal/milestone/milestone.go:266 |  |
| `BUG-111` | [F220] GenerateWiki derives the repo name from `filepath.Base(repoRoot)`, but the only caller passes "." so every generated Home.md says cordanaLLM | p2 | open | internal/forge/wiki.go:42 |  |
| `BUG-112` | [F221] standards_audit claims '100% Compliance with HISS-16 baseline' without running the HISS scanner or the other checks standardsctl audit perfo | p2 | open | cmd/standards-mcp/server.go:164 |  |
| `BUG-113` | [F222] HISS-04 breach in inspectSingleFile is invisible to every repo gate (golangci config has no complexity linters, CI lint step is `go vet`, hi | p2 | open | cmd/standards-mcp/server.go:432 |  |
| `BUG-114` | [F223] standards_plan always reports 'Local state matches declared policy. No changes required.' without checking anything | p2 | open | cmd/standards-mcp/server.go:205 |  |
| `BUG-115` | [F224] resolvePath double-joins a relative rootDir whenever the default is s.rootDir | p2 | open | cmd/standards-mcp/server.go:382 |  |
| `BUG-116` | [F225] standards_inspect_symbols labels functions PASS while only counting LOC and top-level statements; cyclomatic/cognitive bounds it advertises | p2 | open | cmd/standards-mcp/server.go:455 |  |
| `BUG-117` | [F228] HTTP/SSE WriteTimeout of 30 s is shorter than every long-running tool's budget; results are silently dropped after side effects have been ap | p2 | open | cmd/standards-mcp/server.go:768 |  |
| `BUG-118` | [F235] HTTP and SSE transport tests exercise handlers written inside the test, never RunHTTP/RunSSE/RunStdio | p2 | open | cmd/standards-mcp/server_test.go:278 |  |
| `BUG-119` | [F236] Six MCP tool-call tests assert only the JSON-RPC envelope, which no registered handler can populate | p2 | open | cmd/standards-mcp/server_test.go:401 |  |
| `BUG-120` | [F239] Network and file I/O invoked with no deadline: version_audit/needs_report/hindsight_optimize pass the raw signal ctx, audit/plan/compile/ins | p2 | open | cmd/standards-mcp/tools_docs.go:82 |  |
| `BUG-121` | [F242] HISS-15 gaps in cmd/standards-mcp: 4 of 13 tools, all mutating paths and all request-boundary cases untested | p2 | open | cmd/standards-mcp/server.go:82 |  |
| `BUG-122` | [F246] FuzzLSPHandleMessage shares one Server across inputs; after the 'shutdown' seed every input short-circuits at 'Server is shutdown' | p2 | open | cmd/standards-lsp/fuzz_test.go:17 |  |
| `BUG-123` | [F247] LSP shutdown response omits required `result`; vscode-jsonrpc rejects it as invalid, so client shutdown never resolves | p2 | open | cmd/standards-lsp/server.go:146 |  |
| `BUG-124` | [F249] HISS-01 recursion check matches any selector call whose method name equals the enclosing function, flagging non-recursive wrappers | p2 | open | cmd/standards-lsp/server.go:403 |  |
| `BUG-125` | [F251] HISS-07 blank-identifier check flags every multi-assign with `_` (e.g. `_, err := os.Stat`) and has no test-file exemption | p2 | open | cmd/standards-lsp/server.go:501 |  |
| `BUG-126` | [F252] collectASTNodes silently truncates analysis at 5000 nodes; internal/adopt/adopt.go already exceeds it | p2 | open | cmd/standards-lsp/server.go:548 |  |
| `BUG-127` | [F253] Daemon read loop has no context-aware I/O; NotifyContext swallows SIGINT/SIGTERM so an idle server cannot be signalled to exit | p2 | open | cmd/standards-lsp/server.go:573 |  |
| `BUG-128` | [F254] maxLSPMessageSize is decorative: ReadString fallback and oversize Content-Length path read unbounded bytes | p2 | open | cmd/standards-lsp/server.go:610 |  |
| `BUG-129` | [F257] hiss.Scan returns an empty, error-free report for a nonexistent or unreadable repo root | p2 | open | internal/hiss/hiss.go:64 |  |
| `BUG-130` | [F259] Scanner silently exempts any directory named model/build*/target/compat/harvest/testdata from all HISS rules in every scanned repo | p2 | open | internal/hiss/hiss.go:104 |  |
| `BUG-131` | [F261] hiss.ScanOptions.Timeout and DefaultScanTimeout are never applied; audit and baseline walk the repo with context.Background() | p2 | open | internal/hiss/hiss.go:52 |  |
| `BUG-132` | [F266] HISS-15 gaps in internal/hiss: no negative or boundary test for Scan, ShouldIgnoreDir untested, 3 of 4 language scanners stub-tolerant | p2 | open | internal/hiss/hiss_test.go:102 |  |
| `BUG-133` | [F269] Python HISS-04 LOC counts trailing blank lines into the function and stops at the first nested def | p2 | open | internal/hiss/rules.go:104 |  |
| `BUG-134` | [F272] HISS-09 scanner flags every Rust `unsafe` regardless of the documented `// SAFETY:` exemption, does not scan Go `unsafe` at all, and files P | p2 | open | internal/hiss/rules.go:250 |  |
| `BUG-135` | [F274] standards-lsp AST walk caps at 5000 nodes, so HISS-02/07 checks silently stop on larger files | p2 | open | cmd/standards-lsp/server.go:548 |  |
| `BUG-136` | [F275] hiss.Scan follows file symlinks out of repoPath, bypassing the 2 MiB size bound (G304 path) | p2 | open | internal/hiss/hiss.go:87 |  |
| `BUG-137` | [F276] HISS-15 gaps in cmd/standards-lsp: HISS-02 detection has no positive test, transport negative paths untested, several checks stub-tolerant | p2 | open | cmd/standards-lsp/server_test.go:84 |  |
| `BUG-138` | [F279] Directory-read errors turned into nil: mistyped --targets/--brain produce empty success results | p2 | open | internal/dogfood/dogfood.go:81 |  |
| `BUG-139` | [F28] verify-all gate audits the developer's live $HOME/dev tree, making the harness result machine-dependent | p2 | open | Makefile:78 |  |
| `BUG-140` | [F281] RunDogfood reports [SUCCESS] regardless of target/remote adoption results, and a mistyped --targets dir is silently treated as zero targets | p2 | open | internal/dogfood/dogfood.go:196 |  |
| `BUG-141` | [F282] internal/dogfood tests depend on the real repo root, the developer's $HOME and outbound network git clone | p2 | open | internal/dogfood/dogfood_test.go:15 |  |
| `BUG-142` | [F284] cloneEphemeralRepo timeout not enforced on pipe wait: CombinedOutput without WaitDelay blocks on orphaned git-remote-https | p2 | open | internal/dogfood/remote.go:69 |  |
| `BUG-143` | [F285] Remote dogfood grades a repo 'A' when hiss.Scan fails (error swallowed, infractions default to 0) | p2 | open | internal/dogfood/remote.go:111 |  |
| `BUG-144` | [F288] Bundle silently drops nested skill subdirectories (references/, scripts/, assets/) and vault depth>2 | p2 | open | internal/harvester/bundle.go:125 |  |
| `BUG-145` | [F29] The anti-direct-merge gating pipeline (HISS-18) runs only in a local pre-push hook, never in CI or make verify-all | p2 | open | Makefile:82 |  |
| `BUG-146` | [F290] IngestBundle never verifies manifest SHA256; ValidIntegrity is true regardless of file contents | p2 | open | internal/harvester/bundle.go:662 |  |
| `BUG-147` | [F292] ScanLocalWorkstation reports every directory under *-worktrees as a stale worktree without any staleness check | p2 | open | internal/harvester/harvester.go:182 |  |
| `BUG-148` | [F295] Onboarding swallows transpile/editor errors while the returned plan asserts those actions were performed | p2 | open | internal/harvester/onboard.go:119 |  |
| `BUG-149` | [F298] AGENTS.md advertises a 'CI coverage gate' for HISS-15 but no coverage measurement or threshold exists in CI, Makefile or the audit command | p2 | open | AGENTS.md:29 |  |
| `BUG-150` | [F299] HISS-15 gaps in internal/dogfood: target-adoption loop, remote loop, VerifyOnly, report writing and DryRun are untested | p2 | open | internal/dogfood/dogfood.go:101 |  |
| `BUG-151` | [F3] Fallback pre-commit hook is written to .git/hooks even when core.hooksPath redirects hooks, and is silently skipped for worktree/gitlink che | p2 | open | internal/adopt/adopt.go:500 |  |
| `BUG-152` | [F300] HISS-15 gaps in internal/harvester: ExtractMemoryInsights has zero tests; five exported functions lack negative and/or boundary tests | p2 | open | internal/harvester/memory.go:27 |  |
| `BUG-153` | [F303] bump audit toolchain check reports missing tools only when `go` is also missing | p2 | open | internal/bump/audit.go:88 |  |
| `BUG-154` | [F306] bump 'positive' scan tests exec PATH go/pnpm with forced network proxy and no deadline; they only ever exercise the offline fallback | p2 | open | internal/bump/bump_test.go:54 |  |
| `BUG-155` | [F307] Tests exercise only the offline fallback and dry-run paths; the online scanner, applyGoUpdate and real canary are untested and the phantom-c | p2 | open | internal/bump/bump_test.go:59 |  |
| `BUG-156` | [F308] Stub-passable tests: TestRunCanary_Positive_DryRun and the three *_NilContext tests assert only guard lines; boundary test passes vacuously | p2 | open | internal/bump/bump_test.go:113 |  |
| `BUG-157` | [F309] Bump train misreports deadline-killed canaries as candidate breakage and stages a patch stub | p2 | open | internal/bump/canary.go:93 |  |
| `BUG-158` | [F311] Canary 'adaptation patch' is a log file that git apply always rejects, so bump apply --patch cannot succeed | p2 | open | internal/bump/canary.go:114 |  |
| `BUG-159` | [F313] RunCanary defaults the test command to `go test -v ./...` regardless of manifest type; Node candidates are always reported as breakage | p2 | open | internal/bump/canary.go:189 |  |
| `BUG-160` | [F315] ApplyBump applies the canary's staged 'patch' with git apply, but the staged file is not a diff; go.mod is mutated before the failure | p2 | open | internal/bump/canary.go:222 |  |
| `BUG-161` | [F316] ReconcileCatalog produces downgrade candidates whenever a repo is newer than the hardcoded FleetCatalog; unify --apply executes them | p2 | open | internal/bump/catalog.go:204 |  |
| `BUG-162` | [F318] bump scan overrides GOPROXY with proxy.golang.org, breaking corporate-proxy setups and leaking private module paths | p2 | open | internal/bump/scan_go.go:130 |  |
| `BUG-163` | [F32] HISS-15 gaps in internal/adopt exported surface and untested option branches | p2 | open | internal/adopt/templates.go:25 |  |
| `BUG-164` | [F322] UpdateAll reports success after go get failure leaves go.mod textually edited and tidy errors swallowed | p2 | open | internal/bump/update.go:40 |  |
| `BUG-165` | [F324] UpdateAll discards every per-candidate and tidy error and always returns nil, so `bump update --all` / `bump unify --apply` print success on | p2 | open | internal/bump/update.go:128 |  |
| `BUG-166` | [F326] applyNodeUpdate rewrites package.json through an unordered map and forces caret ranges; lockfile left inconsistent after pnpm failure | p2 | open | internal/bump/update.go:286 |  |
| `BUG-167` | [F328] Staged 'adaptation patch' is a comment header plus raw test output; `bump apply --patch` cannot apply anything from it | p2 | open | internal/bump/canary.go:116 |  |
| `BUG-168` | [F331] Hostile go.work/pnpm-workspace steers go get, go mod tidy, pnpm update and manifest rewrites outside repoPath | p2 | open | internal/bump/scan_go.go:92 |  |
| `BUG-169` | [F334] devcontainer.Verify is blind to injected top-level keys, so "100% in sync" passes on a tampered devcontainer.json | p2 | open | internal/devcontainer/devcontainer.go:309 |  |
| `BUG-170` | [F337] HISS-04 cyclomatic cap (10) is breached in internal/editor and no configured gate measures cyclomatic complexity at all | p2 | open | internal/editor/editor.go:158 |  |
| `BUG-171` | [F339] editor.Write silently overwrites developer-owned IDE state (.idea/workspace.xml, .vscode/settings.json, .nvim.lua, .dir-locals.el) with no b | p2 | open | internal/editor/editor.go:815 |  |
| `BUG-172` | [F34] audit uses three independent roots (manifest dir, baseline dir, agents dir) | p2 | open | cmd/standardsctl/audit.go:38 |  |
| `BUG-173` | [F341] editor.Verify only checks existence of .editorconfig and .clang-tidy, so "[PASS] verified in sync" is unconditional for those two files | p2 | open | internal/editor/editor.go:869 |  |
| `BUG-174` | [F355] Unbounded network/IO loops with no overall deadline contradict HISS-02 | p2 | open | internal/docdistill/cache.go:116 |  |
| `BUG-175` | [F356] Documentation-coverage audit can never fail after one sync: synthesized stubs count as documented | p2 | open | internal/docdistill/cache.go:163 |  |
| `BUG-176` | [F36] Unbounded context.Background() on repo walks, git and gh subprocesses (HISS-02) | p2 | open | cmd/standardsctl/audit.go:101 |  |
| `BUG-177` | [F360] Token-budget assertions in both packages are unfalsifiable by construction | p2 | open | internal/docdistill/docdistill_test.go:184 |  |
| `BUG-178` | [F362] Go module-cache probe uses the raw module path, which never matches the case-escaped on-disk layout (and ignores GOMODCACHE) | p2 | open | internal/docdistill/harvester.go:47 |  |
| `BUG-179` | [F364] harvestNodePackage reads node_modules relative to the process CWD, ignoring repoPath | p2 | open | internal/docdistill/harvester.go:124 |  |
| `BUG-180` | [F368] hindsight sync reports success for every fact even when nothing was transmitted | p2 | open | internal/hindsight/client.go:83 |  |
| `BUG-181` | [F369] DistillWorkspace swallows all four distiller errors and always returns nil, reporting a successful distillation of zero facts | p2 | open | internal/hindsight/distiller.go:32 |  |
| `BUG-182` | [F37] audit swallows devcontainer Synthesize/Verify errors and still reports 100% compliance | p2 | open | cmd/standardsctl/audit.go:141 |  |
| `BUG-183` | [F371] hindsight: no HTTP test, no corrupt-cache test, distillers never run against a populated repo | p2 | open | internal/hindsight/hindsight_test.go:13 |  |
| `BUG-184` | [F375] ScanDeclaredDependencies discards every manifest-scan error and always returns nil, so a malformed manifest silently yields a vacuous 100% c | p2 | open | internal/docdistill/scanner.go:36 |  |
| `BUG-185` | [F378] SettingItem.Validator is never assigned or called; auditSettings reports 'settings valid' on file existence alone | p2 | open | internal/flavor/audit.go:76 |  |
| `BUG-186` | [F379] Flavor audit pass/fail depends on which binaries happen to be on the auditing machine's PATH | p2 | open | internal/flavor/audit.go:84 |  |
| `BUG-187` | [F381] The flavor scoring tests assert only 0 <= Score <= 100, which the formula makes unfalsifiable | p2 | open | internal/flavor/flavor_test.go:61 |  |
| `BUG-188` | [F383] flavor apply returns success even when every template write failed | p2 | open | internal/flavor/scaffold.go:50 |  |
| `BUG-189` | [F387] Scaffolded .gosec.json ships the security scanner pre-disabled for command injection, path traversal and unhandled errors | p2 | open | internal/flavor/scaffold.go:84 |  |
| `BUG-190` | [F389] State audit and sync swallow every ledger read error via blank identifiers, so an unreadable BUGS.md audits as clean | p2 | open | internal/state/audit.go:49 |  |
| `BUG-191` | [F391] `saveBugs` / `saveQuestions` rewrite the whole ledger file, erasing any human-written content outside the table | p2 | open | internal/state/bugs.go:102 |  |
| `BUG-192` | [F393] `--context` on `state bug add` / `state question add` is silently discarded (never rendered or parsed) | p2 | open | internal/state/bugs.go:131 |  |
| `BUG-193` | [F397] Flavor settings are never validated: SettingItem.Validator is dead and "Settings: N/N valid" only means the file exists | p2 | open | internal/flavor/audit.go:72 |  |
| `BUG-194` | [F400] SyncState drops every git error and writes git's error text into STATE.md as the commit SHA | p2 | open | internal/state/state.go:71 |  |
| `BUG-195` | [F405] Gate security stage passes with nothing executed when govulncheck/gosec are absent, and a receipt still attests ALL_GATED_CHECKS_PASSED | p2 | open | internal/gating/pipeline.go:135 |  |
| `BUG-196` | [F406] Security stage and CI gosec exclude every gosec class that covers this unit's side-effect sites | p2 | open | internal/gating/pipeline.go:141 |  |
| `BUG-197` | [F408] `gate run` HISS stage fails on any infraction and never consults the debt baseline, so adopted brownfield repos can never pass pre-push | p2 | open | internal/gating/pipeline.go:125 |  |
| `BUG-198` | [F409] gate --dry-run skips the test stage yet still emits an ADMITTED receipt identical to a full run, and scan stages run on the dirty tree while | p2 | open | internal/gating/pipeline.go:161 |  |
| `BUG-199` | [F41] No gate detects divergence of the 42 persona copies, and the .agents/plugins copies are regenerated by nothing | p2 | open | cmd/standardsctl/audit.go:132 |  |
| `BUG-200` | [F410] Test-stage worktree cleanup errors are discarded and worktree creation failure silently falls back to testing the live tree | p2 | open | internal/gating/pipeline.go:172 |  |
| `BUG-201` | [F412] Gate stage 1 runs `go mod verify` unconditionally, so every non-Go repository is REJECTED | p2 | open | internal/gating/prefetch.go:35 |  |
| `BUG-202` | [F417] HISS-10 'zero warnings' and HISS-04 complexity caps are not enforced by any gate; this unit carries 15 live lint hits and a cyclomatic-14 fu | p2 | open | internal/paperclip/disposition.go:76 |  |
| `BUG-203` | [F419] `paperclip verify` in_review check fails on its own default disposition path and never checks that anything was pushed | p2 | open | internal/paperclip/verify.go:42 |  |
| `BUG-204` | [F42] audit silently ignores devcontainer drift: Synthesize/Verify errors are dropped and the run still ends in '100% Compliance' | p2 | open | cmd/standardsctl/audit.go:141 |  |
| `BUG-205` | [F424] Gating positive test additionally depends on PATH toolchains, network and a 30 s wall clock (K11 delta) | p2 | open | internal/gating/gating_test.go:59 |  |
| `BUG-206` | [F425] Race-Detector Tests stage (non-dry-run runTestStage) has zero coverage; reachable only via a git-repo fixture or by mutating the real repo | p2 | open | internal/gating/pipeline.go:160 |  |
| `BUG-207` | [F427] HeavyVolumeCapping test never reaches the cap it claims to test | p2 | open | internal/lockdown/lockdown_test.go:154 |  |
| `BUG-208` | [F428] Sanitize boundary assertion is vacuous: literal substrings never present in the input | p2 | open | internal/lockdown/lockdown_test.go:254 |  |
| `BUG-209` | [F43] HISS-13 monotonic ratchet has no guard: baseline --record accepts any increase and CI never compares against the base branch | p2 | open | cmd/standardsctl/baseline.go:38 |  |
| `BUG-210` | [F431] paperclip HISS-15 gaps: Validate field checks, invalid status, harness fallbacks and rules.md content untested | p2 | open | internal/paperclip/disposition.go:87 |  |
| `BUG-211` | [F432] VerifyRun has no positive test; the in_review 'pushing is not shipping' enforcement is 0% covered | p2 | open | internal/paperclip/paperclip_test.go:168 |  |
| `BUG-212` | [F435] astmerge silently discards declarations past 2000 and still reports Clean=true | p2 | open | internal/astmerge/astmerge.go:151 |  |
| `BUG-213` | [F436] Grouped const/var/type blocks are duplicated in merged output and produce false conflicts | p2 | open | internal/astmerge/astmerge.go:185 |  |
| `BUG-214` | [F438] astmerge silently drops build constraints, package docs and free-floating comments | p2 | open | internal/astmerge/astmerge.go:491 |  |
| `BUG-215` | [F44] compile-context drops the CompileAgents error and still prints success | p2 | open | cmd/standardsctl/compile_context.go:48 |  |
| `BUG-216` | [F443] buildInvocation synthesizes arguments from type flags, not from the parameter list, so calls have wrong arity | p2 | open | internal/difftest/difftest.go:164 |  |
| `BUG-217` | [F444] Change detection and function selection key on the bare function name, ignoring the receiver | p2 | open | internal/difftest/difftest.go:254 |  |
| `BUG-218` | [F445] Synthesized test files never compile: fmt used without import, context/strings imported unconditionally | p2 | open | internal/difftest/difftest.go:294 |  |
| `BUG-219` | [F446] Synthesized tests assign the wrong number of values for the target's actual return signature | p2 | open | internal/difftest/difftest.go:354 |  |
| `BUG-220` | [F45] gate --json exits 0 on a REJECTED pipeline (rejection check skipped on JSON path) | p2 | open | cmd/standardsctl/gate.go:35 |  |
| `BUG-221` | [F455] Memory-stability stress test cannot fail in practice and misreports nil errors | p2 | open | internal/stress/stress_test.go:210 |  |
| `BUG-222` | [F458] The touched-file clean rule is unreachable in production: the only caller passes nil touchedFiles | p2 | open | internal/baseline/baseline.go:97 |  |
| `BUG-223` | [F46] init/plan/sync resolve companion files against cwd, not the --output/--config directory | p2 | open | cmd/standardsctl/init.go:77 |  |
| `BUG-224` | [F463] CompileAgents' 50-file budget counts directories and non-.md entries, silently dropping real agents | p2 | open | internal/compiler/agents.go:44 |  |
| `BUG-225` | [F469] Transpiler.Verify's failure branch - the entire HISS-16 drift gate - has no negative test | p2 | open | internal/compiler/transpiler_test.go:164 |  |
| `BUG-226` | [F470] The policy lattice is dead code: declared profiles never reach ResolvedPolicy, and Linters/DevFeatures are never read | p2 | open | internal/config/config.go:110 |  |
| `BUG-227` | [F472] internal/config has no negative or boundary tests: the 'overrides can only increase strictness' invariant and all of hierarchy.go are untest | p2 | open | internal/config/config_test.go:54 |  |
| `BUG-228` | [F476] gosec runs everywhere with the exact exclusion list that suppresses all 22 findings in this unit, and .golangci.yml is never executed | p2 | open | internal/compiler/agents.go:103 |  |
| `BUG-229` | [F478] LoadManifest performs no validation and silently ignores unknown/misspelled .standards.yaml keys, downgrading policy to the looser defaults | p2 | open | internal/config/config.go:69 |  |
| `BUG-230` | [F479] gc silently ignores ReadDir failures on user-supplied directories and the CLI never surfaces report.Errors; a typo'd --worktrees-dir reports | p2 | open | internal/gc/gc.go:151 |  |
| `BUG-231` | [F48] needs report/requests/epic tests fork on the workstation-absolute framework default: they exercise a different production branch on CI than | p2 | open | cmd/standardsctl/standardsctl_test.go:48 |  |
| `BUG-232` | [F487] gc HISS-15 gaps: real git-prune path, 2000-entry truncation, dir-size caps and Errors channel untested; RootDir="." path reachable only by a | p2 | open | internal/gc/gc.go:116 |  |
| `BUG-233` | [F488] gc test runs `go clean -testcache` against the global GOCACHE on every suite run and cannot detect its failure | p2 | open | internal/gc/gc_test.go:142 |  |
| `BUG-234` | [F49] All 18 tests assert only nil/non-nil error on dispatchCommand; every positive case survives a stub implementation | p2 | open | cmd/standardsctl/standardsctl_test.go:133 |  |
| `BUG-235` | [F491] topology HISS-15 gaps: Violations/OrgContainers never asserted, DEV-01 root-repo branch and Clean error paths untested | p2 | open | internal/topology/topology_test.go:84 |  |
| `BUG-236` | [F493] Unclassified file kinds (Dockerfile, *.sh, *.tmpl, *.toml, *.json) silently disable tests/linters/security with a misleading reason; no test | p2 | open | internal/cifilter/filter.go:218 |  |
| `BUG-237` | [F495] util.RunCommand timeout only applies to a nil context, so every Background caller runs git/gh with no deadline | p2 | open | internal/util/util.go:73 |  |
| `BUG-238` | [F497] ResolveAuthToken runs 'gh auth token' with context.Background() and no deadline | p2 | open | internal/util/util.go:124 |  |
| `BUG-239` | [F498] worktree.Manager double-applies a relative rootDir: returned Path does not match where git created the worktree | p2 | open | internal/worktree/worktree.go:89 |  |
| `BUG-240` | [F501] validateBaseBranch rejects every base branch containing '-' (ContainsAny set is not a range); test suite only ever uses base "main" | p2 | open | internal/worktree/worktree.go:234 |  |
| `BUG-241` | [F507] CI filter fails open: unrecognized file types skip tests, linters, security and verify-all | p2 | open | internal/cifilter/filter.go:218 |  |
| `BUG-242` | [F508] compile-context vendor outputs are not classified as agent files, so HISS-16 verify is skipped on vendor-only PRs | p2 | open | internal/cifilter/filter.go:316 |  |
| `BUG-243` | [F512] cifilter HISS-15 gaps: RunContextSync is never asserted (ci.yml consumes it), GetChangedFiles has no positive test, AgentChanged/isAgent unt | p2 | open | internal/cifilter/cifilter_test.go:110 |  |
| `BUG-244` | [F513] util HISS-15 gaps: ResolveAuthToken has no test at all; RunCommand/RunGit negative and nil-ctx paths untested | p2 | open | internal/util/util.go:113 |  |
| `BUG-245` | [F514] TestResolveRepoIdentity cannot fail: fallback for "." always returns non-empty, and the boundary expectation equals the hard-coded default o | p2 | open | internal/util/util_test.go:145 |  |
| `BUG-246` | [F517] LoadFragments silently discards unreadable or malformed fragments, and `changelog verify` then prints [PASS] | p2 | open | internal/changelog/changelog.go:102 |  |
| `BUG-247` | [F519] RenderRelease deletes fragments whose type is not in sectionOrder without ever rendering them | p2 | open | internal/changelog/changelog.go:144 |  |
| `BUG-248` | [F52] Unit tests reach PATH binaries, the developer's GitHub token, api.github.com, a sibling repo under /home/kilian, and the CI step's $GITHUB_O | p2 | open | cmd/standardsctl/standardsctl_test.go:270 |  |
| `BUG-249` | [F521] The spliceChangelog branch that injects a new [Unreleased] header is executed by no test, and the CLI defaults --version to "Unreleased" | p2 | open | internal/changelog/changelog.go:209 |  |
| `BUG-250` | [F522] All three fuzz targets in this unit assert nothing that can fail except a panic | p2 | open | internal/changelog/fuzz_test.go:34 |  |
| `BUG-251` | [F523] CheckCadence turns every git failure into "no sweep needed" (nil error), silently disabling the post-commit dedupe gate | p2 | open | internal/dedupe/cadence.go:30 |  |
| `BUG-252` | [F525] ScanRepo swallows every filepath.Walk error and reports Passed=true for a path it never read | p2 | open | internal/dedupe/dedupe.go:58 |  |
| `BUG-253` | [F53] TestDispatchCommand_StateSubcommands exercises the broken `sync dir --log` order and cannot detect the dropped log | p2 | open | cmd/standardsctl/standardsctl_test.go:339 |  |
| `BUG-254` | [F548] The capacity arbiter and governance policy are write-only: nothing reads them outside tests | p2 | open | internal/router/arbiter.go:18 |  |
| `BUG-255` | [F55] Subprocess and scan I/O run with context.Background(), defeating RunCommand's timeout | p2 | open | cmd/standardsctl/sync.go:52 |  |
| `BUG-256` | [F556] HISS-04 complexity caps are configurable and printed but enforced nowhere; five functions in this unit exceed them | p2 | open | internal/router/sync.go:67 |  |
| `BUG-257` | [F557] ClassifyTier ignores its family and benchmarkELO arguments and misclassifies Qwen3 frontier models as midweight | p2 | open | internal/router/sync.go:67 |  |
| `BUG-258` | [F559] Local model discovery silently probes only port-11434 endpoints and swallows every HTTP error | p2 | open | internal/router/sync.go:176 |  |
| `BUG-259` | [F56] sync synthesizes a ruleset that ignores the resolved branch-protection policy | p2 | open | cmd/standardsctl/sync.go:137 |  |
| `BUG-260` | [F561] DiscoverLocalModels only queries endpoints whose URL contains "11434"; the default vLLM endpoint is silently ignored | p2 | open | internal/router/sync.go:210 |  |
| `BUG-261` | [F562] SyncCatalog regenerates routing.yaml from a hardcoded catalog, silently discarding operator edits and governance settings | p2 | open | internal/router/sync.go:300 |  |
| `BUG-262` | [F564] The repo's gosec gate excludes exactly the rule classes that fire in this unit | p2 | open | internal/supplychain/sbom.go:46 |  |
| `BUG-263` | [F565] CycloneDX SBOM hard-codes the praetor identity and ignores go.mod replace directives | p2 | open | internal/supplychain/sbom.go:63 |  |
| `BUG-264` | [F568] SLSA provenance statement attests a caller-supplied, unvalidated digest and is never signed | p2 | open | internal/supplychain/slsa.go:61 |  |
| `BUG-265` | [F569] supplychain's positive test never asserts that any component was parsed | p2 | open | internal/supplychain/supplychain_test.go:15 |  |
| `BUG-266` | [F570] PrepareRelease reports success when no changelog fragments exist and nothing is rendered | p2 | open | internal/release/release.go:55 |  |
| `BUG-267` | [F574] audit and sync report the branch-protection ruleset "verified" after checking only that the file exists | p2 | open | .github/rulesets/main.json:27 |  |
| `BUG-268` | [F577] /adopt and /dogfood PR comments run against the default branch, never the PR | p2 | open | .github/workflows/adopt.yml:35 |  |
| `BUG-269` | [F579] Security toolchains are installed from @latest on every CI run | p2 | open | .github/workflows/ci.yml:54 |  |
| `BUG-270` | [F58] sync pushes a GitHub ruleset to whatever repo the local .standards.yaml names, using auto-harvested credentials | p2 | open | cmd/standardsctl/sync.go:63 |  |
| `BUG-271` | [F586] Agent-dispatch governance is entirely unwired: pre_agent_dispatch.py, detect_loop.py and the router arbiter have no invoker | p2 | open | .config/agent/hooks/pre_agent_dispatch.py:1 |  |
| `BUG-272` | [F590] The scaffolder emits the same golangci-lint v1 config into every adopted repository, so their lint gate errors out too | p2 | open | .golangci.yml:1 |  |
| `BUG-273` | [F592] Pre-commit hook auto-stages .workingdir/ into whatever the developer is committing | p2 | open | lefthook.yml:10 |  |
| `BUG-274` | [F593] block_evasion.py can never block anything: its only argument-free branch is unreachable by construction under lefthook | p2 | open | lefthook.yml:19 |  |
| `BUG-275` | [F594] gosec gate is vacuous: exclusion list equals the exact set of rules that fire (232 findings, 0 reported) | p2 | open | lefthook.yml:33 |  |
| `BUG-276` | [F595] lefthook pre-commit topology-audit uses Makefile `$$HOME` escaping in YAML, so the hook is a permanent no-op | p2 | open | lefthook.yml:22 |  |
| `BUG-277` | [F599] adopt_priority_repos.sh prints "[PASS] ... published to remote" and exits 0 even when every adoption command failed | p2 | open | scripts/adopt_priority_repos.sh:29 |  |
| `BUG-278` | [F6] Live-mode report classifies newly created baseline, editor and transpiled files as 'reconciled' | p2 | open | internal/adopt/adopt.go:682 |  |
| `BUG-279` | [F600] `standardsctl devcontainer --verify` prints "100% in sync" while ignoring every unknown JSON key | p2 | open | .devcontainer/devcontainer.json:1 |  |
| `BUG-280` | [F602] Dev container cannot build: devcontainers Go feature does not support the Alpine base image | p2 | open | .devcontainer/devcontainer.json:12 |  |
| `BUG-281` | [F604] Release ldflags `-X main.version` are silent no-ops: every shipped binary reports v1.0.0 | p2 | open | .goreleaser.yaml:25 |  |
| `BUG-282` | [F606] HISS-11 claims SLSA-3 provenance attestations verified on all binaries; no provenance is generated and nothing verifies a signature | p2 | open | .goreleaser.yaml:79 |  |
| `BUG-283` | [F607] Cosign keyless signature is published with no certificate, so it cannot be verified | p2 | open | .goreleaser.yaml:85 |  |
| `BUG-284` | [F610] serviceAccount.create=false renders a Deployment referencing a ServiceAccount that is never created | p2 | open | deploy/helm/praetor/templates/deployment.yaml:25 |  |
| `BUG-285` | [F615] flavors.yaml source_ref/tag_pattern are never read; `flavors sync` force-moves every flavor tag to HEAD | p2 | open | .config/flavors.yaml:25 |  |
| `BUG-286` | [F62] HISS-15 gaps: gate, sync, init, plan, baseline --record, compile-context write path and every audit [FAIL] branch have no positive/negative/ | p2 | open | cmd/standardsctl/gate.go:14 |  |
| `BUG-287` | [F621] `standardsctl adopt --force` truncates .config/labels.yaml from 8 labels to 3 | p2 | open | .config/labels.yaml:16 |  |
| `BUG-288` | [F627] `overrides.ci` in .standards.yaml has no Go struct field and is silently discarded | p2 | open | .standards.yaml:45 |  |
| `BUG-289` | [F631] STATE.md grows without bound and is appended to on every sync, guaranteeing rebase conflicts | p2 | open | .workingdir/STATE.md:88 |  |
| `BUG-290` | [F632] buildRulesetJSON has diverged from the in-tree .github/rulesets/main.json it generates; re-running adopt drops the security gate | p2 | open | internal/adopt/adopt.go:317 |  |
| `BUG-291` | [F633] Scaffolded branch ruleset requires status checks that no scaffolded workflow ever reports, permanently blocking PRs | p2 | open | internal/adopt/adopt.go:345 |  |
| `BUG-292` | [F637] Scaffolded .golangci.yml uses the v1 schema and is rejected outright by the installed golangci-lint v2 | p2 | open | internal/flavor/scaffold.go:82 |  |
| `BUG-293` | [F640] hiss-audit skill promises AST scanners (recursion, complexity, context deadlines) that the scanner does not implement | p2 | open | .agents/skills/hiss-audit/SKILL.md:16 |  |
| `BUG-294` | [F641] paperclip-operate's own 4-step workflow can never pass its final verification step | p2 | open | .agents/skills/paperclip-operate/SKILL.md:60 |  |
| `BUG-295` | [F642] prerelease-bump's documented `bump apply` invocation drops --version and --patch | p2 | open | .agents/skills/prerelease-bump/SKILL.md:36 |  |
| `BUG-296` | [F643] .claude/mcp.json and .gemini/mcp_config.json are never loaded by their tools — the repo's own MCP server is not wired up | p2 | open | .claude/mcp.json:1 |  |
| `BUG-297` | [F644] `paperclip verify` checks none of the operating contract it is documented to verify | p2 | open | .paperclip/harness.json:5 |  |
| `BUG-298` | [F647] Gatekeeper persona's `gate run --target=. --dry-run` silently executes the full mutating pipeline | p2 | open | <agent-copies>/praetor-gatekeeper.md:29 |  |
| `BUG-299` | [F654] Documented `gate run --path=.` command silently ignores --path due to missing subcommand-arg handling | p2 | open | CONTRIBUTING.md:44 |  |
| `BUG-300` | [F658] Hermetic-build Dockerfile pins a Go toolchain image older than go.mod's required version | p2 | open | docs/adr/0005-cloudnative-oci-distroless-containers.md:11 |  |
| `BUG-301` | [F662] Live GitHub branch ruleset requires zero reviews and no signed commits, contradicting every governance doc including this unit's own inline | p2 | open | docs/llms-full.txt:90 |  |
| `BUG-302` | [F668] HISS-02 loop bound is documented as universal but 13 production loops have no scalar bound and neither engine can see them; LSP 'I/O without | p2 | open | cmd/standardsctl/serve.go:49 |  |
| `BUG-303` | [F669] HISS-14 (Migration footer), prompt-injection sanitizer and SEO validators exist only as unreachable code: documented invariants with zero li | p2 | open | internal/forge/pr.go:181 |  |
| `BUG-304` | [F670] HISS-07: 25 unchecked errors + 17 swallowed errors in production Go, invisible to the repo's '_ = ' substring scanner and to every gate | p2 | open | internal/harvester/bundle.go:593 |  |
| `BUG-305` | [F671] HISS-15 in the scanner's own tests: FuzzHissScan asserts a condition that cannot be false and no test checks that clean code yields zero vio | p2 | open | internal/hiss/fuzz_test.go:43 |  |
| `BUG-306` | [F672] Declared linters semgrep and golangci-lint are never executed; HISS-08 has no enforcement path at all | p2 | open | .config/semgrep/hiss-invariants.yml:26 |  |
| `BUG-307` | [F679] block_evasion.py is invoked with no arguments, so every evasion pattern it defines is unreachable | p2 | open | lefthook.yml:19 |  |
| `BUG-308` | [F68] resolveRepoCoordinates runs git with context.Background() instead of the command's timeout context | p2 | open | cmd/standardsctl/needs.go:352 |  |
| `BUG-309` | [F684] HISS-04 max-function-LOC threshold diverges across four values in live code paths | p2 | open | internal/hiss/hiss.go:15 |  |
| `BUG-310` | [F69] state sync <dir> --log=... silently drops the log message (documented order); test follows that form and cannot detect it | p2 | open | cmd/standardsctl/state.go:71 |  |
| `BUG-311` | [F70] `state status` is documented as inspect-only but creates .workingdir and appends a STATE.md log entry on every call | p2 | open | cmd/standardsctl/state.go:108 |  |
| `BUG-312` | [F75] Workstation bundler copies 0600 shell history and agent credentials into world-readable 0644 files | p2 | open | internal/harvester/bundle.go:72 |  |
| `BUG-313` | [F76] BundleWorkstation follows symlinks inside third-party skill/plugin dirs: hostile skill exfiltrates arbitrary user files into the bundle | p2 | open | internal/harvester/bundle.go:126 |  |
| `BUG-314` | [F79] IngestBundle never verifies manifest SHA256; ValidIntegrity=true is reported for tampered bundles | p2 | open | internal/harvester/bundle.go:662 |  |
| `BUG-315` | [F8] scanLegacyDebt swallows hiss.Scan errors and writes an empty debt baseline that the report presents as a successful scan | p2 | open | internal/adopt/adopt.go:720 |  |
| `BUG-316` | [F80] AuditSkills double-scans ~/.gemini/skills and ~/.gemini/config/skills on the CLI default path, inflating counts and fabricating duplicates | p2 | open | internal/harvester/skills.go:48 |  |
| `BUG-317` | [F82] Import rewrite result depends on map iteration order when a package and its subpackage are both catalog-covered | p2 | open | internal/needs/migrate.go:85 |  |
| `BUG-318` | [F83] ApplyMigration hard-codes Success:true and discards every error; the only test asserts a field that cannot be false | p2 | open | internal/needs/migrate.go:107 |  |
| `BUG-319` | [F84] bufio.Scanner errors are never checked in seven line parsers; updateGoMod rewrites go.mod from a truncated scan | p2 | open | internal/needs/migrate.go:167 |  |
| `BUG-320` | [F86] Name-only stray-directory rule recursively deletes generic directories (docs, lua, .config, .github, .vscode, ...) inside org containers inc | p2 | open | internal/topology/topology.go:256 |  |
| `BUG-321` | [F90] HISS-15 gap: the unit's dispatch functions have no positive/negative/boundary coverage beyond err==nil; dogfood, state task, harvest sub-com | p2 | open | cmd/standardsctl/adopt.go:17 |  |
| `BUG-322` | [F91] adopt --all-missing and harvest ingest are hard-wired to $HOME; the paths are reachable only by a HOME-mutating test and silently retarget t | p2 | open | cmd/standardsctl/adopt.go:60 |  |
| `BUG-323` | [F94] Flags written after the subcommand are silently discarded (devcontainer/models/flavors) | p2 | open | cmd/standardsctl/devcontainer.go:23 |  |
| `BUG-324` | [F95] flavors sync force-moves every flavor tag (latest, lts, edge, bleeding) onto HEAD | p2 | open | cmd/standardsctl/flavors.go:32 |  |
| `BUG-325` | [F98] Network entrypoints run on an undeadlined context.Background (HISS-02), and hindsight sync is unbounded | p2 | open | cmd/standardsctl/hindsight.go:22 |  |
| `BUG-326` | [F99] issue reconcile prints "[PASS] transitions successfully applied" before applying, and exits 0 when every update fails | p2 | open | cmd/standardsctl/issue.go:65 |  |
| `BUG-327` | [F1] A mid-run failure discards the report of files already written | p3 | open | internal/adopt/adopt.go:115 |  |
| `BUG-328` | [F102] models list's friendly "run models sync first" branch is unreachable (os.IsNotExist on a wrapped error) | p3 | open | cmd/standardsctl/models.go:77 |  |
| `BUG-329` | [F103] The /livez NOT_STARTED branch is unreachable and its only test assertion cannot fail | p3 | open | internal/container/health.go:50 |  |
| `BUG-330` | [F105] WaitForGracefulDrain leaks its signal registration and drains on a context the caller cannot cancel | p3 | open | internal/container/health.go:110 |  |
| `BUG-331` | [F106] internal/container tests leave Start/Shutdown/drain error paths uncovered and bind a real socket | p3 | open | internal/container/health_test.go:12 |  |
| `BUG-332` | [F107] `build`'s --optimize guard is always true, so `optimize: false` in the build manifest is always overridden | p3 | open | cmd/standardsctl/build.go:135 |  |
| `BUG-333` | [F109] `bump apply <package> --version=X`, the documented form, silently drops --version | p3 | open | cmd/standardsctl/bump.go:437 |  |
| `BUG-334` | [F113] serve's periodic audit conflates a scan error with an infraction and marks the container unready with no diagnostic | p3 | open | cmd/standardsctl/serve.go:58 |  |
| `BUG-335` | [F116] AggregateFleetWithHarvest double-counts repositories present in both the fleet scan and the harvest bundle | p3 | open | internal/needs/aggregate.go:104 |  |
| `BUG-336` | [F118] Framework demand report ordering is nondeterministic (map iteration plus non-stable sort with no tiebreak) | p3 | open | internal/needs/aggregate.go:169 |  |
| `BUG-337` | [F120] --framework is silently ignored by epic generation (dead parameter) | p3 | open | internal/needs/epic.go:35 |  |
| `BUG-338` | [F121] needs epic --framework has no effect: the generated epic always names the hard-coded framework | p3 | open | internal/needs/epic.go:39 |  |
| `BUG-339` | [F122] Published epic body cross-references placeholder issue numbers #1-#4 | p3 | open | internal/needs/epic.go:90 |  |
| `BUG-340` | [F124] Fleet epic regeneration writes into every discovered repository with no dry-run or confirmation | p3 | open | internal/needs/epic.go:239 |  |
| `BUG-341` | [F125] PublishPreMigrationEpic exceeds the declared HISS-04 cyclomatic cap and no repo gate measures cyclomatic complexity | p3 | open | internal/needs/epic.go:145 |  |
| `BUG-342` | [F126] 3D-test claim is false for this unit: publish test asserts nothing real, emit has no negative test | p3 | open | internal/needs/epic_test.go:93 |  |
| `BUG-343` | [F127] HISS-15 gaps in internal/needs: untested exported functions and untested on-disk branches | p3 | open | internal/needs/framework.go:58 |  |
| `BUG-344` | [F128] Exported framework-index API is dead: IsCapabilityCovered and MapCapabilityToReplacement are unreachable from non-test code | p3 | open | internal/needs/framework.go:109 |  |
| `BUG-345` | [F129] Generated fleet artifacts are written world-readable (0644/0755) and the repo's gosec gate excludes exactly these rules | p3 | open | internal/needs/requests.go:132 |  |
| `BUG-346` | [F130] Exported harvest-aggregation path is unreachable, untested, drops its error and double-counts repos | p3 | open | internal/needs/aggregate.go:50 |  |
| `BUG-347` | [F135] isThirdPartyImport uses a bare prefix match without a path-separator boundary | p3 | open | internal/needs/extract.go:126 |  |
| `BUG-348` | [F14] reconcileReadme silently skips an unreadable README.md | p3 | open | internal/adopt/adopt.go:1254 |  |
| `BUG-349` | [F141] inferLanguageFromItem classifies any harvested repo whose name contains "arr" as Python; only the default and 'svelte' branches are tested | p3 | open | internal/needs/harvester.go:106 |  |
| `BUG-350` | [F142] parsePatchDependencyLine cannot match grouped Go imports, JS require(), or Python imports, so harvested repos get zero dependencies and 100% | p3 | open | internal/needs/harvester.go:185 |  |
| `BUG-351` | [F143] PlanMigration ignores its frameworkPath argument; the `needs migrate --framework` flag has no effect | p3 | open | internal/needs/migrate.go:15 |  |
| `BUG-352` | [F148] parseGoMod cyclomatic complexity 11 exceeds documented HISS-04 cap of 10 and no gate detects it | p3 | open | internal/needs/extract.go:33 |  |
| `BUG-353` | [F156] HISS-10 CI step runs only go vet; the staticcheck configured in .golangci.yml never executes | p3 | open | .github/workflows/ci.yml:107 |  |
| `BUG-354` | [F16] `lefthook install` subprocess runs with no context or timeout despite adopt holding a context | p3 | open | internal/adopt/adopt.go:502 |  |
| `BUG-355` | [F160] ReconcileLabels treats every non-404 status (401/403/422) as success and discards the create-call status | p3 | open | internal/forge/github.go:210 |  |
| `BUG-356` | [F161] Production GitHub driver contains a token-prefix stub mode ('test-') that fabricates success for every write and read | p3 | open | internal/forge/github.go:307 |  |
| `BUG-357` | [F162] CreateIssue drops Assignees, Milestone and DependsOn from IssueSpec; SyncIssues' dependency merge has no effect | p3 | open | internal/forge/github.go:315 |  |
| `BUG-358` | [F166] GitLab and Gitea drivers are stubs that report unconditional success without performing any operation | p3 | open | internal/forge/gitlab.go:83 |  |
| `BUG-359` | [F167] SyncIssues exceeds the documented HISS-04 cyclomatic cap and the repo's scanners cannot detect it | p3 | open | internal/forge/issues.go:87 |  |
| `BUG-360` | [F168] SyncIssues is create-only, so re-running the 'synchronize' batch duplicates every issue | p3 | open | internal/forge/issues.go:118 |  |
| `BUG-361` | [F172] AssignReviewers implements CODEOWNERS pattern matching incorrectly (prefix without separator, no **, no *.ext recursion, no 'dir/' form, uni | p3 | open | internal/forge/pr.go:172 |  |
| `BUG-362` | [F173] AnalyzeCommit ignores the Conventional-Commits 'BREAKING-CHANGE:' footer, bypassing the HISS-14 Migration requirement | p3 | open | internal/forge/pr.go:194 |  |
| `BUG-363` | [F175] run_context_sync can never be true for an AGENTS.md-only PR, so the HISS-16 transpile gate is skipped | p3 | open | .github/workflows/ci.yml:88 |  |
| `BUG-364` | [F177] ci.yml runs the entire verification suite twice on every code PR | p3 | open | .github/workflows/ci.yml:109 |  |
| `BUG-365` | [F178] Test assertions cannot distinguish the Gitea/GitLab stub drivers or a no-op SyncIssues from real implementations | p3 | open | internal/forge/forge_test.go:16 |  |
| `BUG-366` | [F179] No test exercises the real HTTP path: sendRequest, parseGitHubIssues, UpdateIssue and ReconcileProtection payloads are untested | p3 | open | internal/forge/forge_test.go:54 |  |
| `BUG-367` | [F180] GitLab and Gitea drivers are success-returning stubs for every enforcement method; NewForge exposes them as real providers | p3 | open | internal/forge/gitea.go:57 |  |
| `BUG-368` | [F183] Forge response bodies up to 16 MB are embedded verbatim in error strings printed to the terminal | p3 | open | internal/forge/github.go:289 |  |
| `BUG-369` | [F189] Dead surface: discussion-to-ADR transcription (4 funcs) and ReconcileEngine.SetIssueState are unreachable from any binary; SetIssueState is | p3 | open | internal/forge/discussions.go:50 |  |
| `BUG-370` | [F19] MCP standards_adopt accepts absolute paths outside rootDir and is annotated non-destructive although Adopt --force overwrites files and the | p3 | open | cmd/standards-mcp/tools_adoption.go:42 |  |
| `BUG-371` | [F190] TranscribeDiscussionToADR: title with no [a-z0-9] characters yields slug "", producing 0001-.md that the numbering regex ignores, so the nex | p3 | open | internal/forge/discussions.go:78 |  |
| `BUG-372` | [F194] Local ProjectItem IDs collide within the same second | p3 | open | internal/forge/project.go:122 |  |
| `BUG-373` | [F195] Unbounded error-body reads and full response bodies embedded in returned errors; one read error suppressed via `body, _ :=` | p3 | open | internal/forge/project.go:154 |  |
| `BUG-374` | [F197] HISS-07 ignored error 'body, _ := io.ReadAll' is invisible to the repo's '_ = ' substring scanner | p3 | open | internal/forge/project.go:151 |  |
| `BUG-375` | [F198] Reconcile iterates Go maps, so report ordering and the order of applied label transitions are nondeterministic | p3 | open | internal/forge/reconciler.go:134 |  |
| `BUG-376` | [F20] standards_dogfood and standards_version_audit annotated read-only/closed-world but perform network egress, git clone, exec and temp-dir writ | p3 | open | cmd/standards-mcp/tools_adoption.go:134 |  |
| `BUG-377` | [F203] Dependency state resolution by repo-name suffix iterates a map and is nondeterministic across same-named repos | p3 | open | internal/forge/reconciler.go:248 |  |
| `BUG-378` | [F204] Generated wiki asserts enforcement mechanisms (AST check, compiler lint, race test suite) that the repo's pipeline does not implement | p3 | open | internal/forge/wiki.go:123 |  |
| `BUG-379` | [F205] HISS-15 not met: milestone/forge.go and all remote paths of project.go have zero tests; ParseCheckboxDependencies has no negative/boundary c | p3 | open | internal/milestone/forge.go:29 |  |
| `BUG-380` | [F207] fetchRemoteMilestones fetches a single page (per_page=100) and never follows Link pagination; extra milestones are silently absent from sync | p3 | open | internal/milestone/forge.go:63 |  |
| `BUG-381` | [F210] HISS-04 complexity caps (10/15) breached by three functions and not gated: .golangci.yml enables no complexity linter | p3 | open | internal/milestone/forge.go:132 |  |
| `BUG-382` | [F213] Remote milestone titles rendered unescaped into BACKLOG.md can break the section-replacement scan | p3 | open | internal/milestone/milestone.go:230 |  |
| `BUG-383` | [F214] milestone package performs all file I/O without a context and iterates file lines / store entries with no scalar bound (HISS-02) | p3 | open | internal/milestone/milestone.go:182 |  |
| `BUG-384` | [F216] Forge response bodies read without a size bound and echoed into error strings | p3 | open | internal/milestone/forge.go:79 |  |
| `BUG-385` | [F219] HISS-15 gaps in internal/forge (unit files): SetIssueState untested/unreachable, ParseCheckboxDependencies positive-only, remote ProjectMana | p3 | open | internal/forge/reconciler.go:71 |  |
| `BUG-386` | [F226] RunStdio's read loop ignores context cancellation while blocked in Scan and evades the repo's HISS-02 matcher | p3 | open | cmd/standards-mcp/server.go:673 |  |
| `BUG-387` | [F227] A single stdio line over 1 MB terminates the whole MCP server instead of returning a JSON-RPC error | p3 | open | cmd/standards-mcp/server.go:704 |  |
| `BUG-388` | [F230] RunSSE streams are cut after 10 s because http.Server.ReadTimeout cancels the long-lived request context | p3 | open | cmd/standards-mcp/server.go:840 |  |
| `BUG-389` | [F231] Developer home path /home/kilian/dev/golusoris/golusoris hard-coded as the default framework path in a shipped binary | p3 | open | cmd/standards-mcp/server.go:350 |  |
| `BUG-390` | [F232] -root does not confine tool paths; mutating tools write into any directory the MCP client names | p3 | open | cmd/standards-mcp/server.go:382 |  |
| `BUG-391` | [F237] cmd/standards-mcp tests run against the live repo root, $HOME agent-skill dirs and a sibling repo under /home/kilian | p3 | open | cmd/standards-mcp/server_test.go:406 |  |
| `BUG-392` | [F238] standards_adopt advertises destructiveHint=false while its `force` argument overwrites twelve existing configuration files | p3 | open | cmd/standards-mcp/tools_adoption.go:68 |  |
| `BUG-393` | [F24] Dry-run contract verified for a single file out of ~20 writers | p3 | open | internal/adopt/adopt_test.go:94 |  |
| `BUG-394` | [F240] Tool.Validate accepts a nil Handler; handleToolsCall then calls it and panics | p3 | open | internal/mcp/tool.go:57 |  |
| `BUG-395` | [F241] README advertises tool schema translation but all bridge.go translators are unreachable from any binary | p3 | open | internal/mcp/bridge.go:49 |  |
| `BUG-396` | [F243] hissRuleExplanations drifts from AGENTS.md (missing HISS-17/18) and from its own schema text; only HISS-01 is tested | p3 | open | cmd/standards-mcp/server.go:483 |  |
| `BUG-397` | [F244] standards_dogfood and standards_version_audit are annotated read-only/closed-world but scan $HOME, clone remotes and hit GOPROXY; annotation | p3 | open | cmd/standards-mcp/tools_adoption.go:134 |  |
| `BUG-398` | [F245] HISS-15 gaps in internal/mcp: ErrorResult never executed, constructor defaults and batch-error branches uncovered | p3 | open | internal/mcp/tool.go:136 |  |
| `BUG-399` | [F248] LSP retains every opened document forever: no didClose handling, documents/versions maps only grow | p3 | open | cmd/standards-lsp/server.go:200 |  |
| `BUG-400` | [F25] Tests exercise only unreachable URL wrappers (cleanGitURL/extractOwnerFromURL/extractRepoFromURL) and no test covers symlink, worktree, or h | p3 | open | internal/adopt/adopt_test.go:286 |  |
| `BUG-401` | [F250] LSP HISS-02 loop check ignores `range` loops and unbounded `for init; ; post` forms | p3 | open | cmd/standards-lsp/server.go:417 |  |
| `BUG-402` | [F255] ScanOptions.Timeout and DefaultScanTimeout are declared but never applied | p3 | open | internal/hiss/hiss.go:17 |  |
| `BUG-403` | [F258] Infraction cap saturates the report, blinding the debt ratchet once Cap is reached | p3 | open | internal/hiss/hiss.go:81 |  |
| `BUG-404` | [F26] Lint gate is `go vet` only: staticcheck SA1012 hits in bump tests (untyped nil context) never gate despite .golangci.yml and HISS-10 | p3 | open | Makefile:47 |  |
| `BUG-405` | [F264] ShouldIgnorePath substring match silently exempts any nested model/, target/, compat/, build_* path from HISS scanning | p3 | open | internal/hiss/hiss.go:125 |  |
| `BUG-406` | [F267] Brace-counting LOC scanner desyncs on `"{"`/`"}"` string literals in its own source, blinding HISS-04 for the rest of rules.go and server.go | p3 | open | internal/hiss/rules.go:60 |  |
| `BUG-407` | [F270] Go function whose signature wraps onto multiple lines is never tracked by scanGoLines, so its LOC is unchecked | p3 | open | internal/hiss/rules.go:157 |  |
| `BUG-408` | [F271] Rust HISS-07 unwrap/expect exemption matches the substring "test" anywhere in the path | p3 | open | internal/hiss/rules.go:244 |  |
| `BUG-409` | [F273] standards-lsp swallows SIGINT/SIGTERM while blocked on stdin, so the daemon cannot be terminated by signal | p3 | open | cmd/standards-lsp/main.go:15 |  |
| `BUG-410` | [F278] Options and symbols that do nothing: DogfoodOptions.DryRun, BundleOptions.DevDir, harvester.DetectArchetype | p3 | open | internal/dogfood/dogfood.go:27 |  |
| `BUG-411` | [F286] Silent truncation of remote list (>20) and target count consumed by non-directory entries | p3 | open | internal/dogfood/remote.go:130 |  |
| `BUG-412` | [F287] Deferred Close on written bundle files discards write-completion errors; manifest may record hashes of data not on disk | p3 | open | internal/harvester/bundle.go:76 |  |
| `BUG-413` | [F289] BundleWorkstation can report success after a context deadline: bundlePlugins swallows the cancellation and later helpers never check ctx | p3 | open | internal/harvester/bundle.go:295 |  |
| `BUG-414` | [F291] HISS-02/HISS-04 breaches in this unit are invisible to the repo's own gates | p3 | open | internal/harvester/harvester.go:50 |  |
| `BUG-415` | [F293] processTranscript silently stops on lines >64KiB (bufio.Scanner ErrTooLong never checked) | p3 | open | internal/harvester/memory.go:67 |  |
| `BUG-416` | [F294] OnboardRepository formats %w with a nil error when the path is not a directory | p3 | open | internal/harvester/onboard.go:32 |  |
| `BUG-417` | [F296] Onboarding writes fabricated manifest metadata and an AGENTS.md that references a non-existent make target | p3 | open | internal/harvester/onboard.go:142 |  |
| `BUG-418` | [F30] Unreachable duplicate helpers: dead one-line forwarders and unused cache/config accessors across eight packages | p3 | open | internal/adopt/adopt.go:182 |  |
| `BUG-419` | [F301] Silently dropped errors across the package: workflow scan, ScanDependencies, workflow file read | p3 | open | internal/bump/audit.go:27 |  |
| `BUG-420` | [F304] HISS-02/HISS-04 self-compliance: unbounded line loops with an unused bound constant, and four functions over the documented cognitive cap | p3 | open | internal/bump/bump.go:11 |  |
| `BUG-421` | [F305] ClassifyChannel treats common prerelease markers (-next, -canary, -pre, -snapshot) as stable | p3 | open | internal/bump/bump.go:16 |  |
| `BUG-422` | [F31] Live tests shell out to PATH binaries (lefthook, git) against a fake .git and assert neither outcome | p3 | open | internal/adopt/adopt.go:500 |  |
| `BUG-423` | [F310] Canary SARIF wrapper uses Go %q escaping, so colorized test output makes distillation fail and raw output is returned | p3 | open | internal/bump/canary.go:106 |  |
| `BUG-424` | [F314] executeCanaryTest indexes parts[0] of strings.Fields without a length check (runtime panic on whitespace-only TestCmd) | p3 | open | internal/bump/canary.go:193 |  |
| `BUG-425` | [F317] Workflow action audit compares raw tag strings against a major-tag table and scans commented-out text | p3 | open | internal/bump/scan_actions.go:121 |  |
| `BUG-426` | [F320] scanGoModuleDir overrides the user's GOPROXY, so private-module repos silently fall back to the offline listing | p3 | open | internal/bump/scan_go.go:277 |  |
| `BUG-427` | [F321] pnpm workspace discovery: filepath.Glob has no ** support and the root package.json is skipped when a workspace matches | p3 | open | internal/bump/scan_node.go:38 |  |
| `BUG-428` | [F323] bump rewrites package.json through map[string]interface{} — key order and formatting destroyed | p3 | open | internal/bump/update.go:96 |  |
| `BUG-429` | [F329] Canary output persisted world-readable under the main repo's untracked, un-ignored .standards/ directory | p3 | open | internal/bump/canary.go:118 |  |
| `BUG-430` | [F33] Repo's own HISS-02/HISS-07 scanner cannot see the unbounded execs and swallowed errors reachable from standardsctl | p3 | open | internal/hiss/rules.go:177 |  |
| `BUG-431` | [F330] HISS-04 complexity caps exceeded in five bump functions; repo's own scanner measures LOC only | p3 | open | internal/bump/scan_go.go:24 |  |
| `BUG-432` | [F332] scanGoModuleDir overrides the user's GOPROXY with the public proxy | p3 | open | internal/bump/scan_go.go:130 |  |
| `BUG-433` | [F333] internal/bump HISS-15 gap: 11 functions at 0% coverage, RunCanary/ApplyBump/AuditCodebaseVersions/UpdateAll have no positive or boundary tes | p3 | open | internal/bump/update.go:123 |  |
| `BUG-434` | [F335] The nil-context guards in devcontainer are never exercised; the test labelled "Nil context" only passes a cancelled context | p3 | open | internal/devcontainer/devcontainer_test.go:195 |  |
| `BUG-435` | [F338] 'standardsctl editors verify/generate' cannot pass on this repo and would overwrite working dev commands with unqualified 'standardsctl' inv | p3 | open | internal/editor/editor.go:848 |  |
| `BUG-436` | [F342] HISS-02 "context timeout on all I/O" is decorative in both packages: ctx is checked once, then non-cancellable os.ReadFile/os.WriteFile run | p3 | open | internal/editor/editor.go:881 |  |
| `BUG-437` | [F344] Options.WorkspaceRoot is an exported, JSON-tagged field that no code in the package reads | p3 | open | internal/editor/editor.go:41 |  |
| `BUG-438` | [F345] Six editor generators accept an `arch` argument and discard it, so every adopted repo gets a C/C++-only .clang-tidy regardless of archetype | p3 | open | internal/editor/editor.go:766 |  |
| `BUG-439` | [F346] `standardsctl editors verify` fails on praetor itself and is in no gate, so the editor package's enforcement is never exercised | p3 | open | internal/editor/editor.go:848 |  |
| `BUG-440` | [F347] HISS-15 gaps: the two enforcement exemptions, the write-failure path and the unknown-key path have no tests in either package | p3 | open | internal/editor/editor_test.go:111 |  |
| `BUG-441` | [F348] Negative editor test dereferences err after a non-fatal t.Errorf, turning a regression into a nil-pointer panic | p3 | open | internal/editor/editor_test.go:153 |  |
| `BUG-442` | [F349] synthesizeFeatures ignores its profiles argument: the Go 1.27 devcontainer feature is injected even for native-gpu-systems repos | p3 | open | internal/devcontainer/devcontainer.go:106 |  |
| `BUG-443` | [F35] Non-NotExist stat/read errors are treated as PASS or silently skip creation | p3 | open | cmd/standardsctl/audit.go:88 |  |
| `BUG-444` | [F351] HISS-02 applied inconsistently in devcontainer.go: two range loops over profiles have no scalar bound while every facets loop does, and the | p3 | open | internal/devcontainer/devcontainer.go:135 |  |
| `BUG-445` | [F352] Emitted LSP config allows 75 LOC per function while the emitted CONTRIBUTING/PR checklist for the same repo says 60 | p3 | open | internal/editor/editor.go:439 |  |
| `BUG-446` | [F353] praetor itself fails `standardsctl editors verify`: .editorconfig and .clang-tidy are absent from the repo and no gate runs the command | p3 | open | internal/editor/editor.go:848 |  |
| `BUG-447` | [F354] Catalog and memory caches are rewritten non-atomically, so an interrupted sync destroys all distilled metadata | p3 | open | internal/docdistill/cache.go:64 |  |
| `BUG-448` | [F361] `go doc` is executed without cmd.Dir or the pinned version, so harvested docs can describe a different module version | p3 | open | internal/docdistill/harvester.go:38 |  |
| `BUG-449` | [F363] GitHub Action harvest returns a content-free header as if it were documentation when the name is long enough | p3 | open | internal/docdistill/harvester.go:107 |  |
| `BUG-450` | [F366] Tainted package names produce out-of-tree file reads whose content is published to agents | p3 | open | internal/docdistill/harvester.go:124 |  |
| `BUG-451` | [F367] Cache directories and files are created world-readable (0755/0644) | p3 | open | internal/hindsight/cache.go:24 |  |
| `BUG-452` | [F373] Write-only catalog schema version and never-populated Stale field | p3 | open | internal/docdistill/cache.go:19 |  |
| `BUG-453` | [F376] Silent truncation caps drop manifest entries, workflow actions and recall hits with no signal to the caller | p3 | open | internal/docdistill/scanner.go:181 |  |
| `BUG-454` | [F377] SynthesizeKnowledgePages ignores repoPath and returns hardcoded Praetor prose; its only test asserts a constant and cannot fail | p3 | open | internal/hindsight/pages.go:11 |  |
| `BUG-455` | [F385] Generated Dockerfile is unbuildable when the repo has no origin remote and the CLI's default dir="." is used | p3 | open | internal/flavor/scaffold.go:86 |  |
| `BUG-456` | [F39] Repo's HISS-02 enforcement cannot see deadline-less context.Background() I/O; the audit CLI itself breaches the rule undetected | p3 | open | cmd/standardsctl/audit.go:101 |  |
| `BUG-457` | [F390] HISS-15 gaps in internal/state: no negative or boundary tests for AddBug/AddQuestion input, ArchiveCompletedTasks, or the parsers | p3 | open | internal/state/bugs.go:28 |  |
| `BUG-458` | [F395] ArchiveCompletedTasks deletes tasks from OPEN.md before appending them to BACKLOG.md, with no atomicity | p3 | open | internal/state/tasks.go:167 |  |
| `BUG-459` | [F396] ArchiveCompletedTasks exceeds the documented cyclomatic cap and no gate in the repo measures cyclomatic complexity | p3 | open | internal/state/tasks.go:138 |  |
| `BUG-460` | [F398] DetectFlavor silently returns "go-library" for repositories that match nothing, driving a misleading gate failure | p3 | open | internal/flavor/flavor.go:101 |  |
| `BUG-461` | [F401] Read failure on a ledger file is swallowed and the file is then overwritten with the default template | p3 | open | internal/state/state.go:112 |  |
| `BUG-462` | [F402] STATE.md grows without bound: every post-commit state sync appends a new entry and nothing prunes | p3 | open | internal/state/state.go:126 |  |
| `BUG-463` | [F403] RunGatedPipeline documented as 4-stage; it runs 6 | p3 | open | internal/gating/pipeline.go:45 |  |
| `BUG-464` | [F407] HISS-02 breaches inside the gate: I/O without context/timeout in flavor stage and harness platform resolution | p3 | open | internal/gating/pipeline.go:150 |  |
| `BUG-465` | [F411] Receipt 'repository' field is the path basename: '.' for the CLI default, '..' from the test suite (K5 delta) | p3 | open | internal/gating/pipeline.go:198 |  |
| `BUG-466` | [F413] checkFileExistsAndNonEmpty treats the repo path as a glob pattern: '[', '*', '?' in repoDir falsely reject a valid repo (K9 delta) | p3 | open | internal/gating/prefetch.go:68 |  |
| `BUG-467` | [F414] SARIF context extraction reports '[source line out of range]' for any diagnostic past line 1000 | p3 | open | internal/lockdown/distill.go:118 |  |
| `BUG-468` | [F415] DistillSARIF counts absent `level` as error and picks 'top failures' by position, not severity | p3 | open | internal/lockdown/distill.go:244 |  |
| `BUG-469` | [F416] lockdown prompt-injection sanitizers are unreachable although every adopted repo declares the agent:sandboxed facet | p3 | open | internal/lockdown/sanitize.go:119 |  |
| `BUG-470` | [F418] Validate accepts whitespace-only fields and rejects case variants that CreateDisposition accepts | p3 | open | internal/paperclip/disposition.go:87 |  |
| `BUG-471` | [F420] DistillSARIF reads context lines from unconfined SARIF URIs (absolute or ../ paths) into the summary | p3 | open | internal/lockdown/distill.go:190 |  |
| `BUG-472` | [F421] Unbounded git subprocess in resolvePlatform and ctx-less flavor stage (HISS-02 I/O without timeout) | p3 | open | internal/paperclip/harness.go:70 |  |
| `BUG-473` | [F423] VerifyLockfiles 'non-empty' contract has no boundary test, so K9 is invisible to the suite | p3 | open | internal/gating/gating_test.go:38 |  |
| `BUG-474` | [F426] Distill positive test's OR-ed assertion makes the target-line marker check dead | p3 | open | internal/lockdown/lockdown_test.go:83 |  |
| `BUG-475` | [F429] Negative tests never assert which error: six exported sentinels have zero errors.Is checks | p3 | open | internal/lockdown/lockdown_test.go:361 |  |
| `BUG-476` | [F430] lockdown HISS-15 gaps: NormalizeWhitespace untested, ErrInvalidSig/ErrNonZeroExit-on-verify/bad-privkey/context-line clamps uncovered | p3 | open | internal/lockdown/sanitize.go:160 |  |
| `BUG-477` | [F433] Merge's ours==theirs shortcut returns unparsed source as a clean merge | p3 | open | internal/astmerge/astmerge.go:66 |  |
| `BUG-478` | [F434] readFileWithContext's context timeout does not bound the read (HISS-02 I/O timeout is decorative) | p3 | open | internal/astmerge/astmerge.go:115 |  |
| `BUG-479` | [F437] MergeResult.ResolvedCount undercounts cleanly-deleted symbols | p3 | open | internal/astmerge/astmerge.go:407 |  |
| `BUG-480` | [F439] HISS-15 gaps: MergeFiles has no negative/boundary coverage and no test validates merged-code compilability | p3 | open | internal/astmerge/astmerge_test.go:369 |  |
| `BUG-481` | [F442] maxFunctionsToSynthesize is applied to the declaration index, not the function count | p3 | open | internal/difftest/difftest.go:136 |  |
| `BUG-482` | [F447] Generated negative tests assert an error that the target is not required to return | p3 | open | internal/difftest/difftest.go:399 |  |
| `BUG-483` | [F450] HISS-01 breach in tree: internal/difftest.formatTypeExpr is directly recursive and no gate can see it (LSP would, but is not wired into any | p3 | open | internal/difftest/difftest.go:219 |  |
| `BUG-484` | [F451] difftest-generated 3D tests contain checks that can never fail | p3 | open | internal/difftest/difftest.go:377 |  |
| `BUG-485` | [F452] HISS-15 gaps: difftest's file-reading and target-package options have zero tests; no negative test for SynthesizeFromDiff | p3 | open | internal/difftest/difftest_test.go:1 |  |
| `BUG-486` | [F453] difftest tests only parse-check generated code, so no compile defect can ever be caught | p3 | open | internal/difftest/difftest_test.go:45 |  |
| `BUG-487` | [F454] Stress git test depends on the developer's global git configuration | p3 | open | internal/stress/stress_test.go:96 |  |
| `BUG-488` | [F457] gosec is run with every rule class this unit triggers excluded, so the 'AST Security Scan' gate is empty here | p3 | open | .github/workflows/security.yml:49 |  |
| `BUG-489` | [F459] EvaluateRatchet can fail with both violation lists empty, producing an audit failure with no listed violations | p3 | open | internal/baseline/baseline.go:106 |  |
| `BUG-490` | [F460] Both fuzz targets in this unit assert only properties that cannot be false | p3 | open | internal/baseline/fuzz_test.go:147 |  |
| `BUG-491` | [F461] No context or timeout reaches any I/O in the unit; the declared agent timeout constant is never used | p3 | open | internal/compiler/agents.go:14 |  |
| `BUG-492` | [F464] Agent projection follows symlinks: a symlinked .md in .agents/agents copies an arbitrary readable file into four tracked vendor directories | p3 | open | internal/compiler/agents.go:66 |  |
| `BUG-493` | [F465] compiler.CompileFrameworkAssets (the llms.txt/llms-full.txt generator) is unreachable; the dual-surface docs are hand-maintained and have dr | p3 | open | internal/compiler/framework_assets.go:33 |  |
| `BUG-494` | [F466] extractRepoTitle accepts the first '# ' line anywhere, including inside a fenced code block | p3 | open | internal/compiler/transpiler.go:122 |  |
| `BUG-495` | [F47] HISS-04 complexity caps breached in this package and not enforced by the repo linter config | p3 | open | cmd/standardsctl/main.go:122 |  |
| `BUG-496` | [F471] The policy engine itself breaches the HISS-04 caps it stores, and nothing in the repo computes them | p3 | open | internal/config/config.go:149 |  |
| `BUG-497` | [F474] TestLoadManifest_Dogfood reads the live repository root and accepts either of two repo names | p3 | open | internal/config/config_test.go:184 |  |
| `BUG-498` | [F475] repository.owner from an audited repo's .standards.yaml is interpolated into a read path, escaping rootDir | p3 | open | internal/config/hierarchy.go:58 |  |
| `BUG-499` | [F481] gc cleanTempBinaries deletes any root-level file named *.tmp / tmp.* / *.test in the user's repo | p3 | open | internal/gc/gc.go:251 |  |
| `BUG-500` | [F482] HISS-04 complexity caps breached in this unit and not enforced by any repo gate (.golangci.yml enables only govet/staticcheck/errcheck) | p3 | open | internal/gc/gc.go:246 |  |
| `BUG-501` | [F486] HISS-04 complexity caps breached in gc, topology and sentinel (cognitive 26/19/18/18, cyclomatic 14) | p3 | open | internal/gc/gc.go:246 |  |
| `BUG-502` | [F489] CanAllocateModel 85%-utilization guard is unreachable for consistent stats and documented as a check | p3 | open | internal/sentinel/sentinel.go:200 |  |
| `BUG-503` | [F490] sentinel HISS-15 gaps: live-host smoke test, ReadHostStats untested directly, parser/statfs error branches uncovered | p3 | open | internal/sentinel/sentinel_test.go:12 |  |
| `BUG-504` | [F492] ci filter silently truncates the diff at 5000 paths, fail-open: dropped code files can make a PR look docs-only | p3 | open | internal/cifilter/filter.go:114 |  |
| `BUG-505` | [F494] Repo gates cannot see this unit's HISS-02 and HISS-04 breaches: complexity caps exceeded in 4 functions and unbounded I/O pass audit, lint s | p3 | open | internal/cifilter/filter.go:129 |  |
| `BUG-506` | [F496] ResolveRepoIdentity fabricates owner 'cordanaLLM' when no origin remote exists, and needs epic publishing writes to that repo | p3 | open | internal/util/util.go:92 |  |
| `BUG-507` | [F499] Manager.Remove is not idempotent: if `git worktree remove` fails the wt/<id> branch is never deleted and Prune does not clean branches | p3 | open | internal/worktree/worktree.go:148 |  |
| `BUG-508` | [F50] Tests exercise a non-existent 'flavors list' action and an ignored --dev-dir flag, so their isolation and negative coverage are illusory | p3 | open | cmd/standardsctl/standardsctl_test.go:181 |  |
| `BUG-509` | [F500] validateBaseBranch accepts option-like and untrimmed values, so `worktree create <id> --force` silently creates from HEAD | p3 | open | internal/worktree/worktree.go:229 |  |
| `BUG-510` | [F502] worktree tests run real git commit/worktree commands without isolating global git config or pinning git version | p3 | open | internal/worktree/worktree_test.go:13 |  |
| `BUG-511` | [F503] HISS-15 gaps: no boundary test for relative worktree rootDir, no negative test for unrecognized ci-filter paths, ResolveAuthToken untested | p3 | open | internal/worktree/worktree_test.go:262 |  |
| `BUG-512` | [F504] Boundary test silently downgrades MaxTaskIDLength to 48 on Windows, so the documented 128-char limit is never verified there | p3 | open | internal/worktree/worktree_test.go:430 |  |
| `BUG-513` | [F505] PR body is interpolated into a shell heredoc in the workflow that consumes the CI filter (outside G20 file list) | p3 | open | .github/workflows/ci.yml:80 |  |
| `BUG-514` | [F506] HISS-04 complexity caps exceeded in unit; repo scanner measures only LOC | p3 | open | internal/cifilter/filter.go:129 |  |
| `BUG-515` | [F509] HISS-02 timeout invariant is not enforced by any repo scanner for subprocess I/O | p3 | open | internal/util/util.go:72 |  |
| `BUG-516` | [F51] Test fixture setup discards MkdirAll/WriteFile errors (HISS-07 in _test.go, outside scanner scope) | p3 | open | cmd/standardsctl/standardsctl_test.go:251 |  |
| `BUG-517` | [F510] 'worktree remove --force' advertises removing locked worktrees but Manager.Remove passes a single --force | p3 | open | internal/worktree/worktree.go:142 |  |
| `BUG-518` | [F511] validateBaseBranch permits option-shaped values; git parses them as worktree add options | p3 | open | internal/worktree/worktree.go:229 |  |
| `BUG-519` | [F515] worktree HISS-15 gaps: Remove partial-failure path untested, cancelled-context test accepts any error, no non-"main" base branch | p3 | open | internal/worktree/worktree.go:148 |  |
| `BUG-520` | [F516] Fragment filenames use UnixNano()%1000000, so same-title fragments can overwrite each other and section ordering is random | p3 | open | internal/changelog/changelog.go:67 |  |
| `BUG-521` | [F518] RenderRelease discards every os.Remove error, so an undeletable fragment is re-rendered into the next release | p3 | open | internal/changelog/changelog.go:136 |  |
| `BUG-522` | [F524] Neither changelog nor dedupe.ScanRepo accepts a context; all file I/O and the repo walk are unbounded | p3 | open | internal/dedupe/dedupe.go:48 |  |
| `BUG-523` | [F527] Duplicate groups are collected by ranging a map, so the dedupe report's ordering is nondeterministic | p3 | open | internal/dedupe/dedupe.go:167 |  |
| `BUG-524` | [F528] HISS-15 negative/boundary coverage is nominal: ScanRepo has no negative test and "HighVolumeCapping" tests 50 against a 50000 cap | p3 | open | internal/dedupe/dedupe_test.go:13 |  |
| `BUG-525` | [F529] The cadence test's git setup swallows every error, turning environment-dependent git failures into a misleading assertion failure | p3 | open | internal/dedupe/dedupe_test.go:105 |  |
| `BUG-526` | [F530] internal/seo is unreachable from every binary, yet a shipped skill instructs operators to "run the validator" | p3 | open | internal/seo/seo.go:119 |  |
| `BUG-527` | [F532] ValidateRobotsTxt silently ignores every line past MaxRobotsLines without reporting truncation | p3 | open | internal/seo/seo.go:281 |  |
| `BUG-528` | [F533] robots.txt parser never strips trailing '#' comments, producing spurious errors and wrong rule paths | p3 | open | internal/seo/seo.go:287 |  |
| `BUG-529` | [F534] Allow/Disallow/Crawl-delay appearing before the first User-agent are silently dropped from the parsed rules | p3 | open | internal/seo/seo.go:319 |  |
| `BUG-530` | [F535] The documented cognitive-complexity cap of 15 is measured by no gate in the repository | p3 | open | internal/seo/seo.go:311 |  |
| `BUG-531` | [F536] internal/seo HISS-15 gap: the documented bounds have no boundary test; the "HighVolumeCapping" test exercises 0.1% of the cap | p3 | open | internal/seo/seo_test.go:384 |  |
| `BUG-532` | [F538] Fuzz targets assert nothing beyond a non-nil result, so they can only catch panics | p3 | open | internal/seo/fuzz_test.go:15 |  |
| `BUG-533` | [F539] "Invariant bounds adhering to HISS-02" constants MaxFieldLength and StandardSitemapNS are never used; sitemap xmlns is parsed but never chec | p3 | open | internal/seo/seo.go:14 |  |
| `BUG-534` | [F540] PreBuildOptimizer results are written into a discarded copy of the target config | p3 | open | internal/builder/builder.go:125 |  |
| `BUG-535` | [F545] The "noop" transition is unreachable in production: currentTags is keyed by tag name, compared against a target tag string | p3 | open | internal/flavors/flavors.go:60 |  |
| `BUG-536` | [F546] internal/flavors HISS-15 gaps: LoadConfig has no negative test and the `edge` branch is never exercised | p3 | open | internal/flavors/flavors_test.go:1 |  |
| `BUG-537` | [F547] Three test files depend on the real repo root, the ambient git config and PATH | p3 | open | internal/release/release_test.go:141 |  |
| `BUG-538` | [F549] SelectModel's fallback cascade is an unbounded loop that hangs on a cyclic fallback_tier | p3 | open | internal/router/arbiter.go:68 |  |
| `BUG-539` | [F550] SelectOrthogonalAuditor hardcodes a 0.20 headroom floor, ignoring the configured ExhaustionThresholdPercent | p3 | open | internal/router/arbiter.go:103 |  |
| `BUG-540` | [F551] SelectOrthogonalAuditor's second pass iterates a map, making auditor choice nondeterministic | p3 | open | internal/router/arbiter.go:110 |  |
| `BUG-541` | [F552] LimitTracker RPM/TPM counters never decay, so headroom monotonically falls to zero and never recovers | p3 | open | internal/router/limits.go:31 |  |
| `BUG-542` | [F554] internal/router HISS-15 gaps: the documented boundary behaviour of matchParamTag and the whole network path are untested | p3 | open | internal/router/router_test.go:50 |  |
| `BUG-543` | [F555] ClassifyTier ignores the family and benchmarkELO arguments its doc comment claims to use | p3 | open | internal/router/sync.go:66 |  |
| `BUG-544` | [F563] ResolveRunner's routing-table miss returns a self-hosted Linux ARC spec without running the darwin platform constraint | p3 | open | internal/runner/matrix.go:24 |  |
| `BUG-545` | [F566] go.mod parser emits bogus SBOM components for comment lines inside a require block | p3 | open | internal/supplychain/sbom.go:95 |  |
| `BUG-546` | [F57] lefthook runs `gate run --path=.`: positional 'run' stops flag parsing so --path is never read | p3 | open | lefthook.yml:41 |  |
| `BUG-547` | [F571] The composite action can never run a non-dry-run dogfood benchmark | p3 | open | .github/actions/praetor-adopt/action.yml:60 |  |
| `BUG-548` | [F572] Issue templates and renovate.json apply labels that are not in the canonical label taxonomy | p3 | open | .github/ISSUE_TEMPLATE/bug.yml:4 |  |
| `BUG-549` | [F575] adopt.yml declares a target_path input it never uses | p3 | open | .github/workflows/adopt.yml:6 |  |
| `BUG-550` | [F576] Any GitHub user can trigger the write-permissioned adopt job by commenting on a PR | p3 | open | .github/workflows/adopt.yml:26 |  |
| `BUG-551` | [F578] go.mod's Go version requirement is not mirrored in one CI workflow and one Dockerfile template | p3 | open | .github/workflows/adopt.yml:41 |  |
| `BUG-552` | [F580] pages.yml grants pages:write and id-token:write to a job that runs on pull_request | p3 | open | .github/workflows/pages.yml:24 |  |
| `BUG-553` | [F581] Only two of the four declared release flavors are ever published as tags | p3 | open | .github/workflows/sync-flavors.yml:44 |  |
| `BUG-554` | [F587] pre_agent_dispatch.py is never invoked and its routing-config loader is dead code, while the standards doc claims it enforces concurrency qu | p3 | open | .config/agent/hooks/pre_agent_dispatch.py:15 |  |
| `BUG-555` | [F588] Semgrep and the Go scanner file the same banned-function checks under different HISS ids (HISS-08 vs HISS-09) | p3 | open | .config/semgrep/hiss-invariants.yml:26 |  |
| `BUG-556` | [F589] Semgrep rule hiss-02-go-http-without-context is an unsatisfiable AND of two patterns and can never match | p3 | open | .config/semgrep/hiss-invariants.yml:43 |  |
| `BUG-557` | [F59] sync exits 0 and prints 'complete' when the remote reconciliation fails or is bypassed by a test- token | p3 | open | cmd/standardsctl/sync.go:68 |  |
| `BUG-558` | [F591] .gosec.json is never loaded by any praetor gate, so its exclusion list is a fourth unsynchronised copy that only has to exist | p3 | open | .gosec.json:1 |  |
| `BUG-559` | [F596] make verify-all and CI install govulncheck and gosec from @latest, so the security gate is unpinned and irreproducible | p3 | open | Makefile:57 |  |
| `BUG-560` | [F597] Local Makefile build layout names binaries differently from the goreleaser release pipeline | p3 | open | Makefile:15 |  |
| `BUG-561` | [F598] make verify-all contains no formatting check, so the receipt the PR template demands can pass on unformatted code | p3 | open | Makefile:82 |  |
| `BUG-562` | [F60] CI filter classifies agent markdown and requirements.txt as documentation, skipping context-sync and security gates | p3 | open | internal/cifilter/filter.go:153 |  |
| `BUG-563` | [F601] Generated .devcontainer/devcontainer.json has no verification gate in the Makefile or CI | p3 | open | .devcontainer/devcontainer.json:1 |  |
| `BUG-564` | [F603] Dev container creation auto-runs unpinned `go install ...@latest` supply-chain fetches | p3 | open | .devcontainer/devcontainer.json:39 |  |
| `BUG-565` | [F605] goreleaser archives use the `format` key deprecated since v2.6 under a floating `~> v2` version | p3 | open | .goreleaser.yaml:59 |  |
| `BUG-566` | [F609] GPU ARC runner pods run unconstrained while the amd64 set is hardened | p3 | open | deploy/arc/runner-scale-set.yaml:42 |  |
| `BUG-567` | [F612] Helm values imagePullSecrets / nameOverride / fullnameOverride are read by no template | p3 | open | deploy/helm/praetor/values.yaml:8 |  |
| `BUG-568` | [F613] Root Dockerfile has no CMD, so the image it describes would CrashLoopBackOff even if built | p3 | open | Dockerfile:3 |  |
| `BUG-569` | [F616] Deleting a platform from .config/fleet.yaml has no effect — merge only overwrites, never removes | p3 | open | .config/fleet.yaml:20 |  |
| `BUG-570` | [F617] .config/github-app/ is unreferenced by any code, workflow, or build step | p3 | open | .config/github-app/manifest.json:1 |  |
| `BUG-571` | [F618] GitHub App permission matrix omits two write scopes the manifest actually requests | p3 | open | .config/github-app/permissions.md:8 |  |
| `BUG-572` | [F619] labels.yaml is never parsed or applied; the labels the tool actually writes to issues are disjoint from the taxonomy | p3 | open | .config/labels.yaml:1 |  |
| `BUG-573` | [F622] Unread keys in .config/orgs/*.yaml: native_gpu routing and the golusoris builder-kit registry are silently dropped | p3 | open | .config/orgs/golusoris.yaml:28 |  |
| `BUG-574` | [F623] Paperclip harness omits HISS-17 and HISS-18, and rules.md is never validated against its generator | p3 | open | .paperclip/harness.json:12 |  |
| `BUG-575` | [F624] The schema URL declared in .standards.yaml points at an artifact that does not exist and nothing validates against | p3 | open | .standards.yaml:2 |  |
| `BUG-576` | [F625] Repository metadata in .standards.yaml (topics, homepage, description, visibility) is parsed but never applied | p3 | open | .standards.yaml:7 |  |
| `BUG-577` | [F629] Every archived task in BACKLOG.md is stamped commit `local`, breaking the HISS-17 audit trail | p3 | open | .workingdir/BACKLOG.md:19 |  |
| `BUG-578` | [F63] reorderAdoptArgs (cyclomatic 14) and runHarvestSkills (11) exceed the documented HISS-04 cap of 10 with no enforcing gate | p3 | open | cmd/standardsctl/adopt.go:17 |  |
| `BUG-579` | [F630] PRE_MIGRATION_EPIC.md instructs a 60-LOC function limit, contradicting the manifest's 75 | p3 | open | .workingdir/PRE_MIGRATION_EPIC.md:35 |  |
| `BUG-580` | [F636] adopt injects a 'HISS-16 Compliant' README badge regardless of the repository's measured compliance | p3 | open | internal/adopt/adopt.go:1261 |  |
| `BUG-581` | [F638] Scaffolded Dockerfile literal has drifted from templates/go/Dockerfile.distroless.tmpl into an unbuildable single-stage file | p3 | open | internal/flavor/scaffold.go:86 |  |
| `BUG-582` | [F639] templates/**/*.tmpl reference context fields that do not exist on adopt.TemplateContext, so they cannot be rendered even if re-wired | p3 | open | templates/agent/AGENTS.md.tmpl:1 |  |
| `BUG-583` | [F64] os.UserHomeDir error discarded; HOME unset yields cwd-relative 'dev' / '.gemini' paths, and the repo's HISS-07 scanner misses the `, _ :=` f | p3 | open | cmd/standardsctl/adopt.go:60 |  |
| `BUG-584` | [F645] Documented AGit submission protocol (refs/for/main, -o topic) does not apply to this repo's GitHub origin | p3 | open | .paperclip/rules.md:12 |  |
| `BUG-585` | [F648] needs-miner persona names FRAMEWORK_DEMAND.md; the tool emits FRAMEWORK_DEMAND.yaml | p3 | open | <agent-copies>/praetor-needs-miner.md:22 |  |
| `BUG-586` | [F65] `harvest fleet` prints a hard-coded static topology and ignores all arguments | p3 | open | cmd/standardsctl/harvest.go:134 |  |
| `BUG-587` | [F650] standards-lsp never reads maxCyclomatic/maxCognitive/hissEnforcement settings and never checks cyclomatic or cognitive complexity | p3 | open | cmd/standards-lsp/server.go:171 |  |
| `BUG-588` | [F651] Neovim ':StandardsRatchetSweep' invokes a baseline flag that does not exist | p3 | open | editors/neovim/lua/standards.lua:64 |  |
| `BUG-589` | [F652] VS Code HISS status bar item is a hardcoded '100% Pass' string, never updated from any audit result | p3 | open | editors/vscode/src/extension.ts:66 |  |
| `BUG-590` | [F653] VS Code 'Check Sentinel Host Headroom' command fabricates a verification result without checking anything | p3 | open | editors/vscode/src/extension.ts:126 |  |
| `BUG-591` | [F656] Documented Ed25519-signed waiver mechanism (.standards-waivers.yaml) does not exist in code | p3 | open | docs/guides/onboarding.md:53 |  |
| `BUG-592` | [F657] ADR-0004 documents a 4-stage gating pipeline with baseline zero-debt checking; the real pipeline runs 6 stages and never consults the baseli | p3 | open | docs/adr/0004-prefetch-dogfood-gating-pipeline.md:10 |  |
| `BUG-593` | [F659] ADR-0006's linux/arm64 self-hosted runner tier has no matching ARC deployment manifest | p3 | open | docs/adr/0006-hierarchical-multi-tier-runner-matrix.md:17 |  |
| `BUG-594` | [F66] `needs migrate --apply` alone is a no-op because --dry-run defaults to true, contrary to the usage line | p3 | open | cmd/standardsctl/needs.go:195 |  |
| `BUG-595` | [F661] HISS-12 is defined as two mutually exclusive invariants across canonical docs in the same unit | p3 | open | docs/llms-full.txt:19 |  |
| `BUG-596` | [F664] Documented anti-spam concurrency lock script is never invoked anywhere in the repo | p3 | open | docs/standards/model-routing-and-fanout.md:63 |  |
| `BUG-597` | [F665] Model-routing doc names a catalog output file that the router code never writes | p3 | open | docs/standards/model-routing-and-fanout.md:94 |  |
| `BUG-598` | [F667] filepath.Walk traversals have no scalar iteration bound (HISS-02 documented, not enforced) | p3 | open | internal/hiss/hiss.go:64 |  |
| `BUG-599` | [F674] .agents/plugins/praetor/agents holds 12 unread duplicate agent definitions that no audit verifies | p3 | open | <agent-copies>/praetor-fuzzer.md:1 |  |
| `BUG-600` | [F676] editors/ tree is orphaned and has already drifted from the generator that overwrites its outputs | p3 | open | editors/neovim/lua/standards.lua:1 |  |
| `BUG-601` | [F677] Multi-forge federation is dead: NewForge and the entire GitLab and Gitea drivers have no caller | p3 | open | internal/forge/forge.go:56 |  |
| `BUG-602` | [F678] Receipt output binding is dead: VerifyReceiptWithOutput has no caller and the only producer signs a constant payload | p3 | open | internal/lockdown/receipts.go:127 |  |
| `BUG-603` | [F680] make fuzz is invoked by no CI workflow, so eight fuzz targets only ever run their seed corpora | p3 | open | Makefile:28 |  |
| `BUG-604` | [F681] topology-audit is a no-op inside CI: verify-all's DEV-01..05 gate is guarded on $HOME/dev existing | p3 | open | Makefile:79 |  |
| `BUG-605` | [F682] templates/ tree has no reader: 14 .tmpl files are orphaned while scaffolding uses hard-coded Go strings | p3 | open | templates/common/workingdir/STATE.md.tmpl:1 |  |
| `BUG-606` | [F71] K26 delta: literal `--dir=.` treated as the directory in 7 more state subcommands whose usage text advertises it | p3 | open | cmd/standardsctl/state.go:226 |  |
| `BUG-607` | [F72] K25 delta: topology.go falls back to the hard-coded workstation path /home/kilian/dev | p3 | open | cmd/standardsctl/topology.go:52 |  |
| `BUG-608` | [F77] harvest bundle copies shell history and MCP/agent configs verbatim into a transferable bundle with no redaction | p3 | open | internal/harvester/bundle.go:255 |  |
| `BUG-609` | [F89] ctx is accepted but never observed inside the I/O and deletion loops; the CLI's 2-minute timeout does not bound the destructive phase | p3 | open | internal/topology/topology.go:304 |  |
| `BUG-610` | [F92] Positive test cases pass flags that the command never reads; the assertions cannot fail against a stub | p3 | open | cmd/standardsctl/worktree.go:30 |  |
| `BUG-611` | [F93] Declared cyclomatic-complexity cap (HISS-04) is enforced by nothing; runBumpAudit is already over it | p3 | open | cmd/standardsctl/bump.go:300 |  |
| `BUG-612` | [F139] HISS-02 "context timeout on all I/O" not held by this package's file writers, and unenforceable by the repo's own rule | p3 | open | internal/needs/requests.go:122 |  |
| `BUG-613` | [F312] DryRun certifies every candidate; `bump train --dry-run` prints [CERTIFIED] without running anything | p3 | open | internal/bump/canary.go:154 |  |
| `BUG-614` | [F370] Distilled fact file is rewritten in nondeterministic order on every run | p3 | open | internal/hindsight/distiller.go:117 |  |
| `BUG-615` | [F374] HISS-04 cognitive-complexity cap is breached by five functions in this unit and the repo's own scanner cannot measure it | p3 | open | internal/docdistill/compressor.go:112 |  |
| `BUG-616` | [F399] Question options are joined and re-split on ",", so any option containing a comma is silently fragmented | p3 | open | internal/state/questions.go:141 |  |
| `BUG-617` | [F441] internal/difftest (463 LOC HISS-15 test synthesizer) has no importer and no consumer anywhere | p3 | open | internal/difftest/difftest.go:64 |  |
| `BUG-618` | [F477] CompileFrameworkAssets writes a file whose name is the unvalidated kit_name, allowing directory escape | p3 | open | internal/compiler/framework_assets.go:116 |  |
| `BUG-619` | [F531] internal/seo has zero production importers although every adopted repo declares the docs:seo-portal facet | p3 | open | internal/seo/seo.go:178 |  |
| `BUG-620` | [F543] Two staticcheck SA1012 violations sit in this unit's tests despite the zero-warnings invariant | p3 | open | internal/builder/builder_test.go:67 |  |
| `BUG-621` | [F583] renovate.json uses matchPackagePatterns, which current Renovate no longer supports | p3 | open | renovate.json:13 |  |
| `BUG-622` | [F584] detect_loop.py is referenced by nothing in the repository | p3 | open | .config/agent/hooks/detect_loop.py:1 |  |
| `BUG-623` | [F585] detect_loop.py writes its state to a fixed, predictable world-writable /tmp path with no O_EXCL | p3 | open | .config/agent/hooks/detect_loop.py:13 |  |
| `BUG-624` | [F673] Five .config subdirectories are empty with no producer or consumer, and scripts/adopt_priority_repos.sh has no invoker | p3 | open | .config/waivers |  |
| `BUG-625` | [F683] Neovim and JetBrains standards-integration files exist as two independently-maintained copies per editor, with real content differences | p3 | open | editors/neovim/lua/standards.lua:1 |  |
| `BUG-626` | [F685] Root mkdocs.yml and the distributed docs/presets/mkdocs/mkdocs.yml template have diverged in branding and theme content | p3 | open | mkdocs.yml:1 |  |
| `BUG-627` | [R1-25] Starlight preset cannot build: hero image src/assets/houston.webp does not exist | p1 | open | docs/presets/starlight/src/content/docs/index.mdx:8 |  |
| `BUG-628` | [R2-11] ADR-0002's "Highest Standard Wins" lattice join has zero production callers; facets are printed, never resolved | p1 | open | docs/adr/0002-highest-standard-wins-lattice.md:12 |  |
| `BUG-629` | [R2-12] ADR-0007's "universal builder compiles polyglot targets" — internal/builder never invokes a compiler and fabricates artifact paths | p1 | open | docs/adr/0007-universal-frameworks-org-and-demand-deduplication.md:20 |  |
| `BUG-630` | [R1-1] VS Code extension entrypoint ./dist/extension.js can never be produced: no tsconfig.json, no bundler, no build in Makefile or CI | p2 | open | editors/vscode/package.json:16 |  |
| `BUG-631` | [R1-19] hiss-audit's verification ladder orders scanners that do not exist and mislabels the one banned-call rule that does (HISS-08 vs implemented | p2 | open | .agents/skills/hiss-audit/SKILL.md:16 |  |
| `BUG-632` | [R1-2] extension.ts imports vscode-languageclient at runtime but package.json lists it only in devDependencies, so a packaged .vsix cannot activate | p2 | open | editors/vscode/package.json:93 |  |
| `BUG-633` | [R1-22] `project add <num> <url> --owner=...` and `hindsight sync <path> --bank=...` drop their flags — items land on the default org's board and fa | p2 | open | cmd/standardsctl/project.go:82 |  |
| `BUG-634` | [R1-26] mkdocs preset pins a PyPI package that does not exist (mkdocs-sitemap-plugin): install fails and the name is squattable | p2 | open | docs/presets/mkdocs/requirements.txt:3 |  |
| `BUG-635` | [R1-27] mkdocs preset's own `mkdocs build --strict` fails: nav lists three pages the preset does not ship | p2 | open | docs/presets/mkdocs/mkdocs.yml:63 |  |
| `BUG-636` | [R1-28] SEOHead.astro is never rendered: no component override, no import -- the preset's advertised SEO component is dead code | p2 | open | docs/presets/starlight/src/components/SEOHead.astro:1 |  |
| `BUG-637` | [R1-30] Starlight preset's Core Web Vitals stylesheet is inert: invalid property, selectors that match no Starlight markup, unused @font-face, dead | p2 | open | docs/presets/starlight/src/styles/custom.css:9 |  |
| `BUG-638` | [R1-41] C4 shows .standards.yaml driving the gating engine; the pipeline hardcodes its limits and never loads the manifest | p2 | open | docs/architecture/c4-models.md:62 |  |
| `BUG-639` | [R1-44] Published invariant tables are two invariants stale and their own index page misdescribes them | p2 | open | docs/wiki/HISS-Matrix.md:3 |  |
| `BUG-640` | [R1-46] "SLSA Level 3" is advertised in three places but no provenance is ever generated and no signature is ever verified | p2 | open | .github/workflows/sbom.yml:16 |  |
| `BUG-641` | [R1-47] sbom.yml uploads to a GitHub release that its own workflow never creates, racing a concurrent workflow | p2 | open | .github/workflows/sbom.yml:60 |  |
| `BUG-642` | [R1-48] wiki-sync bootstrap branch leaves cwd in /tmp/wiki, so the relative cp after the if/else cannot resolve and the step dies under set -e | p2 | open | .github/workflows/wiki-sync.yml:47 |  |
| `BUG-643` | [R1-49] wiki-sync mirror is additive only: pages deleted from docs/wiki are never removed from the wiki | p2 | open | .github/workflows/wiki-sync.yml:47 |  |
| `BUG-644` | [R1-5] Helm chart's container image is published by no workflow: every install ends in ImagePullBackOff | p2 | open | deploy/helm/praetor/values.yaml:4 |  |
| `BUG-645` | [R1-52] DCO 1.1 gate is skipped entirely on push, so direct commits to main and lts-* bypass it | p2 | open | .github/workflows/compliance.yml:41 |  |
| `BUG-646` | [R1-6] ServiceAccount name is a fixed literal, so a second release collides and create=false dangles | p2 | open | deploy/helm/praetor/templates/serviceaccount.yaml:5 |  |
| `BUG-647` | [R2-1] Optimizer output is written into a value copy and is never consumed by anything | p2 | open | internal/builder/builder.go:127 |  |
| `BUG-648` | [R2-13] docs/wiki "Multi-Forge Federation": GitLab and Gitea drivers are no-op stubs that return fabricated success | p2 | open | docs/wiki/API-Reference.md:21 |  |
| `BUG-649` | [R2-17] Wiki and C4 docs advertise AST / gocyclo / gitleaks / semgrep enforcement for HISS rules implemented as substring greps or not at all | p2 | open | docs/architecture/c4-models.md:82 |  |
| `BUG-650` | [R2-2] NativeGPUConfig / GenerateMesonArgs / GenerateCMakeArgs have no non-test caller; the native-gpu target ignores them entirely | p2 | open | internal/builder/native.go:34 |  |
| `BUG-651` | [R2-22] Every archetype's devcontainer_features list is contradicted by internal/devcontainer, which hardcodes the Go feature for all repos | p2 | open | internal/devcontainer/devcontainer.go:110 |  |
| `BUG-652` | [R2-23] HISS-18's manifest switch `overrides.ci` has no struct field and internal/cifilter never loads the manifest | p2 | open | .standards.yaml:45 |  |
| `BUG-653` | [R2-31] Prompt-injection neutralizer is 100% dead code while five untrusted-text paths feed agent context verbatim | p2 | open | internal/lockdown/sanitize.go:119 |  |
| `BUG-654` | [R2-32] docdistill actively promotes attacker-chosen imperative lines into the agent-served "Invariants & Gotchas" section | p2 | open | internal/docdistill/compressor.go:160 |  |
| `BUG-655` | [R2-4] Config writes follow symlinks (no O_NOFOLLOW / lstat), so a repo-controlled symlink redirects the write outside the tree | p2 | open | internal/editor/editor.go:844 |  |
| `BUG-656` | [R2-6] JetBrains inspection profile enables 9 inspection classes that no plugin in this repo implements | p2 | open | editors/jetbrains/inspectionProfiles/standards.xml:6 |  |
| `BUG-657` | [R2-8] AGENTS.md:29 advertises HISS-15 enforcement as a "CI coverage gate"; no coverage instrumentation exists in any workflow, Makefile target, or | p2 | open | AGENTS.md:29 |  |
| `BUG-658` | [R2-9] The entire fuzz battery is unreachable from any gate, has no persisted seed corpus, and every one of its 8 bodies asserts only "did not pani | p2 | open | Makefile:28 |  |
| `BUG-659` | [R1-10] No .dockerignore: the "hermetic builder" COPYs .git, .workingdir and agent configs into the build stage | p3 | open | build/package/Dockerfile:19 |  |
| `BUG-660` | [R1-11] deploy/k8s/kustomization.yaml is orphaned, and its commonLabels would mutate the immutable Deployment selector | p3 | open | deploy/k8s/kustomization.yaml:6 |  |
| `BUG-661` | [R1-12] Pod gets a ServiceAccount token it cannot use: the binary has zero Kubernetes dependencies | p3 | open | deploy/helm/praetor/templates/deployment.yaml:25 |  |
| `BUG-662` | [R1-13] standards-sync step 4 invokes `baseline --verify`, a flag the baseline FlagSet rejects; no ratchet check exists at all | p3 | open | .agents/skills/standards-sync/SKILL.md:43 |  |
| `BUG-663` | [R1-14] All 11 .agents/skills/**/SKILL.md are orphaned: no loader, no CLI, no audit gate, no plugin manifest entry | p3 | open | .agents/skills/adhd-format/SKILL.md:1 |  |
| `BUG-664` | [R1-15] infocard-generate makes every generated infocard assert four enforcement tools that do not exist in the repo | p3 | open | .agents/skills/infocard-generate/SKILL.md:32 |  |
| `BUG-665` | [R1-16] infocard-generate's mandated template is a broken nested fence: the closing ``` at :51 ends the template early and :60 opens an unterminated | p3 | open | .agents/skills/infocard-generate/SKILL.md:51 |  |
| `BUG-666` | [R1-17] seo-audit's step 1 cannot pass for this repo: the JSON-LD overrides live in docs/presets and mkdocs.yml never sets theme.custom_dir | p3 | open | .agents/skills/seo-audit/SKILL.md:13 |  |
| `BUG-667` | [R1-18] seo-audit declares dateModified a required TechArticle field, but ValidateTechArticle accepts it empty and both shipped presets omit it | p3 | open | .agents/skills/seo-audit/SKILL.md:15 |  |
| `BUG-668` | [R1-20] adr-scaffold's template and checklist diverge from docs/adr/README.md's own lifecycle, receipt rule and index/nav registration | p3 | open | .agents/skills/adr-scaffold/SKILL.md:24 |  |
| `BUG-669` | [R1-21] adr-scaffold prompts authors for "HISS-01 through HISS-16" although HISS-17 and HISS-18 are now declared invariants | p3 | open | .agents/skills/adr-scaffold/SKILL.md:27 |  |
| `BUG-670` | [R1-23] praetor-adopt composite action: `dry-run: false` (its own default) still produces a dry-run dogfood, because standardsctl's dogfood --dry-ru | p3 | open | .github/actions/praetor-adopt/action.yml:60 |  |
| `BUG-671` | [R1-24] praetor-adopt composite action: `record-baseline: false` is inert — adopt's --record-baseline defaults to true, so the opt-out never reaches | p3 | open | .github/actions/praetor-adopt/action.yml:73 |  |
| `BUG-672` | [R1-29] Both presets emit canonical URLs and sitemaps for standards.cordana.ai, a domain that does not resolve | p3 | open | docs/presets/starlight/astro.config.mjs:7 |  |
| `BUG-673` | [R1-3] Contributed standards.mcp.* settings are never read by the extension, and standards.lsp.trace.server is written under a key the language cli | p3 | open | editors/vscode/package.json:65 |  |
| `BUG-674` | [R1-31] Preset dependencies are unpinned with no lockfile and no CI build, in two ecosystems the repo's pinning policy does not cover | p3 | open | docs/presets/starlight/package.json:13 |  |
| `BUG-675` | [R1-32] Starlight content config uses the pre-0.32 collection shape: no loader: docsLoader() | p3 | open | docs/presets/starlight/src/content/config.ts:5 |  |
| `BUG-676` | [R1-33] Starlight sidebar autogenerates from directories that do not exist and the hero CTA links to a 404 | p3 | open | docs/presets/starlight/astro.config.mjs:78 |  |
| `BUG-677` | [R1-38] C4 container diagram claims the HISS scanner emits SARIF; it emits no SARIF at all | p3 | open | docs/architecture/c4-models.md:69 |  |
| `BUG-678` | [R1-39] C4 lists a distroless OCI image as a generated projection, but no workflow builds or publishes any image | p3 | open | docs/architecture/c4-models.md:59 |  |
| `BUG-679` | [R1-4] Unused `path` import in extension.ts breaks the zero-warnings invariant and fails tsc under noUnusedLocals | p3 | open | editors/vscode/src/extension.ts:1 |  |
| `BUG-680` | [R1-42] Wiki front page inverts the compile-context data flow (.standards.yaml -> compile-context -> AGENTS.md) | p3 | open | docs/wiki/Home.md:9 |  |
| `BUG-681` | [R1-43] Verification ladder's fifth layer (cordana-standards[bot] admission gate) does not exist | p3 | open | docs/wiki/HISS-Matrix.md:36 |  |
| `BUG-682` | [R1-45] HISS-Matrix.md is hand-written into a generator-owned directory and is unlinked from the generated index | p3 | open | docs/wiki/HISS-Matrix.md:1 |  |
| `BUG-683` | [R1-50] SBOMs attached to the release describe the source tree, not the released binaries | p3 | open | .github/workflows/sbom.yml:39 |  |
| `BUG-684` | [R1-51] supply_chain policy keys (slsa_level, enforce_cosign, require_sbom) are parsed, merged and printed but never enforced by any code path | p3 | open | .standards.yaml:42 |  |
| `BUG-685` | [R1-53] The "Licensing Verification Gate" cannot detect the Apache/EUPL license contradiction it is named for | p3 | open | .github/workflows/compliance.yml:57 |  |
| `BUG-686` | [R1-54] goreleaser uses archives.format / format_overrides.format, deprecated since goreleaser v2.6, against a floating '~> v2' pin | p3 | open | .goreleaser.yaml:59 |  |
| `BUG-687` | [R1-55] The release archives the whole pipeline exists to produce have no documented consumer | p3 | open | .goreleaser.yaml:63 |  |
| `BUG-688` | [R1-56] topology clean accepts a positional dev-root, after which flag.Parse stops and --dry-run=false is silently ignored | p3 | open | cmd/standardsctl/topology.go:108 |  |
| `BUG-689` | [R1-7] ArgoCD Application auto-syncs an unpinned git HEAD with prune and selfHeal | p3 | open | deploy/k8s/application.yaml:12 |  |
| `BUG-690` | [R1-8] Three values.yaml keys are read by no template (imagePullSecrets, nameOverride, fullnameOverride) | p3 | open | deploy/helm/praetor/values.yaml:8 |  |
| `BUG-691` | [R2-10] The seo-audit skill's only "run the validator" instruction is `go test ./internal/seo/...`, which re-validates hardcoded fixtures and never | p3 | open | .agents/skills/seo-audit/SKILL.md:19 |  |
| `BUG-692` | [R2-14] ADR-0003's "100% test coverage" on the canonical engine is false (measured 57.1% in internal/config) | p3 | open | docs/adr/0003-canonical-engine-and-reverse-dogfooding-topology.md:18 |  |
| `BUG-693` | [R2-15] ADR-0006's 4-tier runner matrix routes no CI job: Tier 3 key absent, every workflow hardcodes ubuntu-latest | p3 | open | docs/adr/0006-hierarchical-multi-tier-runner-matrix.md:14 |  |
| `BUG-694` | [R2-16] ADR-0007 clause 5: the framework asset compiler has no CLI entry point | p3 | open | docs/adr/0007-universal-frameworks-org-and-demand-deduplication.md:22 |  |
| `BUG-695` | [R2-18] ADR-0001 claims a single canonical source for all agent instructions, but names a vendor with no compiler target and coexists with hand-main | p3 | open | docs/adr/0001-universal-context-transpiler.md:7 |  |
| `BUG-696` | [R2-19] Four archetypes declare complexity/supply-chain thresholds WEAKER than the fleet-wide HISS-04/HISS-11 invariants, and the lattice has no flo | p3 | open | .config/archetypes/app-service.yaml:7 |  |
| `BUG-697` | [R2-20] ADR-0002 lattice dimensions `memory` and `error unwraps` have no field in ResolvedPolicy and no rule in Join(), so two archetype files decla | p3 | open | internal/config/config.go:60 |  |
| `BUG-698` | [R2-21] Facet id -> filename mapping is undefined: three different transformations across five facets, none matching the documented `{facet-id}.yaml | p3 | open | docs/guides/archetype-authoring.md:49 |  |
| `BUG-699` | [R2-24] routing.yaml mislabels two local open-weights models as anthropic/openai, defeating the orthogonal-auditor independence rule | p3 | open | .config/models/routing.yaml:122 |  |
| `BUG-700` | [R2-25] Two lattice entries are orphans: closed-private.yaml and facets/perf-hotpath.yaml ids appear nowhere else in the repository | p3 | open | .config/archetypes/closed-private.yaml:1 |  |
| `BUG-701` | [R2-26] Eleven linters declared by archetypes exist nowhere else in the repo; for container-image and gitops-infra the entire declared linter set is | p3 | open | .config/archetypes/gitops-infra.yaml:23 |  |
| `BUG-702` | [R2-28] routing.yaml target_tasks (16 entries) and two of three governance keys are parsed and regenerated but never read by any selection logic | p3 | open | .config/models/routing.yaml:5 |  |
| `BUG-703` | [R2-29] template-seed archetype is byte-for-byte config.DefaultPolicy(): under the min/max lattice it can only ever contribute one extra linter | p3 | open | .config/archetypes/template-seed.yaml:6 |  |
| `BUG-704` | [R2-3] cfg.OutputDir from the YAML manifest is passed to MkdirAll unvalidated | p3 | open | internal/builder/builder.go:121 |  |
| `BUG-705` | [R2-30] Facet descriptions cite HISS-03 and HISS-14, which are absent from the canonical AGENTS.md invariant table and from .standards.yaml | p3 | open | .config/archetypes/facets/perf-hotpath.yaml:3 |  |
| `BUG-706` | [R2-33] Untrusted clone URL is passed positionally to git with no `--` separator (option and ext:: transport injection) | p3 | open | internal/dogfood/remote.go:69 |  |
| `BUG-707` | [R2-34] sanitize.go's pattern set is exact-ASCII and phrase-literal: trivially evaded if it were ever wired | p3 | open | internal/lockdown/sanitize.go:17 |  |
| `BUG-708` | [R2-5] `editors generate` reports files it deliberately skipped as successfully generated | p3 | open | cmd/standardsctl/editors.go:37 |  |
| `BUG-709` | [R2-7] Three VS Code configuration keys are declared and written into .vscode/settings.json but read by nothing | p3 | open | editors/vscode/package.json:65 |  |
| `BUG-710` | [R1-34] Both presets ship machine-readable Apache-2.0 license assertions that downstream copies inherit | p3 | open | docs/presets/starlight/astro.config.mjs:66 |  |
| `BUG-711` | [R1-9] docker/dev/Dockerfile's "< 250MB" header claim is false by a wide margin | p3 | open | docker/dev/Dockerfile:2 |  |
| `BUG-712` | [R2-27] Archetype top-level keys id/name/description/runtime match no Go struct, and ResolvedPolicy's untagged fields would not accept branch_protec | p3 | open | internal/config/config.go:59 |  |

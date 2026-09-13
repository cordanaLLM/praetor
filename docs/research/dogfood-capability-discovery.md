# Public dogfood capability discovery

The 2026-09-13 public discovery run examined a cohort of 100 external,
unowned GitHub repositories pinned to immutable default-branch commits. It was
an observation run only: no upstream commands, builds, tests, repairs or
publication actions were performed. The selected cohort spans 26 language
categories and includes application frameworks, compilers, CLIs, formatters,
build systems, documentation tools, infrastructure, security tools and data
systems.

## Result

| Measure | Result |
| --- | ---: |
| Cohort cases attempted | 100 |
| Cases fully observed | 75 |
| Cases partial | 13 |
| Cases failed | 12 |
| Deduplicated candidates | 37 |

A fresh full replay retained identical case statuses, source/tree identities, and
candidate rankings. Both runs were `partial` because 25 cases did not complete;
all discovery reports remain `verified: false`, including complete ones. A
candidate groups observations where a selected Praetor capability or
adapter was unavailable in the examined context. Its recurrence ranks demand;
it does not establish correctness, project readiness, a missing upstream
feature, or permission to open an issue or dispatch a repair.

The policy catalog currently mixes scanner-extension checks and marker-based
adapter checks. For example, `hiss:javascript`, `hiss:ruby`, `hiss:lua` and
`needs:ruby` describe selected scanner or analyzer availability in Praetor.
They do not assert that a repository is written in that language. Marker
matches are evidence of a file in a bounded tree, not proof that the file is
the project's active build root. The `console.csproj` match in
`sharkdp/bat` is an intentionally invalid fixture and must not be treated as
a real .NET project.

## Incomplete cases

The 25 incomplete cases fall into bounded, actionable classes:

| Class | Cases | Meaning |
| --- | ---: | --- |
| Checkout exceeded 20,000 entries | 7 | Snapshot entry bound was reached. |
| One file exceeded 16 MiB | 4 | Snapshot refused an oversized input file. |
| Checkout exceeded 256 MiB | 1 | Aggregate snapshot byte bound was reached. |
| More than 128 marker roots | 7 | A rule had too many distinct matched directories. |
| Marker and verification bounds together | 2 | One case hit both bounded discovery paths. |
| Verification input entries exceeded 4,096 | 2 | Declarative verification planning exceeded its input bound. |
| Verification metadata exceeded byte bounds | 1 | A metadata input exceeded the planner's byte limit. |
| Invalid marker fixture | 1 | `sharkdp/bat` contained an invalid `console.csproj` fixture. |

These outcomes mean the case is incomplete, not that the repository lacks the
capability. The next run should retain each case error and classify it as
`not-checked` or `partial` rather than turning a bounded omission into a clean
result. Large generated, fixture and data trees are useful stress inputs, but
their presence should not make a missing adapter claim look like a project
qualification result.

## Highest recurrence candidates

The first ten candidates by repository recurrence are shown with one stable
public example from the retained evidence. The pin identifies the observed
source revision; it is not a release or correctness claim.

| Candidate | Repositories | Example observed revision |
| --- | ---: | --- |
| `hiss:javascript` | 36 | [starship/starship](https://github.com/starship/starship/tree/9dc44a57b1833e4b124bd6237f20d898ac4262e1) |
| `hiss:typescript` | 14 | [starship/starship](https://github.com/starship/starship/tree/9dc44a57b1833e4b124bd6237f20d898ac4262e1) |
| `hiss:ruby` | 10 | [helix-editor/helix](https://github.com/helix-editor/helix/tree/079a789e8cb08ead67f19e1971a1b7438b37354b) |
| `hiss:lua` | 8 | [starship/starship](https://github.com/starship/starship/tree/9dc44a57b1833e4b124bd6237f20d898ac4262e1) |
| `needs:ruby` | 7 | [rails/rails](https://github.com/rails/rails/tree/f3deab27b7b7ec2166ea9637f4522cd711d82abd) |
| `hiss:java` | 6 | [helix-editor/helix](https://github.com/helix-editor/helix/tree/079a789e8cb08ead67f19e1971a1b7438b37354b) |
| `hiss:vimscript` | 6 | [helix-editor/helix](https://github.com/helix-editor/helix/tree/079a789e8cb08ead67f19e1971a1b7438b37354b) |
| `hiss:dotnet` | 5 | [AvaloniaUI/Avalonia](https://github.com/AvaloniaUI/Avalonia/tree/eb5cce2492afaf2e296c290acbcffbf117e46dcc) |
| `hiss:perl` | 5 | [curl/curl](https://github.com/curl/curl/tree/efd42dfbbf656870f8885815d07b0d16a5771f69) |
| `hiss:swift` | 5 | [rustdesk/rustdesk](https://github.com/rustdesk/rustdesk/tree/bf1ebe5be2f5ac1ce634572e9ff7bc88e8b1d6aa) |

The examples show why recurrence must be reviewed with evidence paths and
marker context. A repository can contain generated, vendored, test or fixture
files for several languages. The observation records the selected rule's
availability in that context; it does not infer the repository's root language
or its supported product surface.

## Qualification and implementation order

1. Preserve the cohort manifest, requested pins, resolved source SHA, filtered
   tree digest, per-file evidence and per-case status in a durable run receipt.
   Require explicit `observed`, `partial`, `failed`, `not-checked` and
   `not-applicable` meanings before aggregating candidates.
2. Add a root-aware marker policy. A matched file in a test, fixture or nested
   example tree should be reported as context evidence and should not silently
   qualify an adapter or build root. Keep the invalid `console.csproj` case as
   a negative control.
3. Make bounds first-class results. Add bounded selection or sampling for
   oversized files, large trees and marker roots while retaining an explicit
   omitted-count and reason. Re-run the 25 incomplete cases before comparing
   recurrence across cohorts.
4. Reconcile the selected catalog with actual analyzer and scanner support.
   Add adapters only behind their existing policy, source ownership and
   evidence contracts; an unsupported selected rule is a demand signal, not an
   implementation acceptance.
5. Qualify each proposed adapter with positive, negative and boundary fixtures,
   then replay the complete observed cohort and compare candidate keys,
   evidence paths, source pins and tree digests. Do not infer a missing product
   capability from a filename or from an unavailable analyzer alone.

The run supplies a bounded external workload and prioritization signal. It
does not qualify upstream builds, language identity, release artifacts,
application behavior, or a production rollout.

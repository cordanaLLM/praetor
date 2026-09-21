# Bounded local repair execution

The repair executor turns one routed dogfood failure into a retained candidate
patch. It uses the configured Responses provider once, after existing Go tests
reproduce a failure in the pinned Praetor source. This stage supports reviewed
owned Praetor engine source. Public repositories remain dogfood inputs; their
arbitrary application tests are not dispatched by this executor.

`praetorctl dogfood repairs status --config /absolute/execution.json --report
/absolute/report.json` reads admission state. `repairs run` consumes one eligible
case. Both strictly validate the retained suite report and recompute the route
from the explicitly configured routing policy. They never execute saved plan
instructions or follow transcript paths in the report.

The private execution JSON has these required fields:

| Field | Contract |
|---|---|
| `version` | `1` |
| `source_root`, `source_sha` | Clean absolute source checkout and full 40-character commit SHA |
| `state_dir` | Private absolute state directory; parent exists |
| `allowed_files` | 1–8 explicit existing non-test `.go` paths under `internal/` or `cmd/` |
| `test_packages` | 1–4 exact `./internal/name` or `./cmd/name` packages |
| `timeout_seconds` | 30–300 seconds for the complete execution |
| `max_patch_bytes` | 1,024–262,144 bytes |
| `repair_policy` | Existing routing policy with declared token estimates and cost ceiling |
| `provider` | Exact endpoint, model, pinned credential helper, input and output bounds |

`repair_policy` accepts two optional fields beside its routing inputs: `register`
(`social`, `docs` or `internal`) and `max_output_tokens` (256..8192). `dogfood repairs`
fills them from the [text register](text-register.md) row of `--task`. Each planned job
carries both, its instructions end with one register sentence, and the run report records
`register` beside `usage`. A job budget lowers the provider's `max_output_tokens` for that
request and never raises it above the configured value. A policy without the fields plans
and runs exactly as before.

When `register` is present, the planner validates its generated job instructions before it
retains the plan. The executor validates the engine-owned prompt prefix and the separate
Responses API instructions before provider dispatch, then validates the returned
`proposal.summary` before any candidate source mutation. Internal text must pass the
Caveman brief, message or return contract as appropriate; `social` and `docs` produce an
explicit `not_applicable` record. The execution report preserves these records as
`prompt_validation`, `request_instructions_validation` and `summary_validation`. Untrusted
source and test payloads are appended only after the prompt prefix passes and are never
linted as instructions.

`provider` fields are `base_url`, `token_command`, `token_command_sha256`, `model`,
`max_output_tokens`, and `max_input_bytes`. The routed model must equal the
configured provider model; there is no fallback. Configured cost is an estimate,
not a billing reservation. Unknown measured cost remains absent. The returned
model is server-reported identity.

The endpoint is configurable: a canonical HTTPS DNS/IP URL of at most 4096 bytes,
with an optional port 1–65535 and clean literal path. Credentials, query strings,
fragments, escapes and trailing slashes are rejected. The transport appends
`/responses`; redirects and environment proxies remain disabled. Selecting a URL
does not establish ownership or authorization. Helper digest and credential
handling checks still apply. [Connection profiles](client-connections.md) can
generate this provider subsection alongside the shared MCP registry.

Admission uses a stable key derived from the source commit, exact configuration
and routing input hashes, and case kind, ID and input hash. Report timestamps,
error messages and attempt paths do not create new attempts. A crash after
`started.json` consumes that key. Status reports it as `interrupted`; execution
continues only for a different unconsumed case or changed source/policy. A lock
prevents overlapping execution. At most 32 attempts are retained per state root;
there is no automatic evidence deletion.

Source extraction reads the immutable Git tree and individual blobs. Dirty files,
untracked files, mutable export attributes, shared Git metadata, original
transcripts and global CLI configuration are excluded. Trees are limited to
20,000 entries and 16 MiB, with regular files only. Only the allowlisted source
contents and isolated failed-test identities enter the model prompt. Raw retained
failure excerpts are never sent.

Verification runs fixed `go test -json` arguments with candidate source mounted
read-only in bubblewrap. The sandbox has no network, host home or credentials;
it can read the installed toolchain and workstation Go dependency cache. That
cache visibility is why this rollout is restricted to reviewed owned source.
Temporary writes use a 1 GiB private tmpfs. A user systemd scope limits the whole
process tree to 4 GiB memory, zero swap, 128 tasks and 200% CPU; per-process limits
also bound address space, CPU time, output file size and open descriptors. Missing
namespaces, user scopes or dependencies fail before provider dispatch.

A successful candidate must execute every completed baseline test identity and
pass every previously non-skipped test. Additional tests may complete after an
early failure is fixed.
Exit status zero with missing tests is rejected. Every changed path and file mode
is checked against the baseline; tests, instructions and configuration cannot be
edited. This first stage also preserves imports, declarations, function signatures
and call expressions, rejects new initialization hooks, and rejects added test
control framing. It supports local expression and control-flow fixes; broader
changes are rejected for review. Test events alone are not a tamper-proof proof
of arbitrary Go semantics. Logs, exact patch, source manifest, proposal, usage and result remain in
the private attempt directory.

`not_reproduced` means baseline tests passed and no provider was called.
`scoped_test_verified` means the retained candidate passed these unchanged tests.
It does not claim the original private corpus was fixed. Failed, timed-out,
rejected and interrupted attempts remain visible and consumed. Execution does
not modify the source checkout, install the candidate, publish, merge or promote
it. The separate local timer may select the next retained failure for execution.

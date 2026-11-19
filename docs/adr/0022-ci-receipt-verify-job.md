# ADR-0022: Optional CI Receipt Verification Job

## Status

Proposed — 2026-11-19. Design only; policy fields, rotation support and scaffolding below are proposed interfaces.

## Context

A review bot needs independently readable evidence for the pull request head when it cannot run the author's local gate. The existing receipt already signs the command, exit code, timestamp, commit SHA, repository and SHA-256 of the gate output using Ed25519. Its envelope version is `v1`; the certified output format is `praetor-gate-output/v2`, which distinguishes passed, failed, skipped and not-applicable stages (`internal/lockdown/receipts.go:19`, `internal/lockdown/receipts.go:99`).

The gate writes the signed envelope and verbatim output to `.standards-receipt.json` (`internal/gating/pipeline.go:1069`). Adoption ignores this file; committing it would move HEAD beyond the commit it certifies (`internal/util/gate_receipt.go:8`, `internal/adopt/gitignore_history_test.go:41`). The existing PR template asks for that envelope in a labelled receipt fence, and admission checks its signature, output and head binding (`internal/forge/pr_template_test.go:61`, `internal/forge/wiki.go:325`, `internal/forge/pr.go:200`). Receipt examples in agent skills are reference tokens, not another transport (`internal/compiler/skill_references.go:49`).

Adoption already scaffolds selected flavor, documentation and API workflows. The narrower current claim is that it installs no CI job running a `praetorctl` gate (`docs/guides/adoption-verification.md:2566`). Receipt verification must not imply that CI ran declared linters: that remains a separate policy obligation (`docs/guides/effective-policy.md:54`). HISS-19 requires reuse of the existing receipt verifier; HISS-21 requires truthful platform coverage and explicit unsupported cases (`AGENTS.md`).

## Decision

### Opt-in and scaffold ownership

Add a default-off policy switch, `receipt.ci_verify`, to the existing receipt section of `.standards.yaml`. An adopter would commit its public key and set this switch to true, then apply adoption to render a shared receipt-verification template through the flavor template machinery. The proposed workflow filename is praetor-receipt.yml under `.github/workflows/`; its head-associated check name is “praetor-receipt-verify”. A false or absent switch preserves today's behavior, including existing admission wherever configured; it adds no workflow or required receipt check.

Extend the existing manifest and template resolver rather than introducing another policy file or per-language verifier (`internal/config/config.go:339`, `internal/flavor/flavor.go:16`, `internal/flavor/planned.go:28`). The dry-run plan must show the opt-in workflow and required head check, and refuse activation if the key, supported forge or template requirements are missing. Preserve repository-owned workflows under the existing ownership rules. This adds configuration and CI maintenance for adopters who opt in; others retain the present adoption contract. Opting out later must explicitly reconcile the owned workflow and required check in the plan, so it cannot leave an unsatisfiable merge requirement.

### Host custody and committed trust

Keep the private signing key on the adopter's signing host, private to its user; never put it in forge secrets, runner storage or artifacts. Retain the existing host key loader and its permission checks (`internal/lockdown/keys.go:19`, `internal/lockdown/keys.go:81`). This proposal constrains custody; it does not claim the current file loader provides hardware isolation or encryption at rest.

Commit the matching 32-byte Ed25519 public key as 64 hexadecimal characters in the existing `receipt.public_key` field. The receipt's embedded key must match an authorized committed key before signature verification; trusting that embedded key alone would allow self-signed evidence (`internal/lockdown/keys.go:185`, `internal/lockdown/keys.go:217`). Read this policy from a recorded, current protected target-branch revision, never from the PR's manifest or historical merge base. A key change becomes authoritative only after it lands through the target branch's existing protections. This deliberately prevents a PR introducing a new key from using it to certify itself, at the cost of a separate trust-bootstrap change.

For rotation, propose two optional fields beside the primary key: `receipt.previous_public_key` and `receipt.previous_key_valid_until` (an explicit UTC deadline). First land the new primary and the old key as previous, with a reviewed deadline; then switch host signing and replace outstanding PR receipts. Accept either key before the deadline; accept only the primary at or after it. The overlap is adopter-chosen and finite, never extended automatically. Finally remove the previous-key fields. The current schema and verifier accept one pinned key, so this requires a shared verifier extension, not workflow-side signature logic (`internal/config/config.go:342`, `internal/lockdown/keys.go:248`).

Retired-key receipts remain historical signed records but fail every new check, regardless of their signed timestamp; reissue them for the unchanged head using the active key. On suspected compromise, remove the key immediately instead of granting overlap. Rotation trades continuity for a bounded period with two authorized signers. A signer-controlled timestamp cannot extend trust. Trust-policy changes and retirement deadlines must invalidate or refresh outstanding check results; review must reject a result whose trust revision or key authorization is stale.

### Receipt input, verification and head association

Keep `.standards-receipt.json` ignored. The author transports its complete envelope through the existing PR-body receipt fence; the job reads that fence as untrusted data. No receipt-only commit is needed. Reuse the existing fence parser and bounded envelope reader, including the 1 MiB limit and refusal of escaping links, FIFOs and appended JSON documents (`internal/forge/pr.go:170`, `internal/lockdown/receipts.go:190`, `internal/lockdown/receipts_read_test.go:27`). Materialize the envelope only in an isolated job directory, never by writing PR-controlled paths.

The job must use a pinned released verifier and trusted workflow, without building or executing PR code. For the initial GitHub scaffold, use base-context PR metadata events, including body edits and head updates; do not check out the PR. GitHub documents the security risk of executing untrusted code under `pull_request_target`; this data-only use avoids that execution path ([GitHub security guidance](https://docs.github.com/en/actions/reference/security/securely-using-pull_request_target), checked 2026-11-19).

Verify the authorized key, Ed25519 signature, zero exit code and exact gate-output hash through `VerifyPinnedReceiptFile`; retain its output-version, clean-worktree declaration and race-output checks (`internal/lockdown/keys.go:238`). Additionally require receipt version `v1`, the canonical gate command, the target repository identity and `commit_sha` equal to the live PR head SHA. The existing local head-binding and explicit-key repository checks provide reusable behavior, not permission to trust a synthetic merge SHA (`cmd/standardsctl/gate.go:200`, `internal/lockdown/receipts.go:250`, `internal/util/gate_receipt.go:13`). Missing, malformed, untrusted, stale-head or unsupported-version evidence fails closed; a skipped job is not verification.

Publish the result on that exact head, recording the repository, PR, receipt digest, output hash, signer, verifier version, trusted workflow/run identity and trust-policy revision. A base-context workflow's success alone is insufficient: the GitHub adapter must publish a separate check run with the verified SHA as `head_sha` ([Checks API](https://docs.github.com/en/rest/checks/runs#create-a-check-run), checked 2026-11-19). Limit job rights to contents read, pull requests read and checks write for policy retrieval, PR retrieval and check publication respectively; grant no content writes or signing access. Other forge adapters must demonstrate equivalent head association before activation, otherwise report unsupported.

Re-read the head, receipt digest and trusted policy before publishing success; discard a raced result. Recheck on body edits, new heads and trust changes, and at overlap expiry for outstanding PRs. Review must match the current body digest and authorized signer even if an older success remains visible. This freshness requirement adds event and expiry handling, but prevents a passing result for removed evidence or retired trust from satisfying review.

### Meaning of a passing check and review behavior

A pass proves that a holder of an authorized key signed the named head and gate-output hash, and that the supplied output matches that hash and satisfies the verifier's contract. It verifies the signer's declaration of a clean working tree; it does not independently prove a clean machine, an unmodified gate or toolchain, honest execution, or anything about later commits. Signed skipped or not-applicable stages do not become passed stages. An adopter needing independent execution must keep that separate requirement (`internal/lockdown/receipts.go:99`, `internal/lockdown/receipts.go:283`, `internal/lockdown/keys.go:238`).

Require review automation to accept a current, passing, trusted head check as the answer to a bot asking to run the local gate, asking for independent verification of its receipt, or reporting that it cannot run that gate. The author can point to the check result instead of repeating a local-run assertion. Match the expected check producer and trusted workflow/run identity as well as its name, head and evidence; another workflow using the same forge app or check name does not qualify. Keep findings about actual failures, omitted stages, independent execution or later commits open. An absent opt-in or stale, failed or skipped check preserves today's review behavior; this record cannot guarantee that an external bot will consume the check without integration.

### Adoption documentation contract

At implementation, replace the server-side sentence at `docs/guides/adoption-verification.md:2566` with exactly:

> Adoption installs no CI job that executes the local `praetorctl` gate pipeline. When `receipt.ci_verify` is enabled, adoption scaffolds a CI job that verifies the signed `.standards-receipt.json` envelope supplied in the pull request body against committed public-key policy and the exact pull request head; otherwise receipt handling is unchanged.

Update the generated rule and its tests at the same time (`internal/adopt/harness_ci.go:126`, `internal/adopt/harness_ci_test.go:133`). Distinguish verification from execution when enumerating scaffolded steps. Do not describe adoption as adding no CI workflows: that would contradict the existing guide. This ADR changes neither the guide nor generated text yet.

## Consequences

- Reviewers gain a forge-visible signature and head-binding result while full gate execution stays on the host.
- Host compromise or dishonest signing remains inside the trust boundary. Protected committed policy and cryptographic verification are independent barriers against PR-supplied replacement keys and altered evidence, not proof of honest execution.
- CI still consumes runner time for verification, metadata reads and reporting. Key rotation, evidence edits and expiry need explicit refresh handling; a signature-only job does not satisfy declared-linter execution policy.
- Acceptance requires positive, negative and boundary fixtures for opt-in rendering, missing keys, forged signatures, modified output, wrong repository/head/command/version, key substitution, removed fences, update races and overlap deadlines. Replay the existing bounded-reader cases, verify real checks on the exact head, and exercise each supported platform or report its gap. Every new check needs a planted failing case before its green result is trusted (HISS-15, HISS-20 and HISS-21 in `AGENTS.md`). This design adds no executable check.

## Alternatives

| Alternative | Benefit | Trade-off and disposition |
| :--- | :--- | :--- |
| Hosted runner executes the gate itself | Independent execution of the checked-out code; less reliance on the host's assertion | Repeats toolchain, dependency and test work in CI; requires measuring runner time and cost. HISS-21 still requires coverage on each supported platform or an explicit gap. Keep available for adopters requiring that stronger claim; receipt verification cannot substitute for it. |
| Workstation sets a forge status | Avoids the verification runner | Requires host write authority and makes the forge display another host assertion without independently checking the signed envelope. Reject as the default evidence path; a status label alone lacks the verifier's contract. |
| Sigstore Cosign keyless signing with OIDC | Removes long-lived signing-key custody and rotation | Adds an identity/issuer trust policy, certificate and transparency-log bundle verification, and service dependencies. It changes the existing Ed25519 receipt trust contract; defer to a separate design. OIDC identity attestation alone still does not prove that the local gate ran honestly. |

The Cosign alternative is verified against upstream documentation checked 2026-11-19: [signing blobs](https://docs.sigstore.dev/cosign/signing/signing_with_blobs/) documents `cosign sign-blob` and the signature/certificate/inclusion-proof bundle; [identity-based signing](https://docs.sigstore.dev/cosign/signing/overview/) describes ephemeral keys, Fulcio certificates bound to OIDC identities and Rekor logging; [verification](https://docs.sigstore.dev/cosign/verifying/verify/) requires the expected certificate identity and OIDC issuer for `cosign verify-blob`. These are alternative mechanisms, not commands introduced by this ADR.

## References

Repository sources above were checked on 2026-11-19 at commit `ccbd22c456e0472bbb011b69a9cc0d3526edf415`; line references refer to that revision. Public upstream sources were read on the same date.

- `internal/lockdown/receipts.go:77`: signed fields, canonical payload and envelope; `internal/lockdown/receipts_read_test.go:27`: positive, negative and boundary reader coverage.
- `internal/lockdown/keys.go:185`: committed-key parsing and pinned verification; `cmd/standardsctl/gate.go:200`: local verification bindings.
- `internal/gating/pipeline.go:1054`: receipt writer; `internal/forge/wiki.go:325`, `internal/forge/pr_template_test.go:61`, `internal/adopt/gitignore_history_test.go:41` and `internal/compiler/skill_references.go:49`: receipt consumers and conventions.
- `docs/guides/adoption-verification.md:2563`: local minting and scaffolded CI claims; `docs/guides/effective-policy.md:54`: independently executed declared linters.
- [ADR-0020: Forge App Installation Tokens](0020-forge-app-installation-tokens.md): record structure and separate forge credential design; this proposal needs no forge-held receipt signing key.

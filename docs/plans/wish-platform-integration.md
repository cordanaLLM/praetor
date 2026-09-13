# Wish platform integration plan

This plan extends the private wishes ledger toward optional forge and Discord
projections while keeping the local contract authoritative. It is a design
plan, not evidence of live integrations, credentials, external reads, public
posts, or bot deployment.

## Contract and ownership

The canonical object is a versioned `WishBallot`: wish ID, ballot revision,
ordered choice IDs and labels, eligibility policy, close policy, and digest.
Platform bindings are separate records containing provider namespace,
installation/account identity, repository or guild/channel, and external
resource and choice IDs. Never use a login string without its provider and
account namespace. Linking accounts is an explicit, revocable operation.

The local request API remains bounded and revision checked: `init`,
`add-wish`, `set-status`, `open-poll`, `vote`, `withdraw`, and `close-poll`.
Policy uses `max_wishes`, `max_polls`, `max_voters`, `allow_vote_changes`, and
`allow_withdrawal`. Version 1 accepts only `local:<id>` actors and one choice per vote.
External identities, multi-select ballots, and provider-specific moderation
are later schema versions.

## Staged implementation

1. **Core snapshot — implemented.** The shared Go package serves CLI and MCP
   with strict JSON decoding, state transitions, limits,
   deterministic status output, and conflict-safe revision handling. Acceptance
   tests cover valid init/apply/status, every action, positive/negative/boundary
   limits, duplicate IDs, malformed or unknown fields, revision conflicts,
   withdrawal, and cancellation. No source command or network call is allowed.

2. **Durable event seam.** Add an optional request/result receipt with stable
   idempotency key, actor namespace, before/after revision, and content digest.
   Add a bounded worker lease and retry state before any network adapter. Replay
   must produce the same snapshot; duplicate delivery must be harmless and
   removals must remain represented. Acceptance covers crash/retry, duplicate,
   out-of-order, changed ballot definition, and deletion handling.

3. **Forge observation.** Implement read-only bindings behind explicit provider
   capability flags. GitHub Discussions use the GraphQL poll/upvote model;
   Gitea and GitLab begin with issue/comment reactions as unqualified signals.
   Store page cursors, source API/schema version, observed totals, voter
   identities when available, and incomplete/error status. A reaction never
   becomes a qualified ballot without policy approval. Acceptance uses recorded
   fixtures for pagination, rate limits, removals, inaccessible resources,
   changed choices, duplicate events, and stable external IDs.

4. **Discord edge service.** Keep Gateway/HTTP handling at a narrow adapter
   boundary. Persist message and answer IDs, per-answer voter cursors, Gateway
   sequence/resume state, dedupe keys, and interaction acknowledgements. Honor
   signature validation, initial response deadlines, and REST/Gateway rate
   limits. Poll totals are distinct from imported qualified votes; an
   unfinalized or missing result is unknown. Acceptance covers add/remove vote,
   reconnect replay, pagination, finalized versus running results, expired
   tokens, invalid signatures, and bounded backoff.

5. **Outbox and moderation.** Add a reviewed outbox for public projections,
   with immutable ballot revision, redacted content, destination binding,
   approval, and delivery receipt. Private submissions remain private unless
   explicitly approved. Moderators can accept, decline, merge, or withdraw a
   wish while retaining the reason and source evidence. No automatic issue or
   discussion creation is permitted from a candidate count.

6. **Lifecycle and configuration.** Add provider credentials by external secret
   reference, account-link policy, retention/deletion policy, moderation policy,
   and deployment health checks. Configuration changes require a plan,
   reviewable diff, bounded migration, and replay. A missing credential,
   unsupported provider feature, or ambiguous identity yields `unknown` or
   `unsupported`, never a fabricated success.

## Reuse and acceptance boundary

The [platform research](../research/wish-voting-platforms.md) records the
verified upstream capabilities and limits behind these stages.

Reuse existing strict JSON/config validation, forge identity and pagination
helpers, package lifecycle state, and the shared bounded worker/lease seam
where their contracts match. Keep provider adapters free of package mutation,
source execution, or upstream issue classification. Acceptance is staged:
core tests first, then fixture replay, then provider contract tests, then an
explicitly authorized live canary. The global package pipeline is not complete
until all required stages have receipts; this plan does not claim that outcome.

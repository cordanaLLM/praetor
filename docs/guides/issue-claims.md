# Issue claims

An issue claim tells everyone reading the forge that one agent session works on an issue, on
which branch, and at which stage. It provides advisory coordination between sessions and
gives the issue a status before any pull request exists.

Claims are a Praetor capability, not a pipeline script: the same code serves the CLI, the MCP
tools and the dispatch gate (`internal/forge/claim.go`).

## Commands

Every command takes an issue reference `<owner>/<repo>#<number>`; the repository can differ
from the working directory. A reference without an owner, or with a non-numeric or zero
number, is refused (`forge.ParseClaimRef`). The forge token comes from `GITHUB_TOKEN`, `gh auth
token` or `--token`.

| CLI | MCP tool | Effect |
| :--- | :--- | :--- |
| `praetorctl issue claim <ref> --session <id> --lane <lane> --branch <branch>` | `standards_issue_claim` | Claim the issue. |
| `praetorctl issue status <ref> --session <id> --stage <stage> [--note <text>]` | `standards_issue_status` | Record a stage. Without `--stage` it reports the current claim and writes nothing. |
| `praetorctl issue release <ref> --session <id> --outcome landed\|abandoned\|handed-over [--note <text>]` | `standards_issue_release` | Finalise the claim. |
| `standardsctl forge claims migrate --repo <owner/repo> --issue N [--apply]` | - | Migrate legacy claim comments with plain identities to keyed tags. |

The CLI subcommands and the MCP tools build the same `forge.ClaimCommand` and run it on the
same `forge.ClaimDesk`, so they cannot drift
(`TestIssueClaimMCP_Positive_MirrorsTheCLIDesk`). A session identifier is 1 to 128 characters
of letters, digits and `. _ / : + @ -`, and the first character must be a letter or digit; lane
and branch follow the same rule. A branch name outside it, such as one with a space, is
refused.

## What a claim writes

A claim needs an open issue. On an issue the forge reports in any other state, such as
`closed`, the claim is refused with `forge.ErrIssueNotOpen`, the refusal names the state, and
nothing is written: reopening an issue stays a deliberate act on the forge
(`TestClaim_Negative_ClosedIssueIsRefusedNamingItsState`,
`TestClaim_Boundary_OnlyTheOpenStateIsClaimable`).

On an open issue, a claim does three things:

- It posts one claim comment. That comment is the only place the claim lives; every later
  stage of the session edits it in place, so an issue never collects one comment per stage.
- It adds the `status:in-progress` label, creating the label first when the repository has
  none.
- It assigns the authenticated account, and records whether the account was assigned before
  the claim.

The comment starts with a marker line, an HTML comment the forge does not render, followed by
human text. To prevent internal session IDs, lane names, hostnames, or private names from leaking into
public forge text, sessions and lanes are written as keyed identity tags (`h:` plus 12 hex characters):

```text
<!-- praetor-claim v=1 session=h:5f4dcc3b5aa7 lane=h:098f6bcd4621 branch=feat/x started=2026-10-08T12:00:00Z updated=2026-10-08T12:30:00Z stage=review assignee=claim -->
**Praetor claim** - session `h:5f4dcc3b5aa7`, lane `h:098f6bcd4621`, branch `feat/x`

- Stage: review
- Started: 2026-10-08T12:00:00Z
- Updated: 2026-10-08T12:30:00Z
- Assignee: added by this claim; release removes it
- Note: round one
```

The marker keys are `v`, `session`, `lane`, `branch`, `started`, `updated`, `stage`,
`assignee` and, once released, `outcome`. Times are RFC 3339. `assignee` is `claim` when the
claim assigned the account and `before` when the issue listed the account already; the human
text repeats it. A resumed claim keeps the value its comment records. A takeover of a stale
claim that the same account wrote and whose assignment it added keeps `claim`. A marker
written before this key existed has none.

### Identity tags and key storage

Session and lane values are rendered as keyed HMAC tags: `ClaimTag(key, kind, value)` computes
`h:` followed by the first 12 hexadecimal characters of HMAC-SHA256(key, kind + "\x00" + value).
The key is 32 random bytes stored in `<os.UserConfigDir()>/praetor/claim-tag.key` with owner-only
permissions (`0600`). It is created on first use and is never printed, logged, returned in an error,
or committed. If the key cannot be read or created, the claim operation fails; it never falls back
to plain values.

Legacy claim markers with plain session or lane values continue to parse. Comparisons between stored
claims and requested identities evaluate via `SameIdentity`: a stored tag compares with the tag of
the request, and a stored plain value compares plain.

### Public text guard

All forge driver write paths (issue comments, issue bodies and titles, pull request bodies and
titles) pass through a centralized text guard. The guard refuses text that contains any protected
value (the session and lane of the current operation, `os.Hostname()`, or non-empty lines from
`<os.UserConfigDir()>/praetor/private-names.txt`) using case-insensitive word-boundary matching.
Refusal returns an error naming the category of protected value without leaking the value itself.

### Legacy claim migration

Legacy claim comments carrying plain session or lane values are migrated to keyed identity tags
via `standardsctl forge claims migrate` (`internal/forge/claim_migrate.go`):

```bash
# Dry run: report legacy claim comments and their target tags without modifying the forge
standardsctl forge claims migrate --repo OWNER/NAME --issue 7 --issue 8

# Apply migration: post tagged replacement comments, delete legacy comments, and verify read-back
standardsctl forge claims migrate --repo OWNER/NAME --issue 7 --issue 8 --apply
```

A comment whose first line is a valid claim marker with an untagged session or lane is identified
as a legacy claim. Comments that are not claims are never touched.

- **Dry run (default)**: prints one line per legacy comment naming the issue number, comment ID,
  stage, and computed identity tags (`issue N comment ID: stage S, session h:... lane h:...`).
  Plain values are never printed. If a free-text note carries a protected value, dry run prints
  `note dropped: carries a protected <kind>`.
- **Apply (`--apply`)**:
  1. Posts a new comment rendering the same claim with tagged identities (`h:...`), preserving stage,
     started and updated timestamps, outcome, branch, and assignee case. Any free-text note is dropped
     unless it passes the public text guard; when dropped, apply prints
     `note dropped: carries a protected <kind>`.
  2. Deletes the legacy comment via `DeleteIssueComment`. If deletion fails after posting, both comment
     IDs are reported and the command halts without posting duplicate comments.
  3. Reads back the issue thread. The command fails if any legacy claim marker remains, any newly
     posted comment is missing, or the active issue holder changed
     (`TestClaimsMigrate_Positive_LegacyCommentMigrates`,
     `TestClaimsMigrate_Negative_MutantSkipsReadback`,
     `TestClaimsMigrate_Negative_Planted_ReadbackFailsWhenHolderChanges`). Migration also refuses an issue
     up front (and dry run names it) when a tagged unreleased claim sits after a legacy unreleased claim
     (`TestClaimsMigrate_Negative_Planted_TaggedUnreleasedAfterLegacyRefusedUpfront`).

### How the marker is read

The reader accepts a comment as a claim only when its first line is exactly this grammar:
version 1, every key from the list above, no key twice, each value in its allowed shape, and
both times valid. Any other comment is skipped. Comments by authors who are not the owner, a
member or a collaborator of the repository are skipped too, so a passer-by cannot block an
issue by posting a marker. Tests: `TestClaim_Negative_MalformedAndForeignMarkersIgnored` and
`TestClaimMarker_RoundTripAndBounds`.

## Stages

| Stage | Set by | Notes |
| :--- | :--- | :--- |
| `claimed` | `claim` | The initial stage. |
| `implementing` | `status` | |
| `review` | `status` | |
| `fix-round-N` | `status` | `N` is 1 to 999. |
| `blocked` | `status` | Adds the `status:blocked` label; a short findings summary goes in `--note`. Any other stage removes the label. |
| `queued` | `status` | |
| `landing` | `status` | |
| `released` | `release` | Final. Carries the outcome. |

Newlines and runs of whitespace in a note are folded into single spaces before the
280-character limit applies; a longer note is refused, not cut.

## Refusal, takeover and the stale window

A claim is refused while another session holds a live claim. The refusal names that claim:
its session, lane, branch, stage and last update (`forge.ClaimHeldError`).

A claim is stale once its last update is older than the window. A claim whose last update is
exactly one window old is still live; one second more makes it stale. Future clock skew is capped
at the stale window: a claim update timestamp further in the future than the window is treated
as stale. A session that finds only a stale claim takes it over: a new claim comment is
written with a `Takeover` line naming the claim it replaced, and the old stale comment is
finalised as `abandoned`. Tests: `TestClaim_Boundary_StaleWindowEdge`.

The window is 6 hours by default. Set `forge.claim_stale` in the operator settings to change
it, from 10 minutes to 720 hours ([effective policy](effective-policy.md)). It is read through
the same settings loader as every other operator value; there is no second configuration
file.

A status update from a session that does not hold the claim is refused, so a session whose
claim was taken over learns that at its next update.

Issue claims are advisory: they converge through comment timestamps and IDs rather than hard
distributed locking. Every new claim (first claim, reuse after release, stale takeover) writes
a new claim comment. Stages of an ongoing claim edit the session's own comment in place. When
two sessions claim or update at the same moment, both may write; there is a residual race
window where two sessions can each act once before their next read. On the next claim, status
update or release, the reconcile step computes the holder as the oldest live claim by comment
id; a session owning a live comment that is not the holder finalises it as `abandoned`, and when
another session holds the issue that session is refused with `forge.ClaimHeldError` naming the
winner. Read-only operations (status without a stage, `LiveClaim` for the dispatch gate) report
the holder and change nothing.

Claims are advisory and do not exclude conflicting work on their own; until
[#1033](https://github.com/cordanaLLM/praetor/issues/1033) (hard mutual exclusion through a
git-ref lease) lands, exclusion against conflicting work has to come from the repository's
own pipeline, for example one worktree per branch and serialized merges. Tests:
`TestClaim_Negative_ConcurrentClaimerLosesToLowerCommentID`,
`TestClaim_Negative_InterleavedClaimersOnReleasedCommentExactlyOneHolds`,
`TestClaim_Negative_InterleavedClaimersOnStaleCommentExactlyOneHolds`,
`TestClaim_Negative_StaleHolderEditRacingTakeover_TakeoverWins`,
`TestClaim_Negative_StaleHolderEditRacingTakeover_ResumeWins`,
`TestClaim_Negative_NonHolderStatusAndReleaseFinalisesComment`,
`TestClaim_ZombieScenario_ResumeRacingTakeover`.

## Release

`release` removes `status:in-progress` and `status:blocked`. When the claim recorded
`assignee=claim`, it also unassigns the account that wrote the claim comment and keeps every
other assignee. With `assignee=before`, or no `assignee` key, the assignees stay as they are.
It then rewrites the comment with stage `released` and the outcome, and leaves the issue open.
The pull request closes the issue, and a closed issue can still be released
(`TestRelease_Positive_RemovesTheAssigneeTheClaimAdded`,
`TestRelease_Boundary_KeepsAnAssigneeFromBeforeTheClaim`,
`TestRelease_Positive_ClosedIssueStillReleasesAndUnassigns`). Subsequent claims on the same issue write a new claim comment, leaving earlier
released comments as historical records. Labels and the assignee go first, so a failure to remove
either leaves the claim live and a retried release succeeds
(`TestRelease_Negative_LabelFailureKeepsClaimReleasable`,
`TestRelease_Negative_UnassignFailureKeepsTheClaimReleasable`).

## Failure behaviour

Every operation runs under a 45-second context deadline, and every forge call inside it
carries that context. Any forge failure, including an issue number that is a pull request, an
unreadable comment thread or a thread longer than 1,000 comments, returns an error that wraps
`forge.ErrClaimUnverifiable`. A claim that could not be verified is never reported as held,
and a hold that could not be checked is never reported as absent. When the comment was written
but the labels or the read-back failed, the claim is finalised as `abandoned` so a half-made
claim does not hold the issue and any status labels it applied are removed again; on a
takeover, status labels are cleared as well (`TestClaim_Negative_ForgeErrorsFailClosed`,
`TestClaim_Negative_LabelFailureAbandonsTheComment`,
`TestClaim_Negative_AbandonedClaimLeavesNoStatusLabel`,
`TestClaim_Negative_TakeoverConfirmHoldFailureClearsLabels`). A session that re-runs `claim` on its
own live claim and hits a transient failure keeps that claim live; it is never finalised as
`abandoned` (`TestClaim_Negative_FailedResumeKeepsTheClaimLive`).

## Dispatch enforcement

The pre-dispatch hook ([agent hooks](agent-hooks.md)) extracts claim tokens from a subagent
brief with Caveman (`caveman.ExtractBriefClaim`) and parses them with `forge.ParseBriefClaim`:

```text
issue: acme/widgets#7, acme/widgets#8
session: unit7
```

`issue:` lists the issues the brief works on; `session:` names the session that holds their
claims. A token that is not `<owner>/<repo>#<number>` (including `#0` or leading zeros) is an
error, not skipped. The gate then decides per issue:

| Situation | Verdict |
| :--- | :--- |
| Claimed by the brief's session | Allow. |
| Claimed by another live session | Deny, naming that claim. |
| Unclaimed, or the claim is stale | Deny, with the `praetorctl issue claim` command to run first. |
| The brief names an issue but no `session:` | Deny. |
| The claim cannot be read, or no lookup is wired | Deny. |

A brief that names no issue is not touched and needs no token or network. A field is a line
that starts at column 0 with the lowercase word `issue:` or `session:`; bulleted, indented or
capitalised lines are prose and never match. A `session:` line is validated only when the brief
also names an issue. Tests: `TestExtractBriefClaimPositive`,
`TestBriefClaims_Negative_ForeignLiveClaimRefusesDispatchNamingIt`,
`TestBriefClaims_Negative_UnclaimedUnverifiableAndMalformedRefuse` and
`TestHookIssueClaims_Negative_FailsClosed`.

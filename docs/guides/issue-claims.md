# Issue claims

An issue claim tells everyone reading the forge that one agent session works on an issue, on
which branch, and at which stage. It keeps two sessions from picking up the same issue and
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

The CLI subcommands and the MCP tools build the same `forge.ClaimCommand` and run it on the
same `forge.ClaimDesk`, so they cannot drift
(`TestIssueClaimMCP_Positive_MirrorsTheCLIDesk`). A session identifier is 1 to 128 characters
of letters, digits and `. _ / : + @ -`; lane and branch follow the same rule. A branch name
outside it, such as one with a space, is refused.

## What a claim writes

A claim does three things on the issue:

- It posts one claim comment. That comment is the only place the claim lives; every later
  stage of the session edits it in place, so an issue never collects one comment per stage.
- It adds the `status:in-progress` label, creating the label first when the repository has
  none.
- It assigns the authenticated account.

The comment starts with a marker line, an HTML comment the forge does not render, followed by
human text:

```text
<!-- praetor-claim v=1 session=s1 lane=agy branch=feat/x started=2026-10-08T12:00:00Z updated=2026-10-08T12:30:00Z stage=review -->
**Praetor claim** - session `s1`, lane `agy`, branch `feat/x`

- Stage: review
- Started: 2026-10-08T12:00:00Z
- Updated: 2026-10-08T12:30:00Z
- Note: round one
```

The marker keys are `v`, `session`, `lane`, `branch`, `started`, `updated`, `stage` and, once
released, `outcome`. Times are RFC 3339.

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

A note is one line of at most 280 characters; a longer note is refused, not cut.

## Refusal, takeover and the stale window

A claim is refused while another session holds a live claim. The refusal names that claim:
its session, lane, branch, stage and last update (`forge.ClaimHeldError`).

A claim is stale once its last update is older than the window. A claim whose last update is
exactly one window old is still live; one second more makes it stale. Future clock skew is capped
at the stale window: a claim update timestamp further in the future than the window is malformed
and ignored. A session that finds only a stale claim takes it over: a new claim comment is
written with a `Takeover` line naming the claim it replaced, and the old stale comment is
finalised as `abandoned`. Tests: `TestClaim_Boundary_StaleWindowEdge`.

The window is 6 hours by default. Set `forge.claim_stale` in the operator settings to change
it, from 10 minutes to 720 hours ([effective policy](effective-policy.md)). It is read through
the same settings loader as every other operator value; there is no second configuration
file.

A status update from a session that does not hold the claim is refused, so a session whose
claim was taken over learns that at its next update.

Every new claim (first claim, reuse after release, stale takeover) writes a new claim comment.
Stages of an ongoing claim edit the session's own comment in place. When two sessions claim
at the same moment, both write a comment; each reads the comments back after writing, and the
claim with the lower comment identifier holds. Any loser finalises its own comment as `abandoned`
and is refused naming the winner (`TestClaim_Negative_ConcurrentClaimerLosesToLowerCommentID`,
`TestClaim_Negative_InterleavedClaimersOnReleasedCommentExactlyOneHolds`,
`TestClaim_Negative_InterleavedClaimersOnStaleCommentExactlyOneHolds`,
`TestClaim_Negative_StaleHolderEditRacingTakeover_TakeoverWins`,
`TestClaim_Negative_StaleHolderEditRacingTakeover_ResumeWins`).

## Release

`release` removes `status:in-progress` and `status:blocked`, then rewrites the comment with
stage `released` and the outcome, and leaves the issue open. The pull request closes
the issue. Subsequent claims on the same issue write a new claim comment, leaving earlier
released comments as historical records. Labels go first, so a label failure leaves the claim
live and a retried release succeeds (`TestRelease_Negative_LabelFailureKeepsClaimReleasable`).

## Failure behaviour

Every operation runs under a 45-second context deadline, and every forge call inside it
carries that context. Any forge failure, including an issue number that is a pull request, an
unreadable comment thread or a thread longer than 1,000 comments, returns an error that wraps
`forge.ErrClaimUnverifiable`. A claim that could not be verified is never reported as held,
and a hold that could not be checked is never reported as absent. When the comment was written
but the labels or the read-back failed, the claim is finalised as `abandoned` so a half-made
claim does not hold the issue and its status labels are removed again; on a takeover, status
labels are cleared as well (`TestClaim_Negative_ForgeErrorsFailClosed`,
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

A brief that names no issue is not touched and needs no token or network. A field is a line that starts at column 0 with the lowercase word `issue:` or `session:`; bulleted, indented or capitalised lines are prose and never match. A `session:` line is validated only when the brief also names an issue. Test: `TestExtractBriefClaimPositive`. The brief is also
judged by the text register gate, so a session identifier must pass it: tokens such as `a` or
`mine` inside the identifier are rejected as grammar words. Tests:
`TestBriefClaims_Negative_ForeignLiveClaimRefusesDispatchNamingIt`,
`TestBriefClaims_Negative_UnclaimedUnverifiableAndMalformedRefuse` and
`TestHookIssueClaims_Negative_FailsClosed`.

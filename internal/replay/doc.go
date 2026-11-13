// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package replay evaluates review and combiner arms against real merged pull requests at a
// historical cutoff without post-cutoff history leaks.
//
// Cutoff resolves the latest commit on a base branch whose committer timestamp is at or before
// the cutoff, and VerifyTree refuses any materialized checkout whose HEAD or ancestor history
// contains a committer timestamp after the cutoff. GroundTruth inspects later commits on the
// base branch up to an end timestamp, matching repair commits by pull-request or commit
// reference, normalized subject, or git blame attribution onto lines the pull request introduced.
// Patch-identical re-lands detected via git patch-id --stable are excluded.
//
// Until internal/reviewmerge lands, each review arm command prints one JSON object on standard
// output with the minimal shape:
//
//	{"verdict": "changes_requested", "findings": [{"file": "pkg/example.go", "line": 42, "summary": "..."}]}
//
// Canonical verdicts are "changes_requested" and "approve" (aliases "fail", "reject",
// "request_changes", "pass", and "ok" normalize to those two values). Each arm executes twice
// per sample, once with drafts in the supplied order and once with drafts reversed, flagging
// order sensitivity whenever the normalized verdict flips between the two runs, and appends one
// scored outcome row per arm through internal/router. Each execution receives an independent
// shallow repository containing only the cutoff commit and tree; no source refs or alternates
// are copied. Dirty supplied trees are refused. Source ancestors are verified before snapshots
// remove history, so a future-dated merge parent still refuses the sample.
//
// Findings use repository-relative paths (directories named a or b are preserved) and cutoff
// line numbers. Repair spans are mapped through intervening edits; lines added after the cutoff
// cannot earn retention credit. Files introduced by the repair have no cutoff location.
// MaxDiffLines also bounds the accumulated ground-truth hunks.
//
// The CLI accepts --samples with a JSON array of Sample objects instead of individual PR flags.
// Reports preserve cutoff and verdict metadata in ArmScore.Samples; aggregate singular metadata
// is omitted for multiple samples. Context digests include suite, PR metadata, repair window and
// draft contents. Replay outcomes use lane "replay" and are published only after every arm succeeds.
//
// Dataset authors may independently annotate added_verified and fabrications with Finding arrays.
// Added verified locations count only when found in both orders; disproven locations count in
// either order. Nil annotation arrays report unmeasured (JSON null), while explicit empty arrays
// measure zero. Unmatched findings are never assumed fabricated from incomplete repair history.
// Outcomes retain these two metrics in Note using the existing router schema. Publication goes
// through router.AppendOutcomes: every row is validated before one append write, so a refused
// row leaves the log unchanged, and the log keeps its own record bound (router.MaxOutcomeLines).
//
// The historical cutoff, post-merge ground-truth derivation, and swapped-order combiner
// evaluation design idea (no code or text) is credited to Fairies by Michael Niedermayer
// (GPL-2.0, https://code.ffmpeg.org/michaelni/Fairies at c4ed93c30469: simpast-runs,
// tools/match_prs_to_master.py, tools/replay_combiner.py).
package replay

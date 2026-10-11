// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Claim labels, created on the repository when missing.
const (
	LabelInProgress = "status:in-progress"
	LabelBlocked    = "status:blocked"

	// claimOpTimeout bounds one claim operation, every forge call inside it included
	// (HISS-02). The caller's own deadline still applies when it is shorter.
	claimOpTimeout = 45 * time.Second
	// claimBlockedStage is the stage that adds LabelBlocked.
	claimBlockedStage = "blocked"
	// takeoverPrefix starts the line of a claim comment that names the claim it replaced.
	takeoverPrefix = "- Takeover: "
)

// Claim errors. ErrClaimUnverifiable wraps every forge failure: a claim that could not be
// read or written is never reported as held, and a hold that could not be checked is never
// reported as absent (no silent fallback).
var (
	ErrClaimHeld         = errors.New("issue is claimed by another live session")
	ErrNoClaim           = errors.New("issue has no live claim of this session")
	ErrClaimUnverifiable = errors.New("claim could not be verified on the forge")
)

// ClaimHeldError names the live claim that refused a claim, a status update or a dispatch.
type ClaimHeldError struct {
	Ref    ClaimRef
	Holder Claim
}

func (e *ClaimHeldError) Error() string {
	return fmt.Sprintf("%s: %s", e.Ref, DescribeClaim(e.Holder))
}

// Is lets errors.Is(err, ErrClaimHeld) match.
func (e *ClaimHeldError) Is(target error) bool { return target == ErrClaimHeld }

// DescribeClaim is the one-line name of a claim used in refusals and takeover notes.
func DescribeClaim(c Claim) string {
	return fmt.Sprintf("claimed by session %s (lane %s, branch %s, stage %s, updated %s)",
		c.Session, c.Lane, c.Branch, c.Stage, c.Updated.UTC().Format(time.RFC3339))
}

// ClaimRef names one issue on one repository: owner/repo#n.
type ClaimRef struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
}

func (r ClaimRef) String() string { return fmt.Sprintf("%s/%s#%d", r.Owner, r.Repo, r.Number) }

// MaxIssueNumberDigits bounds the digits of an issue number in a reference.
const MaxIssueNumberDigits = 9

// ParseClaimRef reads "<owner>/<repo>#<number>". Owner and repository pass the same identity
// check every API path does; a bare number or a reference without an owner is refused, so a
// claim never lands on a repository nobody named.
func ParseClaimRef(text string) (ClaimRef, error) {
	coordinate, digits, ok := strings.Cut(text, "#")
	if !ok {
		return ClaimRef{}, fmt.Errorf("issue reference %q is not <owner>/<repo>#<number>", text)
	}
	number, err := parseIssueNumber(digits)
	if err != nil {
		return ClaimRef{}, fmt.Errorf("issue reference %q: %w", text, err)
	}
	owner, repo, err := util.SplitGitHubRepository(coordinate)
	if err != nil {
		return ClaimRef{}, fmt.Errorf("issue reference %q: %w", text, err)
	}
	return ClaimRef{Owner: owner, Repo: repo, Number: number}, nil
}

func parseIssueNumber(digits string) (int, error) {
	if digits == "" || len(digits) > MaxIssueNumberDigits || digits[0] == '0' {
		return 0, errors.New("needs a positive issue number without leading zeros")
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, errors.New("issue number contains non-digits")
		}
	}
	return strconv.Atoi(digits)
}

// IssueComment is one comment of an issue as the claim code reads it.
type IssueComment struct {
	ID int64 `json:"id"`
	// Author is the commenter's login.
	Author string `json:"author"`
	// Association is the forge's relation of the commenter to the repository (OWNER, MEMBER,
	// COLLABORATOR, CONTRIBUTOR, NONE, ...).
	Association string `json:"association"`
	Body        string `json:"body"`
}

// ClaimForge is the slice of a forge the claim protocol needs for one repository. The GitHub
// driver implements it; tests substitute a fake. Every method takes the caller's context and
// returns an error on any forge failure.
type ClaimForge interface {
	// GetIssue reads an issue and refuses a pull request number.
	GetIssue(ctx context.Context, number int) (IssueSpec, error)
	// Viewer returns the login of the authenticated account.
	Viewer(ctx context.Context) (string, error)
	// ListIssueComments returns every comment of the issue in creation order, or an error
	// when the listing is incomplete.
	ListIssueComments(ctx context.Context, number int) ([]IssueComment, error)
	CreateIssueComment(ctx context.Context, number int, body string) (IssueComment, error)
	EditIssueComment(ctx context.Context, commentID int64, body string) error
	// EnsureLabel creates the label when the repository lacks it and changes nothing else.
	EnsureLabel(ctx context.Context, label Label) error
	AddLabels(ctx context.Context, number int, labels []string) error
	RemoveLabel(ctx context.Context, number int, label string) error
	AddAssignees(ctx context.Context, number int, logins []string) error
}

// ClaimForgeOpener returns the claim forge of one repository.
type ClaimForgeOpener func(owner, repo string) (ClaimForge, error)

// ClaimDesk runs the claim protocol over the forge Open returns. Stale is the window after
// which a claim without an update may be taken over; it must be positive. Now is the clock.
type ClaimDesk struct {
	Open  ClaimForgeOpener
	Stale time.Duration
	Now   func() time.Time
}

// ClaimRequest is what `issue claim` asks for.
type ClaimRequest struct {
	Session, Lane, Branch string
}

// StatusRequest is what `issue status` asks for. An empty Stage only reads the claim.
type StatusRequest struct {
	Session, Stage, Note string
}

// ReleaseRequest is what `issue release` asks for.
type ReleaseRequest struct {
	Session, Outcome, Note string
}

// ClaimResult reports what an operation did.
type ClaimResult struct {
	Ref   string `json:"ref"`
	Claim Claim  `json:"claim"`
	// Action is created, resumed, took-over, updated, released, already-released or read.
	Action string `json:"action"`
	// TookOver names the stale claim a takeover replaced.
	TookOver *Claim `json:"took_over,omitempty"`
}

type claimComment struct {
	claim Claim
	body  string
}

// claimLabels are the labels the protocol manages, with the colour and text they are created with.
var claimLabels = map[string]Label{
	LabelInProgress: {Name: LabelInProgress, Color: "1d76db", Description: "An agent session holds a claim on this issue"},
	LabelBlocked:    {Name: LabelBlocked, Color: "d93f0b", Description: "The claimed work is blocked; see the claim comment"},
}

// session prepares one operation: a bounded context, the forge of ref and its verified issue.
func (d *ClaimDesk) session(ctx context.Context, ref ClaimRef) (context.Context, context.CancelFunc, ClaimForge, error) {
	if ctx == nil {
		return nil, nil, nil, errors.New("claim operation needs a context")
	}
	if d == nil || d.Open == nil || d.Now == nil || d.Stale <= 0 {
		return nil, nil, nil, errors.New("claim desk needs a forge opener, a clock and a positive stale window")
	}
	ctx, cancel := context.WithTimeout(ctx, claimOpTimeout)
	f, err := d.Open(ref.Owner, ref.Repo)
	if err != nil {
		cancel()
		return nil, nil, nil, fmt.Errorf("%w: open %s/%s: %w", ErrClaimUnverifiable, ref.Owner, ref.Repo, err)
	}
	if _, err := f.GetIssue(ctx, ref.Number); err != nil {
		cancel()
		return nil, nil, nil, fmt.Errorf("%w: %w", ErrClaimUnverifiable, err)
	}
	return ctx, cancel, f, nil
}

func unverifiable(what string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrClaimUnverifiable, what, err)
}

// readClaims lists the claim comments of the issue, oldest first.
func readClaims(ctx context.Context, f ClaimForge, number int) ([]claimComment, error) {
	comments, err := f.ListIssueComments(ctx, number)
	if err != nil {
		return nil, unverifiable("list comments", err)
	}
	var found []claimComment
	for i := 0; i < len(comments) && i < MaxIssueCommentsLimit; i++ {
		if !trustedAssociation(comments[i].Association) {
			continue
		}
		claim, ok := ParseClaimMarker(comments[i].Body)
		if !ok {
			continue
		}
		claim.CommentID = comments[i].ID
		found = append(found, claimComment{claim: claim, body: comments[i].Body})
	}
	return found, nil
}

// isFresh reports whether a live claim is still inside the stale window: a claim is stale
// once its last update is older than the window. Future clock skew is capped at the stale
// window: an update timestamp further in the future than the window is treated as stale.
func isFresh(c Claim, now time.Time, stale time.Duration) bool {
	if c.Released() {
		return false
	}
	if c.Updated.After(now.Add(stale)) || c.Updated.Before(now.Add(-stale)) {
		return false
	}
	return true
}

// holder computes the active claim holder from the comment list: the oldest live claim
// by comment id, after finalised and stale claims are discarded.
func holder(found []claimComment, now time.Time, stale time.Duration) (claimComment, bool) {
	var (
		best claimComment
		have bool
	)
	for _, entry := range found {
		if !isFresh(entry.claim, now, stale) {
			continue
		}
		if !have || entry.claim.CommentID < best.claim.CommentID {
			best = entry
			have = true
		}
	}
	return best, have
}

// holder returns the first fresh live claim: the lowest comment id wins when a race left two.
func (d *ClaimDesk) holder(found []claimComment) (Claim, bool) {
	entry, ok := holder(found, d.Now(), d.Stale)
	return entry.claim, ok
}

// reconcile reconciles the comment thread against the active claim holder: any live comment
// owned by the session that is not the holder is finalised as abandoned (the loser rule).
// It returns the holder comment (if any), whether a holder exists, and any refusal or failure.
func (d *ClaimDesk) reconcile(ctx context.Context, f ClaimForge, ref ClaimRef, session string, found []claimComment) (claimComment, bool, error) {
	now, stale := d.Now(), d.Stale
	heldComment, held := holder(found, now, stale)
	finaliseErrs := d.abandonOwnNonHolderLive(ctx, f, session, held, heldComment.claim.CommentID, found, now, stale)
	if held && heldComment.claim.Session != session {
		refusal := &ClaimHeldError{Ref: ref, Holder: heldComment.claim}
		return heldComment, true, errors.Join(append([]error{refusal}, finaliseErrs...)...)
	}
	if len(finaliseErrs) > 0 {
		return heldComment, held, errors.Join(finaliseErrs...)
	}
	return heldComment, held, nil
}

func (d *ClaimDesk) abandonOwnNonHolderLive(ctx context.Context, f ClaimForge, session string, held bool, holderID int64, found []claimComment, now time.Time, stale time.Duration) []error {
	var errs []error
	for i := range found {
		c := found[i].claim
		if c.Session != session || !isFresh(c, now, stale) {
			continue
		}
		if held && c.CommentID == holderID {
			continue
		}
		if err := d.finalise(ctx, f, c, "abandoned"); err != nil {
			errs = append(errs, err)
		}
		found[i].claim.Stage = ClaimStageReleased
		found[i].claim.Outcome = "abandoned"
	}
	return errs
}

func sanitizeNote(note string) (string, error) {
	cleaned := strings.Join(strings.FieldsFunc(note, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }), " ")
	if n := len([]rune(cleaned)); n > MaxClaimNoteRunes {
		return "", fmt.Errorf("note has %d characters, the limit is %d", n, MaxClaimNoteRunes)
	}
	return cleaned, nil
}

// renderClaimBody renders the marker line and the human text of a claim.
func renderClaimBody(c Claim, takeover string) (string, error) {
	marker, err := RenderClaimMarker(c)
	if err != nil {
		return "", err
	}
	lines := []string{
		marker,
		fmt.Sprintf("**Praetor claim** - session `%s`, lane `%s`, branch `%s`", c.Session, c.Lane, c.Branch),
		"",
		"- Stage: " + c.Stage,
		"- Started: " + c.Started.UTC().Format(time.RFC3339),
		"- Updated: " + c.Updated.UTC().Format(time.RFC3339),
	}
	if c.Outcome != "" {
		lines = append(lines, "- Outcome: "+c.Outcome)
	}
	if c.Note != "" {
		lines = append(lines, "- Note: "+c.Note)
	}
	if takeover != "" {
		lines = append(lines, takeoverPrefix+takeover)
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// takeoverNote returns the takeover line a comment body already carries.
func takeoverNote(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if rest, ok := strings.CutPrefix(line, takeoverPrefix); ok {
			return rest
		}
	}
	return ""
}

// Claim claims the issue for the session. It refuses while another session holds a fresh
// claim, takes a stale claim over, and resumes the session's own claim.
func (d *ClaimDesk) Claim(ctx context.Context, ref ClaimRef, req ClaimRequest) (ClaimResult, error) {
	probe := Claim{Session: req.Session, Lane: req.Lane, Branch: req.Branch, Stage: "claimed", Started: time.Unix(1, 0), Updated: time.Unix(1, 0)}
	if err := validateClaim(probe); err != nil {
		return ClaimResult{}, err
	}
	ctx, cancel, f, err := d.session(ctx, ref)
	if err != nil {
		return ClaimResult{}, err
	}
	defer cancel()
	found, err := readClaims(ctx, f, ref.Number)
	if err != nil {
		return ClaimResult{}, err
	}
	heldComment, held, err := d.reconcile(ctx, f, ref, req.Session, found)
	if err != nil {
		return ClaimResult{}, err
	}
	return d.writeClaim(ctx, f, ref, req, found, heldComment, held)
}

// writeClaim writes the claim comment and the labels once no other session holds the issue.
func (d *ClaimDesk) writeClaim(ctx context.Context, f ClaimForge, ref ClaimRef, req ClaimRequest, found []claimComment, heldComment claimComment, held bool) (ClaimResult, error) {
	now := d.Now().UTC().Truncate(time.Second)
	claim := Claim{Session: req.Session, Lane: req.Lane, Branch: req.Branch, Stage: "claimed", Started: now, Updated: now}
	var target claimComment
	var hasTarget bool
	if held {
		target, hasTarget = heldComment, true
	} else {
		target, hasTarget = d.pickClaimTarget(found, req.Session)
	}
	action, tookOver, takeover := resolveClaimAction(target, hasTarget, req.Session)
	if action == "resumed" {
		claim.Started, claim.Stage = target.claim.Started, target.claim.Stage
		takeover = takeoverNote(target.body)
	}
	body, err := renderClaimBody(claim, takeover)
	if err != nil {
		return ClaimResult{}, err
	}
	resumed, isTakeover := action == "resumed", action == "took-over"
	id, err := d.putClaim(ctx, f, ref.Number, target, resumed, body)
	if err != nil {
		return ClaimResult{}, err
	}
	claim.CommentID = id
	if isTakeover {
		if err := d.handleTakeoverFinalise(ctx, f, ref, req.Session, target, claim); err != nil {
			return ClaimResult{}, err
		}
	}
	if err := d.confirmHold(ctx, f, ref, claim, resumed, isTakeover); err != nil {
		return ClaimResult{}, err
	}
	if err := d.applyClaimLabels(ctx, f, ref.Number, claim.Stage == claimBlockedStage); err != nil {
		if resumed {
			// The session's earlier claim was valid and stays live; a retry repairs the labels.
			return ClaimResult{}, err
		}
		return ClaimResult{}, errors.Join(d.abandon(ctx, f, claim, err), d.clearLabels(ctx, f, ref.Number))
	}
	return ClaimResult{Ref: ref.String(), Action: action, Claim: claim, TookOver: tookOver}, nil
}

func (d *ClaimDesk) finaliseStaleTarget(ctx context.Context, f ClaimForge, ref ClaimRef, session string, target claimComment) error {
	found, err := readClaims(ctx, f, ref.Number)
	if err != nil {
		return err
	}
	cur, ok := findClaimComment(found, target.claim.CommentID)
	if !ok {
		return unverifiable("finalise claim comment", fmt.Errorf("stale claim comment %d not found", target.claim.CommentID))
	}
	if !markersEqual(target.claim, cur.claim) {
		if holder, held := d.holder(found); held {
			if holder.Session != session {
				return &ClaimHeldError{Ref: ref, Holder: holder}
			}
			if cur.claim.Released() {
				return nil
			}
		}
		return unverifiable("finalise claim comment", errors.New("stale claim comment modified before finalise"))
	}
	return d.finalise(ctx, f, target.claim, "abandoned")
}

func (d *ClaimDesk) handleTakeoverFinalise(ctx context.Context, f ClaimForge, ref ClaimRef, session string, target claimComment, claim Claim) error {
	if err := d.finaliseStaleTarget(ctx, f, ref, session, target); err != nil {
		if errors.Is(err, ErrClaimHeld) {
			return errors.Join(err, d.abandon(ctx, f, claim, nil))
		}
		return errors.Join(d.abandon(ctx, f, claim, err), d.clearLabels(ctx, f, ref.Number))
	}
	return nil
}

func markersEqual(a, b Claim) bool {
	return a.Session == b.Session &&
		a.Lane == b.Lane &&
		a.Branch == b.Branch &&
		a.Stage == b.Stage &&
		a.Outcome == b.Outcome &&
		a.Started.Equal(b.Started) &&
		a.Updated.Equal(b.Updated)
}

func findClaimComment(comments []claimComment, id int64) (claimComment, bool) {
	for i := range comments {
		if comments[i].claim.CommentID == id {
			return comments[i], true
		}
	}
	return claimComment{}, false
}

func resolveClaimAction(target claimComment, hasTarget bool, session string) (string, *Claim, string) {
	if !hasTarget {
		return "created", nil, ""
	}
	if target.claim.Session == session && !target.claim.Released() {
		return "resumed", nil, ""
	}
	if !target.claim.Released() {
		old := target.claim
		takeover := "replaces the stale claim of " + strings.TrimPrefix(DescribeClaim(old), "claimed by ")
		return "took-over", &old, takeover
	}
	return "created", nil, ""
}

// pickClaimTarget finds an existing claim to resume (the session's own live claim) or to
// take over (a stale live claim). Released claims are never reused; every new claim writes
// a new comment.
func (d *ClaimDesk) pickClaimTarget(found []claimComment, session string) (claimComment, bool) {
	var stale *claimComment
	for i := range found {
		switch {
		case found[i].claim.Session == session && !found[i].claim.Released():
			return found[i], true
		case !found[i].claim.Released() && stale == nil:
			stale = &found[i]
		}
	}
	if stale != nil {
		return *stale, true
	}
	return claimComment{}, false
}

func (d *ClaimDesk) putClaim(ctx context.Context, f ClaimForge, number int, target claimComment, resumed bool, body string) (int64, error) {
	if resumed {
		if err := f.EditIssueComment(ctx, target.claim.CommentID, body); err != nil {
			return 0, unverifiable("edit claim comment", err)
		}
		return target.claim.CommentID, nil
	}
	created, err := f.CreateIssueComment(ctx, number, body)
	if err != nil {
		return 0, unverifiable("post claim comment", err)
	}
	return created.ID, nil
}

// confirmHold reads the comments back after the write. When two sessions claimed at once the
// lowest comment id holds; a loser finalises its own comment as abandoned and is refused,
// naming the winner.
func (d *ClaimDesk) confirmHold(ctx context.Context, f ClaimForge, ref ClaimRef, mine Claim, resumed, takeover bool) error {
	found, err := readClaims(ctx, f, ref.Number)
	if err != nil {
		return d.failHold(ctx, f, ref.Number, mine, err, resumed, takeover)
	}
	winner, held := d.holder(found)
	if held && winner.Session == mine.Session {
		return nil
	}
	if !held {
		cause := fmt.Errorf("%w: claim comment %d not found on read-back", ErrClaimUnverifiable, mine.CommentID)
		return d.failHold(ctx, f, ref.Number, mine, cause, resumed, takeover)
	}
	refusal := &ClaimHeldError{Ref: ref, Holder: winner}
	if winner.CommentID != mine.CommentID {
		return errors.Join(refusal, d.finalise(ctx, f, mine, "abandoned"))
	}
	return refusal
}

func (d *ClaimDesk) failHold(ctx context.Context, f ClaimForge, number int, mine Claim, cause error, resumed, takeover bool) error {
	if resumed {
		return cause
	}
	if takeover {
		return errors.Join(d.abandon(ctx, f, mine, cause), d.clearLabels(ctx, f, number))
	}
	return d.abandon(ctx, f, mine, cause)
}

// abandon finalises a claim whose labels or read-back failed, so a half-made claim does not
// hold the issue, and returns cause together with any failure of that cleanup.
func (d *ClaimDesk) abandon(ctx context.Context, f ClaimForge, mine Claim, cause error) error {
	return errors.Join(cause, d.finalise(ctx, f, mine, "abandoned"))
}

// clearLabels removes both status labels, reporting every failure.
func (d *ClaimDesk) clearLabels(ctx context.Context, f ClaimForge, number int) error {
	var errs []error
	for _, name := range []string{LabelInProgress, LabelBlocked} {
		if err := f.RemoveLabel(ctx, number, name); err != nil {
			errs = append(errs, unverifiable("remove label "+name, err))
		}
	}
	return errors.Join(errs...)
}

func (d *ClaimDesk) finalise(ctx context.Context, f ClaimForge, c Claim, outcome string) error {
	c.Stage, c.Outcome, c.Updated = ClaimStageReleased, outcome, d.Now().UTC().Truncate(time.Second)
	body, err := renderClaimBody(c, "")
	if err != nil {
		return err
	}
	if err := f.EditIssueComment(ctx, c.CommentID, body); err != nil {
		return unverifiable("finalise claim comment", err)
	}
	return nil
}

func (d *ClaimDesk) applyClaimLabels(ctx context.Context, f ClaimForge, number int, blocked bool) error {
	want := []string{LabelInProgress}
	if blocked {
		want = append(want, LabelBlocked)
	}
	for _, name := range want {
		if err := f.EnsureLabel(ctx, claimLabels[name]); err != nil {
			return unverifiable("create label "+name, err)
		}
	}
	if err := f.AddLabels(ctx, number, want); err != nil {
		return unverifiable("add labels", err)
	}
	login, err := f.Viewer(ctx)
	if err != nil {
		return unverifiable("read account", err)
	}
	if err := f.AddAssignees(ctx, number, []string{login}); err != nil {
		return unverifiable("assign account", err)
	}
	return nil
}

// Status records a stage on the session's claim by editing its comment. Without a stage it
// reads the current holder and changes nothing.
func (d *ClaimDesk) Status(ctx context.Context, ref ClaimRef, req StatusRequest) (ClaimResult, error) {
	if req.Stage != "" && !ValidClaimStage(req.Stage) {
		return ClaimResult{}, fmt.Errorf("stage %q is not one of claimed, implementing, review, fix-round-N, blocked, queued, landing", req.Stage)
	}
	note, err := sanitizeNote(req.Note)
	if err != nil {
		return ClaimResult{}, err
	}
	ctx, cancel, f, err := d.session(ctx, ref)
	if err != nil {
		return ClaimResult{}, err
	}
	defer cancel()
	found, err := readClaims(ctx, f, ref.Number)
	if err != nil {
		return ClaimResult{}, err
	}
	if req.Stage == "" {
		return d.readStatus(ref, found), nil
	}
	heldComment, held, err := d.reconcile(ctx, f, ref, req.Session, found)
	if err != nil {
		return ClaimResult{}, err
	}
	if !held {
		return ClaimResult{}, fmt.Errorf("%w: %s (claim it first)", ErrNoClaim, ref)
	}
	return d.recordStage(ctx, f, ref, heldComment, req, note)
}

// recordStage edits the session's claim comment to the requested stage and syncs the blocked label.
func (d *ClaimDesk) recordStage(ctx context.Context, f ClaimForge, ref ClaimRef, own claimComment, req StatusRequest, note string) (ClaimResult, error) {
	claim := own.claim
	claim.Stage, claim.Note, claim.Updated = req.Stage, note, d.Now().UTC().Truncate(time.Second)
	body, err := renderClaimBody(claim, takeoverNote(own.body))
	if err != nil {
		return ClaimResult{}, err
	}
	if err := f.EditIssueComment(ctx, claim.CommentID, body); err != nil {
		return ClaimResult{}, unverifiable("edit claim comment", err)
	}
	if err := d.syncBlocked(ctx, f, ref.Number, req.Stage == claimBlockedStage); err != nil {
		return ClaimResult{}, err
	}
	return ClaimResult{Ref: ref.String(), Claim: claim, Action: "updated"}, nil
}

func (d *ClaimDesk) readStatus(ref ClaimRef, found []claimComment) ClaimResult {
	if holder, held := d.holder(found); held {
		return ClaimResult{Ref: ref.String(), Claim: holder, Action: "read"}
	}
	if len(found) > 0 {
		return ClaimResult{Ref: ref.String(), Claim: found[len(found)-1].claim, Action: "read"}
	}
	return ClaimResult{Ref: ref.String(), Action: "read"}
}

func (d *ClaimDesk) syncBlocked(ctx context.Context, f ClaimForge, number int, blocked bool) error {
	if !blocked {
		if err := f.RemoveLabel(ctx, number, LabelBlocked); err != nil {
			return unverifiable("remove label "+LabelBlocked, err)
		}
		return nil
	}
	if err := f.EnsureLabel(ctx, claimLabels[LabelBlocked]); err != nil {
		return unverifiable("create label "+LabelBlocked, err)
	}
	if err := f.AddLabels(ctx, number, []string{LabelBlocked}); err != nil {
		return unverifiable("add label "+LabelBlocked, err)
	}
	return nil
}

// Release finalises the session's claim with an outcome and removes the status labels. It
// does not close the issue: the pull request does.
func (d *ClaimDesk) Release(ctx context.Context, ref ClaimRef, req ReleaseRequest) (ClaimResult, error) {
	if !ValidClaimOutcome(req.Outcome) {
		return ClaimResult{}, fmt.Errorf("outcome %q is not one of %s", req.Outcome, strings.Join(ClaimOutcomes, ", "))
	}
	note, err := sanitizeNote(req.Note)
	if err != nil {
		return ClaimResult{}, err
	}
	ctx, cancel, f, err := d.session(ctx, ref)
	if err != nil {
		return ClaimResult{}, err
	}
	defer cancel()
	found, err := readClaims(ctx, f, ref.Number)
	if err != nil {
		return ClaimResult{}, err
	}
	heldComment, held, err := d.reconcile(ctx, f, ref, req.Session, found)
	if err != nil {
		return ClaimResult{}, err
	}
	var own claimComment
	if held {
		own = heldComment
	} else {
		target, ok := findOwnUnreleased(found, req.Session)
		if !ok {
			return ClaimResult{}, fmt.Errorf("%w: %s (claim it first)", ErrNoClaim, ref)
		}
		own = target
	}
	return d.finaliseRelease(ctx, f, ref, own, req, note)
}

func (d *ClaimDesk) finaliseRelease(ctx context.Context, f ClaimForge, ref ClaimRef, own claimComment, req ReleaseRequest, note string) (ClaimResult, error) {
	claim := own.claim
	claim.Stage, claim.Outcome, claim.Note = ClaimStageReleased, req.Outcome, note
	claim.Updated = d.Now().UTC().Truncate(time.Second)
	body, err := renderClaimBody(claim, takeoverNote(own.body))
	if err != nil {
		return ClaimResult{}, err
	}
	// Labels go first: when the label removal fails the claim stays live and a retried release
	// finds it, instead of ErrNoClaim with labels left behind.
	if err := d.clearLabels(ctx, f, ref.Number); err != nil {
		return ClaimResult{}, err
	}
	if err := f.EditIssueComment(ctx, claim.CommentID, body); err != nil {
		return ClaimResult{}, unverifiable("finalise claim comment", err)
	}
	return ClaimResult{Ref: ref.String(), Claim: claim, Action: "released"}, nil
}

func findOwnUnreleased(found []claimComment, session string) (claimComment, bool) {
	for _, entry := range found {
		if entry.claim.Session == session && !entry.claim.Released() {
			return entry, true
		}
	}
	return claimComment{}, false
}

// LiveClaim returns the fresh live claim on the issue, or nil when none holds it. A forge
// failure is ErrClaimUnverifiable, never "no claim".
func (d *ClaimDesk) LiveClaim(ctx context.Context, ref ClaimRef) (*Claim, error) {
	ctx, cancel, f, err := d.session(ctx, ref)
	if err != nil {
		return nil, err
	}
	defer cancel()
	found, err := readClaims(ctx, f, ref.Number)
	if err != nil {
		return nil, err
	}
	if holder, held := d.holder(found); held {
		return &holder, nil
	}
	return nil, nil
}

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
	if !ok || digits == "" || len(digits) > MaxIssueNumberDigits {
		return ClaimRef{}, fmt.Errorf("issue reference %q is not <owner>/<repo>#<number>", text)
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return ClaimRef{}, fmt.Errorf("issue reference %q is not <owner>/<repo>#<number>", text)
		}
	}
	number, err := strconv.Atoi(digits)
	if err != nil || number < 1 {
		return ClaimRef{}, fmt.Errorf("issue reference %q needs a positive issue number", text)
	}
	owner, repo, err := util.SplitGitHubRepository(coordinate)
	if err != nil {
		return ClaimRef{}, fmt.Errorf("issue reference %q: %w", text, err)
	}
	return ClaimRef{Owner: owner, Repo: repo, Number: number}, nil
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

// fresh reports whether a live claim is still inside the stale window: a claim is stale
// once its last update is older than the window, and a claim whose update lies in the
// future (clock skew) is fresh.
func (d *ClaimDesk) fresh(c Claim) bool {
	return !c.Released() && d.Now().Sub(c.Updated) <= d.Stale
}

// holder returns the first fresh live claim: the lowest comment id wins when a race left two.
func (d *ClaimDesk) holder(found []claimComment) (Claim, bool) {
	for _, entry := range found {
		if d.fresh(entry.claim) {
			return entry.claim, true
		}
	}
	return Claim{}, false
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
	if other, held := d.holder(found); held && other.Session != req.Session {
		return ClaimResult{}, &ClaimHeldError{Ref: ref, Holder: other}
	}
	return d.writeClaim(ctx, f, ref, req, found)
}

// writeClaim writes the claim comment and the labels once no other session holds the issue.
func (d *ClaimDesk) writeClaim(ctx context.Context, f ClaimForge, ref ClaimRef, req ClaimRequest, found []claimComment) (ClaimResult, error) {
	now := d.Now().UTC().Truncate(time.Second)
	claim := Claim{Session: req.Session, Lane: req.Lane, Branch: req.Branch, Stage: "claimed", Started: now, Updated: now}
	result := ClaimResult{Ref: ref.String(), Action: "created"}
	target, hasTarget := pickClaimTarget(found, req.Session)
	takeover := ""
	if hasTarget {
		result.Action = "took-over"
		switch {
		case target.claim.Session == req.Session && !target.claim.Released():
			result.Action = "resumed"
			claim.Started, claim.Stage = target.claim.Started, target.claim.Stage
		case target.claim.Released():
			result.Action = "created"
		default:
			old := target.claim
			result.TookOver = &old
			takeover = "replaces the stale claim of " + strings.TrimPrefix(DescribeClaim(old), "claimed by ")
		}
	}
	body, err := renderClaimBody(claim, takeover)
	if err != nil {
		return ClaimResult{}, err
	}
	id, err := d.putClaim(ctx, f, ref.Number, target, hasTarget, body)
	if err != nil {
		return ClaimResult{}, err
	}
	claim.CommentID = id
	if err := d.confirmHold(ctx, f, ref, claim); err != nil {
		return ClaimResult{}, err
	}
	if err := d.applyClaimLabels(ctx, f, ref.Number, claim.Stage == claimBlockedStage); err != nil {
		return ClaimResult{}, d.abandon(ctx, f, claim, err)
	}
	result.Claim = claim
	return result, nil
}

// pickClaimTarget chooses the one comment a claim is written to: the session's own, else a
// stale live one, else a released one. No claim comment at all means a new comment.
func pickClaimTarget(found []claimComment, session string) (claimComment, bool) {
	var live, released *claimComment
	for i := range found {
		switch {
		case found[i].claim.Session == session && !found[i].claim.Released():
			return found[i], true
		case !found[i].claim.Released() && live == nil:
			live = &found[i]
		case found[i].claim.Released() && released == nil:
			released = &found[i]
		}
	}
	switch {
	case live != nil:
		return *live, true
	case released != nil:
		return *released, true
	}
	return claimComment{}, false
}

func (d *ClaimDesk) putClaim(ctx context.Context, f ClaimForge, number int, target claimComment, hasTarget bool, body string) (int64, error) {
	if hasTarget {
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
func (d *ClaimDesk) confirmHold(ctx context.Context, f ClaimForge, ref ClaimRef, mine Claim) error {
	found, err := readClaims(ctx, f, ref.Number)
	if err != nil {
		return d.abandon(ctx, f, mine, err)
	}
	winner, held := d.holder(found)
	if held && winner.Session == mine.Session {
		return nil
	}
	if !held {
		return d.abandon(ctx, f, mine, fmt.Errorf("%w: claim comment %d not found on read-back", ErrClaimUnverifiable, mine.CommentID))
	}
	refusal := &ClaimHeldError{Ref: ref, Holder: winner}
	if winner.CommentID != mine.CommentID {
		return errors.Join(refusal, d.finalise(ctx, f, mine, "abandoned"))
	}
	return refusal
}

// abandon finalises a claim whose labels or read-back failed, so a half-made claim does not
// hold the issue, and returns cause together with any failure of that cleanup.
func (d *ClaimDesk) abandon(ctx context.Context, f ClaimForge, mine Claim, cause error) error {
	return errors.Join(cause, d.finalise(ctx, f, mine, "abandoned"))
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

// ownClaim returns the session's live claim, or the error that says why there is none.
func (d *ClaimDesk) ownClaim(ref ClaimRef, found []claimComment, session string) (claimComment, error) {
	for _, entry := range found {
		if entry.claim.Session == session && !entry.claim.Released() {
			return entry, nil
		}
	}
	if other, held := d.holder(found); held {
		return claimComment{}, &ClaimHeldError{Ref: ref, Holder: other}
	}
	return claimComment{}, fmt.Errorf("%w: %s (claim it first)", ErrNoClaim, ref)
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
	return d.recordStage(ctx, f, ref, found, req, note)
}

// recordStage edits the session's claim comment to the requested stage and syncs the blocked label.
func (d *ClaimDesk) recordStage(ctx context.Context, f ClaimForge, ref ClaimRef, found []claimComment, req StatusRequest, note string) (ClaimResult, error) {
	own, err := d.ownClaim(ref, found, req.Session)
	if err != nil {
		return ClaimResult{}, err
	}
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
	own, err := d.ownClaim(ref, found, req.Session)
	if err != nil {
		return ClaimResult{}, err
	}
	claim := own.claim
	claim.Stage, claim.Outcome, claim.Note = ClaimStageReleased, req.Outcome, note
	claim.Updated = d.Now().UTC().Truncate(time.Second)
	body, err := renderClaimBody(claim, takeoverNote(own.body))
	if err != nil {
		return ClaimResult{}, err
	}
	if err := f.EditIssueComment(ctx, claim.CommentID, body); err != nil {
		return ClaimResult{}, unverifiable("finalise claim comment", err)
	}
	for _, name := range []string{LabelInProgress, LabelBlocked} {
		if err := f.RemoveLabel(ctx, ref.Number, name); err != nil {
			return ClaimResult{}, unverifiable("remove label "+name, err)
		}
	}
	return ClaimResult{Ref: ref.String(), Claim: claim, Action: "released"}, nil
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

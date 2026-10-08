// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Issue claims (#937). One comment per issue carries the claim. Its first line is a machine
// readable marker, an HTML comment GitHub does not render; the rest is human text that every
// stage update rewrites.
//
// The marker is read with an allow-list: a comment is a claim only when its first line is
// exactly the marker grammar below, with every key from a fixed set, every value of a fixed
// shape, and no key twice. Anything else is not a claim and is skipped, so no comment, whoever
// wrote it and whatever it holds, can be mistaken for one by enumerating its defects.

// Claim marker constants.
const (
	// ClaimMarkerVersion is the only marker version this reader accepts.
	ClaimMarkerVersion = 1
	// ClaimStageReleased is the stage of a finalised claim; release sets it, no status update.
	ClaimStageReleased = "released"
	// MaxClaimNoteRunes bounds the note a stage update carries.
	MaxClaimNoteRunes = 280
	// MaxClaimMarkerBytes bounds the marker line the reader looks at.
	MaxClaimMarkerBytes = 1024

	claimMarkerOpen  = "<!-- praetor-claim "
	claimMarkerClose = " -->"
)

// ClaimOutcomes are the outcomes a release may name.
var ClaimOutcomes = []string{"landed", "abandoned", "handed-over"}

var (
	claimValuePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:+@-]{0,127}$`)
	claimStagePattern = regexp.MustCompile(`^(claimed|implementing|review|blocked|queued|landing|released|fix-round-[1-9][0-9]{0,2})$`)
	claimKeys         = []string{"v", "session", "lane", "branch", "started", "updated", "stage", "outcome"}
)

// Claim is the state one claim comment records.
type Claim struct {
	Session string    `json:"session"`
	Lane    string    `json:"lane"`
	Branch  string    `json:"branch"`
	Started time.Time `json:"started"`
	Updated time.Time `json:"updated"`
	Stage   string    `json:"stage"`
	Outcome string    `json:"outcome,omitempty"`
	// Note is the free text of the latest stage update. It lives in the human part of the
	// comment, not in the marker, so it is empty on a claim read back from a comment.
	Note string `json:"note,omitempty"`
	// CommentID is the forge comment that carries the claim.
	CommentID int64 `json:"comment_id,omitempty"`
}

// Released reports whether the claim was finalised.
func (c Claim) Released() bool { return c.Stage == ClaimStageReleased }

// ValidClaimStage reports whether stage is one a status update may set.
func ValidClaimStage(stage string) bool {
	return stage != ClaimStageReleased && claimStagePattern.MatchString(stage)
}

// ValidClaimOutcome reports whether outcome is one a release may name.
func ValidClaimOutcome(outcome string) bool {
	return slices.Contains(ClaimOutcomes, outcome)
}

// validateClaim checks every field the marker would carry.
func validateClaim(c Claim) error {
	values := [][2]string{{"session", c.Session}, {"lane", c.Lane}, {"branch", c.Branch}}
	for _, pair := range values {
		if !claimValuePattern.MatchString(pair[1]) {
			return fmt.Errorf("claim %s %q is not 1..128 characters of letters, digits and . _ / : + @ -", pair[0], pair[1])
		}
	}
	if !claimStagePattern.MatchString(c.Stage) {
		return fmt.Errorf("claim stage %q is not a known stage", c.Stage)
	}
	if c.Started.IsZero() || c.Updated.IsZero() {
		return errors.New("claim needs a start and an update time")
	}
	if c.Outcome != "" && !ValidClaimOutcome(c.Outcome) {
		return fmt.Errorf("claim outcome %q is not one of %s", c.Outcome, strings.Join(ClaimOutcomes, ", "))
	}
	return nil
}

// RenderClaimMarker renders the marker line of a claim after validating it.
func RenderClaimMarker(c Claim) (string, error) {
	if err := validateClaim(c); err != nil {
		return "", err
	}
	parts := []string{
		fmt.Sprintf("v=%d", ClaimMarkerVersion), "session=" + c.Session, "lane=" + c.Lane, "branch=" + c.Branch,
		"started=" + c.Started.UTC().Format(time.RFC3339), "updated=" + c.Updated.UTC().Format(time.RFC3339),
		"stage=" + c.Stage,
	}
	if c.Outcome != "" {
		parts = append(parts, "outcome="+c.Outcome)
	}
	return claimMarkerOpen + strings.Join(parts, " ") + claimMarkerClose, nil
}

// ParseClaimMarker reads the claim a comment body carries. The second return is false for any
// body whose first line is not exactly a version 1 marker: another version, an unknown or
// repeated key, a value outside its shape, a missing key, a time that is not RFC 3339.
func ParseClaimMarker(body string) (Claim, bool) {
	line, _, _ := strings.Cut(body, "\n")
	line = strings.TrimRight(line, "\r")
	if len(line) > MaxClaimMarkerBytes {
		return Claim{}, false
	}
	inner, ok := strings.CutPrefix(line, claimMarkerOpen)
	if !ok {
		return Claim{}, false
	}
	inner, ok = strings.CutSuffix(inner, claimMarkerClose)
	if !ok {
		return Claim{}, false
	}
	fields, ok := claimFields(inner)
	if !ok {
		return Claim{}, false
	}
	return claimFromFields(fields)
}

// claimFields splits the marker interior into key=value pairs, refusing a key outside the
// allow-list and a repeated key.
func claimFields(inner string) (map[string]string, bool) {
	tokens := strings.Split(inner, " ")
	if len(tokens) > len(claimKeys) {
		return nil, false
	}
	fields := make(map[string]string, len(tokens))
	for i := 0; i < len(tokens) && i < len(claimKeys); i++ {
		key, value, ok := strings.Cut(tokens[i], "=")
		if !ok || !slices.Contains(claimKeys, key) || value == "" {
			return nil, false
		}
		if _, dup := fields[key]; dup {
			return nil, false
		}
		fields[key] = value
	}
	return fields, true
}

func claimFromFields(fields map[string]string) (Claim, bool) {
	if fields["v"] != fmt.Sprint(ClaimMarkerVersion) {
		return Claim{}, false
	}
	started, err := time.Parse(time.RFC3339, fields["started"])
	if err != nil {
		return Claim{}, false
	}
	updated, err := time.Parse(time.RFC3339, fields["updated"])
	if err != nil {
		return Claim{}, false
	}
	claim := Claim{
		Session: fields["session"], Lane: fields["lane"], Branch: fields["branch"],
		Started: started, Updated: updated, Stage: fields["stage"], Outcome: fields["outcome"],
	}
	if validateClaim(claim) != nil {
		return Claim{}, false
	}
	return claim, true
}

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/caveman"
)

// BriefClaim is what a brief says about the forge issues it works on: the parsed issue
// references of its `issue:` lines and the session of its `session:` line.
type BriefClaim struct {
	Issues  []string
	Session string
}

// ParseBriefClaim parses and validates the `issue:` and `session:` fields of a brief.
// Caveman extracts the raw tokens, and this parser validates each reference with ParseClaimRef
// (which refuses #0 and leading zeros) and validates the session with the claim value pattern.
func ParseBriefClaim(text string) (BriefClaim, error) {
	raw, err := caveman.ExtractBriefClaim(text)
	if err != nil {
		return BriefClaim{}, err
	}
	if len(raw.Issues) == 0 {
		return BriefClaim{}, nil
	}
	if raw.Session != "" && !claimValuePattern.MatchString(raw.Session) {
		return BriefClaim{}, errors.New("brief session field must be 1..128 characters of letters, digits and . _ / : + @ -")
	}
	var refs []string
	for _, token := range raw.Issues {
		ref, err := ParseClaimRef(token)
		if err != nil {
			return BriefClaim{}, fmt.Errorf("brief issue %s is not <owner>/<repo>#<number>", token)
		}
		refs = append(refs, ref.String())
	}
	return BriefClaim{Issues: refs, Session: raw.Session}, nil
}

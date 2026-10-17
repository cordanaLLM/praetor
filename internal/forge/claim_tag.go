// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
)

const (
	// ClaimTagPrefix starts every keyed identity tag.
	ClaimTagPrefix = "h:"
	// claimTagHexLength is the number of hex characters following ClaimTagPrefix.
	claimTagHexLength = 12
	// ClaimTagLength is the exact length of an identity tag ("h:" + 12 hex chars).
	ClaimTagLength = len(ClaimTagPrefix) + claimTagHexLength
)

// ClaimTag computes a 14-character keyed identity tag for an issue claim session or lane.
// It returns "h:" followed by the first 12 hex characters of HMAC-SHA256(key, kind + "\x00" + value).
func ClaimTag(key []byte, kind, value string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(kind + "\x00" + value))
	sum := mac.Sum(nil)
	return ClaimTagPrefix + hex.EncodeToString(sum[:claimTagHexLength/2])
}

// IsClaimTag reports whether s is a valid identity tag (starts with "h:" followed by 12 hex chars).
func IsClaimTag(s string) bool {
	if !strings.HasPrefix(s, ClaimTagPrefix) || len(s) != ClaimTagLength {
		return false
	}
	for i := len(ClaimTagPrefix); i < ClaimTagLength; i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// SameIdentity compares a stored claim session or lane value with a requested one.
// When stored is a tag (starts with "h:"), it compares with the tag of the request under key.
// When stored is a legacy plain value, it compares plain.
func SameIdentity(stored, requested, kind string, key []byte) bool {
	if IsClaimTag(stored) {
		reqTag := requested
		if !IsClaimTag(requested) && len(key) > 0 {
			reqTag = ClaimTag(key, kind, requested)
		}
		return subtle.ConstantTimeCompare([]byte(stored), []byte(reqTag)) == 1
	}
	return stored == requested
}

// tagClaim replaces plain Session and Lane in c with their keyed tags under key.
// A field that is already an identity tag or is empty is not modified.
func tagClaim(c Claim, key []byte) Claim {
	if c.Session != "" && !IsClaimTag(c.Session) && len(key) > 0 {
		c.Session = ClaimTag(key, "session", c.Session)
	}
	if c.Lane != "" && !IsClaimTag(c.Lane) && len(key) > 0 {
		c.Lane = ClaimTag(key, "lane", c.Lane)
	}
	return c
}

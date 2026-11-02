// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"strings"
	"testing"
)

// TestValidGitObjectID_Positive verifies that ValidGitObjectID accepts 40-hex SHA-1 and 64-hex
// SHA-256 object ids in lower, upper and mixed case.
func TestValidGitObjectID_Positive(t *testing.T) {
	cases := []struct {
		name string
		oid  string
	}{
		{name: "sha1 lowercase", oid: "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"},
		{name: "sha1 uppercase", oid: "E69DE29BB2D1D6434B8B29AE775AD8C2E48C5391"},
		{name: "sha1 mixed case", oid: "E69de29bB2D1d6434B8b29ae775ad8c2E48C5391"},
		{name: "sha1 all zeroes", oid: strings.Repeat("0", 40)},
		{name: "sha256 lowercase", oid: strings.Repeat("abcdef0123456789", 4)},
		{name: "sha256 uppercase", oid: strings.Repeat("ABCDEF0123456789", 4)},
		{name: "sha256 mixed case", oid: strings.Repeat("aBcDeF0123456789", 4)},
		{name: "sha256 all zeroes", oid: strings.Repeat("0", 64)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !ValidGitObjectID(tc.oid) {
				t.Fatalf("ValidGitObjectID(%q) = false; want true", tc.oid)
			}
		})
	}
}

// TestValidGitObjectID_Negative verifies that ValidGitObjectID refuses full-length ids holding a
// non-hex character, a space, a hyphen, punctuation, a newline or a null byte.
func TestValidGitObjectID_Negative(t *testing.T) {
	cases := []struct {
		name string
		oid  string
	}{
		{name: "sha1 non-hex char g", oid: "g" + strings.Repeat("0", 39)},
		{name: "sha1 non-hex char z", oid: strings.Repeat("0", 39) + "z"},
		{name: "sha1 space inside", oid: strings.Repeat("0", 20) + " " + strings.Repeat("0", 19)},
		{name: "sha1 hyphen inside", oid: strings.Repeat("0", 20) + "-" + strings.Repeat("0", 19)},
		{name: "sha1 punctuation", oid: strings.Repeat("0", 39) + "!"},
		{name: "sha256 non-hex char g", oid: strings.Repeat("0", 63) + "g"},
		{name: "sha256 non-hex char Z", oid: strings.Repeat("0", 63) + "Z"},
		{name: "sha256 newline", oid: strings.Repeat("0", 63) + "\n"},
		{name: "sha256 null byte", oid: strings.Repeat("0", 63) + "\x00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if ValidGitObjectID(tc.oid) {
				t.Fatalf("ValidGitObjectID(%q) = true; want false", tc.oid)
			}
		})
	}
}

// TestValidGitObjectID_Boundary verifies that ValidGitObjectID refuses the empty string, one
// character and the lengths next to 40 and 64 (39, 41, 48, 63, 65).
func TestValidGitObjectID_Boundary(t *testing.T) {
	cases := []struct {
		name string
		oid  string
	}{
		{name: "empty", oid: ""},
		{name: "single char", oid: "a"},
		{name: "len 39 one short of sha1", oid: strings.Repeat("a", 39)},
		{name: "len 41 one over sha1", oid: strings.Repeat("a", 41)},
		{name: "len 48 between sha1 and sha256", oid: strings.Repeat("a", 48)},
		{name: "len 63 one short of sha256", oid: strings.Repeat("a", 63)},
		{name: "len 65 one over sha256", oid: strings.Repeat("a", 65)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if ValidGitObjectID(tc.oid) {
				t.Fatalf("ValidGitObjectID(%q) = true; want false", tc.oid)
			}
		})
	}
}

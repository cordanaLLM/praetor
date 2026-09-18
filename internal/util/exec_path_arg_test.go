// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// Positive: an absolute path on the host is accepted, whatever the host's separator.
func TestValidateExecPathArgAcceptsHostAbsolutePath(t *testing.T) {
	path, err := filepath.Abs(filepath.Join("checkout", "source"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateExecPathArg(path); err != nil {
		t.Fatalf("host absolute path refused: %v", err)
	}
}

// Negative: the exemption is the separator and nothing else. Every other shell
// metacharacter is still refused inside a path, so relaxing paths did not relax injection.
func TestValidateExecPathArgStillRefusesEveryOtherMetacharacter(t *testing.T) {
	base, err := filepath.Abs("checkout")
	if err != nil {
		t.Fatal(err)
	}
	for _, meta := range strings.Split(";&|$`<>(){}\"'", "") {
		if err := ValidateExecPathArg(base + meta + "x"); !errors.Is(err, ErrExecArgMeta) {
			t.Fatalf("metacharacter %q accepted in a path: %v", meta, err)
		}
	}
}

// Boundary: the option guard and the identifier validator are unchanged. A path
// beginning with '-' is still an option injection, and ValidateExecArg still refuses a
// backslash, because package names and URLs must not start admitting one.
func TestValidateExecPathArgKeepsOptionGuardAndLeavesIdentifiersStrict(t *testing.T) {
	if err := ValidateExecPathArg("-rf"); !errors.Is(err, ErrExecArgOption) {
		t.Fatalf("leading dash accepted as a path: %v", err)
	}
	if err := ValidateExecArg(`name\with\backslash`); !errors.Is(err, ErrExecArgMeta) {
		t.Fatalf("identifier validator started admitting a backslash: %v", err)
	}
	// On POSIX the separator is '/', absent from the metacharacter set, so the two
	// validators must agree exactly: the exemption removes nothing there.
	if filepath.Separator == '/' {
		for _, v := range []string{"/srv/checkout", `a\b`, "x;y"} {
			if (ValidateExecArg(v) == nil) != (ValidateExecPathArg(v) == nil) {
				t.Fatalf("validators disagree on POSIX for %q", v)
			}
		}
	}
}

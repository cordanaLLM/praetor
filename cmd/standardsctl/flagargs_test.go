// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"flag"
	"io"
	"strings"
	"testing"
)

func newQuietFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func TestParseInterspersed_Positive_FlagsAfterPositionals(t *testing.T) {
	fs := newQuietFlagSet("flavor audit")
	flavor := fs.String("flavor", "auto", "")
	force := fs.Bool("force", false, "")

	positional, err := parseInterspersed(fs, []string{"./services/api", "--flavor=rust-systems", "--force"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(positional) != 1 || positional[0] != "./services/api" {
		t.Fatalf("expected the directory as the only positional, got %v", positional)
	}
	if *flavor != "rust-systems" {
		t.Errorf("a flag written after the positional must still bind, got %q", *flavor)
	}
	if !*force {
		t.Errorf("--force written after the positional must still bind")
	}
}

func TestParseInterspersed_Positive_SpaceSeparatedFlagValue(t *testing.T) {
	fs := newQuietFlagSet("bump canary")
	target := fs.String("target", "", "")

	positional, err := parseInterspersed(fs, []string{"github.com/spf13/cobra", "--target", "v1.9.1"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if *target != "v1.9.1" {
		t.Errorf("space-separated flag value mis-bound: %q", *target)
	}
	if len(positional) != 1 || positional[0] != "github.com/spf13/cobra" {
		t.Errorf("package positional mis-bound: %v", positional)
	}
}

func TestParseInterspersed_Positive_TerminatorMakesRestPositional(t *testing.T) {
	fs := newQuietFlagSet("term")
	name := fs.String("name", "", "")

	positional, err := parseInterspersed(fs, []string{"--name=a", "first", "--", "--not-a-flag", "second"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if *name != "a" {
		t.Errorf("expected name=a, got %q", *name)
	}
	want := []string{"first", "--not-a-flag", "second"}
	if len(positional) != len(want) {
		t.Fatalf("expected %v, got %v", want, positional)
	}
	for i := range want {
		if positional[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, positional)
		}
	}
}

func TestParseInterspersed_Negative_UnknownFlagAndNilSet(t *testing.T) {
	fs := newQuietFlagSet("unknown")
	fs.String("flavor", "auto", "")

	if _, err := parseInterspersed(fs, []string{"dir", "--nope=1"}); err == nil {
		t.Error("an unknown flag after a positional must be rejected, not accepted as a positional")
	}
	if _, err := parseInterspersed(nil, []string{"x"}); err == nil {
		t.Error("expected an error for a nil flag set")
	}
}

func TestParseInterspersed_Boundary_EmptyAndOverflow(t *testing.T) {
	fs := newQuietFlagSet("empty")
	positional, err := parseInterspersed(fs, nil)
	if err != nil {
		t.Fatalf("parse of no arguments: %v", err)
	}
	if len(positional) != 0 {
		t.Errorf("expected no positionals, got %v", positional)
	}

	overflow := make([]string, maxCLIArgs+1)
	for i := range overflow {
		overflow[i] = "a"
	}
	_, err = parseInterspersed(newQuietFlagSet("overflow"), overflow)
	if err == nil || !strings.Contains(err.Error(), "too many arguments") {
		t.Errorf("expected the argument-count bound to be enforced, got %v", err)
	}
}

func TestPositionalAt_3D(t *testing.T) {
	positional := []string{"first", "", "third"}

	if got := positionalAt(positional, 0, "fallback"); got != "first" {
		t.Errorf("expected first, got %q", got)
	}
	if got := positionalAt(positional, 1, "fallback"); got != "fallback" {
		t.Errorf("an empty positional must fall back, got %q", got)
	}
	if got := positionalAt(positional, 9, "fallback"); got != "fallback" {
		t.Errorf("an out-of-range index must fall back, got %q", got)
	}
	if got := positionalAt(nil, -1, "fallback"); got != "fallback" {
		t.Errorf("a negative index on a nil slice must fall back, got %q", got)
	}
}

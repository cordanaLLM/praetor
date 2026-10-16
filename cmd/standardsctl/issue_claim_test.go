// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/forge/forgetest"
)

// useFakeClaimDesk points the claim commands at an in-memory forge and captures their output.
func useFakeClaimDesk(t *testing.T, fake *forgetest.ClaimFake, now func() time.Time) *bytes.Buffer {
	t.Helper()
	previousFactory, previousOutput := claimDeskFactory, claimOutput
	var out bytes.Buffer
	claimOutput = &out
	claimDeskFactory = func(context.Context, string, string, *operatorSettingsFlags) (*forge.ClaimDesk, error) {
		return &forge.ClaimDesk{Open: fake.Opener(), Stale: 6 * time.Hour, Now: now}, nil
	}
	t.Cleanup(func() { claimDeskFactory, claimOutput = previousFactory, previousOutput })
	return &out
}

func claimCLI(op string, extra ...string) error {
	return dispatchCommand("issue", append([]string{op, "acme/widgets#7", "--token=test-fixture"}, extra...))
}

func TestIssueClaimCLI_Positive_ClaimStatusRelease(t *testing.T) {
	fake := forgetest.NewClaimFake()
	clock := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	out := useFakeClaimDesk(t, fake, func() time.Time { return clock })
	if err := claimCLI("claim", "--session=s1", "--lane=agy", "--branch=feat/x"); err != nil {
		t.Fatal(err)
	}
	if err := claimCLI("status", "--session=s1", "--stage=review", "--note=round one"); err != nil {
		t.Fatal(err)
	}
	if len(fake.Comments) != 1 || !strings.Contains(fake.Comments[0].Body, "stage=review") {
		t.Fatalf("status must edit the one claim comment: %+v", fake.Comments)
	}
	if err := claimCLI("release", "--session=s1", "--outcome=landed"); err != nil {
		t.Fatal(err)
	}
	if len(fake.OnIssue) != 0 || !strings.Contains(fake.Comments[0].Body, "outcome=landed") {
		t.Fatalf("release must clear labels and finalise: %v %q", fake.OnIssue, fake.Comments[0].Body)
	}
	for _, want := range []string{"created acme/widgets#7", "updated acme/widgets#7", "released acme/widgets#7"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestIssueClaimCLI_Negative_SecondSessionRefusedNamingFirst(t *testing.T) {
	fake := forgetest.NewClaimFake()
	useFakeClaimDesk(t, fake, time.Now)
	if err := claimCLI("claim", "--session=first", "--lane=agy", "--branch=feat/first"); err != nil {
		t.Fatal(err)
	}
	err := claimCLI("claim", "--session=second", "--lane=codex", "--branch=feat/second")
	if !errors.Is(err, forge.ErrClaimHeld) || !strings.Contains(err.Error(), "session first") {
		t.Fatalf("second claim must be refused naming the first: %v", err)
	}
}

func TestIssueClaimCLI_Negative_ArgumentsAndToken(t *testing.T) {
	fake := forgetest.NewClaimFake()
	useFakeClaimDesk(t, fake, time.Now)
	cases := map[string][]string{
		"no reference":      {"claim", "--token=x", "--session=s", "--lane=l", "--branch=b"},
		"two references":    {"claim", "a/b#1", "a/b#2", "--token=x", "--session=s", "--lane=l", "--branch=b"},
		"bare number":       {"claim", "7", "--token=x", "--session=s", "--lane=l", "--branch=b"},
		"missing session":   {"claim", "acme/widgets#7", "--token=x", "--lane=l", "--branch=b"},
		"unknown flag":      {"claim", "acme/widgets#7", "--token=x", "--bogus"},
		"bad stage":         {"status", "acme/widgets#7", "--token=x", "--session=s", "--stage=done"},
		"release w/o claim": {"release", "acme/widgets#7", "--token=x", "--session=s", "--outcome=landed"},
		"bad outcome":       {"release", "acme/widgets#7", "--token=x", "--session=s", "--outcome=done"},
	}
	for name, args := range cases {
		if err := dispatchCommand("issue", args); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if len(fake.Comments) != 0 {
		t.Fatalf("refused commands must write nothing: %+v", fake.Comments)
	}
}

func TestIssueClaimCLI_Negative_ForgeErrorFailsClosed(t *testing.T) {
	fake := forgetest.NewClaimFake()
	fake.FailOn = "ListIssueComments"
	useFakeClaimDesk(t, fake, time.Now)
	err := claimCLI("claim", "--session=s1", "--lane=agy", "--branch=feat/x")
	if !errors.Is(err, forge.ErrClaimUnverifiable) {
		t.Fatalf("a forge error must fail the command closed: %v", err)
	}
}

func TestIssueClaimCLI_Positive_HelpListsTheCommands(t *testing.T) {
	out, err := captureStdout(t, func() error { return dispatchCommand("issue", []string{"--help"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"claim <owner/repo#n>", "status <owner/repo#n>", "release <owner/repo#n>"} {
		if !strings.Contains(out, want) {
			t.Errorf("issue help lacks %q", want)
		}
	}
}

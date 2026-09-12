// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavors"
)

func TestApplyFlavorTransitions_Negative_RefusesUnresolvedSourceRef(t *testing.T) {
	transitions := []flavors.TagTransition{{
		FlavorName: "latest",
		CurrentRef: "8feca96",
		TargetRef:  "refs/tags/v9.9.9",
		Action:     flavors.ActionUnresolved,
	}}

	// The target directory does not matter: the refusal happens before any git call, so
	// an unresolvable release pointer can never be force-moved onto the checkout.
	err := applyFlavorTransitions(context.Background(), t.TempDir(), transitions)
	if err == nil {
		t.Fatal("expected an unresolved transition to abort the sync")
	}
	if !strings.Contains(err.Error(), "resolves to no commit") {
		t.Errorf("unexpected error text: %v", err)
	}
}

func TestApplyFlavorTransitions_Positive_NoopsAreNotRetagged(t *testing.T) {
	transitions := []flavors.TagTransition{
		{FlavorName: "latest", CurrentRef: "abc", TargetRef: "refs/tags/v1.0.0", TargetCommit: "abc", Action: flavors.ActionNoop},
		{FlavorName: "lts", CurrentRef: "def", TargetRef: "refs/heads/lts-1.x", TargetCommit: "def", Action: flavors.ActionNoop},
	}

	if err := applyFlavorTransitions(context.Background(), t.TempDir(), transitions); err != nil {
		t.Fatalf("a plan of noops must not fail: %v", err)
	}
}

func TestApplyFlavorTransitions_Boundary_EmptyPlan(t *testing.T) {
	if err := applyFlavorTransitions(context.Background(), t.TempDir(), nil); err != nil {
		t.Fatalf("an empty plan must succeed: %v", err)
	}
}

func TestFirstOutputLine_3D(t *testing.T) {
	if got := firstOutputLine("  abc \n def\n"); got != "abc" {
		t.Errorf("expected the first trimmed line, got %q", got)
	}
	if got := firstOutputLine("only"); got != "only" {
		t.Errorf("expected the single line, got %q", got)
	}
	if got := firstOutputLine("   \n\n"); got != "" {
		t.Errorf("expected an empty result for blank output, got %q", got)
	}
}

func TestResolveFlavorRef_Negative_RejectsShellMetacharacters(t *testing.T) {
	if _, ok := resolveFlavorRef(context.Background(), t.TempDir(), "refs/tags/$(touch pwned)"); ok {
		t.Error("a ref carrying shell metacharacters must not resolve")
	}
	if _, ok := resolveFlavorRef(context.Background(), t.TempDir(), ""); ok {
		t.Error("an empty ref must not resolve")
	}
}

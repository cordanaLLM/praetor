// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"strconv"
	"strings"
	"testing"
)

func TestOperatorMergeHelper_Positive_Layers(t *testing.T) {
	for _, helper := range []string{"squash_merge.sh", "/abs/squash_merge.sh", `C:\tools\squash_merge.cmd`} {
		doc := "hooks: {merge: {helper: " + strconv.Quote(helper) + "}}"
		policy, err := loadOperatorDocs(t, doc, "")
		if err != nil {
			t.Fatal(err)
		}
		if got := policy.OperatorSettings().Hooks.Merge.Helper; got != helper {
			t.Errorf("helper = %q, want %q", got, helper)
		}
	}
	policy, err := loadOperatorDocs(t, "hooks: {merge: {helper: first.sh}}", "hooks: {merge: {helper: second.sh}}")
	if err != nil {
		t.Fatal(err)
	}
	if got := policy.OperatorSettings().Hooks.Merge.Helper; got != "second.sh" {
		t.Errorf("later scalar = %q, want second.sh", got)
	}
}

func TestOperatorMergeHelper_Negative_InvalidWord(t *testing.T) {
	for _, helper := range []string{"two words", " padded", "padded ", "two\twords", "two\nwords", "two\u00a0words", "helper;other", "$(helper)", strings.Repeat("h", 257)} {
		doc := "hooks: {merge: {helper: " + strconv.Quote(helper) + "}}"
		if _, err := loadOperatorDocs(t, doc, ""); err == nil || !strings.Contains(err.Error(), "hooks.merge.helper") {
			t.Errorf("helper %q: error = %v, want hooks.merge.helper", helper, err)
		}
	}
}

func TestOperatorMergeHelper_Boundary_Empty(t *testing.T) {
	for _, fleet := range []string{"hooks: {scope: governed}", "hooks: {merge: {helper: first.sh}}"} {
		policy, err := loadOperatorDocs(t, fleet, `hooks: {merge: {helper: ""}}`)
		if err != nil {
			t.Fatal(err)
		}
		if got := policy.OperatorSettings().Hooks.Merge.Helper; got != "" {
			t.Errorf("empty helper = %q", got)
		}
	}
	if got := DefaultOperatorSettings().Hooks.Merge.Helper; got != "" {
		t.Errorf("default helper = %q", got)
	}
}

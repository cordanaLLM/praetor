// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import "testing"

// TestAdminMergeBooleanSpellings verifies that all true boolean spellings of gh pr merge --admin are denied while false or non-boolean values are allowed.
func TestAdminMergeBooleanSpellings(t *testing.T) {
	for _, value := range []string{"1", "t", "T", "true", "True", "TRUE", "tRuE"} {
		for _, command := range []string{"gh pr merge 123 --admin=", "gh --admin=", "gh pr --admin="} {
			text := command + value
			switch command {
			case "gh --admin=":
				text += " pr merge 123"
			case "gh pr --admin=":
				text += " merge 123"
			}
			if got := policy(t).Command(text); got.Outcome != Deny {
				t.Errorf("admin merge escaped: %q: %+v", text, got)
			}
		}
	}
	for _, value := range []string{"0", "false", "False", "trueish", "turtle", "10"} {
		if got := policy(t).Command("gh pr merge 123 --admin=" + value); got.Outcome != Allow {
			t.Errorf("false or invalid value denied: %q: %+v", value, got)
		}
	}
}

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"encoding/json"
	"os"
	"testing"
)

// TestPolicyHeredoc distinguishes data bodies from commands and expansions.
func TestPolicyHeredoc(t *testing.T) {
	word := "su" + "do"
	p := policy(t)
	for _, tc := range []struct {
		name, command string
		want          Outcome
	}{
		{"data privilege", "cat > notes.md <<'EOF'\n" + word + " id\nEOF", Allow},
		{"data merge", "cat <<'EOF'\ngh pr merge 1 --admin\nEOF", Allow},
		{"command", word + " id", Deny},
		{"chain", "true && " + word + " id", Deny},
		{"environment", "env FOO=1 " + word + " id", Deny},
		{"expansion", "cat <<EOF\ntext $(" + word + " id)\nEOF", Deny},
		{"shell", "bash <<'EOF'\n" + word + " id\nEOF", Deny},
		{"pipe", "cat <<'EOF' | sh\n" + word + " id\nEOF", Deny},
		{"after", "cat <<'EOF'\ndata\nEOF\n" + word + " id", Deny},
		{"quoted backslash before delimiter", "cat <<'EOF'\ndata\\\nEOF\n" + word + " id", Deny},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.Command(tc.command); got.Outcome != tc.want {
				t.Fatalf("got %v; want %v", got.Outcome, tc.want)
			}
		})
	}
}

// TestPolicyHeredocFixtures checks the privilege verdict on the shared fixtures.
func TestPolicyHeredocFixtures(t *testing.T) {
	data, err := os.ReadFile("../hiss/testdata/heredoc_mask_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, Input string
		Masked      bool
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	p := policy(t)
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			want := Deny
			if tc.Masked {
				want = Allow
			}
			if got := p.textVerdict(tc.Input); got.Outcome != want {
				t.Errorf("got %v want %v", got.Outcome, want)
			}
		})
	}
}

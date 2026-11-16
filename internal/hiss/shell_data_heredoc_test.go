// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestMaskDataHeredocs checks data, executable, and unfinished bodies.
func TestMaskDataHeredocs(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"quoted", "cat <<'EOF'\nsecret\nEOF", "cat <<'EOF'\n\nEOF"},
		{"tabs", "cat <<-'EOF'\n\tsecret\n\tEOF", "cat <<-'EOF'\n\n\tEOF"},
		{"shell", "bash <<'EOF'\ncommand\nEOF", "bash <<'EOF'\ncommand\nEOF"},
		{"pipe", "cat <<'EOF' | sh\ncommand\nEOF", "cat <<'EOF' | sh\ncommand\nEOF"},
		{"tee", "tee file <<'EOF'\nsecret\nEOF", "tee file <<'EOF'\n\nEOF"},
		{"two", "cat <<'A' <<'B'\none\nA\ntwo\nB", "cat <<'A' <<'B'\n\nA\n\nB"},
		{"unfinished", "cat <<'EOF'\nsecret", "cat <<'EOF'\n"},
		{"same delimiters", "cat <<'A' <<'A'\none\nA\ntwo\nA", "cat <<'A' <<'A'\n\nA\n\nA"},
		{"env wrapper", "env X=1 cat <<'EOF'\nsecret\nEOF", "env X=1 cat <<'EOF'\n\nEOF"},
		{"chain", "true && cat <<'EOF' && true\nsecret\nEOF", "true && cat <<'EOF' && true\nsecret\nEOF"},
		{"mixed", "cat <<'A'; bash <<'B'\none\nA\ntwo\nB", "cat <<'A'; bash <<'B'\none\nA\ntwo\nB"},
		{"process substitution", "cat <<'EOF' > >(sh)\ncommand\nEOF", "cat <<'EOF' > >(sh)\ncommand\nEOF"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := MaskDataHeredocs(tc.input, 20)
			if got != tc.want || strings.Count(got, "\n") != strings.Count(tc.input, "\n") {
				t.Fatalf("got %q; want %q", got, tc.want)
			}
		})
	}
}

// TestMaskDataHeredocsUnreadable keeps ambiguous input unchanged.
func TestMaskDataHeredocsUnreadable(t *testing.T) {
	for _, input := range []string{"cat <<EOF\ntext $(id) end\nEOF", "cat <<EOF\ntext `id` end\nEOF", "bash \\\ncat <<'EOF'\nx\nEOF", "cat <<EOF\nx\\\nEOF", "cat <<EOF\r\nx\r\nEOF\r\n"} {
		if got, ok := MaskDataHeredocs(input, 20); ok || got != input {
			t.Fatalf("got %q, %v for %q", got, ok, input)
		}
	}
	input := "cat <<'EOF'\nx\nEOF"
	if got, ok := MaskDataHeredocs(input, 2); ok || got != input {
		t.Fatalf("line bound: got %q, %v", got, ok)
	}
}

// TestMaskDataHeredocFixtures replays the shared masking decisions.
func TestMaskDataHeredocFixtures(t *testing.T) {
	data, err := os.ReadFile("testdata/heredoc_mask_cases.json")
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
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			got, _ := MaskDataHeredocs(tc.Input, 100)
			masked := !strings.Contains(got, "sudo id")
			if masked != tc.Masked {
				t.Errorf("masked=%v want=%v", masked, tc.Masked)
			}
		})
	}
}

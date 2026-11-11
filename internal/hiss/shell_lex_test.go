// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"strings"
	"testing"
)

// TestSplitHereDocs checks body boundaries, targets and reader limits.
func TestSplitHereDocs(t *testing.T) {
	code, docs, err := SplitHereDocs("cat <<A > '$D/body'\nA \nreviewer's\nA\ntee other <<-B\n\ttwo\n\tB\n")
	if err != nil || len(docs) != 2 {
		t.Fatalf("split: %q %#v %v", code, docs, err)
	}
	if docs[0].Text != "A \nreviewer's\n" || docs[0].Target != "'$D/body'" || docs[1].Text != "\ttwo\n" || docs[1].Target != "other" {
		t.Fatalf("bodies: %#v", docs)
	}
	if strings.Contains(code, "reviewer") || strings.Contains(code, "\ttwo") {
		t.Fatalf("data body present in code: %q", code)
	}
}

// TestSplitHereDocsBodyQuotes checks data removal and verbatim code bodies.
func TestSplitHereDocsBodyQuotes(t *testing.T) {
	code, docs, err := SplitHereDocs("cat <<A\nit's \"quoted\" `x` \\y $(z)\nA\nbash <<B\necho \"quoted\"\nB\n")
	if err != nil || len(docs) != 2 || strings.Contains(code, "quoted   x") || !strings.Contains(code, `echo "quoted"`) {
		t.Fatalf("body transformation: %q %#v %v", code, docs, err)
	}
}

// TestSplitHereDocsBounds checks paired targets and rejects inputs over reader bounds.
func TestSplitHereDocsBounds(t *testing.T) {
	_, paired, err := SplitHereDocs("cat > first <<A; tee second <<B\none\nA\ntwo\nB\n")
	if err != nil || len(paired) != 2 || paired[0].Target != "first" || paired[1].Target != "second" {
		t.Fatalf("paired targets: %#v, %v", paired, err)
	}
	for _, input := range []string{strings.Repeat("x", 1<<19) + "\n" + strings.Repeat("x", 1<<19), "cat <<EOF\nmissing\n", strings.Repeat("cat <<X\na\nX\n", maxShellHeredocs+1)} {
		if _, _, err := SplitHereDocs(input); err == nil {
			t.Errorf("accepted invalid input of %d bytes", len(input))
		}
	}
}

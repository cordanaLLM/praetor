package agentcontext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadOnlyProjection_Positive(t *testing.T) {
	agentsMdPath := filepath.Join("..", "..", "AGENTS.md")
	content, err := os.ReadFile(agentsMdPath)
	if err != nil {
		t.Fatalf("failed to read AGENTS.md: %v", err)
	}

	got, err := ReadOnlyProjection(string(content))
	if err != nil {
		t.Fatalf("ReadOnlyProjection failed on AGENTS.md: %v", err)
	}

	// Banner must be present at top
	if !strings.Contains(got, ReadOnlyBanner) {
		t.Errorf("read-only banner missing from output")
	}

	// Verify mutating commands and turn-end steps are dropped
	forbidden := []string{
		"Before concluding any turn:",
		"make verify-all",
		"state sync",
		"state task add",
		"state task complete",
		"state task archive",
		"agent-checkpoint-tool",
		"agent-checkpoint-stop",
		"commit with sign-off",
	}
	for _, term := range forbidden {
		if strings.Contains(got, term) {
			t.Errorf("read-only projection contains forbidden mutating term %q", term)
		}
	}

	// Non-mutating rules must be preserved
	required := []string{
		"## Core Directives & Invariants",
		"**HISS-01** control flow",
		"**HISS-02** loops, I/O",
		"**HISS-04** complexity",
		"## Operational Rules",
		"1. **Act on verified state.**",
		"10. **State ledger discipline (HISS-17).**",
		"## Text Register",
		"## Primary Verification Commands",
		"go test -v -race ./...",
	}
	for _, req := range required {
		if !strings.Contains(got, req) {
			t.Errorf("read-only projection missing required preserved rule %q", req)
		}
	}

	// Verify HISS-17 row is replaced
	if !strings.Contains(got, "read-only: no ledger mutation") {
		t.Errorf("read-only projection missing HISS-17 read-only replacement")
	}

	// Verify Rule 2 runs inside verification gate
	if !strings.Contains(got, "runs inside verification gate") {
		t.Errorf("read-only projection missing Rule 2 gate replacement")
	}
}

func TestReadOnlyProjection_Negative(t *testing.T) {
	for name, input := range map[string]string{
		"empty":             "",
		"whitespace":        "   \n\t  \n  ",
		"mutating survivor": "# Harness\n\nSome unhandled command `state sync`\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ReadOnlyProjection(input)
			if err == nil {
				t.Fatalf("expected error for input %s, got:\n%s", name, got)
			}
		})
	}
}

func TestReadOnlyProjection_Boundary(t *testing.T) {
	// Boundary: minimal heading only
	minHeading := "# Single Heading\n"
	got, err := ReadOnlyProjection(minHeading)
	if err != nil {
		t.Fatalf("minimal heading failed: %v", err)
	}
	if !strings.Contains(got, "# Single Heading\n\n"+ReadOnlyBanner) {
		t.Fatalf("banner not placed after heading: %q", got)
	}

	// Boundary: document without H1 heading
	noH1 := "Just some plain text without top heading.\n"
	got, err = ReadOnlyProjection(noH1)
	if err != nil {
		t.Fatalf("no H1 failed: %v", err)
	}
	if !strings.HasPrefix(got, ReadOnlyBanner) {
		t.Fatalf("banner not prepended when no H1: %q", got)
	}

	// Boundary: CRLF line endings
	crlf := "# Heading\r\nSome body line.\r\n"
	got, err = ReadOnlyProjection(crlf)
	if err != nil {
		t.Fatalf("CRLF input failed: %v", err)
	}
	if strings.Contains(got, "\r") {
		t.Fatalf("CRLF carriage returns not normalized to LF")
	}
}

func TestIsReadOnlyRole_3D(t *testing.T) {
	// Positive
	positives := []string{
		"research",
		"researcher",
		"review",
		"reviewer",
		"audit",
		"auditor",
		"praetor-auditor",
		"read-only",
		"readonly",
		"Codebase Reviewer",
		"security-audit",
		"deep-research-agent",
	}
	for _, r := range positives {
		if !IsReadOnlyRole(r) {
			t.Errorf("IsReadOnlyRole(%q) = false, want true", r)
		}
	}

	// Negative
	negatives := []string{
		"",
		"developer",
		"implementer",
		"gatekeeper",
		"praetor-gatekeeper",
		"praetor-packager",
		"praetor-dogfooder",
		"praetor-fuzzer",
		"praetor-needs-miner",
	}
	for _, r := range negatives {
		if IsReadOnlyRole(r) {
			t.Errorf("IsReadOnlyRole(%q) = true, want false", r)
		}
	}

	// Boundary
	boundaries := map[string]bool{
		"   ":          false,
		"  AUDITOR  ":  true,
		"  Review  ":   true,
		" \t audit \n": true,
		"praetor":      false,
	}
	for r, want := range boundaries {
		if got := IsReadOnlyRole(r); got != want {
			t.Errorf("IsReadOnlyRole(%q) = %v, want %v", r, got, want)
		}
	}
}

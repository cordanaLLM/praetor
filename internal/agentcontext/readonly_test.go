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

	// The HISS-17 row keeps its read-only clause and its own enforcement columns; the task and
	// turn-end clauses are dropped.
	if !strings.Contains(got, "never whole `.workingdir/STATE.md` | pre-commit / CI | gate |") {
		t.Errorf("read-only projection lost the HISS-17 read clause or its enforcement")
	}

	// Rule 2 describes the gate by name, without its command.
	if !strings.Contains(got, "runs inside verification gate") {
		t.Errorf("read-only projection missing Rule 2 gate replacement")
	}

	// Primary Verification Commands loses the gate line with its comment, nothing more.
	if strings.Contains(got, "\nverification gate\n") || strings.Contains(got, "# all format, lint, security gates") {
		t.Errorf("Primary Verification Commands contains bare verification gate or dangling comment")
	}
	if !strings.Contains(got, "go run ./cmd/standardsctl topology audit \"${PRAETOR_DEV_ROOT:-$HOME/dev}\"\n```") {
		t.Errorf("Primary Verification Commands fence lost its last kept command or closing line")
	}
}

func TestReadOnlyProjection_AdopterHarness(t *testing.T) {
	input := strings.Join([]string{
		"# Adopter Harness",
		"",
		"| **HISS-17** state ledger | turn start `praetorctl state status`; turn end `praetorctl state sync .` | not enforced | advisory |",
		"",
		"## Operational Rules",
		"",
		"1. **Act on verified state.**",
		"- end: run `praetorctl state sync .`",
		"",
		"## Primary Verification Commands",
		"",
		"```bash",
		"# Repository gate; steps live in Makefile",
		"make verify-all",
		"```",
	}, "\n")

	got, err := ReadOnlyProjection(input)
	if err != nil {
		t.Fatalf("ReadOnlyProjection failed on adopter harness: %v", err)
	}

	// Adopter row must retain 'not enforced | advisory |'
	if !strings.Contains(got, "| not enforced | advisory |") {
		t.Errorf("adopter row enforcement/failure mode altered: %s", got)
	}
	if strings.Contains(got, "| pre-commit / CI | gate |") {
		t.Errorf("adopter row falsely gained praetor enforcement: %s", got)
	}
	// '- end: run `praetorctl state sync .`' must be dropped
	if strings.Contains(got, "state sync") {
		t.Errorf("mutating command survived in adopter projection: %s", got)
	}
	// 'make verify-all' and comment must be dropped, not replaced by bare 'verification gate'
	if strings.Contains(got, "verification gate") {
		t.Errorf("Primary Verification Commands contains bare verification gate: %s", got)
	}
}

func TestAssertReadOnly_GitGuards(t *testing.T) {
	for _, cmd := range []string{
		"git commit -s",
		"git commit",
		"git push",
		"git push origin main",
		"git add .",
		"git add -A",
		"praetorctl state sync .",
		"praetorctl state task add x",
		"lefthook run agent-checkpoint-stop",
		"make verify-all",
	} {
		if err := assertReadOnly("some text with " + cmd + " included"); err == nil {
			t.Errorf("assertReadOnly(%q) expected error, got nil", cmd)
		}
		if err := assertReadOnly("```bash\n" + cmd + "\n```\n"); err == nil {
			t.Errorf("assertReadOnly(fenced %q) expected error, got nil", cmd)
		}
		if err := assertReadOnly("| a | run " + cmd + " | never " + cmd + " |\n"); err == nil {
			t.Errorf("assertReadOnly(table %q) expected error, got nil", cmd)
		}
		if err := assertReadOnly("Never run " + cmd + " here.\n"); err != nil {
			t.Errorf("assertReadOnly(prohibition %q) = %v, want nil", cmd, err)
		}
	}
	for _, readOnly := range []string{"praetorctl state task list", "state synchronization", "praetorctl state status"} {
		if err := assertReadOnly("run " + readOnly + "\n"); err != nil {
			t.Errorf("assertReadOnly(%q) = %v, want nil", readOnly, err)
		}
	}
}

func TestReadOnlyProjection_Negative(t *testing.T) {
	for name, input := range map[string]string{
		"empty":      "",
		"whitespace": "   \n\t  \n  ",
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

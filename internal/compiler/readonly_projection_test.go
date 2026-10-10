package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
)

func TestCompileReadOnlyContext_Positive(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "AGENTS.md")
	content := "# Project Harness\n\nBefore concluding any turn:\n```bash\nmake verify-all\n```\n\n## Core Directives & Invariants\n\nRule 1.\n\n## Operational Rules\n\n10. **State ledger discipline (HISS-17).** Agents MUST maintain local `.workingdir` ledger every turn.\n    - Turn end: `praetorctl state sync .`\n\n11. **Diff-aware CI efficiency (HISS-18).**\n\n## Primary Verification Commands\n```bash\ngo test ./...\nmake verify-all\n```\n"
	if err := os.WriteFile(source, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write AGENTS.md: %v", err)
	}

	if err := CompileReadOnlyContext(t.Context(), source, dir); err != nil {
		t.Fatalf("CompileReadOnlyContext failed: %v", err)
	}

	// Verify file was written
	roPath := filepath.Join(dir, ReadOnlyFile)
	data, err := os.ReadFile(roPath)
	if err != nil {
		t.Fatalf("failed to read %s: %v", ReadOnlyFile, err)
	}

	text := string(data)
	if !strings.Contains(text, agentcontext.ReadOnlyBanner) {
		t.Errorf("read-only banner missing from %s", ReadOnlyFile)
	}
	if strings.Contains(text, "make verify-all") {
		t.Errorf("make verify-all still present in %s", ReadOnlyFile)
	}
	if strings.Contains(text, "state sync") {
		t.Errorf("state sync still present in %s", ReadOnlyFile)
	}

	// Verify VerifyReadOnlyContext passes
	if err := VerifyReadOnlyContext(t.Context(), source, dir); err != nil {
		t.Fatalf("VerifyReadOnlyContext failed: %v", err)
	}
}

func TestVerifyReadOnlyContext_Negative(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "AGENTS.md")
	content := "# Project Harness\n\nRule 1.\n"
	if err := os.WriteFile(source, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write AGENTS.md: %v", err)
	}

	// Negative 1: file missing
	if err := VerifyReadOnlyContext(t.Context(), source, dir); err == nil {
		t.Fatalf("expected error when %s is missing", ReadOnlyFile)
	}

	// Compile it first
	if err := CompileReadOnlyContext(t.Context(), source, dir); err != nil {
		t.Fatalf("CompileReadOnlyContext failed: %v", err)
	}

	// Negative 2: drift / out of sync
	roPath := filepath.Join(dir, ReadOnlyFile)
	if err := os.WriteFile(roPath, []byte("tampered content\n"), 0644); err != nil {
		t.Fatalf("failed to tamper %s: %v", ReadOnlyFile, err)
	}
	if err := VerifyReadOnlyContext(t.Context(), source, dir); err == nil {
		t.Fatalf("expected error when %s is out of sync", ReadOnlyFile)
	}
}

func TestContextForBrief_3D(t *testing.T) {
	agentsMdPath := filepath.Join("..", "..", "AGENTS.md")
	contentBytes, err := os.ReadFile(agentsMdPath)
	if err != nil {
		t.Fatalf("failed to read AGENTS.md: %v", err)
	}
	fullContent := string(contentBytes)

	// Positive: brief marked readonly: true gets read-only projection
	briefReadOnly := "goal: research architecture\ninputs: internal/\nreturn: report\nevidence: none\ntask: research\nreadonly: true\n"
	got, err := ContextForBrief(fullContent, briefReadOnly)
	if err != nil {
		t.Fatalf("ContextForBrief failed: %v", err)
	}
	if !strings.Contains(got, agentcontext.ReadOnlyBanner) {
		t.Errorf("read-only banner missing from ContextForBrief result")
	}
	if strings.Contains(got, "make verify-all") {
		t.Errorf("mutating command make verify-all present in read-only brief context")
	}

	// Positive: brief with readonly: false gets full projection unchanged
	briefReadWrite := "goal: fix bug\ninputs: internal/\nreturn: diff\nevidence: none\ntask: bug_fix\nreadonly: false\n"
	got, err = ContextForBrief(fullContent, briefReadWrite)
	if err != nil {
		t.Fatalf("ContextForBrief failed: %v", err)
	}
	if got != fullContent {
		t.Errorf("brief with readonly: false must get full content unchanged")
	}

	// Boundary: brief without readonly field gets full projection unchanged
	briefDefault := "goal: fix bug\ninputs: internal/\nreturn: diff\nevidence: none\ntask: bug_fix\n"
	got, err = ContextForBrief(fullContent, briefDefault)
	if err != nil {
		t.Fatalf("ContextForBrief failed: %v", err)
	}
	if got != fullContent {
		t.Errorf("brief without readonly field must get full content unchanged")
	}

	// Negative: invalid readonly value returns error
	briefInvalid := "goal: fix bug\ninputs: internal/\nreturn: diff\nevidence: none\ntask: bug_fix\nreadonly: maybe\n"
	if _, err := ContextForBrief(fullContent, briefInvalid); err == nil {
		t.Fatalf("expected error on invalid brief readonly value")
	}
}

func TestContextForRole_3D(t *testing.T) {
	content := "# Harness\n\nBefore concluding any turn:\n```bash\nmake verify-all\n```\n\n## Rules\n\nRule 1.\n"

	// Positive: read-only role gets read-only projection
	for _, role := range []string{"research", "praetor-auditor", "code-reviewer"} {
		got, err := ContextForRole(content, role)
		if err != nil {
			t.Fatalf("ContextForRole(%q) failed: %v", role, err)
		}
		if !strings.Contains(got, agentcontext.ReadOnlyBanner) {
			t.Errorf("ContextForRole(%q) missing read-only banner", role)
		}
		if strings.Contains(got, "make verify-all") {
			t.Errorf("ContextForRole(%q) still contains make verify-all", role)
		}
	}

	// Negative: non-read-only role gets full projection
	for _, role := range []string{"gatekeeper", "praetor-fuzzer", "packager"} {
		got, err := ContextForRole(content, role)
		if err != nil {
			t.Fatalf("ContextForRole(%q) failed: %v", role, err)
		}
		if got != content {
			t.Errorf("ContextForRole(%q) must return full content unchanged", role)
		}
	}

	// Boundary: empty role gets full content
	got, err := ContextForRole(content, "")
	if err != nil {
		t.Fatalf("ContextForRole(\"\") failed: %v", err)
	}
	if got != content {
		t.Errorf("ContextForRole(\"\") must return full content unchanged")
	}
}

// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package compiler

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
)

func fullContentProjection(path string) bool {
	for _, target := range agentcontext.AllVendorTargets() {
		if target.Path == path {
			return target.FullContent
		}
	}
	return false
}

// TestVerifyRejectsCopiedCanonical plants the old copy form without using the renderer.
func TestVerifyRejectsCopiedCanonical(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "AGENTS.md")
	canonical := "# Harness\n- Shared policy must stay canonical.\n"
	writeCanonicalFixture(t, source, canonical)
	tr := NewTranspiler()
	for _, rel := range agentcontext.VendorTargetPaths() {
		frame := strings.Split(agentcontext.ProjectionFrame(rel), "\n@")[0]
		writeOutputFixture(t, filepath.Join(root, rel), strings.TrimRight(frame, "\n")+"\n\n"+canonical)
	}
	if err := tr.Verify(source, root); err == nil {
		t.Fatal("verification accepted the old copy form with a shared AGENTS.md bullet")
	}
}

// TestVerifyPointerProjections accepts pointers and refuses duplication or a missing import.
func TestVerifyPointerProjections(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "AGENTS.md")
	canonical := "# Harness\n- Shared policy must stay canonical.\n\n## Claude Code\n- Client only.\n"
	writeCanonicalFixture(t, source, canonical)
	tr := NewTranspiler()
	result, err := tr.Compile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.WriteOutputs(result, root); err != nil {
		t.Fatal(err)
	}
	if err := tr.Verify(source, root); err != nil {
		t.Fatal(err)
	}
	for _, file := range result.Files {
		name := filepath.Join(root, file.RelativePath)
		writeOutputFixture(t, name, file.Content+"- Shared policy must stay canonical.\n")
		if err := tr.Verify(source, root); err == nil || !strings.Contains(err.Error(), file.RelativePath) {
			t.Fatalf("%s: duplicate accepted: %v", file.RelativePath, err)
		}
		writeOutputFixture(t, name, file.Content)
		verifyMissingPointerImport(t, tr, source, root, file)
	}
}

// verifyMissingPointerImport plants a missing required import in an otherwise valid projection.
func verifyMissingPointerImport(t *testing.T, tr *Transpiler, source, root string, file TargetFile) {
	t.Helper()
	if !strings.Contains(file.Content, "\n@") {
		return
	}
	lines := strings.Split(file.Content, "\n")
	for i := range lines {
		if strings.HasPrefix(lines[i], "@") {
			lines[i] = ""
		}
	}
	name := filepath.Join(root, file.RelativePath)
	writeOutputFixture(t, name, strings.Join(lines, "\n"))
	if err := tr.Verify(source, root); err == nil {
		t.Fatalf("%s: missing import accepted", file.RelativePath)
	}
	writeOutputFixture(t, name, file.Content)
}

// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package agentcontext

import (
	"strings"
	"testing"
)

// TestProjectionLoadingContract pins policy delivery per client independently of registry flags.
func TestProjectionLoadingContract(t *testing.T) {
	const policy = "- Planted shared instruction."
	result, err := NewTranspiler().CompileContent("# Harness\n" + policy + "\n")
	if err != nil {
		t.Fatal(err)
	}
	for i, target := range vendorTargets {
		if got, want := strings.Contains(result.Files[i].Content, policy), target.client == "copilot"; got != want {
			t.Errorf("%s: shared instruction present=%v, want %v", target.client, got, want)
		}
	}
}

// TestCopilotLoadedLineBudget counts its emitted policy once, without loading AGENTS.md again.
func TestCopilotLoadedLineBudget(t *testing.T) {
	tr := NewTranspiler()
	tr.Clients = []string{"copilot"}
	source := "# Harness\n- Shared.\n\n## GitHub Copilot\n- Copilot only.\n\n## Claude Code\n- Other client.\n"
	result, err := tr.CompileContent(source)
	if err != nil {
		t.Fatal(err)
	}
	file := result.Files[0]
	if !strings.Contains(file.Content, "- Shared.") || !strings.Contains(file.Content, "- Copilot only.") || strings.Contains(file.Content, "Other client") {
		t.Fatalf("Copilot policy selection: %q", file.Content)
	}
	tr.MaxLines = file.LineCount
	if _, err := tr.CompileContent(source); err != nil {
		t.Fatalf("exact emitted line cap: %v", err)
	}
	tr.MaxLines--
	_, err = tr.CompileContent(source)
	assertImportError(t, err, ".github/copilot-instructions.md", "max line budget")
}

// TestPointerProjections keeps shared policy exclusively in the canonical file.
func TestPointerProjections(t *testing.T) {
	source := "# Harness\n- Shared policy must stay canonical.\n"
	for _, target := range vendorTargets {
		source += "\n## " + target.section + "\n- Only " + target.section + ".\n"
	}
	result, err := NewTranspiler().CompileContent(source)
	if err != nil {
		t.Fatal(err)
	}
	for i, file := range result.Files {
		want := testFrontmatterFor(file.RelativePath) + testHeader
		if vendorTargets[i].fullContent {
			want += "# Harness\n- Shared policy must stay canonical.\n\n"
		}
		switch file.RelativePath {
		case "CLAUDE.md":
			want += "@AGENTS.md\n\n"
		case ".gemini/GEMINI.md":
			want += "@../AGENTS.md\n\n"
		}
		want += "## " + vendorTargets[i].section + "\n- Only " + vendorTargets[i].section + ".\n"
		if file.Content != want {
			t.Errorf("%s: got %q, want %q", file.RelativePath, file.Content, want)
		}
		if strings.Contains(file.Content, "Shared policy") != vendorTargets[i].fullContent {
			t.Errorf("%s repeats canonical policy", file.RelativePath)
		}
	}
}

// TestProjectionFrameExcludesGeneratedImportsFromAdoption pins the complete generated frame.
func TestProjectionFrameExcludesGeneratedImportsFromAdoption(t *testing.T) {
	for _, target := range vendorTargets {
		frame := ProjectionFrame(target.path)
		ref := testReferenceFor(target.path)
		if ref != "" && !strings.Contains(frame, ref) {
			t.Errorf("%s: adoption frame omits canonical reference %q", target.path, ref)
		}
		if strings.Contains(frame, "## "+target.section) {
			t.Errorf("%s: adoption frame would drop client-only text", target.path)
		}
	}
}

// testReferenceFor pins the externally visible import syntax independently of the renderer.
func testReferenceFor(path string) string {
	switch path {
	case "CLAUDE.md":
		return "@AGENTS.md\n"
	case ".gemini/GEMINI.md":
		return "@../AGENTS.md\n"
	}
	return ""
}

// vendorSections puts body in each client's own section for projection-specific fixtures.
func vendorSections(body string) string {
	var text strings.Builder
	for _, target := range vendorTargets {
		text.WriteString("## " + target.section + "\n" + body + "\n")
	}
	return text.String()
}

// TestUnknownClientPointer uses the sentence fallback when no loading mechanism is documented.
func TestUnknownClientPointer(t *testing.T) {
	tr := NewTranspiler().withCanonical("policy\n")
	target := vendorTarget{path: "UNKNOWN.md", section: "Unknown"}
	files, err := tr.renderTargets([]vendorTarget{target}, func(vendorTarget) string { return "" })
	want := generatedHeader + "Read AGENTS.md for canonical agent instructions.\n"
	if err != nil || len(files) != 1 || files[0].Content != want {
		t.Fatalf("fallback: files=%v error=%v", files, err)
	}
}

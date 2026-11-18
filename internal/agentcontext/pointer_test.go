// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package agentcontext

import (
	"strings"
	"testing"
)

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
		switch file.RelativePath {
		case "CLAUDE.md":
			want += "@AGENTS.md\n\n"
		case ".gemini/GEMINI.md":
			want += "@../AGENTS.md\n\n"
		case ".github/copilot-instructions.md":
			want += "Read AGENTS.md for canonical agent instructions.\n\n"
		}
		want += "## " + vendorTargets[i].section + "\n- Only " + vendorTargets[i].section + ".\n"
		if file.Content != want {
			t.Errorf("%s: got %q, want %q", file.RelativePath, file.Content, want)
		}
		if strings.Contains(file.Content, "Shared policy") {
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
	case ".github/copilot-instructions.md":
		return "Read AGENTS.md for canonical agent instructions.\n"
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
	files, err := tr.renderTargets([]vendorTarget{target}, func(string) string { return "" })
	want := generatedHeader + "Read AGENTS.md for canonical agent instructions.\n"
	if err != nil || len(files) != 1 || files[0].Content != want {
		t.Fatalf("fallback: files=%v error=%v", files, err)
	}
}

package editor

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/templates"
)

// The Visual Studio target and the native-gpu-systems flavor used to carry two different
// .clang-tidy bodies, one a Go literal and one an unread template. Both now render
// templates/native/.clang-tidy.tmpl, so the file an adopter gets does not depend on which
// command wrote it.
func TestVisualStudioClangTidy_Positive_IsTheShippedTemplate(t *testing.T) {
	opts := DefaultOptions()
	opts.Editors = []string{EditorUniversal, EditorVisualStudio}
	set := mustSynthesize(t, opts)
	want, err := templates.RenderFile(clangTidyTemplate, templates.Context{})
	if err != nil {
		t.Fatalf("render the shipped template: %v", err)
	}
	if got := fileContent(t, set, ".clang-tidy"); got != want {
		t.Fatalf("Visual Studio .clang-tidy differs from %s:\n%s", clangTidyTemplate, got)
	}
	// Boundary: the Visual Studio file stays last, where the generator table put it.
	if last := set.Files[len(set.Files)-1]; last.Path != ".clang-tidy" || last.Editor != EditorVisualStudio {
		t.Fatalf("Visual Studio file moved in the generation order: %+v", last)
	}
}

// Negative: a target without Visual Studio receives no .clang-tidy, and the template's
// maintainer note never reaches the rendered body.
func TestVisualStudioClangTidy_Negative_OnlyForItsTarget(t *testing.T) {
	opts := DefaultOptions()
	opts.Editors = []string{EditorUniversal}
	for _, file := range mustSynthesize(t, opts).Files {
		if file.Path == ".clang-tidy" {
			t.Fatalf("a target without Visual Studio received %s", file.Path)
		}
	}
	body, err := generateVisualStudio()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if content := body[0].Content; strings.Contains(content, "Scaffolded as .clang-tidy") || !strings.Contains(content, "bugprone-*") {
		t.Fatalf("rendered .clang-tidy is not the configuration alone:\n%s", content)
	}
}

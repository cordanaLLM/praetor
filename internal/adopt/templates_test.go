package adopt

import (
	"strings"
	"testing"
)

func TestRenderTemplate_Positive(t *testing.T) {
	tmplStr := "# {{ .Owner }}/{{ .RepoName }}\nArchetype: {{ .Archetype }}\nRunner: {{ .RunnerTag }}"
	ctx := TemplateContext{
		RepoName:  "praetor",
		Owner:     "cordanaLLM",
		Archetype: "framework",
		RunnerTag: "arc-runner-set-linux-amd64",
	}

	res, err := RenderTemplate("test", tmplStr, ctx)
	if err != nil {
		t.Fatalf("expected template render to succeed: %v", err)
	}

	if !strings.Contains(res, "# cordanaLLM/praetor") {
		t.Errorf("expected rendered owner/repo, got: %s", res)
	}
	if !strings.Contains(res, "Runner: arc-runner-set-linux-amd64") {
		t.Errorf("expected rendered runner tag, got: %s", res)
	}
}

func TestRenderTemplate_Negative_And_Boundary(t *testing.T) {
	ctx := TemplateContext{RepoName: "praetor"}

	// Negative: syntax error in template
	_, err := RenderTemplate("invalid", "{{ .RepoName ", ctx)
	if err == nil {
		t.Error("expected syntax error on unclosed action, got nil")
	}

	// Negative: field does not exist or unclosed block
	_, execErr := RenderTemplate("missing", "{{ range .UnknownField }}{{ end }}", ctx)
	if execErr == nil {
		t.Error("expected execution error on missing field, got nil")
	}

	// Boundary: empty template
	_, emptyErr := RenderTemplate("empty", "", ctx)
	if emptyErr == nil {
		t.Error("expected error on empty template string, got nil")
	}
}

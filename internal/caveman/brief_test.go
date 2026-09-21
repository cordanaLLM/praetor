package caveman

import (
	"strings"
	"testing"
)

func TestExtractBriefTaskPositive(t *testing.T) {
	brief := "goal: patch hook\ninputs: event.go\nreturn: diff\nevidence: go test\n- task: feature_implementation\n"
	got, err := ExtractBriefTask(brief)
	if err != nil || got != "feature_implementation" {
		t.Fatalf("task = %q, %v", got, err)
	}
}

func TestExtractBriefTaskNegative(t *testing.T) {
	for name, brief := range map[string]string{
		"missing":   "goal: patch hook\ninputs: event.go\n",
		"empty":     "task:   \n",
		"duplicate": "task: ci_debugging\ntask: unit_test_suites\n",
		"code only": "```text\ntask: ci_debugging\n```\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := ExtractBriefTask(brief); err == nil || got != "" {
				t.Fatalf("task = %q, %v", got, err)
			}
		})
	}
}

func TestExtractBriefTaskBoundary(t *testing.T) {
	task := strings.Repeat("x", 256)
	got, err := ExtractBriefTask("task: " + task + "\n")
	if err != nil || got != task {
		t.Fatalf("task length = %d, %v", len(got), err)
	}
}

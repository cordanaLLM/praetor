package adopt_test

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
)

func TestNeutralText_Positive(t *testing.T) {
	harness := adopt.BuildAgentHarnessDirectives()
	if strings.Contains(harness, "cordana-standards") || strings.Contains(harness, "[bot]") {
		t.Errorf("Harness directives contain bot login: %q", harness)
	}
	if !strings.Contains(harness, "CI re-checks every pull request in an isolated runner") {
		t.Errorf("Harness directives missing the neutral CI sentence")
	}

	labels := adopt.DefaultLabelsYAML()
	if strings.Contains(labels, "cordana-standards") || strings.Contains(labels, "[bot]") {
		t.Errorf("Labels YAML contains bot login: %q", labels)
	}
	if !strings.Contains(labels, adopt.SyncLabelDescription) {
		t.Errorf("Labels YAML missing the SyncLabelDescription")
	}
	if strings.Count(labels, "- name:") != 8 {
		t.Errorf("Labels YAML should contain exactly 8 labels")
	}
}

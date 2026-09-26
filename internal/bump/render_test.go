package bump

import (
	"strings"
	"testing"
)

func TestFormatActionsInventory(t *testing.T) {
	actions := []ActionCandidate{
		{WorkflowFile: ".github/workflows/ci.yml", Action: "actions/checkout", CurrentVersion: "v4", LatestVersion: "v5"},
		{WorkflowFile: ".github/workflows/ci.yml", Action: "actions/setup-go", CurrentVersion: "v5", LatestVersion: "v5"},
		{WorkflowFile: ".github/workflows/old.yml", Action: "actions/upload-artifact", CurrentVersion: "v3", LatestVersion: "v3", Deprecated: true},
	}
	got := FormatActionsInventory(actions)

	// Positive: every action is listed with its drift status and workflow file.
	for _, want := range []string{
		"GitHub Actions Inventory:",
		"[DRIFT]", "actions/checkout", "v4 -> v5 (.github/workflows/ci.yml)",
		"[UP-TO-DATE]", "actions/setup-go",
		"[DEPRECATED]", "actions/upload-artifact", "(.github/workflows/old.yml)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("inventory lacks %q:\n%s", want, got)
		}
	}
	// Negative: deprecation outranks an equal version; it is never reported up to date.
	if status := ActionDriftStatus(actions[2]); status != "[DEPRECATED]" {
		t.Fatalf("deprecated action status = %s", status)
	}
	// Boundary: no actions renders nothing, not an empty heading.
	if out := FormatActionsInventory(nil); out != "" {
		t.Fatalf("empty inventory rendered %q", out)
	}
}

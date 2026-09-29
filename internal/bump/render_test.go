package bump

import (
	"strings"
	"testing"
)

func TestFormatActionsInventory(t *testing.T) {
	actions := []ActionCandidate{
		{WorkflowFile: ".github/workflows/ci.yml", Action: "actions/checkout", CurrentVersion: "v4", LatestVersion: "v5"},
		{WorkflowFile: ".github/workflows/ci.yml", Action: "actions/setup-go", CurrentVersion: "v5", LatestVersion: "v5", UpToDate: true},
		{WorkflowFile: ".github/workflows/old.yml", Action: "actions/upload-artifact", CurrentVersion: "v3", LatestVersion: "v3", UpToDate: true, Deprecated: true},
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
	// Boundary: the status follows the SemVer comparison in UpToDate, not the spelling, so a
	// major-only pin that matches a full latest version is current.
	semverEqual := ActionCandidate{Action: "actions/cache", CurrentVersion: "v5", LatestVersion: "v5.0.0", UpToDate: true}
	if status := ActionDriftStatus(semverEqual); status != "[UP-TO-DATE]" {
		t.Fatalf("SemVer-equal pin status = %s, want [UP-TO-DATE]", status)
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

// Positive, negative and boundary for SHA pins (#609, #610): a refuted pin is a bad pin even
// when its version is current or deprecated, an unverified or unversioned pin is never up to
// date, a verified pin reads by its version, rows name file and line, and each distinct
// reason a pin went unverified is printed once with its count.
func TestFormatActionsInventoryPinStatuses(t *testing.T) {
	pin := func(status PinStatus, detail string) ActionCandidate {
		return ActionCandidate{
			WorkflowFile: "ci.yml", Line: 7, Action: "example/action", CurrentVersion: "v1.0.0", LatestVersion: "v1",
			UpToDate: true, PinnedSHA: "0dceb95e7c4cad8cc7422aee3885998f5cab9c79", Pin: status, PinDetail: detail,
		}
	}
	deprecatedMissing := pin(PinCommitMissing, "gone")
	deprecatedMissing.Deprecated = true
	for _, tc := range []struct {
		action ActionCandidate
		want   string
	}{
		{pin(PinCommitMissing, "gone"), "[BAD-PIN]"},
		{pin(PinReleaseMismatch, "moved"), "[BAD-PIN]"},
		{deprecatedMissing, "[BAD-PIN]"},
		{pin(PinUnverified, "offline"), "[UNVERIFIED]"},
		{pin(PinUnversioned, "bare"), "[UNVERSIONED]"},
		{pin(PinVerified, ""), "[UP-TO-DATE]"},
	} {
		if got := ActionDriftStatus(tc.action); got != tc.want {
			t.Errorf("pin %s: status %s, want %s", tc.action.Pin, got, tc.want)
		}
	}
	got := FormatActionsInventory([]ActionCandidate{
		pin(PinUnverified, "offline"), pin(PinUnverified, "offline"), pin(PinUnverified, "rate limited"), pin(PinVerified, ""),
	})
	for _, want := range []string{
		"[UNVERIFIED]  example/action", "v1.0.0 -> v1 (ci.yml:7)",
		"  2 SHA pin(s) unverified: offline\n", "  1 SHA pin(s) unverified: rate limited\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("inventory lacks %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "unverified:") != 2 {
		t.Fatalf("each unverified reason must print once:\n%s", got)
	}
}

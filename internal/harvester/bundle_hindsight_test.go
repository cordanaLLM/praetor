package harvester

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// bundleHindsightScripts bundles a mock home and returns the base names of the records
// filed as hindsight-script, sorted, together with the report's skip notes.
func bundleHindsightScripts(t *testing.T, home string) ([]string, []string) {
	t.Helper()
	opts := BundleOptions{
		WorkstationName: "hindsight-box",
		OutputDir:       filepath.Join(filepath.Dir(home), "out"),
		HomeDir:         home,
		Roots:           testClientRoots(home),
	}
	rep, err := BundleWorkstation(context.Background(), opts)
	if err != nil {
		t.Fatalf("BundleWorkstation failed: %v", err)
	}
	var scripts []string
	for _, r := range rep.Records {
		if r.Category == "hindsight-script" {
			scripts = append(scripts, filepath.Base(filepath.FromSlash(r.RelativePath)))
		}
	}
	sort.Strings(scripts)
	return scripts, rep.Skipped
}

// Every PowerShell script under ~/.hindsight is captured whatever its name; other files
// in that directory are not filed as scripts.
func TestBundleAgentConfigs_CapturesEveryHindsightScript(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	hindsight := filepath.Join(home, ".hindsight")
	mustWriteFile(t, filepath.Join(hindsight, "start-tunnel.ps1"), "Write-Output tunnel")
	mustWriteFile(t, filepath.Join(hindsight, "sync-bank.ps1"), "Write-Output sync")
	mustWriteFile(t, filepath.Join(hindsight, "notes.txt"), "not a script")

	scripts, _ := bundleHindsightScripts(t, home)
	if want := []string{"start-tunnel.ps1", "sync-bank.ps1"}; strings.Join(scripts, ",") != strings.Join(want, ",") {
		t.Fatalf("hindsight-script records = %v, want %v", scripts, want)
	}
}

// A host without ~/.hindsight contributes no scripts and no skip note: an absent
// directory means Hindsight is not installed, not that something failed.
func TestBundleAgentConfigs_AbsentHindsightDirIsNeutral(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	mustWriteFile(t, filepath.Join(home, ".claude", "settings.json"), "{}")

	scripts, skipped := bundleHindsightScripts(t, home)
	if len(scripts) != 0 {
		t.Fatalf("absent ~/.hindsight produced script records: %v", scripts)
	}
	for _, note := range skipped {
		if strings.Contains(note, ".hindsight") {
			t.Fatalf("absent ~/.hindsight produced a skip note: %q", note)
		}
	}
}

// The script scan is bounded by MaxBundleEntries: a directory at the bound is copied in
// full, one entry more is skipped with a note instead of being read without limit.
func TestBundleAgentConfigs_HindsightScriptsBounded(t *testing.T) {
	for _, count := range []int{MaxBundleEntries, MaxBundleEntries + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "home")
			for i := 0; i < count; i++ {
				mustWriteFile(t, filepath.Join(home, ".hindsight", fmt.Sprintf("script-%04d.ps1", i)), "Write-Output x")
			}
			scripts, skipped := bundleHindsightScripts(t, home)
			noted := strings.Contains(strings.Join(skipped, "\n"), filepath.Join(home, ".hindsight"))
			if count == MaxBundleEntries && (len(scripts) != count || noted) {
				t.Fatalf("directory at the bound: %d scripts captured, skip noted %v", len(scripts), noted)
			}
			if count > MaxBundleEntries && (len(scripts) != 0 || !noted) {
				t.Fatalf("directory over the bound: %d scripts captured, skip noted %v", len(scripts), noted)
			}
		})
	}
}

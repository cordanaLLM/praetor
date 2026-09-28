package adopt

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/paperclip"
)

// absentRebindNote is the manifest note of a re-bind to a harness written where none existed.
const absentRebindNote = "Re-bound register.sources to the Paperclip harness this run writes where none existed"

// The extracted values a register.sources mismatch names (cavemansource.verifyDeclaredResult).
var (
	reportedApplicable    = regexp.MustCompile(`expected \d+ applicable values, extracted (\d+)`)
	reportedNotApplicable = regexp.MustCompile(`expected \d+ not-applicable values, extracted (\d+)`)
	reportedSHA256        = regexp.MustCompile(`sha256 mismatch: declared sha256:[0-9a-f]{64}, actual (sha256:[0-9a-f]{64})`)
)

// reportedSources is declared with every extracted value err names in place of the declared
// one, as an operator copies them from the adoption error into the manifest.
func reportedSources(t *testing.T, declared *config.RegisterSources, err error) *config.RegisterSources {
	t.Helper()
	text, pins := fmt.Sprint(err), *declared
	digest := reportedSHA256.FindStringSubmatch(text)
	if digest == nil {
		t.Fatalf("adoption error names no extracted digest: %v", err)
	}
	pins.SHA256 = digest[1]
	for target, pattern := range map[*int]*regexp.Regexp{&pins.Expected: reportedApplicable,
		&pins.NotApplicable: reportedNotApplicable} {
		if match := pattern.FindStringSubmatch(text); match != nil {
			value, convErr := strconv.Atoi(match[1])
			if convErr != nil {
				t.Fatal(convErr)
			}
			*target = value
		}
	}
	return &pins
}

// currentSynthesis is the harness plain adopt writes today for a fresh acme/legacy checkout.
func currentSynthesis(t *testing.T) string {
	t.Helper()
	repoPath := newTestRepo(t, "legacy")
	mustWrite(t, filepath.Join(repoPath, manifestFile), legacyManifest)
	if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, false)); err != nil {
		t.Fatal(err)
	}
	return mustRead(t, filepath.Join(repoPath, paperclipFile))
}

// TestAdoptDeletedHarnessRebindsPinsAcrossReleases (#502 U9) is the delete remedy after an
// upgrade. The 462e3f3a release bound the pins to the harness it wrote; the operator edited that
// harness, so --force stops before writing and names the remedy. Deleting the harness and running
// plain adopt writes the current synthesis, not the released bytes the pins name, and binds the
// pins to it, since a harness written where none existed is Praetor output. Before the fix the
// fresh synthesis was held to the released pins, so every re-run failed with the same remedy. A
// second run changes nothing.
func TestAdoptDeletedHarnessRebindsPinsAcrossReleases(t *testing.T) {
	synthesis := currentSynthesis(t)
	repoPath := newTestRepo(t, "legacy")
	released := releasedHarness(t, "harness.json.golden")
	bound, err := managedRegisterSources(t.Context(), []byte(released))
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repoPath, manifestFile), legacyManifest+manifestSourcesYAML(t, bound))
	harnessPath := filepath.Join(repoPath, paperclipFile)
	mustWrite(t, harnessPath, strings.Replace(released, "NOT shipping", "not shipping", 1))
	before := snapshotTree(t, repoPath)
	_, err = Adopt(t.Context(), sourceAdoptOptions(t, repoPath, true))
	for _, want := range []string{"register.sources sha256 mismatch", "delete it and re-run praetorctl adopt to regenerate it"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("--force on the edited released harness: error lacks %q: %v", want, err)
		}
	}
	assertTreeUnchanged(t, before, snapshotTree(t, repoPath))
	if err := os.Remove(harnessPath); err != nil {
		t.Fatal(err)
	}
	report, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, false))
	if err != nil {
		t.Fatalf("adopt after deleting the edited harness: %v", err)
	}
	if got := mustRead(t, harnessPath); got != synthesis {
		t.Fatalf("deleted harness not regenerated to the current synthesis:\n%s", got)
	}
	if detail := reportDetail(report, manifestFile); !strings.Contains(detail, absentRebindNote) {
		t.Fatalf("re-bind to the regenerated harness not reported: %q", detail)
	}
	rebound := requirePassingSourceGate(t, repoPath, paperclipFile)
	if rebound.SHA256 == bound.SHA256 || !reflect.DeepEqual(rebound.Inputs, bound.Inputs) {
		t.Fatalf("pins not re-bound to the current synthesis, or inputs changed: %+v", rebound)
	}
	settled := snapshotTree(t, repoPath)
	again, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, false))
	if err != nil {
		t.Fatalf("second adopt: %v", err)
	}
	assertTreeUnchanged(t, settled, snapshotTree(t, repoPath))
	if detail := reportDetail(again, manifestFile); strings.Contains(detail, "Re-bound register.sources") {
		t.Fatalf("second adopt re-bound again: %q", detail)
	}
}

// TestAdoptAbsentHarnessKeepsGateOfMixedContract (#502 U9) Negative, then Positive: a contract
// that also selects operator text, a hook, is not re-bound to a harness adoption writes where
// none existed, because its one digest cannot tell the hook's drift from the new harness. The run
// stops before writing and reports values that cover the harness it would write; with those set,
// a re-run writes that harness and the contract passes its gate, hook included, with no re-bind.
func TestAdoptAbsentHarnessKeepsGateOfMixedContract(t *testing.T) {
	repoPath, declared := extendedSourceRepo(t)
	if err := os.Remove(filepath.Join(repoPath, paperclipFile)); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, repoPath)
	_, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, false))
	for _, want := range []string{"fails its configured gate",
		"the contract also selects files other than " + paperclipFile, "which cover the harness this run writes"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("mixed contract over an absent harness: error lacks %q: %v", want, err)
		}
	}
	assertTreeUnchanged(t, before, snapshotTree(t, repoPath))
	mustWrite(t, filepath.Join(repoPath, manifestFile), legacyManifest+manifestSourcesYAML(t, reportedSources(t, declared, err)))
	report, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, false))
	if err != nil {
		t.Fatalf("adopt with the reported pins: %v", err)
	}
	if detail := reportDetail(report, manifestFile); strings.Contains(detail, "Re-bound register.sources") {
		t.Fatalf("contract re-bound although its pins already cover the written harness: %q", detail)
	}
	requirePassingSourceGate(t, repoPath, paperclipFile, "hooks/notify.sh")
}

// TestRebindsAbsentHarness_Boundary: only a harness this run writes where none existed, under a
// contract whose every input selects it, skips the declared gate. An operator's selector row on
// the same harness still leaves the contract harness-only. A row on another file, a harness on
// disk (refreshed, or operator-owned and patched), and a declined step with none on disk all
// keep the gate.
func TestRebindsAbsentHarness_Boundary(t *testing.T) {
	platformRow := config.RegisterSourceInput{Path: paperclipFile, Surface: config.SurfacePrompts,
		Kind: "message", Format: config.SourceFormatJSON, Selector: "platform"}
	hookRow := config.RegisterSourceInput{Path: "hooks/notify.sh", Surface: config.SurfaceHooks,
		Kind: "message", Format: config.SourceFormatShell}
	managed := &config.RegisterSources{Inputs: managedHarnessInputs()}
	withPlatform := &config.RegisterSources{Inputs: append(managedHarnessInputs(), platformRow)}
	mixed := &config.RegisterSources{Inputs: append(managedHarnessInputs(), hookRow)}
	synthesis := &paperclip.Harness{}
	for name, tc := range map[string]struct {
		declared *config.RegisterSources
		plan     harnessPlan
		want     bool
	}{
		"written over absent, managed rows":    {managed, harnessPlan{write: synthesis}, true},
		"written over absent, selector row":    {withPlatform, harnessPlan{write: synthesis}, true},
		"written over absent, hook row":        {mixed, harnessPlan{write: synthesis}, false},
		"refresh of earlier output":            {managed, harnessPlan{write: synthesis, onDisk: true, refresh: true}, false},
		"operator-owned, platform patched":     {managed, harnessPlan{data: []byte("b"), onDisk: true, owned: []byte("a")}, false},
		"declined, none on disk":               {managed, harnessPlan{}, false},
		"unresolved identity, none on disk":    {managed, harnessPlan{unresolved: true}, false},
		"operator-owned, kept byte for byte":   {managed, harnessPlan{data: []byte("a"), onDisk: true, owned: []byte("a")}, false},
		"written over absent, only a hook row": {&config.RegisterSources{Inputs: []config.RegisterSourceInput{hookRow}}, harnessPlan{write: synthesis}, false},
	} {
		if got := rebindsAbsentHarness(tc.declared, tc.plan); got != tc.want {
			t.Errorf("%s: rebindsAbsentHarness = %v, want %v", name, got, tc.want)
		}
	}
}

package flavor

import "testing"

// The score used to be asserted only as 0 <= Score <= 100, which present/total*100 cannot
// violate for any input, so no arithmetic change could ever fail a test. These cases pin exact
// values instead, and they live in the package because the toolchain-only boundary would
// otherwise need a flavor registered into the process-wide catalog, where it would outlive the
// test and break the catalog's own assertions.

func TestConformanceScore_Positive_CountsTemplatesAndSettings(t *testing.T) {
	report := &FlavorAuditReport{
		TemplatesTotal: 7, TemplatesPresent: 7,
		SettingsTotal: 2, SettingsValid: 2,
	}
	if got := conformanceScore(report); got != 100.0 {
		t.Fatalf("a fully conforming repository must score 100.0, got %v", got)
	}
}

func TestConformanceScore_Positive_IgnoresToolchains(t *testing.T) {
	report := &FlavorAuditReport{
		TemplatesTotal: 8, TemplatesPresent: 8,
		SettingsTotal: 3, SettingsValid: 3,
		ToolchainsTotal: 4, ToolchainsAvailable: 0,
	}
	// The pre-fix formula scored this repository 11/15 = 73.3% and failed it on a host with
	// none of its four tools installed.
	if got := conformanceScore(report); got != 100.0 {
		t.Fatalf("toolchains must not enter the score; got %v for a conforming repository", got)
	}
}

func TestConformanceScore_Negative_OneInvalidSettingCostsItsShare(t *testing.T) {
	report := &FlavorAuditReport{
		TemplatesTotal: 7, TemplatesPresent: 7,
		SettingsTotal: 2, SettingsValid: 1,
	}
	want := 8.0 / 9.0 * 100.0
	if got := conformanceScore(report); got != want {
		t.Fatalf("expected exactly %v for 8 of 9 required items, got %v", want, got)
	}
}

func TestConformanceScore_Negative_NothingPresentScoresZero(t *testing.T) {
	report := &FlavorAuditReport{TemplatesTotal: 7, SettingsTotal: 2}
	if got := conformanceScore(report); got != 0.0 {
		t.Fatalf("a repository carrying none of its required items must score 0.0, got %v", got)
	}
}

func TestConformanceScore_Boundary_ToolchainOnlyFlavorScores100(t *testing.T) {
	report := &FlavorAuditReport{ToolchainsTotal: 3, ToolchainsAvailable: 0}
	if got := conformanceScore(report); got != 100.0 {
		t.Fatalf("a flavor requiring no file has nothing to be missing; got %v", got)
	}
}

// TestConformanceScore_Boundary_TheBarItself pins the arithmetic only. Whether a repository
// on the bar clears it is the comparison in AuditFlavor, asserted against a real fixture by
// TestAuditFlavor_Boundary_ExactlyTheBarClearsIt: this case used to make a second claim
// (`got < passingScore`) that the equality check above it had already ruled out, so it could
// not fail, and nothing exercised the comparison the gate actually makes.
func TestConformanceScore_Boundary_TheBarItself(t *testing.T) {
	report := &FlavorAuditReport{TemplatesTotal: 5, TemplatesPresent: 4}
	if got := conformanceScore(report); got != passingScore {
		t.Fatalf("4 of 5 required items must score exactly the bar %v, got %v", passingScore, got)
	}
}

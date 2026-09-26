package needs

import (
	"strings"
	"testing"
)

func TestFormatReportHeader_3D(t *testing.T) {
	report := &RepoNeeds{Repository: "example.com/app", Readiness: ReadinessMetrics{Score: 50}}
	// Positive: the framework, its version and mapping availability, and an observed contract.
	observed := &FrameworkIndex{Name: "example.com/acme/kit", Version: "unverified", Basis: FrameworkSourceObserved, Contract: FrameworkContractFile}
	header := FormatReportHeader(report, observed)
	for _, want := range []string{"=== Framework Migration Report: example.com/app ===\n",
		"Framework: example.com/acme/kit (unverified) | Mapping availability: 50.0%\n\n",
		"Coverage basis: source-observed; builds and tests not run\n",
		"Capability contract: capabilities.yaml (declared packages source-observed)\n"} {
		if !strings.Contains(header, want) {
			t.Errorf("header lacks %q:\n%s", want, header)
		}
	}
	// Positive: a declared contract says its packages were not observed; deprecations follow.
	report.Deprecations = []string{legacyReplacementDeprecation}
	declared := FormatReportHeader(report, &FrameworkIndex{Name: "example.com/acme/kit", Version: "declared", Basis: FrameworkCatalogDeclared, Contract: "kit.yaml"})
	if !strings.Contains(declared, "(declared packages, not source-observed)") || !strings.Contains(declared, "Deprecated input: ") ||
		!strings.HasSuffix(declared, "\n\n") {
		t.Errorf("declared header:\n%s", declared)
	}
	// Boundary: no framework selected says so and claims no mapping score.
	none := FormatReportHeader(report, &FrameworkIndex{Basis: FrameworkNotConfigured})
	if !strings.Contains(none, "Framework: "+FrameworkNotConfiguredText+" | Mapping availability: n/a") || strings.Contains(none, "50.0%") {
		t.Errorf("not-configured header:\n%s", none)
	}
	// Negative: nothing to report.
	if got := FormatReportHeader(nil, observed); !strings.Contains(got, "unavailable") {
		t.Errorf("nil report header = %q", got)
	}
	if FrameworkDisplay("") != "not configured" || FrameworkDisplay("example.com/acme/kit") != "example.com/acme/kit" || FormatDeprecations(nil) != "" {
		t.Error("FrameworkDisplay / FormatDeprecations boundaries")
	}
}

package hisscoverage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hisscatalog"
)

// recursiveGo is a Go source that HISS-01 reports.
const recursiveGo = "package p\n\nfunc F(n int) int {\n\tif n <= 1 {\n\t\treturn 1\n\t}\n\treturn n * F(n-1)\n}\n"

// cleanGo is a Go source that no invariant reports.
const cleanGo = "package p\n\nfunc F(n int) int { return n + 1 }\n"

// corpusRoot builds a fixture root and returns the repository root containing it.
func corpusRoot(t *testing.T, rule, lang, bucket, name, body string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, filepath.FromSlash(FixtureDir), rule, lang, bucket)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("create fixture dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return root
}

// catalogFor builds a one-claim catalog.
func catalogFor(rule, lang string, state State) *Catalog {
	return &Catalog{Version: 1, Rules: []Rule{{
		ID:       rule,
		Coverage: []Coverage{{Language: lang, State: state, Mechanism: "internal/hiss", Rationale: "measured"}},
	}}}
}

// TestVerifyAcceptsABackedClaim is the positive dimension: a claim of detection whose
// positive fixture the rule actually reports must pass.
func TestVerifyAcceptsABackedClaim(t *testing.T) {
	root := corpusRoot(t, "HISS-01", "go", bucketPositive, "recursion.go", recursiveGo)

	report, err := Verify(t.Context(), root, catalogFor("HISS-01", "go", StatePartial))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !report.Passed() {
		t.Fatalf("a backed claim must pass: %+v", report.Findings)
	}
	if report.Claims != 1 || report.Fixtures != 1 {
		t.Errorf("expected one claim over one fixture, got %d/%d", report.Claims, report.Fixtures)
	}
}

// TestVerifyRejectsAnUnmetClaim is the negative dimension and the defect this exists for: a
// rule declared as enforcing something it does not report is exactly the matrix cell that
// advertised a gate which did not exist.
func TestVerifyRejectsAnUnmetClaim(t *testing.T) {
	root := corpusRoot(t, "HISS-01", "go", bucketPositive, "notrecursive.go", cleanGo)

	report, err := Verify(t.Context(), root, catalogFor("HISS-01", "go", StateEnforced))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.Passed() {
		t.Fatal("a claim of enforcement whose fixture is not reported must fail")
	}
	if !strings.Contains(report.Findings[0].Detail, "not reported") {
		t.Errorf("the finding must say the fixture went unreported: %s", report.Findings[0])
	}
}

// TestVerifyRejectsOverMatching is the negative dimension for the other failure mode: a rule
// that reports legitimate code is as unusable as one that reports nothing, because it gets
// suppressed wholesale.
func TestVerifyRejectsOverMatching(t *testing.T) {
	root := corpusRoot(t, "HISS-01", "go", bucketNegative, "legitimate.go", recursiveGo)

	report, err := Verify(t.Context(), root, catalogFor("HISS-01", "go", StatePartial))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.Passed() {
		t.Fatal("a negative fixture that is reported must fail")
	}
	if !strings.Contains(report.Findings[0].Detail, "over-matches") {
		t.Errorf("the finding must name over-matching: %s", report.Findings[0])
	}
}

// TestVerifyRejectsAClosedGap is the direction that keeps the catalog from going stale in
// the optimistic-looking direction. A fixture recorded as an undetected gap that starts
// being reported means the rule gained coverage, and the catalog must say so rather than
// continuing to understate what the gate does.
func TestVerifyRejectsAClosedGap(t *testing.T) {
	root := corpusRoot(t, "HISS-01", "go", bucketGap, "nowdetected.go", recursiveGo)

	report, err := Verify(t.Context(), root, catalogFor("HISS-01", "go", StatePartial))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.Passed() {
		t.Fatal("a gap fixture that is now reported must fail so the catalog is updated")
	}
	if !strings.Contains(report.Findings[0].Detail, "close the gap") {
		t.Errorf("the finding must direct the reader to close the gap: %s", report.Findings[0])
	}
}

// TestVerifyRejectsUndeclaredCoverage is the same direction for a state that claims no
// enforcement at all: an unsupported rule whose positive fixture fires is understating the
// gate, which matters because an unsupported state is used to justify not relying on it.
func TestVerifyRejectsUndeclaredCoverage(t *testing.T) {
	root := corpusRoot(t, "HISS-01", "go", bucketPositive, "recursion.go", recursiveGo)

	report, err := Verify(t.Context(), root, catalogFor("HISS-01", "go", StateUnsupported))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.Passed() {
		t.Fatal("an unsupported claim whose fixture is reported must fail")
	}
	if !strings.Contains(report.Findings[0].Detail, "understates coverage") {
		t.Errorf("the finding must name the understatement: %s", report.Findings[0])
	}
}

// TestVerifyReportsAnUnbackedClaim is the boundary dimension between wrong and
// undemonstrated: a claim with no fixture at all is not contradicted, but it is not
// evidence either, so it is surfaced without failing the gate while the corpus is filled in.
func TestVerifyReportsAnUnbackedClaim(t *testing.T) {
	report, err := Verify(t.Context(), t.TempDir(), catalogFor("HISS-01", "go", StateEnforced))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !report.Passed() {
		t.Errorf("an unbacked claim is undemonstrated, not contradicted: %+v", report.Findings)
	}
	if len(report.Unbacked) != 1 {
		t.Fatalf("an unbacked claim must be surfaced, got %v", report.Unbacked)
	}
}

// TestValidateRefusesDetectionWithoutMechanism is the boundary dimension on the catalog
// itself. A claim of enforcement with no named mechanism is precisely how a cell comes to
// advertise a gate that does not exist, so it is refused at load rather than verified.
func TestValidateRefusesDetectionWithoutMechanism(t *testing.T) {
	catalog := catalogFor("HISS-01", "go", StateEnforced)
	catalog.Rules[0].Coverage[0].Mechanism = "  "

	err := catalog.Validate()
	if !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("expected an invalid-catalog error, got %v", err)
	}
	if !strings.Contains(err.Error(), "mechanism") {
		t.Errorf("the error must name the missing mechanism: %v", err)
	}
}

// TestValidateRefusesUnknownState is the negative dimension for the state vocabulary: an
// unrecognised state must be an error, because silently ignoring it would read as coverage.
func TestValidateRefusesUnknownState(t *testing.T) {
	catalog := catalogFor("HISS-01", "go", State("mostly"))

	if err := catalog.Validate(); !errors.Is(err, ErrUnknownState) {
		t.Fatalf("expected an unknown-state error, got %v", err)
	}
}

// TestValidateRefusesDuplicateAndEmpty is the boundary dimension for catalog identity: a
// rule declared twice, or a language declared twice within a rule, makes the effective claim
// depend on ordering.
func TestValidateRefusesDuplicateAndEmpty(t *testing.T) {
	empty := &Catalog{Version: 1}
	if err := empty.Validate(); !errors.Is(err, ErrInvalidCatalog) {
		t.Errorf("a catalog with no rules must be refused, got %v", err)
	}

	dupRule := catalogFor("HISS-01", "go", StatePartial)
	dupRule.Rules = append(dupRule.Rules, dupRule.Rules[0])
	if err := dupRule.Validate(); !errors.Is(err, ErrInvalidCatalog) {
		t.Errorf("a rule declared twice must be refused, got %v", err)
	}

	dupLang := catalogFor("HISS-01", "go", StatePartial)
	dupLang.Rules[0].Coverage = append(dupLang.Rules[0].Coverage, dupLang.Rules[0].Coverage[0])
	if err := dupLang.Validate(); !errors.Is(err, ErrInvalidCatalog) {
		t.Errorf("a language declared twice must be refused, got %v", err)
	}
}

// TestLoadCatalogAbsent is the boundary dimension for a repository that declares nothing:
// absence is reported as its own condition rather than as an empty, passing catalog.
func TestLoadCatalogAbsent(t *testing.T) {
	if _, err := LoadCatalog(t.Context(), t.TempDir()); !errors.Is(err, ErrCatalogAbsent) {
		t.Fatalf("expected ErrCatalogAbsent, got %v", err)
	}
}

// TestLoadCatalogRejectsUnknownField is the negative dimension for the catalog document: a
// misspelled key must fail rather than silently dropping a claim.
func TestLoadCatalogRejectsUnknownField(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".config", "hiss")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	body := "version: 1\nrules:\n  - id: HISS-01\n    title: t\n    covrage:\n      - language: go\n"
	if err := os.WriteFile(filepath.Join(dir, "coverage.yaml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}

	if _, err := LoadCatalog(t.Context(), root); err == nil {
		t.Fatal("a misspelled catalog key must be refused")
	}
}

// TestRepositoryCatalogVerifies is the boundary dimension against the real artifact: this
// repository's own catalog must load and every claim must survive its fixtures, which is
// what makes the committed file evidence rather than documentation.
func TestRepositoryCatalogVerifies(t *testing.T) {
	root := filepath.Join("..", "..")
	catalog, err := LoadCatalog(t.Context(), root)
	if err != nil {
		t.Fatalf("the repository catalog must load: %v", err)
	}
	report, err := Verify(t.Context(), root, catalog)
	if err != nil {
		t.Fatalf("verify repository catalog: %v", err)
	}
	if !report.Passed() {
		t.Fatalf("every declared claim must hold: %+v", report.Findings)
	}
	if report.Fixtures == 0 {
		t.Error("the repository catalog must be backed by at least one fixture")
	}
}

// branchyGo returns a Go function of cyclomatic complexity n built from n-1 flat ifs, padded
// with pad extra lines so the length rule can be made to fire as well.
func branchyGo(n, pad int) string {
	var sb strings.Builder
	sb.WriteString("package p\n\nfunc F(x int) int {\n")
	for i := 1; i < n; i++ {
		sb.WriteString("\tif x > 0 {\n\t\tx++\n\t}\n")
	}
	sb.WriteString(strings.Repeat("\t// padding\n", pad))
	sb.WriteString("\treturn x\n}\n")
	return sb.String()
}

// verifyHISS04 replays one fixture of a partial HISS-04/go claim and returns the findings.
func verifyHISS04(t *testing.T, bucket, body string) []Finding {
	t.Helper()
	root := corpusRoot(t, "HISS-04", "go", bucket, "fixture.go", body)
	writeLOCPositive(t, root)
	report, err := Verify(t.Context(), root, catalogFor("HISS-04", "go", StatePartial))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	return report.Findings
}

// writeLOCPositive backs the partial claim with a length violation, so only the fixture under
// test can produce a finding.
func writeLOCPositive(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(FixtureDir), "HISS-04", "go", bucketPositive)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "long.go"), []byte(branchyGo(1, 70)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Positive: a measured fixture that yields a measurement and no violation holds; so does a
// negative fixture sitting exactly at the limit.
func TestVerifyAcceptsAMeasuredFixture(t *testing.T) {
	if findings := verifyHISS04(t, bucketMeasured, branchyGo(11, 0)); len(findings) != 0 {
		t.Errorf("a measured fixture over the limit must hold: %+v", findings)
	}
	if findings := verifyHISS04(t, bucketNegative, branchyGo(10, 0)); len(findings) != 0 {
		t.Errorf("a negative fixture at the limit must hold: %+v", findings)
	}
}

// Negative, both directions: a measured fixture that yields no measurement fails, and a
// negative fixture one over the limit fails because the measurement over-matches.
func TestVerifyRejectsAMissingOrOverMatchingMeasurement(t *testing.T) {
	for _, tc := range []struct {
		bucket, body, detail string
	}{
		{bucketMeasured, branchyGo(10, 0), "yields no measurement"},
		{bucketNegative, branchyGo(11, 0), "measured over a limit"},
	} {
		findings := verifyHISS04(t, tc.bucket, tc.body)
		if len(findings) != 1 || !strings.Contains(findings[0].Detail, tc.detail) {
			t.Errorf("%s fixture: findings %+v, want one saying %q", tc.bucket, findings, tc.detail)
		}
	}
}

// Boundary: a measured fixture that is also a violation fails, because a measurement must
// never be what enforces a shape.
func TestVerifyRejectsAMeasuredFixtureThatIsEnforced(t *testing.T) {
	findings := verifyHISS04(t, bucketMeasured, branchyGo(11, 40))
	if len(findings) != 1 || !strings.Contains(findings[0].Detail, "never be enforced") {
		t.Errorf("findings %+v, want the enforced measured fixture", findings)
	}
}

// TestValidateRefusesUnregisteredRule: coverage evidence for an identifier the HISS catalog
// does not define describes enforcement of nothing any other surface names. Positive: the
// last catalog rule validates. Negative: the next identifier is refused.
func TestValidateRefusesUnregisteredRule(t *testing.T) {
	if err := catalogFor("HISS-21", "go", StateManual).Validate(); err != nil {
		t.Fatalf("the last catalog invariant must validate: %v", err)
	}
	err := catalogFor("HISS-22", "go", StateManual).Validate()
	if !errors.Is(err, ErrInvalidCatalog) || !strings.Contains(err.Error(), "not a registered invariant") {
		t.Fatalf("an unregistered rule must be refused, got %v", err)
	}
}

// titledCatalog is catalogFor with a declared title on its one rule.
func titledCatalog(rule, title string) *Catalog {
	catalog := catalogFor(rule, "go", StateManual)
	catalog.Rules[0].Title = title
	return catalog
}

// TestValidateTitleDefersToTheHISSCatalog: a rule's name lives in the HISS catalog, so a
// coverage entry may omit its title or repeat the catalog's, never state another. Positive: an
// omitted title and the catalog's own title validate, and CatalogTitle reads the catalog.
func TestValidateTitleDefersToTheHISSCatalog(t *testing.T) {
	registered, ok := hisscatalog.LookupRule("HISS-14")
	if !ok {
		t.Fatal("HISS-14 missing from the HISS catalog")
	}
	for _, title := range []string{"", registered.Title} {
		catalog := titledCatalog("HISS-14", title)
		if err := catalog.Validate(); err != nil {
			t.Fatalf("title %q must validate: %v", title, err)
		}
		if got := catalog.Rules[0].CatalogTitle(); got != registered.Title {
			t.Fatalf("CatalogTitle() = %q, want the catalog's %q", got, registered.Title)
		}
	}
}

// Negative: a title the catalog does not state is refused, including the shortened name
// coverage.yaml carried before the catalog was the one list ("Append-Only ABI").
func TestValidateRefusesADriftedTitle(t *testing.T) {
	err := titledCatalog("HISS-14", "Append-Only ABI").Validate()
	if !errors.Is(err, ErrInvalidCatalog) || !strings.Contains(err.Error(), "differs from the HISS catalog title") {
		t.Fatalf("a drifted title must be refused, got %v", err)
	}
}

// Boundary: the comparison is exact. A title differing from the catalog's only in case or by
// surrounding whitespace is still a second spelling and is refused; an unregistered identifier
// is refused for the identifier, and CatalogTitle falls back to the declared title.
func TestValidateTitleComparisonIsExact(t *testing.T) {
	registered, _ := hisscatalog.LookupRule("HISS-01")
	for _, title := range []string{strings.ToLower(registered.Title), " " + registered.Title} {
		if err := titledCatalog("HISS-01", title).Validate(); !errors.Is(err, ErrInvalidCatalog) {
			t.Errorf("title %q must be refused, got %v", title, err)
		}
	}
	unknown := titledCatalog("HISS-22", "Declared Only")
	if err := unknown.Validate(); err == nil || !strings.Contains(err.Error(), "not a registered invariant") {
		t.Fatalf("unregistered rule must be refused for its identifier, got %v", err)
	}
	if got := unknown.Rules[0].CatalogTitle(); got != "Declared Only" {
		t.Fatalf("CatalogTitle() of an unregistered rule = %q, want the declared title", got)
	}
}

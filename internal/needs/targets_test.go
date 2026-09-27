package needs

import (
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// acmeKit is the placeholder go framework the tests configure.
const acmeKit = "example.com/acme/kit"

// acmeContractDir holds the placeholder contracts of every framework language, relative to
// the package directory the tests run in.
var acmeContractDir = filepath.Join("testdata", "contracts")

// acmeTargets configures every framework language with a placeholder framework declared by
// its contract under testdata/contracts; the go target routes to two builder kits.
func acmeTargets() Targets {
	contract := func(name string) string { return filepath.Join(acmeContractDir, name+".capabilities.yaml") }
	return Targets{
		"go":         {Module: acmeKit, BuilderKits: []string{"acme/kit", "acme/kit-extras"}, Contract: contract("kit")},
		"typescript": {Module: "example.com/acme/ui", BuilderKits: []string{"acme/ui"}, Contract: contract("ui")},
		"python":     {Module: "example.com/acme/py", BuilderKits: []string{"acme/py"}, Contract: contract("py")},
		"rust":       {Module: "example.com/acme/rs", BuilderKits: []string{"acme/rs"}, Contract: contract("rs")},
		"native":     {Module: "example.com/acme/native", BuilderKits: []string{"acme/native"}, Contract: contract("native")},
	}
}

// acmeRegistry loads acmeTargets with their contracts, as RegistryFromPolicy does for a
// configured host.
func acmeRegistry(t testing.TB) *AnalyzerRegistry {
	t.Helper()
	registry, err := LoadRegistry(t.Context(), acmeTargets())
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

// acmeDeclared selects the placeholder go framework's declaration, as SelectFrameworkSource
// does for a host without a checkout.
func acmeDeclared() FrameworkSource {
	return FrameworkSource{Contract: acmeTargets()["go"].Contract, Module: acmeKit}
}

// acmeIndex declares the placeholder go framework from its contract.
func acmeIndex(t testing.TB) *FrameworkIndex {
	t.Helper()
	index, err := InspectFramework(t.Context(), acmeDeclared())
	if err != nil {
		t.Fatal(err)
	}
	return index
}

// acmeSource selects checkout under the placeholder go target, as SelectFrameworkSource does
// with --framework: the target's contract is observed there when the checkout publishes none.
func acmeSource(checkout string) FrameworkSource {
	return FrameworkSource{Checkout: checkout, Contract: acmeTargets()["go"].Contract, Module: acmeKit}
}

func TestTargetsFor_3D(t *testing.T) {
	// Positive: a configured target is returned as a copy.
	configured := acmeTargets()
	kits := configured.For("go").BuilderKits
	kits[0] = "mutated/kit"
	if configured["go"].BuilderKits[0] != "acme/kit" || configured.For("go").RoutingKit() != "acme/kit" {
		t.Fatalf("For aliases the configured targets: %+v", configured["go"])
	}
	cloned := configured.clone()
	cloned["go"].BuilderKits[0] = "mutated/kit"
	if configured["go"].BuilderKits[0] != "acme/kit" {
		t.Fatal("clone aliases the configured builder kits")
	}
	// Negative: no target configured has no framework for any language: nothing is built in.
	for _, language := range config.FrameworkLanguages() {
		if target := (Targets{}).For(language); !reflect.DeepEqual(target, Target{}) || target.RoutingKit() != "" {
			t.Fatalf("unconfigured %s target = %+v", language, target)
		}
		if target := Targets(nil).For(language); target.Module != "" {
			t.Fatalf("nil targets resolved %s to %+v", language, target)
		}
	}
	// Boundary: eight builder kits, the configured maximum, route to the first; an unknown
	// language has none.
	eight := make([]string, 8)
	for i := range eight {
		eight[i] = fmt.Sprintf("acme/kit-%d", i)
	}
	wide := Targets{"go": {Module: acmeKit, BuilderKits: eight}}
	if got := wide.For("go"); got.RoutingKit() != "acme/kit-0" || len(got.BuilderKits) != 8 || wide.For("cobol").Module != "" {
		t.Fatalf("eight builder kits = %+v", got)
	}
}

func TestTargetsFromPolicy_3D(t *testing.T) {
	// Boundary: no settings document selected has no targets.
	if targets, err := TargetsFromPolicy(nil); err != nil || len(targets) != 0 {
		t.Fatalf("nil policy = %v, %v", targets, err)
	}
	dir := t.TempDir()
	workstation := filepath.Join(dir, "workstation.yaml")
	writeFixture(t, dir, "workstation.yaml", "framework: {targets: {go: {module: example.com/acme/kit, builder_kits: [acme/kit], "+
		"contract: contracts/kit.yaml}, typescript: {module: example.com/acme/ui}}}\n")
	policy, err := config.LoadOperatorPolicy(t.Context(), config.SettingsSelection{Workstation: config.SettingsDocument{Path: workstation}})
	if err != nil {
		t.Fatal(err)
	}
	// Positive: every configured target converts, the relative contract resolved against the
	// document that set it.
	targets, err := TargetsFromPolicy(policy)
	if err != nil {
		t.Fatal(err)
	}
	want := Targets{
		"go":         {Module: acmeKit, BuilderKits: []string{"acme/kit"}, Contract: filepath.Join(dir, "contracts", "kit.yaml")},
		"typescript": {Module: "example.com/acme/ui"},
	}
	if !reflect.DeepEqual(targets, want) {
		t.Fatalf("targets = %+v, want %+v", targets, want)
	}
	// Negative: a relative contract set by a layer without a file cannot be resolved.
	layer := config.PolicyLayer{Source: config.PolicySource{ID: "fleet", SHA256: strings.Repeat("a", 64)},
		Settings: []config.OperatorSetting{{Path: "framework.targets.go"}, {Path: "framework.targets.go.contract", Value: "kit.yaml"}}}
	fileless, err := config.ResolvePolicy(t.Context(), []config.PolicyLayer{layer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TargetsFromPolicy(fileless); err == nil || !strings.Contains(err.Error(), "framework.targets.go.contract") {
		t.Fatalf("unresolvable contract = %v", err)
	}
}

func TestSelectFrameworkSource_3D(t *testing.T) {
	targets := Targets{"go": {Module: acmeKit, Contract: "/srv/kit.yaml", Checkout: "/srv/kit"}}
	env := func(value string) func(string) string {
		return func(name string) string {
			if name == FrameworkDirEnv {
				return value
			}
			return ""
		}
	}
	withoutGo := acmeTargets()
	delete(withoutGo, "go")
	cases := []struct {
		name      string
		selection FrameworkSelection
		want      FrameworkSource
	}{
		{"configured checkout", FrameworkSelection{Targets: targets},
			FrameworkSource{Checkout: "/srv/kit", Contract: "/srv/kit.yaml", Module: acmeKit}},
		{"environment wins", FrameworkSelection{Targets: targets, Getenv: env("/env/kit")},
			FrameworkSource{Checkout: "/env/kit", Contract: "/srv/kit.yaml", Module: acmeKit}},
		{"explicit wins", FrameworkSelection{Targets: targets, Getenv: env("/env/kit"), Explicit: " /flag/kit ", ExplicitSet: true},
			FrameworkSource{Checkout: "/flag/kit", Contract: "/srv/kit.yaml", Module: acmeKit}},
		{"explicit empty selects the declaration", FrameworkSelection{Targets: targets, Getenv: env("/env/kit"), ExplicitSet: true},
			FrameworkSource{Contract: "/srv/kit.yaml", Module: acmeKit}},
		{"unconfigured host selects nothing", FrameworkSelection{}, FrameworkSource{}},
		{"configured host without a go target selects nothing", FrameworkSelection{Targets: withoutGo}, FrameworkSource{}},
	}
	for _, tc := range cases {
		if got := SelectFrameworkSource(tc.selection); got != tc.want {
			t.Errorf("%s: %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// applyTo records the target's framework and builder kits and leaves every demand as the
// classification produced it; no target records no framework.
func TestTargetApplyTo_3D(t *testing.T) {
	demand := DependencyDemand{Package: "github.com/jackc/pgx/v5", Capability: "db.postgres", Status: StatusGap, Notes: "classified"}
	// Positive: the configured target is recorded, its kits copied.
	repo := &RepoNeeds{Dependencies: []DependencyDemand{demand}}
	target := acmeTargets()["go"]
	target.applyTo(repo)
	repo.BuilderKits[0] = "mutated/kit"
	if repo.Framework != acmeKit || target.BuilderKits[0] != "acme/kit" || !reflect.DeepEqual(repo.Dependencies, []DependencyDemand{demand}) {
		t.Fatalf("configured target = %s %v %+v", repo.Framework, repo.BuilderKits, repo.Dependencies)
	}
	// Negative: no target names no framework and routes nothing.
	none := &RepoNeeds{Dependencies: []DependencyDemand{demand}}
	Target{}.applyTo(none)
	if none.Framework != "" || none.BuilderKits != nil {
		t.Fatalf("no target = %+v", none)
	}
	// Boundary: a target without builder kits names its framework and routes nothing.
	bare := &RepoNeeds{}
	Target{Module: acmeKit}.applyTo(bare)
	if bare.Framework != acmeKit || len(bare.BuilderKits) != 0 {
		t.Fatalf("target without kits = %+v", bare)
	}
}

// Scanning a TypeScript-only repository maps its demands onto the typescript target's
// contract; a host without that target names no framework, and a host without any target
// classifies only.
func TestRegistryScoresEachLanguageAgainstItsTarget(t *testing.T) {
	repo := t.TempDir()
	writeFixture(t, repo, ".git/HEAD", "ref: refs/heads/main\n")
	writeFixture(t, repo, "package.json", `{"name":"ui","dependencies":{"zod":"^3","left-pad":"^1"}}`)
	configured, err := ScanRepo(t.Context(), repo, acmeRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	assertDemand(t, demandFor(t, configured, "zod"), StatusCovered, "example.com/acme/ui/forms", "ui.forms")
	if configured.Framework != "example.com/acme/ui" || !slices.Equal(configured.BuilderKits, []string{"acme/ui"}) ||
		demandFor(t, configured, "left-pad").TargetBuilderKit != "acme/ui" {
		t.Fatalf("configured typescript target = %s %v %+v", configured.Framework, configured.BuilderKits, configured.Dependencies)
	}
	goOnly, err := ScanRepo(t.Context(), repo, NewRegistry(Targets{"go": acmeTargets()["go"]}))
	if err != nil {
		t.Fatal(err)
	}
	zod := demandFor(t, goOnly, "zod")
	if goOnly.Framework != "" || len(goOnly.BuilderKits) != 0 || zod.TargetBuilderKit != "" || zod.FrameworkReplacement != "" || zod.Status != StatusGap {
		t.Fatalf("an unconfigured typescript target reported a framework: %s %v %+v", goOnly.Framework, goOnly.BuilderKits, zod)
	}
	unconfigured, err := ScanRepo(t.Context(), repo, NewRegistry(nil))
	if err != nil {
		t.Fatal(err)
	}
	zod = demandFor(t, unconfigured, "zod")
	if unconfigured.Framework != "" || unconfigured.Readiness.Basis != FrameworkNotConfigured || zod.Capability != "ui.forms" || zod.Status != StatusGap {
		t.Fatalf("an unconfigured host must classify only: %+v %+v", unconfigured.Readiness, zod)
	}
}

func TestRegistryFromPolicy_3D(t *testing.T) {
	// Boundary: no settings document configures no target, so every language is classified only.
	registry, err := RegistryFromPolicy(t.Context(), nil)
	if err != nil || len(registry.Targets()) != 0 || registry.Targets().For("go").Module != "" {
		t.Fatalf("nil policy registry = %+v, %v", registry.Targets(), err)
	}
	dir := t.TempDir()
	writeFixture(t, dir, "kit.yaml", acmeContract)
	load := func(body string) (*AnalyzerRegistry, error) {
		writeFixture(t, dir, "workstation.yaml", body)
		policy, err := config.LoadOperatorPolicy(t.Context(), config.SettingsSelection{Workstation: config.SettingsDocument{Path: filepath.Join(dir, "workstation.yaml")}})
		if err != nil {
			t.Fatal(err)
		}
		return RegistryFromPolicy(t.Context(), policy)
	}
	// Positive: the configured target and its contract, resolved against the document.
	registry, err = load("framework: {targets: {go: {module: example.com/acme/kit, contract: kit.yaml}}}\n")
	if err != nil || registry.Targets().For("go").Module != acmeKit || registry.frameworkFor("typescript", nil) != nil {
		t.Fatalf("configured registry = %v", err)
	}
	// Negative: a contract naming another framework than its target's module refuses to load.
	if _, err := load("framework: {targets: {go: {module: example.com/other/kit, contract: kit.yaml}}}\n"); err == nil ||
		!strings.Contains(err.Error(), "load framework targets") {
		t.Fatalf("mismatched contract = %v", err)
	}
}

// Every placeholder contract under testdata/contracts loads for its language, so the tests
// configure a framework for every language the analyzers report.
func TestAcmeContractsLoadForEveryLanguage(t *testing.T) {
	registry := acmeRegistry(t)
	for _, language := range config.FrameworkLanguages() {
		index := registry.contracts[language]
		if index == nil || index.Name != acmeTargets()[language].Module || len(index.Packages) == 0 || index.Ecosystem != languageEcosystem(language) {
			t.Errorf("%s contract = %+v", language, index)
		}
	}
}

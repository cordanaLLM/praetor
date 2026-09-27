package needs

import (
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// legacySource selects checkout under the built-in go target: the selection a host without
// framework.targets makes (SelectFrameworkSource with empty targets).
func legacySource(checkout string) FrameworkSource {
	return FrameworkSource{Checkout: checkout, Module: defaultFrameworkModule}
}

// acmeTargets configures one framework per language under placeholder identities.
func acmeTargets() Targets {
	return Targets{
		"go":         {Module: "example.com/acme/kit", BuilderKits: []string{"acme/kit", "acme/kit-extras"}},
		"typescript": {Module: "example.com/acme/ui", BuilderKits: []string{"acme/ui"}},
	}
}

// The built-in targets are the ones every analyzer, the harvester and the request router
// used before targets became configuration; an unconfigured host must see them unchanged.
func TestLegacyTargetsKeepEveryLanguage(t *testing.T) {
	legacy := legacyTargets()
	if !slices.Equal(legacy.Languages(), []string{"go", "native", "python", "rust", "typescript"}) {
		t.Fatalf("legacy languages = %v", legacy.Languages())
	}
	// Every language keeps a module and a routing kit whose repository names the module.
	for _, language := range config.FrameworkLanguages() {
		target := legacy[language]
		_, kitRepo, _ := strings.Cut(target.RoutingKit(), "/")
		if kitRepo == "" || path.Base(target.Module) != kitRepo {
			t.Errorf("legacy %s target = %+v", language, target)
		}
	}
	if len(legacy["go"].BuilderKits) != 2 || legacy["go"].Module != defaultFrameworkModule {
		t.Fatalf("legacy go target lost a builder kit: %+v", legacy["go"])
	}
	legacy["go"].BuilderKits[0] = "mutated/kit"
	if legacyTargets()["go"].BuilderKits[0] == "mutated/kit" {
		t.Fatal("legacyTargets must return a fresh copy")
	}
}

func TestTargetsResolution_3D(t *testing.T) {
	// Positive: configured targets win and are copied.
	configured := acmeTargets()
	resolved := configured.resolved()
	resolved["go"].BuilderKits[0] = "mutated/kit"
	if configured["go"].BuilderKits[0] != "acme/kit" || configured.For("go").RoutingKit() != "acme/kit" {
		t.Fatalf("resolved targets alias the configured ones: %+v", configured["go"])
	}
	// Negative: a configured host has no built-in target for an unconfigured language.
	if python := configured.For("python"); !reflect.DeepEqual(python, Target{}) || python.RoutingKit() != "" {
		t.Fatalf("unconfigured python target = %+v", python)
	}
	// Boundary: no target configured selects the built-in ones; an unknown language has none.
	if Targets(nil).For("go").Module != defaultFrameworkModule || (Targets{}).For("cobol").Module != "" {
		t.Fatal("empty targets must resolve to the built-in targets")
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
		"go":         {Module: "example.com/acme/kit", BuilderKits: []string{"acme/kit"}, Contract: filepath.Join(dir, "contracts", "kit.yaml")},
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
	targets := Targets{"go": {Module: "example.com/acme/kit", Contract: "/srv/kit.yaml", Checkout: "/srv/kit"}}
	env := func(value string) func(string) string {
		return func(name string) string {
			if name == FrameworkDirEnv {
				return value
			}
			return ""
		}
	}
	cases := []struct {
		name      string
		selection FrameworkSelection
		want      FrameworkSource
	}{
		{"configured checkout", FrameworkSelection{Targets: targets},
			FrameworkSource{Checkout: "/srv/kit", Contract: "/srv/kit.yaml", Module: "example.com/acme/kit"}},
		{"environment wins", FrameworkSelection{Targets: targets, Getenv: env("/env/kit")},
			FrameworkSource{Checkout: "/env/kit", Contract: "/srv/kit.yaml", Module: "example.com/acme/kit"}},
		{"explicit wins", FrameworkSelection{Targets: targets, Getenv: env("/env/kit"), Explicit: " /flag/kit ", ExplicitSet: true},
			FrameworkSource{Checkout: "/flag/kit", Contract: "/srv/kit.yaml", Module: "example.com/acme/kit"}},
		{"explicit empty selects the declaration", FrameworkSelection{Targets: targets, Getenv: env("/env/kit"), ExplicitSet: true},
			FrameworkSource{Contract: "/srv/kit.yaml", Module: "example.com/acme/kit"}},
		{"unconfigured host keeps the built-in module", FrameworkSelection{}, FrameworkSource{Module: defaultFrameworkModule}},
		{"configured host without a go target selects nothing", FrameworkSelection{Targets: acmeTargets().withoutGo()}, FrameworkSource{}},
	}
	for _, tc := range cases {
		if got := SelectFrameworkSource(tc.selection); got != tc.want {
			t.Errorf("%s: %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func (t Targets) withoutGo() Targets {
	out := t.resolved()
	delete(out, "go")
	return out
}

// A configured target never receives the built-in catalog's packages: the demand keeps its
// capability, becomes a gap, and the note names the configured framework.
func TestTargetScopesBuiltinCatalogPaths(t *testing.T) {
	acme := acmeTargets()["go"]
	covered := DependencyDemand{Package: "github.com/jackc/pgx/v5", Capability: "db.postgres", Status: StatusCovered,
		FrameworkReplacement: defaultFrameworkModule + "/db/pgx", Notes: "built-in note"}
	wrapped := DependencyDemand{Package: "github.com/knadh/koanf/v2", Capability: "config.loader", Status: StatusCovered,
		Relationship: &LibraryRelationship{Kind: RelationshipWrappedBy, FrameworkPackage: defaultFrameworkModule + "/core/config"}}
	foundation := DependencyDemand{Package: "go.uber.org/fx", Capability: "runtime.di", Status: StatusNative,
		Relationship: &LibraryRelationship{Kind: RelationshipFoundation}}
	own := DependencyDemand{Package: "github.com/acme/driver", Capability: "db.postgres", Status: StatusAdapterAvailable,
		FrameworkReplacement: "example.com/acme/kit/db"}
	repo := &RepoNeeds{Dependencies: []DependencyDemand{covered, wrapped, foundation, own}}
	acme.applyTo(repo)
	if repo.Framework != "example.com/acme/kit" || !slices.Equal(repo.BuilderKits, []string{"acme/kit", "acme/kit-extras"}) {
		t.Fatalf("target not recorded: %s %v", repo.Framework, repo.BuilderKits)
	}
	got := repo.Dependencies
	if got[0].FrameworkReplacement != "" || got[0].Status != StatusGap || !strings.Contains(got[0].Notes, "example.com/acme/kit declares no catalog replacement") {
		t.Errorf("built-in replacement kept: %+v", got[0])
	}
	if got[1].Relationship.FrameworkPackage != "" || got[1].Status != StatusGap || wrapped.Relationship.FrameworkPackage == "" {
		t.Errorf("built-in related package kept, or the caller's relationship mutated: %+v", got[1].Relationship)
	}
	if got[2].Relationship != nil || got[2].Status != StatusGap {
		t.Errorf("built-in foundation kept for another framework: %+v", got[2])
	}
	if got[3].FrameworkReplacement != "example.com/acme/kit/db" || got[3].Status != StatusAdapterAvailable {
		t.Errorf("the target's own package was dropped: %+v", got[3])
	}
	// Boundary: the built-in go target keeps every built-in claim, and no target at all
	// explains that none is configured.
	legacy := &RepoNeeds{Dependencies: []DependencyDemand{covered, wrapped, foundation}}
	legacyTargets()["go"].applyTo(legacy)
	if !reflect.DeepEqual(legacy.Dependencies, []DependencyDemand{covered, wrapped, foundation}) {
		t.Fatalf("the built-in target changed built-in claims: %+v", legacy.Dependencies)
	}
	none := &RepoNeeds{Dependencies: []DependencyDemand{covered}}
	Target{}.applyTo(none)
	if none.Framework != "" || none.BuilderKits != nil || !strings.HasPrefix(none.Dependencies[0].Notes, "No target framework is configured") {
		t.Fatalf("no target = %+v", none)
	}
}

// Scanning a TypeScript-only repository on an unconfigured host keeps the built-in
// typescript target; on a host that configures only go it names no typescript framework.
func TestRegistryScoresEachLanguageAgainstItsTarget(t *testing.T) {
	repo := t.TempDir()
	writeFixture(t, repo, ".git/HEAD", "ref: refs/heads/main\n")
	writeFixture(t, repo, "package.json", `{"name":"ui","dependencies":{"zod":"^3","left-pad":"^1"}}`)
	legacy, err := ScanRepo(t.Context(), repo, NewRegistry(nil))
	if err != nil {
		t.Fatal(err)
	}
	zod := demandFor(t, legacy, "zod")
	if legacy.Framework != legacyTargets()["typescript"].Module || !slices.Equal(legacy.BuilderKits, legacyTargets()["typescript"].BuilderKits) ||
		zod.TargetBuilderKit != legacyTargets()["typescript"].BuilderKits[0] || zod.FrameworkReplacement == "" {
		t.Fatalf("built-in typescript target lost: %s %v %+v", legacy.Framework, legacy.BuilderKits, zod)
	}
	goOnly, err := ScanRepo(t.Context(), repo, NewRegistry(Targets{"go": acmeTargets()["go"]}))
	if err != nil {
		t.Fatal(err)
	}
	zod = demandFor(t, goOnly, "zod")
	if goOnly.Framework != "" || len(goOnly.BuilderKits) != 0 || zod.TargetBuilderKit != "" || zod.FrameworkReplacement != "" || zod.Status != StatusGap {
		t.Fatalf("an unconfigured typescript target reported a framework: %s %v %+v", goOnly.Framework, goOnly.BuilderKits, zod)
	}
	configured, err := ScanRepo(t.Context(), repo, NewRegistry(acmeTargets()))
	if err != nil {
		t.Fatal(err)
	}
	if configured.Framework != "example.com/acme/ui" || demandFor(t, configured, "left-pad").TargetBuilderKit != "acme/ui" {
		t.Fatalf("configured typescript target = %s %+v", configured.Framework, configured.Dependencies)
	}
}

func TestRegistryFromPolicy_3D(t *testing.T) {
	// Boundary: no settings document keeps the built-in targets.
	registry, err := RegistryFromPolicy(t.Context(), nil)
	if err != nil || registry.Targets().For("go").Module != defaultFrameworkModule {
		t.Fatalf("nil policy registry = %v", err)
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
	if err != nil || registry.Targets().For("go").Module != "example.com/acme/kit" || registry.frameworkFor("typescript", nil) != nil {
		t.Fatalf("configured registry = %v", err)
	}
	// Negative: a contract naming another framework than its target's module refuses to load.
	if _, err := load("framework: {targets: {go: {module: example.com/other/kit, contract: kit.yaml}}}\n"); err == nil ||
		!strings.Contains(err.Error(), "load framework targets") {
		t.Fatalf("mismatched contract = %v", err)
	}
}

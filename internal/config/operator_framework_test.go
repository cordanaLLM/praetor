// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// Digests of documents that carry no framework, forge or topology section, sealed before
// those sections existed. The new fields are omitzero, so these stay byte-identical. They were
// resealed once when the built-in SLSA level default fell from 1 to 0 (#330): the fixture
// profile declares no supply_chain, so its resolved policy changed with the default.
const (
	goldenOperatorDigest = "c66d71446b2ecbc1a01f56591e610a9876991bf4c0831f4173a36144d3219fb7"
	goldenBareDigest     = "1482ca8653033bc7f6631a63d5a4adec6387aaa6129ebb7e5968dae1787bf430"
)

func operatorTestdata(name string) string {
	return filepath.Join("testdata", "operator", name+".yaml")
}

// loadOperatorDocs loads fleet and workstation bodies (either may be empty) through
// LoadOperatorPolicy, the path hook, clients and needs take.
func loadOperatorDocs(t *testing.T, fleet, workstation string) (*EffectivePolicy, error) {
	t.Helper()
	dir := t.TempDir()
	var selection SettingsSelection
	if fleet != "" {
		selection.Fleet.Path = writePolicyFile(t, dir, "fleet.yaml", fleet)
	}
	if workstation != "" {
		selection.Workstation.Path = writePolicyFile(t, dir, "workstation.yaml", workstation)
	}
	return LoadOperatorPolicy(t.Context(), selection)
}

func TestFrameworkLanguages_3D(t *testing.T) {
	languages := FrameworkLanguages()
	if !slices.Equal(languages, []string{"go", "typescript", "python", "rust", "native"}) {
		t.Fatalf("FrameworkLanguages() = %v", languages)
	}
	languages[0] = "mutated"
	if FrameworkLanguages()[0] != "go" {
		t.Fatal("FrameworkLanguages must return a copy")
	}
	for _, language := range []string{"go", "native"} {
		if got, err := ParseFrameworkLanguage(language); err != nil || got != language {
			t.Errorf("ParseFrameworkLanguage(%q) = %q, %v", language, got, err)
		}
	}
	for _, language := range []string{"", "cobol", "Go", "go ", "svelte"} {
		if _, err := ParseFrameworkLanguage(language); err == nil || !strings.Contains(fmt.Sprint(err), "want one of go, typescript") {
			t.Errorf("ParseFrameworkLanguage(%q) accepted or unhelpful: %v", language, err)
		}
	}
}

func TestIsModulePathShaped_3D(t *testing.T) {
	for _, value := range []string{"example.com/acme/kit", "example.com/k", "sub.example.com/a/b/v2"} {
		if !IsModulePathShaped(value) {
			t.Errorf("%q must be module-path shaped", value)
		}
	}
	// Negative and boundary: relative and home-relative paths, a host with no path element,
	// no host, and an absolute path whose first element has a dot.
	for _, value := range []string{"", "kit", "acme/kit", "./x.y/kit", "../acme", "~/x.y/kit", "example.com",
		".example.com/kit", string(filepath.Separator) + filepath.Join("x.y", "kit")} {
		if IsModulePathShaped(value) {
			t.Errorf("%q must not be module-path shaped", value)
		}
	}
}

func TestOperatorFrameworkSchema_Positive(t *testing.T) {
	host := t.TempDir()
	kits := make([]string, MaxBuilderKits)
	for i := range kits {
		kits[i] = fmt.Sprintf("acme/kit-%d", i)
	}
	fleet := fmt.Sprintf("framework:\n  targets:\n    go:\n      module: example.com/%s\n      builder_kits: [%s]\n"+
		"      contract: kit.capabilities.yaml\n    rust: {}\n", strings.Repeat("k", MaxFrameworkModuleBytes-len("example.com/")),
		strings.Join(kits, ", "))
	workstation := fmt.Sprintf("framework:\n  targets:\n    go:\n      checkout: %q\n", filepath.Join(host, "kit"))
	policy, err := loadOperatorDocs(t, fleet, workstation)
	if err != nil {
		t.Fatal(err)
	}
	targets := policy.OperatorSettings().Framework.Targets
	goTarget := targets["go"]
	if len(goTarget.Module) != MaxFrameworkModuleBytes || !slices.Equal(goTarget.BuilderKits, kits) ||
		goTarget.Checkout != filepath.Join(host, "kit") || goTarget.Contract != "kit.capabilities.yaml" {
		t.Fatalf("go target = %+v", goTarget)
	}
	// Boundary: an empty entry lists the language with every value unset.
	if rust, ok := targets["rust"]; !ok || !reflect.DeepEqual(rust, FrameworkTarget{}) {
		t.Fatalf("rust entry = %+v, %v", rust, ok)
	}
	contract, err := policy.ResolveOperatorPath("framework.targets.go.contract", goTarget.Contract)
	if err != nil || filepath.Base(contract) != "kit.capabilities.yaml" || !filepath.IsAbs(contract) {
		t.Fatalf("contract resolves against the fleet file: %q, %v", contract, err)
	}
}

func TestOperatorFrameworkSchema_Negative(t *testing.T) {
	host := t.TempDir()
	tooMany := make([]string, MaxBuilderKits+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("acme/kit-%d", i)
	}
	cases := []struct{ name, fleet, workstation, want string }{
		{"unknown language", "framework: {targets: {cobol: {module: example.com/acme/kit}}}", "", `unknown framework language "cobol"`},
		{"unknown language entry", "framework: {targets: {cobol: {}}}", "", "unknown framework language"},
		{"unknown target key", "framework: {targets: {go: {version: v1}}}", "", "unknown setting"},
		{"nine builder kits", "framework: {targets: {go: {builder_kits: [" + strings.Join(tooMany, ", ") + "]}}}", "", "at most 8"},
		{"kit not owner/repo", "framework: {targets: {go: {builder_kits: [kit]}}}", "", "not an <owner>/<name> coordinate"},
		{"kit repeated", "framework: {targets: {go: {builder_kits: [acme/kit, acme/kit]}}}", "", "repeats acme/kit"},
		{"module without host", "framework: {targets: {go: {module: acme/kit}}}", "", "module path"},
		{"module relative", "framework: {targets: {go: {module: ./x.y/kit}}}", "", "module path"},
		{"module bare host", "framework: {targets: {go: {module: example.com}}}", "", "module path"},
		{"module too long", "framework: {targets: {go: {module: example.com/" + strings.Repeat("k", MaxFrameworkModuleBytes) + "}}}", "", "at most 256 bytes"},
		{"relative checkout", "", "framework: {targets: {go: {checkout: kit}}}", "clean absolute path"},
		{"checkout on python", "", fmt.Sprintf("framework: {targets: {python: {checkout: %q}}}", host), "go target only"},
		{"checkout outside workstation", fmt.Sprintf("framework: {targets: {go: {checkout: %q}}}", host), "", "host data"},
		{"bad migration branch", "framework: {migration_branch: a..b}", "", "branch name"},
		{"bad default owner", "forge: {default_owner: -acme}", "", "invalid GitHub repository owner"},
		{"owner with space", "forge: {default_owner: 'ac me'}", "", "invalid GitHub repository owner"},
		{"owner too long", "forge: {default_owner: " + strings.Repeat("a", 40) + "}", "", "invalid GitHub repository owner"},
		{"bad review bot", "forge: {review_bot: 'acme[bot][bot]'}", "", "[bot] suffix"},
		{"bad reconcile repo", "forge: {reconcile_repos: [acme/app/extra]}", "", "reconcile_repos entry 0"},
		{"uppercase container", "topology: {org_containers: [Acme]}", "", "org_containers entry 0"},
		{"framework not a mapping", "framework: [go]", "", "must be a mapping"},
	}
	for _, tc := range cases {
		if _, err := loadOperatorDocs(t, tc.fleet, tc.workstation); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to contain %q", tc.name, err, tc.want)
		}
	}
}

func TestOperatorFrameworkSchema_BoundaryListsAndMerge(t *testing.T) {
	repos := make([]string, MaxReconcileRepos)
	for i := range repos {
		repos[i] = fmt.Sprintf("acme/app-%d", i)
	}
	containers := make([]string, MaxOrgContainers)
	for i := range containers {
		containers[i] = fmt.Sprintf("org-%d", i)
	}
	fleet := fmt.Sprintf("forge: {default_owner: %s, reconcile_repos: [%s], review_bot: 'acme-review[bot]'}\n"+
		"topology: {org_containers: [acme, %s]}\nframework: {targets: {go: {builder_kits: [acme/kit]}}}\n",
		strings.Repeat("a", 39), strings.Join(repos, ", "), strings.Join(containers[:MaxOrgContainers-2], ", "))
	workstation := "topology: {org_containers: [acme-labs, acme]}\nframework: {targets: {go: {builder_kits: [acme/other]}}}\n"
	policy, err := loadOperatorDocs(t, fleet, workstation)
	if err != nil {
		t.Fatal(err)
	}
	settings := policy.OperatorSettings()
	if len(settings.Forge.ReconcileRepos) != MaxReconcileRepos || len(settings.Forge.DefaultOwner) != 39 ||
		settings.Forge.ReviewBot != "acme-review[bot]" {
		t.Fatalf("forge = %+v", settings.Forge)
	}
	// Append: the workstation's new container joins the fleet list, the repeated one does not.
	wantContainers := append(append([]string{"acme"}, containers[:MaxOrgContainers-2]...), "acme-labs")
	if !slices.Equal(settings.Topology.OrgContainers, wantContainers) {
		t.Fatalf("org_containers = %v", settings.Topology.OrgContainers)
	}
	// Replace: the later layer's builder kits replace the fleet's.
	if kits := settings.Framework.Targets["go"].BuilderKits; !slices.Equal(kits, []string{"acme/other"}) {
		t.Fatalf("builder_kits = %v", kits)
	}
	// Boundary: one repository and one container past the limit are refused.
	if _, err := loadOperatorDocs(t, fmt.Sprintf("forge: {reconcile_repos: [%s, acme/extra]}", strings.Join(repos, ", ")), ""); err == nil {
		t.Error("257 reconcile repositories accepted")
	}
	if _, err := loadOperatorDocs(t, fleet, "topology: {org_containers: [acme-labs, acme-extra]}"); err == nil ||
		!strings.Contains(err.Error(), "exceeds 64") {
		t.Errorf("65 merged containers: %v", err)
	}
}

func TestOperatorFrameworkUnsetIsEmpty(t *testing.T) {
	defaults := DefaultOperatorSettings()
	if !reflect.DeepEqual(defaults.Framework, FrameworkSettings{}) || !reflect.DeepEqual(defaults.Forge, ForgeSettings{}) ||
		!reflect.DeepEqual(defaults.Topology, TopologySettings{}) {
		t.Fatalf("new sections must default to unset: %+v %+v %+v", defaults.Framework, defaults.Forge, defaults.Topology)
	}
	policy, err := LoadOperatorPolicy(t.Context(), SettingsSelection{})
	if err != nil || policy != nil {
		t.Fatalf("no document selected = %v, %v; want a nil policy", policy, err)
	}
	if !reflect.DeepEqual(policy.OperatorSettings(), defaults) {
		t.Fatal("a nil policy must report the built-in settings")
	}
	settings, err := LoadOperatorSettings(t.Context(), SettingsSelection{})
	if err != nil || len(settings.Framework.Targets) != 0 || settings.Forge.DefaultOwner != "" {
		t.Fatalf("LoadOperatorSettings without documents = %+v, %v", settings.Framework, err)
	}
}

// HISS-20 replay, both directions: documents without the new sections keep the digests they
// had before the sections existed, and a document with them reaches every field and seals
// a different digest.
func TestOperatorFrameworkReplayAndDigestGolden(t *testing.T) {
	root := policyFixture(t, "", "", "")
	without := EffectiveOptions{Root: root, FleetPath: operatorTestdata("fleet"),
		OrganizationPath: operatorTestdata("organization"), DeploymentPath: operatorTestdata("deployment")}
	for name, tc := range map[string]struct {
		opts EffectiveOptions
		want string
	}{"operator documents": {without, goldenOperatorDigest}, "no operator document": {EffectiveOptions{Root: root}, goldenBareDigest}} {
		policy, err := LoadEffectivePolicyContext(t.Context(), tc.opts)
		if err != nil || policy.SHA256 != tc.want {
			t.Errorf("%s: digest %s, %v; want %s", name, policy.SHA256, err, tc.want)
		}
	}
	with := without
	with.WorkstationPath = operatorTestdata("framework")
	policy, err := LoadEffectivePolicyContext(t.Context(), with)
	if err != nil {
		t.Fatal(err)
	}
	if policy.SHA256 == goldenOperatorDigest {
		t.Fatal("the framework, forge and topology sections must be sealed into the digest")
	}
	settings := policy.OperatorSettings()
	want := FrameworkSettings{MigrationBranch: "refactor/acme-adoption", Targets: map[string]FrameworkTarget{
		"go":         {Module: "example.com/acme/kit", BuilderKits: []string{"acme/kit", "acme/kit-extras"}, Contract: "frameworks/kit.capabilities.yaml"},
		"typescript": {Module: "example.com/acme/ui", BuilderKits: []string{"acme/ui"}},
	}}
	if !reflect.DeepEqual(settings.Framework, want) {
		t.Errorf("framework = %+v", settings.Framework)
	}
	if !reflect.DeepEqual(settings.Forge, ForgeSettings{DefaultOwner: "acme", ReconcileRepos: []string{"acme/kit", "acme/app"}, ReviewBot: "acme-review[bot]"}) {
		t.Errorf("forge = %+v", settings.Forge)
	}
	if !slices.Equal(settings.Topology.OrgContainers, []string{"acme", "acme-labs"}) || settings.Clients.Mode != ClientModeStrict {
		t.Errorf("topology = %+v, existing sections still merged: %s", settings.Topology, settings.Clients.Mode)
	}
	if err := policy.VerifyDigest(); err != nil {
		t.Fatalf("sealed digest does not verify: %v", err)
	}
}

func TestSelectOperatorPolicy_3D(t *testing.T) {
	dir := t.TempDir()
	workstation := writePolicyFile(t, dir, "workstation.yaml", "framework: {targets: {go: {module: example.com/acme/kit, contract: kit.yaml}}}\n")
	// Positive: the selected document loads, and its relative contract resolves against it.
	policy, err := SelectOperatorPolicy(t.Context(), SettingsRequest{WorkstationFlag: workstation})
	if err != nil {
		t.Fatal(err)
	}
	contract, err := policy.ResolveOperatorPath("framework.targets.go.contract", policy.OperatorSettings().Framework.Targets["go"].Contract)
	if err != nil || contract != filepath.Join(dir, "kit.yaml") {
		t.Fatalf("contract = %q, %v", contract, err)
	}
	// Negative: a selected document that does not exist is an error, never the defaults.
	if _, err := SelectOperatorPolicy(t.Context(), SettingsRequest{FleetFlag: filepath.Join(dir, "missing.yaml")}); err == nil {
		t.Fatal("a missing selected document loaded")
	}
	// Boundary: nothing selected is a nil policy with the built-in settings.
	none, err := SelectOperatorPolicy(t.Context(), SettingsRequest{Getenv: func(string) string { return "" }})
	if err != nil || none != nil || len(none.OperatorSettings().Framework.Targets) != 0 {
		t.Fatalf("nothing selected = %v, %v", none, err)
	}
}

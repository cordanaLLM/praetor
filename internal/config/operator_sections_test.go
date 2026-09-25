// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/clientid"
)

var hostMarker = regexp.MustCompile(`HOST(/[A-Za-z0-9_./-]+)?`)

// operatorFixture copies testdata/operator into a temporary directory, replacing HOST in the
// workstation layer with a real absolute directory so its host paths hold on every OS.
func operatorFixture(t *testing.T) (EffectiveOptions, string) {
	t.Helper()
	dir, host := t.TempDir(), t.TempDir()
	for _, name := range []string{"fleet", "organization", "deployment", "workstation"} {
		data, err := os.ReadFile(filepath.Join("testdata", "operator", name+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		body := hostMarker.ReplaceAllStringFunc(string(data), func(match string) string {
			return filepath.Join(host, filepath.FromSlash(strings.TrimPrefix(match, "HOST")))
		})
		writePolicyFile(t, dir, name+".yaml", body)
	}
	opts := EffectiveOptions{Root: policyFixture(t, "", "", ""),
		FleetPath: filepath.Join(dir, "fleet.yaml"), OrganizationPath: filepath.Join(dir, "organization.yaml"),
		DeploymentPath: filepath.Join(dir, "deployment.yaml"), WorkstationPath: filepath.Join(dir, "workstation.yaml")}
	return opts, host
}

func TestOperatorSettingsFourLayersMergeWithContributors(t *testing.T) {
	opts, host := operatorFixture(t)
	policy, err := LoadEffectivePolicyContext(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	settings := policy.OperatorSettings()
	agy := settings.Clients.Selected[clientid.AGY]
	checks := map[string][2]any{
		"mode (deployment tightens)": {settings.Clients.Mode, ClientModeStrict},
		"max age (fleet)":            {settings.Clients.VerifiedMaxAge, 72 * time.Hour},
		"scopes (fleet)":             {agy.Scopes, []string{ScopeGlobal, ScopeWorkspace}},
		"grants append":              {agy.Permissions.Allow, []string{"mcp(hindsight/hindsight_list_knowledge_pages)", "command(praetorctl)"}},
		"agy grants opted in":        {agy.Permissions.Manage, true},
		"codex grants not managed":   {settings.Clients.Selected[clientid.Codex].Permissions.Manage, false},
		"binary (workstation)":       {agy.Binary, filepath.Join(host, "bin", "agy")},
		"deny appends de-duplicated": {settings.Hooks.CommandPolicy.Deny, []string{`\bexample-org/`, `\bsecond-org/`}},
		"hook scope (organization)":  {settings.Hooks.Scope, HookScopeAll},
		"python replaced":            {settings.Hooks.Python, [][]string{{filepath.Join(host, "python", "python3"), "-X", "utf8"}, {"python3"}}},
		"interval (fleet)":           {settings.Update.Interval, 30 * time.Minute},
		"checkout (workstation)":     {settings.Update.Checkout, filepath.Join(host, "fork")},
		"remote default":             {settings.Update.Remote, "origin"},
		"deny contributors":          {policy.OperatorFields["hooks.command_policy.deny"], []string{"fleet", "organization"}},
		"grant contributors":         {policy.OperatorFields["clients.selected.agy.permissions.allow"], []string{"fleet", "organization"}},
		"signed contributors":        {policy.OperatorFields["update.require_signed"], []string{"fleet"}},
	}
	for name, pair := range checks {
		if !reflect.DeepEqual(pair[0], pair[1]) {
			t.Errorf("%s: got %#v, want %#v", name, pair[0], pair[1])
		}
	}
	registry, err := policy.ResolveOperatorPath("clients.selected.agy.registry", agy.Registry)
	if err != nil || registry != filepath.Join(filepath.Dir(opts.FleetPath), "registry.json") {
		t.Fatalf("registry resolves against the fleet file: %q, %v", registry, err)
	}
}

func TestOperatorSettingsDefaultsGovernEveryKnownClient(t *testing.T) {
	settings := DefaultOperatorSettings()
	if settings.Clients.Mode != ClientModeAdvisory || settings.Clients.Govern != GovernPresent || !settings.Update.RequireSigned {
		t.Fatalf("unexpected defaults: %+v", settings.Clients)
	}
	for _, id := range clientid.Known() {
		selection, ok := settings.Clients.Selected[id]
		if !ok || !selection.Required || !selection.Plugin || selection.Permissions.Manage {
			t.Errorf("client %s default: %+v", id, selection)
		}
	}
	var absent *EffectivePolicy
	if !reflect.DeepEqual(absent.OperatorSettings(), settings) {
		t.Fatal("nil policy must report the built-in settings")
	}
}

// Grant management is an operator-host opt-in: the engine default is false for every client,
// and only a layer that sets permissions.manage turns it on.
func TestOperatorSettingsGrantManagementIsAnOperatorOptIn(t *testing.T) {
	root := policyFixture(t, "", "", "")
	unmanaged, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	for id, selection := range unmanaged.OperatorSettings().Clients.Selected {
		if selection.Permissions.Manage {
			t.Errorf("client %s manages grants without an operator layer", id)
		}
	}
	operator := writePolicyFile(t, t.TempDir(), "fleet.yaml", "clients: {selected: {agy: {permissions: {manage: true}}}}\n")
	managed, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root, FleetPath: operator})
	if err != nil {
		t.Fatal(err)
	}
	selected := managed.OperatorSettings().Clients.Selected
	if !selected[clientid.AGY].Permissions.Manage || selected[clientid.Claude].Permissions.Manage {
		t.Fatalf("the operator layer must turn grant management on for agy only: %+v", selected[clientid.AGY])
	}
	if got := managed.OperatorFields["clients.selected.agy.permissions.manage"]; len(got) != 1 || got[0] != "fleet" {
		t.Fatalf("manage contributor: %v", got)
	}
}

func TestOperatorSettingsGovernListedSelectsOnlyListedClients(t *testing.T) {
	root := policyFixture(t, "", "", "")
	body := "clients:\n  govern: listed\n  selected:\n    codex: {required: false}\n    agy: {}\n"
	path := writePolicyFile(t, t.TempDir(), "fleet.yaml", body)
	policy, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root, FleetPath: path})
	if err != nil {
		t.Fatal(err)
	}
	selected := policy.OperatorSettings().Clients.Selected
	if len(selected) != 2 || selected[clientid.Codex].Required || !selected[clientid.AGY].Required {
		t.Fatalf("listed selection: %+v", selected)
	}
}

func TestOperatorSettingsSectionOnlyAndEmptySections(t *testing.T) {
	root := policyFixture(t, "", "", "")
	for _, body := range []string{"hooks: {scope: governed}\n", "clients: {}\nhooks: {}\nupdate: {}\n", "update:\n  channel: push\n"} {
		path := writePolicyFile(t, t.TempDir(), "deployment.yaml", body)
		policy, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root, DeploymentPath: path})
		if err != nil {
			t.Fatalf("%q: %v", body, err)
		}
		if !reflect.DeepEqual(policy.OperatorSettings(), DefaultOperatorSettings()) || policy.VerifyDigest() != nil {
			t.Fatalf("%q must resolve to the defaults with a valid digest", body)
		}
	}
}

func TestOperatorSettingsRejectInvalidDocuments(t *testing.T) {
	root := policyFixture(t, "", "", "")
	host := t.TempDir()
	cases := map[string]string{
		"misspelled key":        "clients:\n  mdoe: strict\n",
		"unknown client":        "clients:\n  selected:\n    cursor: {}\n",
		"interpolation":         "update:\n  remote: \"$ORIGIN\"\n",
		"regex":                 "hooks:\n  command_policy:\n    deny: ['(']\n",
		"host path in fleet":    fmt.Sprintf("update:\n  checkout: %q\n", host),
		"python path in fleet":  fmt.Sprintf("hooks:\n  python: [%q]\n", filepath.Join(host, "python3")),
		"string for bool":       "update:\n  require_signed: \"yes\"\n",
		"reserved channel":      "update:\n  channel: release\n",
		"reserved source":       "update:\n  source: artifact\n",
		"interval under range":  "update:\n  interval: 4m59s\n",
		"interval over range":   "update:\n  interval: 24h1s\n",
		"uppercase pin":         "update:\n  pin: 0123456789ABCDEF0123456789ABCDEF01234567\n",
		"branch traversal":      "update:\n  branch: main..evil\n",
		"remote option":         "update:\n  remote: -upload-pack\n",
		"empty scopes":          "clients:\n  selected:\n    agy: {scopes: []}\n",
		"duplicate scope":       "clients:\n  selected:\n    agy: {scopes: [global, global]}\n",
		"section not a mapping": "clients: []\n",
		"null client entry":     "clients:\n  selected:\n    agy:\n",
		"env reserved for X2":   "clients:\n  selected:\n    agy: {env: {}}\n",
		"dotted key":            "clients:\n  selected:\n    agy.required: true\n",
	}
	for name, body := range cases {
		path := writePolicyFile(t, t.TempDir(), "fleet.yaml", body)
		if _, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root, FleetPath: path}); err == nil {
			t.Errorf("%s accepted: %q", name, body)
		}
	}
	path := writePolicyFile(t, t.TempDir(), "fleet.yaml", "clients:\n  mdoe: strict\n")
	_, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root, FleetPath: path})
	if err == nil || !strings.Contains(err.Error(), `"clients.mdoe"`) {
		t.Fatalf("a misspelled key must be named: %v", err)
	}
}

func TestOperatorSettingsLaterLayerCannotLoosen(t *testing.T) {
	root := policyFixture(t, "", "", "")
	cases := map[string][2]string{
		"mode":     {"clients: {mode: strict}\n", "clients: {mode: advisory}\n"},
		"required": {"clients: {selected: {agy: {required: true}}}\n", "clients: {selected: {agy: {required: false}}}\n"},
		"signed":   {"update: {require_signed: true}\n", "update: {require_signed: false}\n"},
		"scope":    {"hooks: {scope: all}\n", "hooks: {scope: governed}\n"},
		"govern":   {"clients: {govern: present}\n", "clients: {govern: listed}\n"},
		"registry": {"clients: {selected: {agy: {registry: a.json}}}\n", "clients: {selected: {agy: {registry: b.json}}}\n"},
	}
	for name, bodies := range cases {
		dir := t.TempDir()
		opts := EffectiveOptions{Root: root, FleetPath: writePolicyFile(t, dir, "fleet.yaml", bodies[0]),
			WorkstationPath: writePolicyFile(t, dir, "workstation.yaml", bodies[1])}
		_, err := LoadEffectivePolicyContext(t.Context(), opts)
		if err == nil || !strings.Contains(err.Error(), "fleet") || !strings.Contains(err.Error(), "workstation") {
			t.Errorf("%s: loosening must fail naming both layers: %v", name, err)
		}
	}
	// A layer may loosen a built-in default: defaults are not a layer.
	path := writePolicyFile(t, t.TempDir(), "fleet.yaml", "update: {require_signed: false}\nclients: {selected: {agy: {required: false}}}\n")
	if _, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root, FleetPath: path}); err != nil {
		t.Fatalf("a first layer may set a default's other value: %v", err)
	}
}

func denyDocument(from, count int) string {
	var body strings.Builder
	body.WriteString("hooks:\n  command_policy:\n    deny:\n")
	for i := from; i < from+count; i++ {
		fmt.Fprintf(&body, "      - 'pattern-%03d'\n", i)
	}
	return body.String()
}

func TestOperatorSettingsDenyListBounds(t *testing.T) {
	root := policyFixture(t, "", "", "")
	load := func(fleet, workstation string) error {
		dir := t.TempDir()
		opts := EffectiveOptions{Root: root, FleetPath: writePolicyFile(t, dir, "fleet.yaml", fleet)}
		if workstation != "" {
			opts.WorkstationPath = writePolicyFile(t, dir, "workstation.yaml", workstation)
		}
		_, err := LoadEffectivePolicyContext(t.Context(), opts)
		return err
	}
	if err := load(denyDocument(0, 64), ""); err != nil {
		t.Fatalf("64 patterns refused: %v", err)
	}
	if err := load(denyDocument(0, 65), ""); err == nil {
		t.Fatal("65 patterns accepted")
	}
	if err := load(denyDocument(0, 40), denyDocument(16, 48)); err != nil {
		t.Fatalf("64 distinct merged patterns refused: %v", err)
	}
	if err := load(denyDocument(0, 40), denyDocument(15, 50)); err == nil {
		t.Fatal("65 distinct merged patterns accepted")
	}
}

func TestValidateCommandPolicyDeny(t *testing.T) {
	if err := ValidateCommandPolicyDeny([]string{`^git\s+push\b.*--force$`, strings.Repeat("a", MaxCommandPolicyPatternBytes)}); err != nil {
		t.Fatalf("valid patterns refused: %v", err)
	}
	for name, patterns := range map[string][]string{
		"empty":      {""},
		"oversized":  {strings.Repeat("a", MaxCommandPolicyPatternBytes+1)},
		"control":    {"a\nb"},
		"no compile": {"(?P<"},
		"too many":   slices.Repeat([]string{"a"}, MaxCommandPolicyDenyPatterns+1),
	} {
		if err := ValidateCommandPolicyDeny(patterns); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestResolvePolicyValidatesCallerBuiltSettings(t *testing.T) {
	for name, setting := range map[string]OperatorSetting{
		"unknown path":   {Path: "update.unknown", Value: "x"},
		"invalid value":  {Path: "update.pin", Value: "not-a-commit"},
		"bool shape":     {Path: "update.require_signed", Value: "yes"},
		"list for text":  {Path: "update.remote", List: []string{"origin"}},
		"control byte":   {Path: "update.remote", Value: "ori\ngin"},
		"host in fleet":  {Path: "update.bin_dir", Value: t.TempDir()},
		"unknown client": {Path: "clients.selected.cursor"},
	} {
		layer := policyTestLayer("fleet", 50)
		layer.Settings = []OperatorSetting{setting}
		if _, err := ResolvePolicy(t.Context(), []PolicyLayer{layer}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestOperatorSettingsDigest(t *testing.T) {
	plain, err := ResolvePolicy(t.Context(), []PolicyLayer{policyTestLayer("fleet", 50)})
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := json.Marshal(struct {
		Policy  ResolvedPolicy
		Sources []PolicySource
		Fields  map[string][]string
	}{plain.Policy, plain.Sources, plain.Fields})
	if err != nil || plain.SHA256 != policyDigest(legacy) || plain.Operator != nil {
		t.Fatalf("a policy without operator settings must keep its previous digest: %v", err)
	}
	layer := policyTestLayer("fleet", 50)
	layer.Settings = []OperatorSetting{{Path: "clients.mode", Value: ClientModeStrict}}
	layer.Source.Path = "/first/mount/fleet.yaml"
	one, err := ResolvePolicy(t.Context(), []PolicyLayer{layer})
	if err != nil || one.SHA256 == plain.SHA256 {
		t.Fatalf("operator settings must enter the digest: %v", err)
	}
	layer.Source.Path = "/other/mount/fleet.yaml"
	two, err := ResolvePolicy(t.Context(), []PolicyLayer{layer})
	if err != nil || two.SHA256 != one.SHA256 {
		t.Fatalf("relocating a layer changed the digest: %v", err)
	}
}

func TestOperatorSettingsRetainedDigestRoundTripAndTamper(t *testing.T) {
	opts, _ := operatorFixture(t)
	retain := func() *EffectivePolicy {
		policy, err := LoadEffectivePolicyContext(t.Context(), opts)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(policy)
		if err != nil {
			t.Fatal(err)
		}
		var retained EffectivePolicy
		if err := json.Unmarshal(data, &retained); err != nil {
			t.Fatal(err)
		}
		return &retained
	}
	if err := retain().VerifyDigest(); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	for name, change := range map[string]func(*EffectivePolicy){
		"setting":        func(p *EffectivePolicy) { p.Operator.Clients.Mode = ClientModeAdvisory },
		"contributor":    func(p *EffectivePolicy) { p.OperatorFields["clients.mode"] = []string{"missing"} },
		"settings alone": func(p *EffectivePolicy) { p.OperatorFields = nil },
		"fields alone":   func(p *EffectivePolicy) { p.Operator = nil },
	} {
		policy := retain()
		change(policy)
		if err := policy.VerifyDigest(); err == nil {
			t.Errorf("%s: changed operator metadata accepted", name)
		}
	}
}

func TestOperatorSchemaKeysHaveFields(t *testing.T) {
	settings := DefaultOperatorSettings()
	selection := defaultClientSelection()
	for path, spec := range operatorSpecs {
		if spec.kind == kindMapping {
			continue
		}
		targets, key := settings.targets(), path
		if field, ok := strings.CutPrefix(path, selectedPrefix+"*"); ok {
			targets, key = selection.targets(), strings.TrimPrefix(field, ".")
		}
		if _, ok := targets[key]; !ok {
			t.Errorf("schema key %s has no settings field", path)
		}
	}
}

func TestResolveOperatorPathBoundaries(t *testing.T) {
	absolute := t.TempDir()
	var policy *EffectivePolicy
	for value, want := range map[string]string{"": "", absolute: absolute} {
		got, err := policy.ResolveOperatorPath("update.allowed_signers", value)
		if err != nil || got != want {
			t.Errorf("ResolveOperatorPath(%q) = %q, %v", value, got, err)
		}
	}
	if _, err := policy.ResolveOperatorPath("update.allowed_signers", "signers"); err == nil {
		t.Fatal("a relative path without a contributing layer resolved")
	}
	layer := policyTestLayer("fleet", 50)
	layer.Settings = []OperatorSetting{{Path: "update.allowed_signers", Value: "signers"}}
	resolved, err := ResolvePolicy(t.Context(), []PolicyLayer{layer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolved.ResolveOperatorPath("update.allowed_signers", "signers"); err == nil {
		t.Fatal("a layer without a file resolved a relative path")
	}
}

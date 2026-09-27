package needs

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestContractEcosystemGrammar_3D(t *testing.T) {
	base := "version: 1\nframework: example.com/acme/ui\necosystem: %s\n%spackages:\n  - import: example.com/acme/ui/forms\n" +
		"    capabilities: [ui.forms]\n    %s: [%s]\n"
	valid := []struct{ ecosystem, field, names, foundations string }{
		{"npm", "replaces", "zod, \"@sveltejs/kit\"", ""},
		{"pypi", "adapts", "Typing_Extensions, requests", ""},
		{"cargo", "wraps", "tokio, serde_json", ""},
		{"system", "tooling_for", "libavcodec, gtk+-3.0", ""},
		{"go", "replaces", "github.com/acme/kv/v2", "foundations: [log/slog, go.uber.org/fx]\n"},
	}
	for _, tc := range valid {
		contract := fmt.Sprintf(base, tc.ecosystem, tc.foundations, tc.field, tc.names)
		if _, err := parseFrameworkContract([]byte(contract), "example.com/acme/ui"); err != nil {
			t.Errorf("%s %s: %v", tc.ecosystem, tc.field, err)
		}
	}
	invalid := []struct{ name, ecosystem, field, names, foundations string }{
		{"unknown ecosystem", "maven", "replaces", "junit", ""},
		{"npm uppercase", "npm", "replaces", "Zod", ""},
		{"go name not a module", "go", "replaces", "requests", ""},
		{"go standard import outside foundations", "go", "wraps", "log/slog", ""},
		{"cargo leading digit", "cargo", "replaces", "9lives", ""},
		{"pypi trailing dash", "pypi", "replaces", "requests-", ""},
		{"system empty", "system", "replaces", "\"\"", ""},
		{"go foundation not a path", "go", "replaces", "github.com/acme/kv", "foundations: [\"Log Slog\"]\n"},
	}
	for _, tc := range invalid {
		contract := fmt.Sprintf(base, tc.ecosystem, tc.foundations, tc.field, tc.names)
		if _, err := parseFrameworkContract([]byte(contract), "example.com/acme/ui"); err == nil {
			t.Errorf("%s: accepted", tc.name)
		}
	}
}

func TestContractNameListBounds(t *testing.T) {
	names := func(count int) string {
		list := make([]string, count)
		for i := range list {
			list[i] = fmt.Sprintf("pkg-%d", i)
		}
		return strings.Join(list, ", ")
	}
	contract := func(field string, count int, foundations int) string {
		return fmt.Sprintf("version: 1\nframework: example.com/acme/ui\necosystem: npm\nfoundations: [%s]\npackages:\n"+
			"  - import: example.com/acme/ui/forms\n    capabilities: [ui.forms]\n    %s: [%s]\n", names(foundations), field, names(count))
	}
	for _, field := range []string{"replaces", "adapts", "wraps", "tooling_for"} {
		if _, err := parseFrameworkContract([]byte(contract(field, maxContractReplaces, 1)), ""); err != nil {
			t.Errorf("%s bound is inclusive: %v", field, err)
		}
		if _, err := parseFrameworkContract([]byte(contract(field, maxContractReplaces+1, 1)), ""); err == nil {
			t.Errorf("%s one past the bound accepted", field)
		}
	}
	if _, err := parseFrameworkContract([]byte(contract("replaces", 1, maxContractFoundations)), ""); err != nil {
		t.Errorf("foundations bound is inclusive: %v", err)
	}
	if _, err := parseFrameworkContract([]byte(contract("replaces", 1, maxContractFoundations+1)), ""); err == nil {
		t.Error("foundations one past the bound accepted")
	}
}

// exportRoundTrip renders index as a contract, parses the rendered bytes back and returns
// both, failing when they differ: parse(export(i)) == export(i).
func exportRoundTrip(t *testing.T, index *FrameworkIndex, ecosystem string) (*frameworkContract, []string) {
	t.Helper()
	var skipped []string
	exported := contractFromIndex(index, ecosystem, &skipped)
	data, err := yaml.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseFrameworkContract(data, index.Name)
	if err != nil {
		t.Fatalf("exported contract does not parse: %v\n%s", err, data)
	}
	if !reflect.DeepEqual(parsed, exported) {
		t.Fatalf("round trip changed the contract:\n got %+v\nwant %+v", parsed, exported)
	}
	return exported, skipped
}

func TestContractExportRoundTrip_Positive(t *testing.T) {
	// A declared contract index exports to the same claims and re-declares the same index.
	declared, err := InspectFramework(t.Context(), FrameworkSource{Contract: writeAcmeContract(t)})
	if err != nil {
		t.Fatal(err)
	}
	contract, skipped := exportRoundTrip(t, declared, "go")
	if len(skipped) != 0 || len(contract.Packages) != 4 || len(contract.Foundations) != 2 {
		t.Fatalf("declared export = %+v, skipped %v", contract, skipped)
	}
	redeclared := &FrameworkIndex{Name: declared.Name, Basis: declared.Basis, Version: declared.Version,
		Packages: map[string]FrameworkPackage{}, Capabilities: map[CapabilityKey][]string{}}
	packages := beginContractIndex(redeclared, contract, declared.Contract)
	for i := range packages {
		addContractPackage(redeclared, &packages[i])
	}
	for name, pair := range map[string][2]any{
		"packages": {redeclared.Packages, declared.Packages}, "capabilities": {redeclared.Capabilities, declared.Capabilities},
		"replacements": {redeclared.Replacements, declared.Replacements}, "adaptations": {redeclared.Adaptations, declared.Adaptations},
		"wrappers": {redeclared.Wrappers, declared.Wrappers}, "tooling": {redeclared.Tooling, declared.Tooling},
		"foundations": {redeclared.Foundations, declared.Foundations},
	} {
		if !reflect.DeepEqual(pair[0], pair[1]) {
			t.Errorf("re-declared %s = %v, want %v", name, pair[0], pair[1])
		}
	}
}

// The built-in tables of every language export to a contract that parses back unchanged,
// carrying their replacement, adapter, relationship and foundation claims; an entry the
// contract grammar cannot carry is listed, never silently dropped.
func TestContractExportBuiltinTables(t *testing.T) {
	for _, language := range []string{"go", "typescript", "python", "rust", "native"} {
		export, err := ExportFrameworkContract(t.Context(), language, FrameworkSource{Module: defaultFrameworkModule}, nil)
		if err != nil {
			t.Fatalf("%s: %v", language, err)
		}
		if export.Framework != legacyTargets()[language].Module || export.Packages == 0 {
			t.Fatalf("%s export = %+v", language, export)
		}
		parsed, err := parseFrameworkContract(export.Data, export.Framework)
		if err != nil || parsed.ecosystem() != languageEcosystem(language) {
			t.Fatalf("%s export does not parse as its ecosystem: %v", language, err)
		}
		claims := 0
		for _, pkg := range parsed.Packages {
			claims += len(pkg.Replaces) + len(pkg.Adapts) + len(pkg.Wraps) + len(pkg.ToolingFor)
		}
		if claims == 0 {
			t.Errorf("%s export carries no claim", language)
		}
		if language == "python" && !strings.Contains(strings.Join(export.Skipped, "\n"), "not a contract capability key") {
			t.Errorf("python export must list the catalog capability the contract grammar rejects: %v", export.Skipped)
		}
	}
	goExport, err := ExportFrameworkContract(t.Context(), "go", FrameworkSource{Module: defaultFrameworkModule}, nil)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseFrameworkContract(goExport.Data, defaultFrameworkModule)
	if err != nil || len(parsed.Foundations) == 0 {
		t.Fatalf("go export lost the built-in foundations: %v %+v", err, parsed)
	}
}

func TestContractExport_NegativeAndBoundary(t *testing.T) {
	// Negative: nothing to export without a configured framework, and an unknown language.
	if _, err := ExportFrameworkContract(t.Context(), "go", FrameworkSource{}, Targets{"typescript": {Module: "example.com/acme/ui"}}); err == nil ||
		!strings.Contains(err.Error(), "set framework.targets.go.module") {
		t.Fatalf("unconfigured go export = %v", err)
	}
	if _, err := ExportFrameworkContract(t.Context(), "python", FrameworkSource{}, Targets{"go": {Module: "example.com/acme/kit"}}); err == nil {
		t.Fatal("an unconfigured python target exported")
	}
	if _, err := ExportFrameworkContract(t.Context(), "cobol", FrameworkSource{}, nil); err == nil {
		t.Fatal("an unknown language exported")
	}
	// Boundary: a configured module the built-in tables do not describe exports a contract
	// that names it and declares no package.
	export, err := ExportFrameworkContract(t.Context(), "typescript", FrameworkSource{}, Targets{"typescript": {Module: "example.com/acme/ui"}})
	if err != nil || export.Framework != "example.com/acme/ui" || export.Packages != 0 {
		t.Fatalf("undescribed module export = %+v, %v", export, err)
	}
	// Boundary: a go module the catalog does not describe exports no catalog foundation;
	// reconciliation would drop those claims for it, so the contract must not carry them.
	goExport, err := ExportFrameworkContract(t.Context(), "go", FrameworkSource{Module: "example.com/acme/kit"}, nil)
	if err != nil {
		t.Fatalf("undescribed go module export: %v", err)
	}
	goParsed, err := parseFrameworkContract(goExport.Data, "example.com/acme/kit")
	if err != nil || len(goParsed.Foundations) != 0 {
		t.Fatalf("undescribed go module export claims foundations: %v %+v", err, goParsed.Foundations)
	}
	// Boundary: a configured contract exports its own claims unchanged.
	contract := writeAcmeContract(t)
	own, err := ExportFrameworkContract(t.Context(), "go", FrameworkSource{Contract: contract, Module: "example.com/acme/kit"}, nil)
	if err != nil || own.Packages != 4 || strings.Contains(string(own.Data), defaultFrameworkModule) {
		t.Fatalf("contract export = %+v, %v", own, err)
	}
}

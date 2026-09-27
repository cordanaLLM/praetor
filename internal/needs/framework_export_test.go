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

// Every configured target exports to a contract that parses back as its language's
// ecosystem with the target contract's packages and claims: the export of a configured
// contract is that contract.
func TestContractExportConfiguredTargets(t *testing.T) {
	targets := acmeTargets()
	for _, language := range []string{"go", "typescript", "python", "rust", "native"} {
		export, err := ExportFrameworkContract(t.Context(), language, acmeDeclared(), targets)
		if err != nil {
			t.Fatalf("%s: %v", language, err)
		}
		declared, err := InspectFramework(t.Context(), FrameworkSource{Contract: targets[language].Contract, Module: targets[language].Module})
		if err != nil {
			t.Fatal(err)
		}
		if export.Framework != targets[language].Module || export.Packages != len(declared.Packages) || len(export.Skipped) != 0 {
			t.Fatalf("%s export = %+v, want %d packages", language, export, len(declared.Packages))
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
	}
	goExport, err := ExportFrameworkContract(t.Context(), "go", acmeDeclared(), targets)
	if err != nil {
		t.Fatal(err)
	}
	if parsed, err := parseFrameworkContract(goExport.Data, acmeKit); err != nil || len(parsed.Foundations) != 2 {
		t.Fatalf("go export lost the contract's foundations: %v %+v", err, parsed)
	}
}

func TestContractExport_NegativeAndBoundary(t *testing.T) {
	// Negative: nothing to export without a configured framework, and an unknown language.
	if _, err := ExportFrameworkContract(t.Context(), "go", FrameworkSource{}, Targets{"typescript": {Module: "example.com/acme/ui"}}); err == nil ||
		!strings.Contains(err.Error(), "set framework.targets.go.module") {
		t.Fatalf("unconfigured go export = %v", err)
	}
	if _, err := ExportFrameworkContract(t.Context(), "python", FrameworkSource{}, Targets{"go": {Module: acmeKit}}); err == nil {
		t.Fatal("an unconfigured python target exported")
	}
	if _, err := ExportFrameworkContract(t.Context(), "cobol", FrameworkSource{}, nil); err == nil {
		t.Fatal("an unknown language exported")
	}
	// Boundary: a target configured with a module and no contract exports an empty contract
	// that names it: praetor ships no framework data to fill it.
	for _, language := range []string{"go", "typescript"} {
		module := acmeTargets()[language].Module
		export, err := ExportFrameworkContract(t.Context(), language, FrameworkSource{Module: module}, Targets{language: {Module: module}})
		if err != nil || export.Framework != module || export.Packages != 0 {
			t.Fatalf("%s module-only export = %+v, %v", language, export, err)
		}
		parsed, err := parseFrameworkContract(export.Data, module)
		if err != nil || len(parsed.Packages) != 0 || len(parsed.Foundations) != 0 {
			t.Fatalf("%s module-only export claims data: %v %+v", language, err, parsed)
		}
	}
	// Boundary: a checkout exports only the configured contract's packages it provides.
	checkout := setupFrameworkCheckout(t, acmeKit, "db")
	writeFixture(t, checkout, "db/db.go", "package db\n\ntype Pool struct{}\n")
	observed, err := ExportFrameworkContract(t.Context(), "go", FrameworkSource{Checkout: checkout, Contract: writeAcmeContract(t), Module: acmeKit}, nil)
	if err != nil || observed.Packages != 1 || !strings.Contains(string(observed.Data), acmeKit+"/db") {
		t.Fatalf("checkout export = %+v, %v", observed, err)
	}
}

package needs

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A contract's umbrellas parse into the index; every malformed description fails the
// contract instead of being skipped.
func TestContractUmbrellaValidation_3D(t *testing.T) {
	contract, err := parseFrameworkContract([]byte(umbrellaContract), "")
	if err != nil {
		t.Fatalf("valid umbrella contract refused: %v", err)
	}
	umbrellas := indexUmbrellas(contract.Umbrellas)
	if got := umbrellas["example.com/kit"]; len(got.Groupings) != 3 || got.Groupings[1].Name != "HTTP" ||
		!slices.Equal(got.Groupings[1].Packages, []string{"example.com/kit/httpx"}) {
		t.Fatalf("indexed umbrella = %+v", got)
	}
	for name, tc := range map[string]struct{ old, new, want string }{
		"outside":    {"  - import: example.com/kit\n    groupings:", "  - import: example.org/other\n    groupings:", "is outside"},
		"undeclared": {"packages: [example.com/kit/config]", "packages: [example.com/kit/missing]", "no package the contract declares"},
		"itself":     {"packages: [example.com/kit/config]", "packages: [example.com/kit]", "the umbrella itself"},
		"repeat":     {"packages: [example.com/kit/config]", "packages: [example.com/kit/config, example.com/kit/config]", "a repeat"},
		"unexported": {"name: Core", "name: core", "not an exported Go identifier"},
		"twice":      {"name: HTTP", "name: Core", `grouping "Core" twice`},
		"name":       {"    groupings:\n      - name: Core", "    name: kit-fx\n    groupings:\n      - name: Core", "not a Go package name"},
		"blank name": {"    groupings:\n      - name: Core", "    name: _\n    groupings:\n      - name: Core", "not a Go package name"},
		"ecosystem":  {"framework: example.com/kit\n", "framework: example.com/kit\necosystem: npm\n", "a npm contract cannot carry them"},
	} {
		raw := strings.Replace(umbrellaContract, tc.old, tc.new, 1)
		if raw == umbrellaContract {
			t.Fatalf("%s: fixture replacement did not apply", name)
		}
		if _, err := parseFrameworkContract([]byte(raw), ""); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
	duplicate := umbrellaContract + "  - import: example.com/kit\n    groupings:\n      - name: Core\n        packages: [example.com/kit/config]\n"
	if _, err := parseFrameworkContract([]byte(duplicate), ""); err == nil || !strings.Contains(err.Error(), "described twice") {
		t.Errorf("duplicate umbrella: err = %v", err)
	}
}

// umbrellaContractWith renders a contract whose umbrellas each carry groupings groupings, all
// grouping the config package.
func umbrellaContractWith(umbrellas, groupings int) string {
	var sb strings.Builder
	sb.WriteString(strings.Split(umbrellaContract, "umbrellas:")[0] + "umbrellas:\n")
	for u := range umbrellas {
		importPath := "example.com/kit"
		if u > 0 {
			importPath = fmt.Sprintf("example.com/kit/u%d", u)
		}
		fmt.Fprintf(&sb, "  - import: %s\n    groupings:\n", importPath)
		for g := range groupings {
			fmt.Fprintf(&sb, "      - name: G%d\n        packages: [example.com/kit/config]\n", g)
		}
	}
	return sb.String()
}

// The umbrella and grouping bounds accept their limit and refuse one more.
func TestContractUmbrellaBounds_3D(t *testing.T) {
	for _, tc := range []struct {
		umbrellas, groupings int
		want                 string
	}{
		{maxContractUmbrellas, 1, ""},
		{maxContractUmbrellas + 1, 1, "umbrellas exceed 16 entries"},
		{1, maxContractGroupings, ""},
		{1, maxContractGroupings + 1, "needs 1..64 groupings"},
		{1, 0, "needs 1..64 groupings"},
	} {
		_, err := parseFrameworkContract([]byte(umbrellaContractWith(tc.umbrellas, tc.groupings)), "")
		if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("%d umbrellas x %d groupings: err = %v, want %q", tc.umbrellas, tc.groupings, err, tc.want)
		}
	}
}

// An exported contract carries the umbrellas back; a grouping package the export leaves out
// is listed as skipped, and a fork's rebased contract moves every umbrella path.
func TestContractUmbrellaExportAndRebase_3D(t *testing.T) {
	path := writeFixture(t, t.TempDir(), "kit.capabilities.yaml", umbrellaContract)
	export, err := ExportFrameworkContract(t.Context(), "go", FrameworkSource{Contract: path}, Targets{})
	if err != nil {
		t.Fatal(err)
	}
	var exported frameworkContract
	if err := yaml.Unmarshal(export.Data, &exported); err != nil {
		t.Fatal(err)
	}
	original, err := parseFrameworkContract([]byte(umbrellaContract), "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.EqualFunc(exported.Umbrellas, original.Umbrellas, umbrellaEqual) || len(export.Skipped) != 0 {
		t.Fatalf("export lost umbrellas: %+v (skipped %v)", exported.Umbrellas, export.Skipped)
	}
	var skipped []string
	partial := contractUmbrellas(indexUmbrellas(original.Umbrellas), original.Packages[:1], &skipped)
	if len(partial) != 1 || len(partial[0].Groupings) != 1 || partial[0].Groupings[0].Name != "Core" || len(skipped) != 2 {
		t.Fatalf("partial export = %+v, skipped %v", partial, skipped)
	}
	if none := contractUmbrellas(indexUmbrellas(original.Umbrellas), nil, &skipped); len(none) != 0 ||
		!strings.Contains(skipped[len(skipped)-1], "no grouping lists an exported package") {
		t.Fatalf("an umbrella without exported packages was kept: %+v %v", none, skipped)
	}
	fork := original.rebase("example.org/fork")
	if fork.Umbrellas[0].Import != "example.org/fork" || fork.Umbrellas[0].Groupings[2].Packages[0] != "example.org/fork/store" ||
		original.Umbrellas[0].Import != "example.com/kit" {
		t.Fatalf("rebase did not move the umbrella alone: %+v / %+v", fork.Umbrellas, original.Umbrellas)
	}
	if err := fork.validate("example.org/fork"); err != nil {
		t.Fatalf("rebased contract invalid: %v", err)
	}
}

func umbrellaEqual(a, b contractUmbrella) bool {
	return a.Import == b.Import && a.Name == b.Name && slices.EqualFunc(a.Groupings, b.Groupings, func(x, y contractGrouping) bool {
		return x.Name == y.Name && slices.Equal(x.Packages, y.Packages)
	})
}

// A checkout of a fork observed against the configured contract judges imports of its own
// umbrella path.
func TestContractUmbrellaObservedFork(t *testing.T) {
	path := writeFixture(t, t.TempDir(), "kit.capabilities.yaml", umbrellaContract)
	fork := t.TempDir()
	writeFixture(t, fork, "go.mod", "module example.org/fork\n\ngo 1.22\n")
	writeFixture(t, fork, "config/config.go", "package config\n\nvar Module = \"config\"\n")
	index, err := InspectFramework(t.Context(), FrameworkSource{Checkout: fork, Contract: path})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := index.Umbrellas["example.org/fork"]; !ok {
		t.Fatalf("fork index lacks its rebased umbrella: %+v", index.Umbrellas)
	}
	app := t.TempDir()
	writeFixture(t, app, "go.mod", "module example.com/app\n\ngo 1.22\n\nrequire example.org/fork v1.0.0\n")
	writeFixture(t, app, "main.go", "package main\n\nimport \"example.org/fork\"\n\nvar app = []any{fork.Core, fork.HTTP}\n\nfunc main() { _ = app }\n")
	finding := onlyFinding(t, reportUmbrellas(t, app, index))
	if finding.Status != UmbrellaRecommend || len(finding.Recommendations) != 2 ||
		!slices.Equal(finding.Recommendations[0].Capabilities, []CapabilityKey{"config.loader"}) ||
		len(finding.Recommendations[1].Capabilities) != 0 {
		t.Fatalf("fork umbrella finding = %+v", finding)
	}
	text := FormatUmbrellaImports(&RepoNeeds{UmbrellaImports: []UmbrellaFinding{finding}})
	if !strings.Contains(text, "-> example.org/fork/httpx (grouping HTTP): capabilities not observed in the selected framework") {
		t.Fatalf("an unobserved grouping package claimed capabilities:\n%s", text)
	}
}

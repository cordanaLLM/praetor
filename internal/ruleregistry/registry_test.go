package ruleregistry

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAudit_EvidenceResolution(t *testing.T) {
	root := fixtureRoot(t, fixtureRegistry(t))
	report, err := Audit(t.Context(), root)
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	row := findRow(t, report, "HISS-01")
	if row.Evidence != "resolved" || row.ProseOnly {
		t.Fatalf("HISS-01 = %#v, want resolved enforcement", row)
	}
	prose := findRow(t, report, "HISS-02")
	if !prose.ProseOnly || prose.Evidence != "prose-only" {
		t.Fatalf("HISS-02 = %#v, want prose-only", prose)
	}
}

func TestAudit_MissingTestFails(t *testing.T) {
	registry := strings.Replace(fixtureRegistry(t), "TestEnforced", "TestMissing", 1)
	report, err := Audit(t.Context(), fixtureRoot(t, registry))
	if err == nil {
		t.Fatal("Audit() error = nil, want unresolved evidence")
	}
	row := findRow(t, report, "HISS-01")
	if row.Evidence != "unresolved: test:internal/check.TestMissing" {
		t.Fatalf("evidence = %q", row.Evidence)
	}
}

func TestAudit_UnresolvablePointersFail(t *testing.T) {
	tests := map[string]string{
		"unknown kind":        "version: 1\nrules:\n  - id: HISS-01\n    class: ci-gate\n    evidence: [nosuchkind:value]\n",
		"unknown gate stage":  "version: 1\nrules:\n  - id: HISS-01\n    class: ci-gate\n    evidence: [gate:No Such Stage]\n",
		"missing make target": "version: 1\nrules:\n  - id: HISS-01\n    class: ci-gate\n    evidence: [make:no-such-target]\n",
	}
	for name, registry := range tests {
		t.Run(name, func(t *testing.T) {
			report, err := Audit(t.Context(), fixtureRoot(t, registry))
			if err == nil || !strings.HasPrefix(findRow(t, report, "HISS-01").Evidence, "unresolved: ") {
				t.Fatalf("Audit() error = %v, want the pointer unresolved", err)
			}
		})
	}
}

func TestAudit_InvalidRegistry(t *testing.T) {
	tests := []struct {
		name     string
		registry string
		want     string
	}{
		{"unknown class", "version: 1\nrules:\n  - id: HISS-01\n    class: absent\n    evidence: [make:verify-all]\n", "unknown class"},
		{"unknown id", "version: 1\nrules:\n  - id: HISS-99\n    class: guidance\n", "unknown rule id"},
		{"duplicate id", "version: 1\nrules:\n  - id: HISS-01\n    class: guidance\n  - id: HISS-01\n    class: guidance\n", "duplicate rule id"},
		{"guidance evidence", "version: 1\nrules:\n  - id: HISS-01\n    class: guidance\n    evidence: [make:verify-all]\n", "guidance must not"},
		{"ci no evidence", "version: 1\nrules:\n  - id: HISS-01\n    class: ci-gate\n", "requires evidence"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, err := Audit(t.Context(), fixtureRoot(t, test.registry))
			if err == nil || !strings.Contains(strings.Join(report.Problems, "\n"), test.want) {
				t.Fatalf("Audit() = (%#v, %v), want %q", report, err, test.want)
			}
		})
	}
}

func TestAudit_EmptyRulesAndMaximumPointer(t *testing.T) {
	tests := []struct {
		name      string
		registry  string
		wantError bool
	}{
		{"empty", "version: 1\nrules: []\n", false},
		{"maximum pointer", maxPointerRegistry(), false},
		{"exceeds maximum pointer", overMaxPointerRegistry(), true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, err := Audit(t.Context(), fixtureRoot(t, test.registry))
			if (err != nil) != test.wantError {
				t.Fatalf("Audit() error = %v, want error=%t", err, test.wantError)
			}
			if report == nil {
				t.Fatal("Audit() report = nil")
			}
		})
	}
}

func TestDecode_UnknownField(t *testing.T) {
	_, err := decode([]byte("version: 1\nrules: []\nextra: no\n"))
	if err == nil || !strings.Contains(err.Error(), "field extra") {
		t.Fatalf("decode() error = %v, want unknown field", err)
	}
}

func TestParseOperationalRules_Bounds(t *testing.T) {
	for _, count := range []int{64, 65} {
		t.Run("count", func(t *testing.T) {
			rules, err := ParseOperationalRules([]byte(operationalDocument(count)))
			if count == 64 && (err != nil || len(rules) != count) {
				t.Fatalf("ParseOperationalRules(64) = (%d, %v)", len(rules), err)
			}
			if count == 65 && err == nil {
				t.Fatal("ParseOperationalRules(65) error = nil")
			}
		})
	}
}

func fixtureRoot(t *testing.T, registry string) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, root, "AGENTS.md", fixtureAgents(t))
	writeTestFile(t, root, File, registry)
	writeTestFile(t, root, "internal/check/check_test.go", "package check\nfunc TestEnforced(t *testing.T) {}\n")
	writeTestFile(t, root, "lefthook.yml", "commit-msg:\n  commands:\n    message-policy:\n      run: test\n")
	maxTarget := strings.Repeat("a", maxPointerLength-len("make:"))
	writeTestFile(t, root, "Makefile", "verify-all:\n\t@true\n"+maxTarget+":\n\t@true\n")
	writeTestFile(t, root, ".github/workflows/ci.yml", "jobs:\n  verify:\n    runs-on: ubuntu-latest\n")
	return root
}

func fixtureAgents(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func fixtureRegistry(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "enforcement.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeTestFile(t *testing.T, root, relative, data string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func findRow(t *testing.T, report *Report, id string) Row {
	t.Helper()
	for i := 0; i < len(report.Rows); i++ {
		if report.Rows[i].ID == id {
			return report.Rows[i]
		}
	}
	t.Fatalf("row %s not found", id)
	return Row{}
}

func maxPointerRegistry() string {
	pointer := "make:" + strings.Repeat("a", maxPointerLength-len("make:"))
	return "version: 1\nrules:\n  - id: HISS-01\n    class: ci-gate\n    evidence: [" + pointer + "]\n"
}

func overMaxPointerRegistry() string {
	pointer := "make:" + strings.Repeat("a", maxPointerLength-len("make:")+1)
	return "version: 1\nrules:\n  - id: HISS-01\n    class: ci-gate\n    evidence: [" + pointer + "]\n"
}

func operationalDocument(count int) string {
	var builder strings.Builder
	builder.WriteString("## Operational Rules\n")
	for i := 1; i <= count; i++ {
		builder.WriteString("\n")
		builder.WriteString(strconv.Itoa(i) + ". Rule.\n")
	}
	builder.WriteString("\n## Text Register\n")
	return builder.String()
}

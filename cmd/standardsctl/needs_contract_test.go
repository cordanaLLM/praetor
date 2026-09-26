package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func TestNeedsContractExport_Positive(t *testing.T) {
	out := filepath.Join(t.TempDir(), "kit.yaml")
	// The configured go target's declared contract exports unchanged.
	stdout, err := captureStdout(t, func() error {
		return dispatchCommand("needs", []string{"contract", "export", "--language=go", "--out=" + out, "--workstation-config=" + acmeWorkstation(t)})
	})
	if err != nil {
		t.Fatalf("export: %v\n%s", err, stdout)
	}
	mustContain(t, stdout, "[PASS] Exported the go framework example.com/acme/kit (1 packages)")
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, string(data), "framework: example.com/acme/kit", "ecosystem: go", "github.com/jackc/pgx")

	// The export can be configured in place of the declaration it came from.
	dir := t.TempDir()
	writeFixtureFile(t, dir, "workstation.yaml", "framework: {targets: {go: {module: example.com/acme/kit, contract: "+filepath.ToSlash(out)+"}}}\n")
	again := filepath.Join(t.TempDir(), "again.yaml")
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("needs", []string{"contract", "export", "--language=go", "--out=" + again,
			"--workstation-config=" + filepath.Join(dir, "workstation.yaml")})
	}); err != nil {
		t.Fatalf("re-export of the export: %v", err)
	}
	if reexported, err := os.ReadFile(again); err != nil || string(reexported) != string(data) {
		t.Fatalf("exporting the exported contract changed it: %v\n%s\n---\n%s", err, data, reexported)
	}
}

func TestNeedsContractExport_Negative(t *testing.T) {
	out := filepath.Join(t.TempDir(), "kit.yaml")
	onlyUI := filepath.Join(t.TempDir(), "workstation.yaml")
	writeFixtureFile(t, filepath.Dir(onlyUI), "workstation.yaml", "framework: {targets: {typescript: {module: example.com/acme/ui}}}\n")
	cases := map[string][]string{
		"unknown language":     {"export", "--language=cobol", "--out=" + out},
		"missing language":     {"export", "--out=" + out},
		"missing output":       {"export", "--language=go"},
		"framework on python":  {"export", "--language=python", "--framework=" + t.TempDir(), "--out=" + out},
		"positional argument":  {"export", "--language=go", "--out=" + out, "extra"},
		"unconfigured target":  {"export", "--language=go", "--out=" + out, "--workstation-config=" + onlyUI},
		"unknown action":       {"import", "--language=go"},
		"no action":            {},
		"invalid settings doc": {"export", "--language=go", "--out=" + out, "--fleet-config=" + filepath.Join(t.TempDir(), "missing.yaml")},
	}
	for name, args := range cases {
		if err := dispatchCommand("needs", append([]string{"contract"}, args...)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("a refused export wrote %s: %v", out, err)
	}
}

// Boundary: an unconfigured host exports the built-in tables of a language, listing the
// entries the contract grammar cannot carry.
func TestNeedsContractExport_BoundaryBuiltinTables(t *testing.T) {
	t.Setenv(config.WorkstationConfigEnv, "")
	out := filepath.Join(t.TempDir(), "python.yaml")
	stdout, err := captureStdout(t, func() error {
		return dispatchCommand("needs", []string{"contract", "export", "--language=python", "--out=" + out})
	})
	if err != nil {
		t.Fatalf("built-in export: %v", err)
	}
	mustContain(t, stdout, "[SKIP] capability clikit", "[PASS] Exported the python framework")
	if data, err := os.ReadFile(out); err != nil || !strings.Contains(string(data), "ecosystem: pypi") {
		t.Fatalf("built-in python export = %v\n%s", err, data)
	}
}

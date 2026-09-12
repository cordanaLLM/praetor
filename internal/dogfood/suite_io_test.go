package dogfood

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSuiteConfigByteBoundAndNonregularFiles(t *testing.T) {
	source := suiteFixture(t, suiteFixtureRecord)
	opts := suiteOptions(t, source)
	data, err := os.ReadFile(opts.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte(strings.Repeat(" ", maxSuiteConfigBytes-len(data)))...)
	if err := os.WriteFile(opts.ConfigPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadSuiteConfig(context.Background(), opts.ConfigPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(opts.ConfigPath, append(data, ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadSuiteConfig(context.Background(), opts.ConfigPath); err == nil {
		t.Fatal("accepted oversized config")
	}
	link := filepath.Join(t.TempDir(), "link.json")
	if err := os.Symlink(opts.ConfigPath, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, filepath.Dir(opts.ConfigPath)} {
		if _, _, err := loadSuiteConfig(context.Background(), path); err == nil {
			t.Fatal("accepted nonregular config")
		}
	}
	parent := t.TempDir()
	if err := os.Symlink(filepath.Dir(opts.ConfigPath), filepath.Join(parent, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadSuiteConfig(context.Background(), filepath.Join(parent, "linked", "suite.json")); err == nil {
		t.Fatal("followed config ancestor symlink")
	}
}

func TestSuitePersistenceFailureClearsSuccess(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "report.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	report := &SuiteReport{Options: SuiteOptions{ArtifactDir: dir}, Status: "verified", Verified: true}
	result, err := finishSuite(report, nil)
	if err == nil || result.Verified || result.Status != "failed" {
		t.Fatalf("false success: %+v %v", result, err)
	}
}

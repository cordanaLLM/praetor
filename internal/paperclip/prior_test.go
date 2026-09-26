package paperclip

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// priorFixture reads a golden harness file an earlier release wrote. CRLF is folded so a
// Windows checkout with core.autocrlf compares the bytes the release produced.
func priorFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "harness-462e3f3a", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

// priorRepo writes a manifest naming acme/legacy plus the given harness files.
func priorRepo(t *testing.T, harness, rules string) (string, *Harness) {
	t.Helper()
	repo := t.TempDir()
	writeRepoFile(t, repo, ".standards.yaml", "repository:\n  owner: acme\n  name: legacy\n")
	writeRepoFile(t, repo, ".paperclip/harness.json", harness)
	if rules != "" {
		writeRepoFile(t, repo, ".paperclip/rules.md", rules)
	}
	current, err := SynthesizeHarness(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	return repo, current
}

func TestPriorGeneratedRecognisesReleasedHarness(t *testing.T) {
	repo, current := priorRepo(t, priorFixture(t, "harness.json.golden"), priorFixture(t, "rules.md.golden"))
	prior, err := PriorGenerated(context.Background(), repo, current)
	if err != nil || !prior {
		t.Fatalf("462e3f3a harness not recognised as earlier output: prior=%v err=%v", prior, err)
	}
}

func TestPriorGeneratedRecognisesEveryDirectiveEra(t *testing.T) {
	for index, directive := range priorRegisterDirectives {
		repo, current := priorRepo(t, "{}", "")
		prior := priorHarness(current, directive)
		if err := WriteHarness(&prior, repo); err != nil {
			t.Fatal(err)
		}
		if ok, err := PriorGenerated(context.Background(), repo, current); err != nil || !ok {
			t.Fatalf("directive era %d not recognised: prior=%v err=%v", index, ok, err)
		}
	}
}

func TestPriorGeneratedRejectsOperatorEdits(t *testing.T) {
	harness := priorFixture(t, "harness.json.golden")
	rules := priorFixture(t, "rules.md.golden")
	cases := map[string][2]string{
		"edited contract":   {strings.Replace(harness, "NOT shipping", "not shipping", 1), rules},
		"edited rules":      {harness, rules + "- local rule\n"},
		"other platform":    {strings.Replace(harness, "acme/legacy", "acme/other", 1), rules},
		"current synthesis": {"", ""},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			repo, current := priorRepo(t, files[0], files[1])
			if name == "current synthesis" {
				if err := WriteHarness(current, repo); err != nil {
					t.Fatal(err)
				}
			}
			if ok, err := PriorGenerated(context.Background(), repo, current); err != nil || ok {
				t.Fatalf("%s treated as unmodified earlier output: prior=%v err=%v", name, ok, err)
			}
		})
	}
}

func TestPriorGeneratedBoundaries(t *testing.T) {
	repo, current := priorRepo(t, priorFixture(t, "harness.json.golden"), "")
	if ok, err := PriorGenerated(context.Background(), repo, current); err != nil || !ok {
		t.Fatalf("earlier harness without rules.md not recognised: prior=%v err=%v", ok, err)
	}
	if _, err := PriorGenerated(context.Background(), t.TempDir(), current); err == nil {
		t.Fatal("missing harness.json reported as earlier output without error")
	}
	if _, err := PriorGenerated(nil, repo, current); err == nil { //nolint:staticcheck // exercising the nil-context contract
		t.Fatal("nil context accepted")
	}
	if _, err := PriorGenerated(context.Background(), repo, nil); err == nil {
		t.Fatal("nil current harness accepted")
	}
}

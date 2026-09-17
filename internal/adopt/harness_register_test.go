package adopt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
)

func registerTestPlan() *VerificationPlan {
	return &VerificationPlan{Status: verificationDeclared, Build: [][]string{{"make", "build"}}, Test: [][]string{{"make", "test"}}}
}

// The harness carries the default block, and that block is byte-for-byte what the
// adoptee's own compile-context renders without a manifest: a fresh adoption verifies.
func TestHarnessCarriesTheDefaultRegisterSection(t *testing.T) {
	harness, err := buildAgentHarness("fixture", "framework", registerTestPlan())
	if err != nil {
		t.Fatal(err)
	}
	for _, once := range []string{config.RegisterBlockHeading + "\n", config.RegisterBlockStart, config.RegisterBlockEnd, harnessFooterHeading, harnessEndMarker} {
		if strings.Count(harness, once) != 1 {
			t.Errorf("harness must contain %q exactly once", once)
		}
	}
	if strings.Index(harness, config.RegisterBlockEnd) > strings.Index(harness, harnessFooterHeading) {
		t.Error("the register section must precede the footer, so the footer stays the last section")
	}

	repo := t.TempDir()
	agents := filepath.Join(repo, agentsFile)
	if err := os.WriteFile(agents, []byte(harness), filePerm); err != nil {
		t.Fatal(err)
	}
	if changed, err := compiler.SyncRegisterBlock(context.Background(), repo, agents, false); err != nil || changed {
		t.Fatalf("a fresh harness must verify against the default policy: changed=%v err=%v", changed, err)
	}
}

func TestDropRegisterSection(t *testing.T) {
	block, err := config.RenderRegisterBlock(config.DefaultRegisterPolicy())
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct{ content, want string }{
		"no section":             {"# Rules\nKeep this.\n", "# Rules\nKeep this.\n"},
		"appended section":       {"# Rules\nKeep this.\n\n" + config.RegisterSectionPrefix + block + "\n", "# Rules\nKeep this."},
		"heading without a gap":  {"# Rules\n" + config.RegisterBlockHeading + "\n" + block + "\n\n## After\nAlso kept.\n", "# Rules\n\n## After\nAlso kept."},
		"markers without a head": {block + "\nKeep this.\n", "Keep this."},
		"only the section":       {config.RegisterSectionPrefix + block + "\n", ""},
		"unterminated marker":    {"# Rules\n" + config.RegisterBlockStart + "\nrest\n", "# Rules\n" + config.RegisterBlockStart + "\nrest\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := dropRegisterSection(tc.content); got != tc.want {
				t.Fatalf("dropRegisterSection = %q, want %q", got, tc.want)
			}
		})
	}
	if got := foreignInstructions("# Rules\nKeep this.\n"); got != "# Rules\nKeep this.\n" {
		t.Fatalf("instructions without a section must stay byte-identical, got %q", got)
	}
}

// compile-context appends the section to an AGENTS.md that has none, so instructions kept
// across a harness refresh can hold one. The refreshed file must carry exactly one.
func TestHarnessRefreshKeepsOneRegisterSection(t *testing.T) {
	block, err := config.RenderRegisterBlock(config.DefaultRegisterPolicy())
	if err != nil {
		t.Fatal(err)
	}
	section := config.RegisterSectionPrefix + block + "\n"
	harness, err := buildAgentHarness("fixture", "framework", registerTestPlan())
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		initial string
		force   bool
	}{
		"forced refresh":      {harness + harnessSeparator + "\nKeep project instructions.\n\n" + section, true},
		"foreign first merge": {"# Repository rules\nKeep project instructions.\n\n" + section, false},
	} {
		t.Run(name, func(t *testing.T) {
			repo := newTestRepo(t, "harness-register")
			path := filepath.Join(repo, agentsFile)
			if err := os.WriteFile(path, []byte(tc.initial), filePerm); err != nil {
				t.Fatal(err)
			}
			s := &adoptSession{repoPath: repo, repoName: "fixture", arch: "framework", report: &AdoptReport{}, verification: registerTestPlan(), opts: AdoptOptions{Force: tc.force}}
			merged, err := mergeExistingAgentsContent(s, path, tc.initial, harness)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(merged, config.RegisterBlockStart) != 1 || !strings.Contains(merged, "Keep project instructions.") {
				t.Fatalf("merged context must keep the instructions and one register section:\n%s", merged)
			}
			if changed, err := compiler.SyncRegisterBlock(context.Background(), repo, path, false); err != nil || changed {
				t.Fatalf("merged context must verify: changed=%v err=%v", changed, err)
			}
		})
	}
}

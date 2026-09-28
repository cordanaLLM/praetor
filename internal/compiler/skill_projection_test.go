package compiler

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func skillFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"hiss-audit", "repo-adopt"} {
		dir := filepath.Join(root, filepath.FromSlash(CanonicalSkillsRel), name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + name + "\ndescription: fixture\n---\n\nBody.\n"
		if err := os.WriteFile(filepath.Join(dir, SkillEntryName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := filepath.Join(root, filepath.FromSlash(PluginManifestRel))
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(`{"name":"praetor"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// Negative: a skill the repository does not declare must not ship.
func TestVerifyPluginSkills_Negative_RejectsAnOrphanSkill(t *testing.T) {
	root := skillFixture(t)
	if err := compileFixture(t, root); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(root, filepath.FromSlash(PluginSkillsRel), "not-declared")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := VerifyPluginSkills(context.Background(), root)
	if err == nil {
		t.Fatal("an undeclared skill was shipped without complaint")
	}
	if !strings.Contains(err.Error(), "not-declared") {
		t.Errorf("error does not name the orphan: %v", err)
	}
}

// Negative: a shipped copy that drifts from its declaration is reported.
func TestVerifyPluginSkills_Negative_RejectsADriftedCopy(t *testing.T) {
	root := skillFixture(t)
	if err := compileFixture(t, root); err != nil {
		t.Fatal(err)
	}
	shipped := filepath.Join(root, filepath.FromSlash(PluginSkillsRel), "hiss-audit", SkillEntryName)
	if err := os.WriteFile(shipped, []byte("drifted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPluginSkills(context.Background(), root); err == nil {
		t.Fatal("a drifted plugin skill verified")
	}
}

// Boundary: every skill the rendered register block names is one this repository declares
// and its plugin ships byte-identical, so a register row never points an agent at a skill
// it cannot load. The internal row names caveman; the forbidden internal-brief name of
// ADR-0010 stays undeclared.
func TestRegisterBlockSkills_Boundary_DeclaredAndProjected(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	block, err := config.RenderRegisterBlock(config.DefaultRegisterPolicy(), false)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var names []string
	for _, match := range regexp.MustCompile("`([a-z0-9-]+)` skill").FindAllStringSubmatch(block, -1) {
		names = append(names, match[1])
	}
	if strings.Join(names, ",") != "social-text,caveman" {
		t.Fatalf("register block must name social-text and caveman, named %v:\n%s", names, block)
	}
	for _, name := range names {
		data, err := readCanonicalSkill(context.Background(), root, name)
		if err != nil {
			t.Fatalf("register block names %s, which the repository does not declare: %v", name, err)
		}
		// \r? keeps the check true on a Windows checkout, where text=auto writes CRLF.
		if !regexp.MustCompile(`(?m)^name: ` + regexp.QuoteMeta(name) + `\r?$`).Match(data) {
			t.Errorf("%s/%s/%s frontmatter must name %s", CanonicalSkillsRel, name, SkillEntryName, name)
		}
		if err := verifyProjection(context.Background(), root, skillEntryRel(PluginSkillsRel, name), data); err != nil {
			t.Errorf("plugin must ship %s: %v", name, err)
		}
	}
	if _, err := readCanonicalSkill(context.Background(), root, "internal-brief"); err == nil {
		t.Error("internal-brief is forbidden by ADR-0010; caveman is the one internal skill")
	}
}

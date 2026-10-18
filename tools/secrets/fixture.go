// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package secrets

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

type recipeConfig struct {
	GitleaksConfig string            `json:"gitleaks_config"`
	Files          map[string]string `json:"files"`
	Messages       map[string]string `json:"messages"`
}

func loadRecipeConfig() (recipeConfig, error) {
	// Look up testdata relative to current working directory or package root
	for _, p := range []string{
		filepath.Join("tools", "secrets", "testdata", "recipe.json"),
		filepath.Join("..", "..", "tools", "secrets", "testdata", "recipe.json"),
		filepath.Join("testdata", "recipe.json"),
	} {
		data, err := util.ReadFileLimited(p, maxRulesSourceBytes)
		if err == nil {
			var cfg recipeConfig
			if jsonErr := json.Unmarshal(data, &cfg); jsonErr == nil {
				return cfg, nil
			}
		}
	}
	return recipeConfig{}, fmt.Errorf("recipe.json not found")
}

// PlantedSecret generates a secret token at run time (never committed to repository history).
func PlantedSecret() string {
	hash := sha256.Sum256([]byte("planted-secrets-history-fixture-token"))
	return base64.URLEncoding.EncodeToString(hash[:])[:40]
}

func runGitCommand(t testing.TB, dir string, args ...string) string {
	t.Helper()
	out, err := util.RunGit(t.Context(), dir, args...)
	if err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, out)
	}
	return out
}

func initBaseRepo(t testing.TB) (string, recipeConfig) {
	t.Helper()
	recipe, err := loadRecipeConfig()
	if err != nil {
		t.Fatalf("load recipe: %v", err)
	}
	dir := t.TempDir()
	runGitCommand(t, dir, "-c", "init.defaultBranch=main", "init", "--quiet")
	runGitCommand(t, dir, "config", "user.name", "Tester")
	runGitCommand(t, dir, "config", "user.email", "tester@example.com")
	runGitCommand(t, dir, "config", "commit.gpgsign", "false")
	cfgPath := filepath.Join(dir, ".gitleaks.toml")
	if writeErr := os.WriteFile(cfgPath, []byte(recipe.GitleaksConfig), 0o600); writeErr != nil {
		t.Fatalf("write .gitleaks.toml: %v", writeErr)
	}
	return dir, recipe
}

// BuildMergeLeakRepo creates a repository where a secret exists only in a merge commit.
func BuildMergeLeakRepo(t testing.TB) (string, string) {
	t.Helper()
	dir, recipe := initBaseRepo(t)
	fileName := recipe.Files["creds_py"]
	credsPath := filepath.Join(dir, fileName)

	if err := os.WriteFile(credsPath, []byte("base = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitCommand(t, dir, "add", ".")
	runGitCommand(t, dir, "commit", "--quiet", "-m", "init")

	runGitCommand(t, dir, "checkout", "--quiet", "-b", "side")
	if err := os.WriteFile(filepath.Join(dir, "side.txt"), []byte("side = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitCommand(t, dir, "add", "side.txt")
	runGitCommand(t, dir, "commit", "--quiet", "-m", "side change")

	runGitCommand(t, dir, "checkout", "--quiet", "main")
	if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte("main = 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitCommand(t, dir, "add", "main.txt")
	runGitCommand(t, dir, "commit", "--quiet", "-m", "main change")

	runGitCommand(t, dir, "merge", "--no-ff", "--no-commit", "side")
	planted := fmt.Sprintf("api_key = %q\n", PlantedSecret())
	if err := os.WriteFile(credsPath, []byte(planted), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitCommand(t, dir, "add", fileName)
	runGitCommand(t, dir, "commit", "--quiet", "-m", recipe.Messages["merge_commit"])
	mergeHash := stringsTrim(runGitCommand(t, dir, "rev-parse", "HEAD"))

	if err := os.WriteFile(credsPath, []byte("clean = 4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitCommand(t, dir, "commit", "--quiet", "-am", recipe.Messages["clean_commit"])
	return dir, mergeHash
}

// BuildOrdinaryLeakRepo creates a repository containing a commit with a secret.
func BuildOrdinaryLeakRepo(t testing.TB) (string, string) {
	t.Helper()
	dir, recipe := initBaseRepo(t)
	fileName := recipe.Files["creds_py"]

	planted := fmt.Sprintf("api_key = %q\n", PlantedSecret())
	credsPath := filepath.Join(dir, fileName)
	if err := os.WriteFile(credsPath, []byte(planted), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitCommand(t, dir, "add", ".")
	runGitCommand(t, dir, "commit", "--quiet", "-m", recipe.Messages["ordinary_commit"])
	commitHash := stringsTrim(runGitCommand(t, dir, "rev-parse", "HEAD"))
	return dir, commitHash
}

// BuildCleanRepo creates a clean repository with zero secrets.
func BuildCleanRepo(t testing.TB) string {
	t.Helper()
	dir, _ := initBaseRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("clean\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitCommand(t, dir, "add", ".")
	runGitCommand(t, dir, "commit", "--quiet", "-m", "init")
	return dir
}

func stringsTrim(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

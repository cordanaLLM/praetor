// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hisscoverage

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// repositoryRoot is the engine checkout this package sits in.
var repositoryRoot = filepath.Join("..", "..")

// secretsRecipe returns the arguments of the gitleaks command `make secrets` runs, with
// $(GO_SECURITY_TOOL) resolved to the pinned tool module of this checkout.
func secretsRecipe(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repositoryRoot, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	recipe := regexp.MustCompile(`(?m)^secrets:\r?\n\t(.+?)\r?$`).FindSubmatch(data)
	if recipe == nil {
		t.Fatal("Makefile has no one-line secrets recipe")
	}
	modfile, err := filepath.Abs(filepath.Join(repositoryRoot, "tools", "go", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Replace(string(recipe[1]), "$(GO_SECURITY_TOOL)", "go tool -modfile="+filepath.ToSlash(modfile), 1)
	var args []string
	for _, word := range regexp.MustCompile(`[^ "]*"[^"]*"[^ "]*|[^ ]+`).FindAllString(line, -1) {
		args = append(args, strings.ReplaceAll(word, `"`, ""))
	}
	return args
}

// HISS-12 says no credential is in the Git history, and make secrets is its mechanism. A
// credential that only a merge commit carries, such as one added while resolving a conflict, is
// in the history too, but git log -p prints no patch for a merge, so the scan never read it
// (#1237). The token is made up at run time; no fixture holds it.
func TestSecretsHistoryScan_Boundary_ReadsMergeCommits(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the pinned gitleaks")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH")
	}
	args := secretsRecipe(t)
	repo := mergeOnlySecretRepository(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	scan := exec.CommandContext(ctx, args[0], append(args[1:], "--source", repo)...)
	scan.Dir = repositoryRoot
	out, err := scan.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(string(out), "leaks found") {
		t.Fatalf("make secrets on a merge-only credential: %v, want exit 1 with leaks found:\n%s", err, out)
	}
}

// mergeOnlySecretRepository returns a repository whose credential is added by a merge commit and
// removed by the next commit, so no ordinary commit's patch holds it.
func mergeOnlySecretRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	ctx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		if out, err := util.RunGit(ctx, repo, args...); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	commit := func(name, content, message string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		git("add", name)
		git("commit", "-q", "-m", message)
	}
	git("init", "-q", "-b", "main")
	commit("base.txt", "base\n", "base")
	git("switch", "-q", "-c", "side")
	commit("side.txt", "side\n", "side")
	git("switch", "-q", "main")
	commit("main.txt", "main\n", "main")
	git("merge", "-q", "--no-ff", "--no-commit", "side")
	commit("leak.txt", "token = \""+madeUpGitHubToken()+"\"\n", "merge side")
	git("rm", "-q", "leak.txt")
	git("commit", "-q", "-m", "remove the file")
	return repo
}

// madeUpGitHubToken returns a token of GitHub's classic personal-access-token shape, random for
// this run (gitleaks rule github-pat).
func madeUpGitHubToken() string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	random := make([]byte, 36)
	if _, err := rand.Read(random); err != nil {
		panic(err)
	}
	for index := range random {
		random[index] = alphabet[int(random[index])%len(alphabet)]
	}
	return "ghp_" + string(random)
}

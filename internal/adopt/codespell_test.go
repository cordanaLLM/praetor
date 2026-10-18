// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"

	"github.com/cordanaLLM/praetor/internal/config"
	figureassets "github.com/cordanaLLM/praetor/tools/figures"
)

func findCodespellBinary(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"codespell",
		"/home/kilian/.local/bin/codespell",
	}
	for _, c := range candidates {
		if path, err := exec.LookPath(c); err == nil {
			return path
		}
	}
	return ""
}

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	outC := make(chan string)
	go func() {
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, r); err != nil {
			outC <- ""
			return
		}
		outC <- buf.String()
	}()

	runErr := fn()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = oldStdout
	captured := <-outC
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return captured, runErr
}

func setupCodespellFixture(t *testing.T) (string, time.Time, *adoptSession, string) {
	t.Helper()
	repo := t.TempDir()
	today := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	playerPath := "tools/figures/player/player.js"
	fullPlayerPath := filepath.Join(repo, filepath.FromSlash(playerPath))
	if err := os.MkdirAll(filepath.Dir(fullPlayerPath), 0755); err != nil {
		t.Fatal(err)
	}

	playerData, err := figureassets.FS().Open("player/player.js")
	if err != nil {
		t.Fatal(err)
	}
	bytesPlayer, err := io.ReadAll(playerData)
	if closeErr := playerData.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPlayerPath, bytesPlayer, 0644); err != nil {
		t.Fatal(err)
	}

	codespellrcPath := filepath.Join(repo, ".codespellrc")
	initialRc := "[codespell]\nskip = .git,tools/figures/dist/player.js\n"
	if err := os.WriteFile(codespellrcPath, []byte(initialRc), 0644); err != nil {
		t.Fatal(err)
	}

	manifestContent := `version: 1
facets:
  - docs:seo-portal
exceptions:
  - rule: codespell
    path: tools/figures/player/player.js
    category: C
    reason: minified third-party interfig bundle
    entered: 2026-10-18
    expires: 2026-10-28
`
	if err := os.WriteFile(filepath.Join(repo, manifestFile), []byte(manifestContent), 0644); err != nil {
		t.Fatal(err)
	}

	s := &adoptSession{
		repoPath: repo,
		facets:   []string{"docs:seo-portal"},
		opts:     AdoptOptions{Path: repo},
		report:   &AdoptReport{},
	}
	return repo, today, s, findCodespellBinary(t)
}

func assertCodespellLiveReconciled(t *testing.T, repo string, today time.Time, s *adoptSession, codespellBin string) {
	t.Helper()
	playerPath := "tools/figures/player/player.js"
	codespellrcPath := filepath.Join(repo, ".codespellrc")

	// 1. Reconcile with live exception
	if err := reconcileCodespell(t.Context(), s); err != nil {
		t.Fatalf("reconcileCodespell with live exception failed: %v", err)
	}

	rcData, err := os.ReadFile(codespellrcPath)
	if err != nil {
		t.Fatal(err)
	}
	skips := ParseCodespellSkip(string(rcData), ConfigTypeCodespellRC)
	if !slices.Contains(skips, ".git") {
		t.Errorf("adopter-owned skip .git was removed: %v", skips)
	}
	if slices.Contains(skips, "tools/figures/dist/player.js") {
		t.Errorf("retired dist/player.js was not removed: %v", skips)
	}
	if !slices.Contains(skips, playerPath) && !slices.Contains(skips, "./"+playerPath) {
		t.Errorf("live exception player.js was not added to skip: %v", skips)
	}

	// 2. Audit with live exception passes and lists expiry
	liveException := config.Exception{
		Rule:     config.ExceptionRuleCodespell,
		Path:     playerPath,
		Category: "C",
		Reason:   "minified third-party interfig bundle",
		Entered:  "2026-10-18",
		Expires:  "2026-10-28",
	}
	out, err := captureStdout(t, func() error {
		return AuditCodespell(t.Context(), repo, []config.Exception{liveException}, today)
	})
	if err != nil {
		t.Fatalf("AuditCodespell failed unexpectedly: %v", err)
	}
	if !strings.Contains(out, "[PASS] codespell: tools/figures/player/player.js, excepted until 2026-10-28") {
		t.Errorf("Audit output missing live entry with expiry: %s", out)
	}

	// 3. Codespell binary execution with skip passes
	if codespellBin != "" {
		if runOut, runErr := runCodespell(t, codespellBin, repo); runErr != nil {
			t.Fatalf("codespell with skip failed unexpectedly (%v):\n%s", runErr, string(runOut))
		}
	}
}

func assertCodespellExpired(t *testing.T, repo string, s *adoptSession, codespellBin string) config.Exception {
	t.Helper()
	playerPath := "tools/figures/player/player.js"
	codespellrcPath := filepath.Join(repo, ".codespellrc")

	// 4. Expired exception: adopt removes skip and codespell fails
	expiredException := config.Exception{
		Rule:     config.ExceptionRuleCodespell,
		Path:     playerPath,
		Category: "C",
		Reason:   "minified third-party interfig bundle",
		Entered:  "2026-10-01",
		Expires:  "2026-10-11",
	}
	expiredManifest := `version: 1
facets:
  - docs:seo-portal
exceptions:
  - rule: codespell
    path: tools/figures/player/player.js
    category: C
    reason: minified third-party interfig bundle
    entered: 2026-10-01
    expires: 2026-10-11
`
	if err := os.WriteFile(filepath.Join(repo, manifestFile), []byte(expiredManifest), 0644); err != nil {
		t.Fatal(err)
	}

	if err := reconcileCodespell(t.Context(), s); err != nil {
		t.Fatalf("reconcileCodespell with expired exception failed: %v", err)
	}

	rcDataExpired, err := os.ReadFile(codespellrcPath)
	if err != nil {
		t.Fatal(err)
	}
	skipsExpired := ParseCodespellSkip(string(rcDataExpired), ConfigTypeCodespellRC)
	if slices.Contains(skipsExpired, playerPath) || slices.Contains(skipsExpired, "./"+playerPath) {
		t.Errorf("expired player.js was NOT removed from skip: %v", skipsExpired)
	}

	if codespellBin != "" {
		if runOut, runErr := runCodespell(t, codespellBin, repo); runErr == nil {
			t.Fatalf("codespell unexpectedly passed when player.js was unskipped:\n%s", string(runOut))
		}
	}
	return expiredException
}

func assertCodespellAuditAndPlanted(t *testing.T, repo string, today time.Time, expiredException config.Exception, codespellBin string) {
	t.Helper()
	codespellrcPath := filepath.Join(repo, ".codespellrc")

	// 5. Audit fails when skip names player.js without a live entry
	// Manually restore skip in .codespellrc while manifest has expired entry
	if err := os.WriteFile(codespellrcPath, []byte("[codespell]\nskip = tools/figures/player/player.js\n"), 0644); err != nil {
		t.Fatal(err)
	}
	auditErr := AuditCodespell(t.Context(), repo, []config.Exception{expiredException}, today)
	if auditErr == nil {
		t.Fatal("AuditCodespell unexpectedly passed for unexcused engine-emitted skip")
	}
	if !strings.Contains(auditErr.Error(), "without a live codespell exception entry") {
		t.Fatalf("audit error message %q missing expected failure clause", auditErr.Error())
	}

	// 6. Planted misspelling in non-bundle emitted file is reported
	// Re-add skip for player.js so bundle passes
	if err := os.WriteFile(codespellrcPath, []byte("[codespell]\nskip = ./tools/figures/player/player.js\n"), 0644); err != nil {
		t.Fatal(err)
	}
	readmePath := filepath.Join(repo, "tools", "figures", "README.md")
	if err := os.WriteFile(readmePath, []byte("# Figures Engine\nThis text has a mispelling here.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if codespellBin != "" {
		plantedOut, plantedErr := runCodespell(t, codespellBin, repo)
		if plantedErr == nil {
			t.Fatalf("codespell unexpectedly passed on planted misspelling:\n%s", string(plantedOut))
		}
		if !strings.Contains(string(plantedOut), "mispelling") {
			t.Fatalf("codespell did not report planted misspelling:\n%s", string(plantedOut))
		}
	}
}

func TestCodespell_AdopterReconciliationAndAudit(t *testing.T) {
	repo, today, s, codespellBin := setupCodespellFixture(t)
	assertCodespellLiveReconciled(t, repo, today, s, codespellBin)
	expiredException := assertCodespellExpired(t, repo, s, codespellBin)
	assertCodespellAuditAndPlanted(t, repo, today, expiredException, codespellBin)
}

func TestCodespell_Formats_SetupCfg_And_Pyproject(t *testing.T) {
	// 1. Setup.cfg format
	setupCfg := `[metadata]
name = mypkg

[codespell]
skip = .git,vendor,tools/figures/dist/player.js
quiet-level = 3
`
	skips := ParseCodespellSkip(setupCfg, ConfigTypeSetupCfg)
	if want := []string{".git", "vendor", "tools/figures/dist/player.js"}; !slices.Equal(skips, want) {
		t.Fatalf("parse setup.cfg skips got %v, want %v", skips, want)
	}
	updatedSetup := UpdateCodespellSkip(setupCfg, ConfigTypeSetupCfg, []string{".git", "vendor", "tools/figures/player/player.js"})
	newSkipsSetup := ParseCodespellSkip(updatedSetup, ConfigTypeSetupCfg)
	if want := []string{".git", "vendor", "tools/figures/player/player.js"}; !slices.Equal(newSkipsSetup, want) {
		t.Fatalf("updated setup.cfg skips got %v, want %v\ncontent:\n%s", newSkipsSetup, want, updatedSetup)
	}

	// 2. Pyproject.toml string format
	pyprojectStr := `[build-system]
requires = ["setuptools"]

[tool.codespell]
skip = ".git,vendor"
`
	skipsTomlStr := ParseCodespellSkip(pyprojectStr, ConfigTypePyprojectToml)
	if want := []string{".git", "vendor"}; !slices.Equal(skipsTomlStr, want) {
		t.Fatalf("parse pyproject string skips got %v, want %v", skipsTomlStr, want)
	}
	updatedTomlStr := UpdateCodespellSkip(pyprojectStr, ConfigTypePyprojectToml, []string{".git", "vendor", "tools/figures/player/player.js"})
	newSkipsTomlStr := ParseCodespellSkip(updatedTomlStr, ConfigTypePyprojectToml)
	if want := []string{".git", "vendor", "tools/figures/player/player.js"}; !slices.Equal(newSkipsTomlStr, want) {
		t.Fatalf("updated pyproject string skips got %v, want %v\ncontent:\n%s", newSkipsTomlStr, want, updatedTomlStr)
	}

	// 3. Pyproject.toml array format
	pyprojectArr := `[build-system]
requires = ["setuptools"]

[tool.codespell]
skip = [
  ".git",
  "vendor",
]
`
	skipsTomlArr := ParseCodespellSkip(pyprojectArr, ConfigTypePyprojectToml)
	if want := []string{".git", "vendor"}; !slices.Equal(skipsTomlArr, want) {
		t.Fatalf("parse pyproject array skips got %v, want %v", skipsTomlArr, want)
	}
	updatedTomlArr := UpdateCodespellSkip(pyprojectArr, ConfigTypePyprojectToml, []string{".git", "vendor", "tools/figures/player/player.js"})
	newSkipsTomlArr := ParseCodespellSkip(updatedTomlArr, ConfigTypePyprojectToml)
	if want := []string{".git", "vendor", "tools/figures/player/player.js"}; !slices.Equal(newSkipsTomlArr, want) {
		t.Fatalf("updated pyproject array skips got %v, want %v\ncontent:\n%s", newSkipsTomlArr, want, updatedTomlArr)
	}
}

// runCodespell runs the codespell binary in dir through the engine's command helper and returns
// its combined output.
func runCodespell(t *testing.T, bin, dir string) ([]byte, error) {
	t.Helper()
	res, err := util.RunCommandBytes(t.Context(), dir, bin, util.MaxCommandOutputBytes)
	return append(res.Stdout, res.Stderr...), err
}

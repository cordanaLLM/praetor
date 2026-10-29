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
	candidates := []string{"codespell"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".local", "bin", "codespell"))
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

// codespellLiveExpiry is the category C expiry (10 days) of the fixture's live exception.
func codespellLiveExpiry(today time.Time) string {
	return today.AddDate(0, 0, 10).Format(time.DateOnly)
}

// setupCodespellFixture injects today as reconcileCodespell's clock and dates every fixture
// exception from it, so no case reads the wall clock: fixed dates made this test fail once
// 2026-10-28 passed (Order: engine-golangci-exceptions-expired-20261029).
func setupCodespellFixture(t *testing.T, today time.Time) (string, time.Time, *adoptSession, string) {
	t.Helper()
	repo := t.TempDir()
	saved := codespellNow
	codespellNow = func() time.Time { return today }
	t.Cleanup(func() { codespellNow = saved })
	playerPath := "tools/figures/player/player.js"
	fullPlayerPath := filepath.Join(repo, filepath.FromSlash(playerPath))
	if err := os.MkdirAll(filepath.Dir(fullPlayerPath), 0755); err != nil {
		t.Fatal(err)
	}

	bytesPlayer, err := figureassets.ReadPlayer("player/player.js")
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
    entered: ` + today.Format(time.DateOnly) + `
    expires: ` + codespellLiveExpiry(today) + `
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
		Entered:  today.Format(time.DateOnly),
		Expires:  codespellLiveExpiry(today),
	}
	out, err := captureStdout(t, func() error {
		return AuditCodespell(t.Context(), repo, []config.Exception{liveException}, today)
	})
	if err != nil {
		t.Fatalf("AuditCodespell failed unexpectedly: %v", err)
	}
	if !strings.Contains(out, "[PASS] codespell: tools/figures/player/player.js, excepted until "+codespellLiveExpiry(today)) {
		t.Errorf("Audit output missing live entry with expiry: %s", out)
	}

	// 3. Codespell binary execution with skip passes
	if codespellBin != "" {
		if runOut, runErr := runCodespell(t, codespellBin, repo); runErr != nil {
			t.Fatalf("codespell with skip failed unexpectedly (%v):\n%s", runErr, string(runOut))
		}
	}
}

func assertCodespellExpired(t *testing.T, repo string, today time.Time, s *adoptSession, codespellBin string) config.Exception {
	t.Helper()
	playerPath := "tools/figures/player/player.js"
	codespellrcPath := filepath.Join(repo, ".codespellrc")

	// 4. Expired exception: adopt removes skip and codespell fails
	expiredException := config.Exception{
		Rule:     config.ExceptionRuleCodespell,
		Path:     playerPath,
		Category: "C",
		Reason:   "minified third-party interfig bundle",
		Entered:  today.AddDate(0, 0, -28).Format(time.DateOnly),
		Expires:  today.AddDate(0, 0, -18).Format(time.DateOnly),
	}
	expiredManifest := `version: 1
facets:
  - docs:seo-portal
exceptions:
  - rule: codespell
    path: tools/figures/player/player.js
    category: C
    reason: minified third-party interfig bundle
    entered: ` + today.AddDate(0, 0, -28).Format(time.DateOnly) + `
    expires: ` + today.AddDate(0, 0, -18).Format(time.DateOnly) + `
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

// The same fixture holds on its base date and with the clock shifted by 30 days: no case
// depends on the wall clock.
func TestCodespell_AdopterReconciliationAndAudit(t *testing.T) {
	base := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	for _, today := range []time.Time{base, base.AddDate(0, 0, 30)} {
		t.Run(today.Format(time.DateOnly), func(t *testing.T) {
			repo, now, s, codespellBin := setupCodespellFixture(t, today)
			assertCodespellLiveReconciled(t, repo, now, s, codespellBin)
			expiredException := assertCodespellExpired(t, repo, now, s, codespellBin)
			assertCodespellAuditAndPlanted(t, repo, now, expiredException, codespellBin)
		})
	}
}

func TestCodespell_DoesNotProposeFigurePlayerExceptions(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".codespellrc"), []byte("[codespell]\nskip = .git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifestContent := "version: 1\nfacets:\n  - docs:seo-portal\n"
	if err := os.WriteFile(filepath.Join(repo, manifestFile), []byte(manifestContent), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &adoptSession{
		repoPath: repo,
		facets:   []string{"docs:seo-portal"},
		opts:     AdoptOptions{Path: repo},
		report:   &AdoptReport{},
	}
	out, err := captureStdout(t, func() error {
		return reconcileCodespell(t.Context(), s)
	})
	if err != nil {
		t.Fatalf("reconcileCodespell failed: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected no codespell proposal on stdout, got: %s", out)
	}
	if len(s.report.Warnings) != 0 {
		t.Fatalf("expected no codespell proposal warning, got: %v", s.report.Warnings)
	}
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

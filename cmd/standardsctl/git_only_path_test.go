package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// maxGitCandidates bounds the git executables gitOnlyPath considers (HISS-02).
const maxGitCandidates = 64

// gitProbeTimeout bounds one "git --version" probe.
const gitProbeTimeout = 20 * time.Second

// gitOnlyPath leaves git as the only executable on PATH, so no test reaches a gh session and
// its token: project commands without credentials then stay on the local cache.
//
// Not every git runs alone (#1306): the MSYS2 build Git for Windows ships in ucrt64\bin loads
// DLLs from its own directory and exits 0xc0000135 (STATUS_DLL_NOT_FOUND) from anywhere else.
// So each git on PATH, in PATH order, is linked into an empty directory and run there with that
// directory as the whole PATH; the first that answers --version is kept. Where none does, the
// test is skipped naming each candidate and why it was passed over (HISS-21).
func gitOnlyPath(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	git, reasons := pickStandaloneGit(gitCandidates(os.Getenv("PATH")), func(candidate string) error {
		return gitRunsAlone(bin, candidate)
	})
	if git == "" {
		t.Skipf("no git on PATH runs as the only executable on PATH, cannot isolate PATH: %s", strings.Join(reasons, "; "))
	}
	t.Setenv("PATH", bin)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
}

// gitCandidates lists every git executable in the directories of path, in order, once each.
func gitCandidates(path string) []string {
	names := []string{"git"}
	if runtime.GOOS == "windows" {
		names = []string{"git.exe"}
	}
	seen := map[string]bool{}
	var candidates []string
	dirs := filepath.SplitList(path)
	for i := 0; i < len(dirs) && len(candidates) < maxGitCandidates; i++ {
		for _, name := range names {
			candidate := filepath.Join(dirs[i], name)
			key := candidate
			if runtime.GOOS == "windows" {
				key = strings.ToLower(candidate)
			}
			if info, err := os.Stat(candidate); err != nil || info.IsDir() || seen[key] {
				continue
			}
			seen[key] = true
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

// pickStandaloneGit returns the first candidate runsAlone accepts, or "" with the reason each
// candidate was passed over.
func pickStandaloneGit(candidates []string, runsAlone func(string) error) (string, []string) {
	if len(candidates) == 0 {
		return "", []string{"no git executable on PATH"}
	}
	reasons := make([]string, 0, len(candidates))
	for i := 0; i < len(candidates) && i < maxGitCandidates; i++ {
		if err := runsAlone(candidates[i]); err != nil {
			reasons = append(reasons, fmt.Sprintf("%s: %v", candidates[i], err))
			continue
		}
		return candidates[i], reasons
	}
	return "", reasons
}

// gitRunsAlone links candidate into bin, replacing an earlier candidate's link, and runs
// "git --version" from there with bin as the whole PATH.
func gitRunsAlone(bin, candidate string) error {
	link := filepath.Join(bin, filepath.Base(candidate))
	if err := os.Remove(link); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("replace link: %w", err)
	}
	if err := os.Symlink(candidate, link); err != nil {
		return fmt.Errorf("symlinks unavailable: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, link, "--version")
	cmd.Env = append(withoutPath(os.Environ()), "PATH="+bin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git --version from an isolated PATH: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	if !strings.HasPrefix(string(out), "git version ") {
		return fmt.Errorf("git --version printed %q", strings.TrimSpace(string(out)))
	}
	return nil
}

// withoutPath drops PATH, in any case, from env.
func withoutPath(env []string) []string {
	kept := make([]string, 0, len(env))
	for _, entry := range env {
		if name, _, _ := strings.Cut(entry, "="); !strings.EqualFold(name, "PATH") {
			kept = append(kept, entry)
		}
	}
	return kept
}

// Positive (#1306): after gitOnlyPath the git on PATH runs, and gh is not reachable.
func TestGitOnlyPath_Positive_LeavesARunningGitAndNoGh(t *testing.T) {
	gitOnlyPath(t)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("no git on the isolated PATH: %v", err)
	}
	out, err := exec.Command(git, "--version").CombinedOutput()
	if err != nil || !strings.HasPrefix(string(out), "git version ") {
		t.Fatalf("the isolated git does not run: %v (%s)", err, out)
	}
	if gh, err := exec.LookPath("gh"); err == nil {
		t.Fatalf("gh is reachable on the isolated PATH: %s", gh)
	}
	if entries, err := os.ReadDir(os.Getenv("PATH")); err != nil || len(entries) != 1 {
		t.Fatalf("the isolated PATH holds %d entries (%v), want git alone", len(entries), err)
	}
}

// Negative: a git that cannot run alone is passed over with its reason, and none that runs
// leaves no pick and names every candidate.
func TestPickStandaloneGit_Negative_PassesOverAGitThatCannotRunAlone(t *testing.T) {
	dllMissing := errors.New("exit status 0xc0000135")
	runs := func(candidate string) error {
		if strings.Contains(candidate, "ucrt64") {
			return dllMissing
		}
		return nil
	}
	got, reasons := pickStandaloneGit([]string{`C:\Git\ucrt64\bin\git.exe`, `C:\Git\cmd\git.exe`}, runs)
	if got != `C:\Git\cmd\git.exe` || len(reasons) != 1 || !strings.Contains(reasons[0], "0xc0000135") {
		t.Fatalf("picked %q with %q, want the launcher after passing over ucrt64", got, reasons)
	}
	got, reasons = pickStandaloneGit([]string{`C:\Git\ucrt64\bin\git.exe`}, runs)
	if got != "" || len(reasons) != 1 || !strings.Contains(reasons[0], `ucrt64\bin\git.exe: exit status 0xc0000135`) {
		t.Fatalf("picked %q with %q, want no pick naming the candidate", got, reasons)
	}
}

// Boundary: no candidate says so; the first git that runs wins even when a later one would too;
// a PATH listing one directory twice, or an entry that is no file, yields one candidate.
func TestPickStandaloneGit_Boundary_OrderAndCandidates(t *testing.T) {
	if got, reasons := pickStandaloneGit(nil, func(string) error { return nil }); got != "" || len(reasons) != 1 || reasons[0] != "no git executable on PATH" {
		t.Fatalf("no candidates: %q %q", got, reasons)
	}
	if got, _ := pickStandaloneGit([]string{"first", "second"}, func(string) error { return nil }); got != "first" {
		t.Fatalf("picked %q, want the first git on PATH", got)
	}
	dir := t.TempDir()
	name := "git"
	if runtime.GOOS == "windows" {
		name = "git.exe"
	}
	writeFixtureFile(t, dir, name, "")
	empty := t.TempDir()
	if got := gitCandidates(strings.Join([]string{dir, empty, dir, filepath.Join(dir, "missing")}, string(os.PathListSeparator))); len(got) != 1 || got[0] != filepath.Join(dir, name) {
		t.Fatalf("candidates %q, want one", got)
	}
}

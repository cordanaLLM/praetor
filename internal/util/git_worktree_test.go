package util

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// completeGitDir gives gitDir the objects/ and refs/ directories git requires.
func completeGitDir(t *testing.T, gitDir string) {
	t.Helper()
	for _, name := range []string{"objects", "refs"} {
		if err := os.MkdirAll(filepath.Join(gitDir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGitWorktreePresent_Positive_RealRepo(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	completeGitDir(t, gitDir)
	if present, err := GitWorktreePresent(t.Context(), nested); err != nil || !present {
		t.Fatalf("real repository not detected through ancestor: present=%v, err=%v", present, err)
	}
}

func TestGitWorktreePresent_Positive_LinkedWorktree(t *testing.T) {
	wtRoot := t.TempDir()
	wtNested := filepath.Join(wtRoot, "sub")
	if err := os.MkdirAll(wtNested, 0o755); err != nil {
		t.Fatal(err)
	}
	targetDir := filepath.Join(wtRoot, "linked-target")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "HEAD"), []byte("ref: refs/heads/feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtRoot, ".git"), []byte("gitdir: linked-target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if present, err := GitWorktreePresent(t.Context(), wtNested); err != nil || !present {
		t.Fatalf("linked-worktree not detected through ancestor: present=%v, err=%v", present, err)
	}
}

func TestGitWorktreePresent_Negative_HeadOnlyDirectory(t *testing.T) {
	// git requires objects/ and refs/ in a git directory and walks past one with only HEAD
	// (verified with git 2.56). GitWorktreePresent must agree; HasValidGitRepo keeps calling
	// it Live because topology fixtures rely on a HEAD-only .git.
	headOnlyRoot := t.TempDir()
	headOnlyGit := filepath.Join(headOnlyRoot, ".git")
	if err := os.MkdirAll(headOnlyGit, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(headOnlyGit, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(headOnlyRoot, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if present, err := GitWorktreePresent(t.Context(), child); err != nil || present {
		t.Fatalf("HEAD-only .git reported as repository: present=%v, err=%v", present, err)
	}
	if !HasValidGitRepo(headOnlyRoot) {
		t.Fatal("HasValidGitRepo must keep accepting a HEAD-only fixture")
	}
	// Boundary: refs/ present but objects/ missing is still incomplete.
	if err := os.MkdirAll(filepath.Join(headOnlyGit, "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if present, err := GitWorktreePresent(t.Context(), child); err != nil || present {
		t.Fatalf("refs-only .git reported as repository: present=%v, err=%v", present, err)
	}
	completeGitDir(t, headOnlyGit)
	if present, err := GitWorktreePresent(t.Context(), child); err != nil || !present {
		t.Fatalf("complete .git not detected: present=%v, err=%v", present, err)
	}
}

func TestGitWorktreePresent_Positive_SymlinkedGit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating repository symlinks requires optional Windows privileges")
	}
	realRoot := t.TempDir()
	realGit := filepath.Join(realRoot, ".git")
	if err := os.MkdirAll(realGit, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realGit, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	completeGitDir(t, realGit)
	childRoot := t.TempDir()
	if err := os.Symlink(realGit, filepath.Join(childRoot, ".git")); err != nil {
		t.Fatal(err)
	}
	childNested := filepath.Join(childRoot, "sub", "dir")
	if err := os.MkdirAll(childNested, 0o755); err != nil {
		t.Fatal(err)
	}
	if present, err := GitWorktreePresent(t.Context(), childNested); err != nil || !present {
		t.Fatalf("symlinked .git not detected through ancestor: present=%v, err=%v", present, err)
	}
	if present, err := GitWorktreePresent(t.Context(), childRoot); err != nil || !present {
		t.Fatalf("symlinked .git not detected at root: present=%v, err=%v", present, err)
	}
}

func TestGitWorktreePresent_Negative_EmptyTree(t *testing.T) {
	emptyRoot := t.TempDir()
	emptyNested := filepath.Join(emptyRoot, "nested")
	if err := os.Mkdir(emptyNested, 0o755); err != nil {
		t.Fatal(err)
	}
	if present, err := GitWorktreePresent(t.Context(), emptyNested); err != nil || present {
		t.Fatalf("empty tree unexpectedly detected: present=%v, err=%v", present, err)
	}
}

func TestGitWorktreePresent_Negative_PlantedEmptyGit(t *testing.T) {
	plantedEmpty := t.TempDir()
	if err := os.Mkdir(filepath.Join(plantedEmpty, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	childEmpty := filepath.Join(plantedEmpty, "child")
	if err := os.Mkdir(childEmpty, 0o755); err != nil {
		t.Fatal(err)
	}
	if present, err := GitWorktreePresent(t.Context(), childEmpty); err != nil || present {
		t.Fatalf("planted empty .git directory detected as repository: present=%v, err=%v", present, err)
	}
}

func TestGitWorktreePresent_Negative_PlantedNoHead(t *testing.T) {
	plantedNoHead := t.TempDir()
	noHeadGit := filepath.Join(plantedNoHead, ".git")
	if err := os.Mkdir(noHeadGit, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noHeadGit, "config"), []byte("[core]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	childNoHead := filepath.Join(plantedNoHead, "child")
	if err := os.Mkdir(childNoHead, 0o755); err != nil {
		t.Fatal(err)
	}
	if present, err := GitWorktreePresent(t.Context(), childNoHead); err != nil || present {
		t.Fatalf("planted .git without HEAD detected as repository: present=%v, err=%v", present, err)
	}
}

func TestGitWorktreePresent_Negative_PlantedDirectoryHead(t *testing.T) {
	plantedHeadDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(plantedHeadDir, ".git", "HEAD"), 0o755); err != nil {
		t.Fatal(err)
	}
	childHeadDir := filepath.Join(plantedHeadDir, "child")
	if err := os.Mkdir(childHeadDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if present, err := GitWorktreePresent(t.Context(), childHeadDir); err != nil || present {
		t.Fatalf("planted .git with directory HEAD detected as repository: present=%v, err=%v", present, err)
	}
}

// git stops at a malformed .git file and dies; the walk must stop there too (never reach an outer repository).
func TestGitWorktreePresent_Positive_PlantedMalformedGitFileStopsWalk(t *testing.T) {
	cases := []string{"", "hello world\n", "gitdir:\n", "gitdir:target\n", " gitdir: target\n"}
	for _, content := range cases {
		plantedFile := t.TempDir()
		if err := os.WriteFile(filepath.Join(plantedFile, ".git"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		child := filepath.Join(plantedFile, "child")
		if err := os.Mkdir(child, 0o755); err != nil {
			t.Fatal(err)
		}
		if present, err := GitWorktreePresent(t.Context(), child); err != nil || !present {
			t.Fatalf("planted malformed .git file %q must stop the walk: present=%v, err=%v", content, present, err)
		}
	}
}

func TestGitWorktreePresent_Positive_DanglingGitlinkMetadataPresent(t *testing.T) {
	danglingRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(danglingRoot, ".git"), []byte("gitdir: nonexistent-target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	danglingChild := filepath.Join(danglingRoot, "child")
	if err := os.Mkdir(danglingChild, 0o755); err != nil {
		t.Fatal(err)
	}
	if present, err := GitWorktreePresent(t.Context(), danglingChild); err != nil || !present {
		t.Fatalf("dangling gitdir metadata not detected as present: present=%v, err=%v", present, err)
	}
	if HasValidGitRepo(danglingRoot) {
		t.Fatal("dangling gitdir target must not be a valid git repo")
	}
}

func TestGitWorktreePresent_Positive_HeadlessTargetMetadataPresent(t *testing.T) {
	noHeadTargetRoot := t.TempDir()
	headlessTarget := filepath.Join(noHeadTargetRoot, "target")
	if err := os.Mkdir(headlessTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noHeadTargetRoot, ".git"), []byte("gitdir: target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	childTarget := filepath.Join(noHeadTargetRoot, "child")
	if err := os.Mkdir(childTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if present, err := GitWorktreePresent(t.Context(), childTarget); err != nil || !present {
		t.Fatalf("gitlink pointing to headless target metadata not detected: present=%v, err=%v", present, err)
	}
	if HasValidGitRepo(noHeadTargetRoot) {
		t.Fatal("gitlink pointing to headless target must not be a valid git repo")
	}
}

func TestGitWorktreePresent_Boundary(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "file.txt")
	if err := os.WriteFile(filePath, []byte("data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := GitWorktreePresent(t.Context(), filePath); err == nil {
		t.Fatal("file root must return error")
	}
	var nilCtx context.Context
	if _, err := GitWorktreePresent(nilCtx, root); err == nil {
		t.Fatal("nil context must return error")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := GitWorktreePresent(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context must return context.Canceled, got %v", err)
	}
}

func TestHasValidGitRepo_Positive(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !HasValidGitRepo(repo) {
		t.Fatalf("HasValidGitRepo(%s) = false, want true", repo)
	}
}

func TestHasValidGitRepo_Negative(t *testing.T) {
	emptyRepo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(emptyRepo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if HasValidGitRepo(emptyRepo) {
		t.Fatalf("HasValidGitRepo(%s) = true, want false", emptyRepo)
	}
}

func TestHasValidGitRepo_Boundary(t *testing.T) {
	if HasValidGitRepo("") || HasValidGitRepo("   ") {
		t.Fatal("empty/whitespace path accepted as repo")
	}
}

// plantGit plants a .git file with content in a fresh root and returns the root plus a
// child directory below it.
func plantGit(t *testing.T, content string) (string, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, child
}

// A dangling .git symlink is skipped by git, which keeps walking up; it is no repository.
func TestGitWorktreePresent_Negative_DanglingSymlinkGit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating repository symlinks requires optional Windows privileges")
	}
	root := t.TempDir()
	if err := os.Symlink(filepath.Join(root, "nowhere"), filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if present, err := GitWorktreePresent(t.Context(), child); err != nil || present {
		t.Fatalf("dangling .git symlink detected as repository: present=%v, err=%v", present, err)
	}
	entry, err := inspectGitEntry(filepath.Join(root, ".git"), true)
	if err != nil || entry.state != GitMetadataAbsent || entry.stopsWalk {
		t.Fatalf("followed dangling symlink entry = %+v, %v; want Absent that does not stop the walk", entry, err)
	}
	if state, err := InspectGitMetadata(filepath.Join(root, ".git")); err != nil || state != GitMetadataUnknown {
		t.Fatalf("unfollowed symlink state = %v, %v; want Unknown", state, err)
	}
}

// Symlink to a gitlink file with a malformed or oversized body stops the walk like a plain one.
func TestGitWorktreePresent_Positive_SymlinkedMalformedAndOversizedGitlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating repository symlinks requires optional Windows privileges")
	}
	for name, content := range map[string]string{
		"malformed": "gitdir:nospace\n",
		"empty":     "gitdir: \n",
		"oversized": "gitdir: " + strings.Repeat("a", MaxGitlinkBytes) + "\n",
	} {
		root := t.TempDir()
		real := filepath.Join(t.TempDir(), "gitfile")
		if err := os.WriteFile(real, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, filepath.Join(root, ".git")); err != nil {
			t.Fatal(err)
		}
		if present, err := GitWorktreePresent(t.Context(), root); err != nil || !present {
			t.Fatalf("%s symlinked gitlink must stop the walk: present=%v, err=%v", name, present, err)
		}
	}
}

// Both predicates read one decision: for every entry they agree on live, and a
// walk-stopping entry is never live unless it is a live repository.
func TestGitPredicates_Boundary_AgreeOnSameEntry(t *testing.T) {
	oversized := "gitdir: " + strings.Repeat("a", MaxGitlinkBytes) + "\n"
	cases := []struct {
		name, content string
		wantState     GitMetadataState
		wantPresent   bool
	}{
		{"valid-target-missing", "gitdir: gone\n", GitMetadataDangling, true},
		{"empty-file", "", GitMetadataMalformed, true},
		{"no-prefix", "target\n", GitMetadataMalformed, true},
		{"empty-target", "gitdir: \n", GitMetadataMalformed, true},
		{"oversized", oversized, GitMetadataMalformed, true},
	}
	for _, tc := range cases {
		root, child := plantGit(t, tc.content)
		state, err := InspectWorktreeGitMetadata(root)
		if err != nil || state != tc.wantState {
			t.Fatalf("%s: state = %v, %v; want %v", tc.name, state, err, tc.wantState)
		}
		present, err := GitWorktreePresent(t.Context(), child)
		if err != nil || present != tc.wantPresent {
			t.Fatalf("%s: present = %v, %v; want %v", tc.name, present, err, tc.wantPresent)
		}
		entry, err := inspectGitEntry(filepath.Join(root, ".git"), false)
		if err != nil || present != entry.stopsWalk {
			t.Fatalf("%s: predicates disagree: present=%v entry=%+v err=%v", tc.name, present, entry, err)
		}
		if HasValidGitRepo(root) {
			t.Fatalf("%s: must not be a live repository", tc.name)
		}
	}
}

func TestGitMetadataState_Positive_Mapping(t *testing.T) {
	walk := map[GitMetadataState]bool{
		GitMetadataLive: true, GitMetadataDangling: true, GitMetadataMalformed: true,
		GitMetadataAbsent: false, GitMetadataHeadless: false, GitMetadataInvalid: false, GitMetadataUnknown: false,
	}
	for state, want := range walk {
		if gitlinkStopsWalk(state) != want {
			t.Fatalf("gitlinkStopsWalk(%v) = %v, want %v", state, !want, want)
		}
	}
	for _, state := range []GitMetadataState{GitMetadataUnknown, GitMetadataDangling, GitMetadataInvalid, GitMetadataMalformed} {
		if !state.Indeterminate() {
			t.Fatalf("%v must be indeterminate", state)
		}
	}
	for _, state := range []GitMetadataState{GitMetadataAbsent, GitMetadataHeadless, GitMetadataLive} {
		if state.Indeterminate() {
			t.Fatalf("%v must not be indeterminate", state)
		}
	}
}

func TestParseGitlink_PositiveNegativeBoundary(t *testing.T) {
	if target, ok := parseGitlink([]byte("gitdir: ../x\r\n")); !ok || target != "../x" {
		t.Fatalf("valid gitlink = %q, %v", target, ok)
	}
	for _, bad := range []string{"", "gitdir:", "gitdir: ", "gitdir: \n", "gitdir:x", " gitdir: x", "x"} {
		if target, ok := parseGitlink([]byte(bad)); ok || target != "" {
			t.Fatalf("parseGitlink(%q) = %q, %v; want rejected", bad, target, ok)
		}
	}
}

func TestInspectGitDirectory_PositiveNegativeBoundary(t *testing.T) {
	dir := t.TempDir()
	if state, err := inspectGitDirectory(dir); err != nil || state != GitMetadataHeadless {
		t.Fatalf("empty dir = %v, %v; want Headless", state, err)
	}
	if err := os.Mkdir(filepath.Join(dir, "HEAD"), 0o755); err != nil {
		t.Fatal(err)
	}
	if state, err := inspectGitDirectory(dir); err != nil || state != GitMetadataInvalid {
		t.Fatalf("HEAD directory = %v, %v; want Invalid", state, err)
	}
	live := t.TempDir()
	if err := os.WriteFile(filepath.Join(live, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if state, err := inspectGitDirectory(live); err != nil || state != GitMetadataLive {
		t.Fatalf("HEAD file = %v, %v; want Live", state, err)
	}
}

func TestInspectGitlinkFile_PositiveNegative(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if state, err := inspectGitlinkFile(filepath.Join(root, ".git")); err != nil || state != GitMetadataDangling {
		t.Fatalf("headless target = %v, %v; want Dangling", state, err)
	}
	if err := os.WriteFile(filepath.Join(target, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if state, err := inspectGitlinkFile(filepath.Join(root, ".git")); err != nil || state != GitMetadataLive {
		t.Fatalf("live target = %v, %v; want Live", state, err)
	}
	if got, ok, err := ResolveGitlinkTarget(root); err != nil || !ok || got != target {
		t.Fatalf("ResolveGitlinkTarget = %q, %v, %v", got, ok, err)
	}
}

func TestResolveGitlinkTarget_Boundary_Oversized(t *testing.T) {
	root, _ := plantGit(t, "gitdir: "+strings.Repeat("a", MaxGitlinkBytes)+"\n")
	got, ok, err := ResolveGitlinkTarget(root)
	if err != nil || ok || got != "" {
		t.Fatalf("oversized gitlink = %q, %v, %v; want empty, false, nil", got, ok, err)
	}
}

func TestGitWorktreePresent_Negative_FilesystemErrorPropagates(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission denial needs POSIX modes and a non-root user")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(locked, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(child, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(child, 0o755); err != nil {
			t.Error(err)
		}
	})
	if _, err := GitWorktreePresent(t.Context(), child); err == nil {
		t.Fatal("unreadable directory must surface a filesystem error")
	}
}

func TestHeadContentValid_PositiveNegativeBoundary(t *testing.T) {
	oid := strings.Repeat("a", 40)
	cases := map[string]bool{
		"ref: refs/heads/main\n":      true,
		"ref:refs/heads/main":         true,
		"ref:   \trefs/heads/main":    true,
		"ref: refs/":                  true,
		"ref: refs/heads/a..b":        true,
		oid + "\n":                    true,
		oid + "junk":                  true,
		strings.Repeat("F", 64):       true,
		"":                            false,
		"garbage\n":                   false,
		"ref: HEAD\n":                 false,
		"ref: heads/main":             false,
		"ref:":                        false,
		strings.Repeat("a", 39):       false,
		strings.Repeat("a", 39) + "g": false,
	}
	for content, want := range cases {
		if got := headContentValid([]byte(content)); got != want {
			t.Errorf("headContentValid(%q) = %v, want %v", content, got, want)
		}
	}
}

// Exactly where GitWorktreePresent and HasValidGitRepo answer differently, by design.
func TestGitPredicates_Boundary_DocumentedDivergence(t *testing.T) {
	t.Run("head-only-directory", func(t *testing.T) {
		root := t.TempDir()
		plantGitDirectory(t, root, false, false, true)
		present, err := GitWorktreePresent(t.Context(), root)
		if err != nil || present || !HasValidGitRepo(root) {
			t.Fatalf("present=%v err=%v live=%v; want not present, live", present, err, HasValidGitRepo(root))
		}
	})
	t.Run("garbage-head-complete-directory", func(t *testing.T) {
		root := t.TempDir()
		plantGitDirectory(t, root, true, true, false)
		if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("garbage\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		present, err := GitWorktreePresent(t.Context(), root)
		if err != nil || present || !HasValidGitRepo(root) {
			t.Fatalf("present=%v err=%v live=%v; want not present, live", present, err, HasValidGitRepo(root))
		}
	})
	t.Run("symlinked-complete-repository", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("creating repository symlinks requires optional Windows privileges")
		}
		real := t.TempDir()
		plantGitDirectory(t, real, true, true, true)
		root := t.TempDir()
		if err := os.Symlink(filepath.Join(real, ".git"), filepath.Join(root, ".git")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		present, err := GitWorktreePresent(t.Context(), root)
		state, serr := InspectWorktreeGitMetadata(root)
		if err != nil || !present || serr != nil || state != GitMetadataUnknown || HasValidGitRepo(root) {
			t.Fatalf("present=%v err=%v state=%v/%v; want present, Unknown, not live", present, err, state, serr)
		}
	})
}

// A HEAD symlink must point into refs/; the target is not resolved, so a dangling one passes.
func TestValidateHeadRef_PositiveNegative_Symlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating repository symlinks requires optional Windows privileges")
	}
	for target, want := range map[string]bool{"refs/heads/main": true, "/etc/passwd": false, "../x": false} {
		head := filepath.Join(t.TempDir(), "HEAD")
		if err := os.Symlink(target, head); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if got, err := validateHeadRef(head); err != nil || got != want {
			t.Errorf("HEAD -> %s: valid=%v err=%v, want %v", target, got, err, want)
		}
	}
	if got, err := validateHeadRef(filepath.Join(t.TempDir(), "HEAD")); err != nil || got {
		t.Errorf("missing HEAD: valid=%v err=%v, want false", got, err)
	}
}

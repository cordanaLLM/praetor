package changelog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/contextopt"

	"github.com/cordanaLLM/praetor/internal/util"
)

func interruptedRender(t *testing.T) (string, string, []byte) {
	t.Helper()
	if blocked, reason := readOnlyDirectoryBlocksRemoval(t); !blocked {
		t.Skip(reason)
	}
	root := t.TempDir()
	fragment, err := CreateFragment(root, Fragment{Type: TypeFixed, Title: "Preserve one release entry"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(fragment)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "changelog.d")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	err = RenderReleaseContext(t.Context(), root, "1.2.3", "2026-09-12")
	if chmodErr := os.Chmod(dir, 0o700); chmodErr != nil {
		t.Fatal(chmodErr)
	}
	if err == nil || !strings.Contains(err.Error(), "remove rendered fragment") {
		t.Fatalf("expected cleanup failure after publication: %v", err)
	}
	assertReleaseCount(t, root, 1)
	info, err := os.Stat(filepath.Join(root, renderJournalName))
	if err != nil || (util.ModeIsProtection() && info.Mode().Perm() != 0o600) {
		t.Fatalf("private recovery journal missing: %v, %v", info, err)
	}
	return root, fragment, before
}

func assertReleaseCount(t *testing.T, root string, want int) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	if err != nil || strings.Count(string(data), "## [1.2.3]") != want {
		t.Fatalf("incorrect published release count: %q, %v", data, err)
	}
}

func TestRenderRetryResumesCleanupWithoutDuplicateRelease(t *testing.T) {
	root, fragment, _ := interruptedRender(t)
	newFragment, err := CreateFragment(root, Fragment{Type: TypeAdded, Title: "Work for a later release"})
	if err != nil {
		t.Fatal(err)
	}
	if err := RenderReleaseContext(t.Context(), root, "1.2.3", "2026-09-12"); err != nil {
		t.Fatal(err)
	}
	assertReleaseCount(t, root, 1)
	for _, path := range []string{fragment, filepath.Join(root, renderJournalName)} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cleanup did not remove recorded input/journal %s: %v", path, err)
		}
	}
	if _, err := os.Stat(newFragment); err != nil {
		t.Fatalf("recovery removed a later fragment: %v", err)
	}
	if err := RenderReleaseContext(t.Context(), root, "1.2.3", "2026-09-12"); err == nil {
		t.Fatal("new fragments cannot silently republish the same release version")
	}
	assertReleaseCount(t, root, 1)
}

func TestRenderRetryPreservesChangedFragment(t *testing.T) {
	root, fragment, before := interruptedRender(t)
	changed := []byte("type: fixed\ntitle: Changed after publication\n")
	if err := os.WriteFile(fragment, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RenderReleaseContext(t.Context(), root, "1.2.3", "2026-09-12"); err == nil {
		t.Fatal("changed fragment must stop cleanup")
	}
	data, err := os.ReadFile(fragment)
	if err != nil || string(data) != string(changed) {
		t.Fatalf("changed fragment was lost: %q, %v", data, err)
	}
	assertReleaseCount(t, root, 1)
	if err := os.WriteFile(fragment, before, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RenderReleaseContext(t.Context(), root, "1.2.3", "2026-09-12"); err != nil {
		t.Fatal(err)
	}
}

func TestRenderRetryRejectsChangedOutputAndOtherVersion(t *testing.T) {
	root, fragment, _ := interruptedRender(t)
	if err := RenderReleaseContext(t.Context(), root, "2.0.0", "2026-09-12"); err == nil {
		t.Fatal("another version cannot consume pending render")
	}
	path := filepath.Join(root, "CHANGELOG.md")
	if err := os.WriteFile(path, []byte("manually replaced changelog\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RenderReleaseContext(t.Context(), root, "1.2.3", "2026-09-12"); err == nil {
		t.Fatal("changed output cannot authorize fragment deletion")
	}
	if _, err := os.Stat(fragment); err != nil {
		t.Fatalf("original fragment was lost: %v", err)
	}
}

func TestRenderRetryAcceptsAlreadyCleanedRecordedFragment(t *testing.T) {
	root, fragment, _ := interruptedRender(t)
	if err := os.Remove(fragment); err != nil {
		t.Fatal(err)
	}
	if err := RenderReleaseContext(t.Context(), root, "1.2.3", "2026-09-12"); err != nil {
		t.Fatal(err)
	}
	assertReleaseCount(t, root, 1)
}

func TestRenderRejectsMultipleYAMLDocumentsBeforeWrites(t *testing.T) {
	root := t.TempDir()
	fragment, err := CreateFragment(root, Fragment{Type: TypeAdded, Title: "first"})
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("type: added\ntitle: first\n---\ntype: fixed\ntitle: second\n")
	if err := os.WriteFile(fragment, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RenderReleaseContext(t.Context(), root, "1.2.3", "2026-09-12"); err == nil {
		t.Fatal("trailing document was silently discarded")
	}
	for _, name := range []string{"CHANGELOG.md", renderJournalName} {
		if _, err := os.Stat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("invalid fragment caused a write to %s: %v", name, err)
		}
	}
	retained, err := os.ReadFile(fragment)
	if err != nil || string(retained) != string(data) {
		t.Fatalf("invalid fragment was changed: %q, %v", retained, err)
	}
}

func TestRenderRejectsCancellationAndPreservesHeaderlessContent(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := RenderReleaseContext(ctx, root, "1.2.3", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled render should fail even with no fragments: %v", err)
	}
	if _, err := CreateFragment(root, Fragment{Type: TypeAdded, Title: "record me"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "CHANGELOG.md")
	if err := os.WriteFile(path, []byte("existing freeform notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RenderReleaseContext(t.Context(), root, "1.2.3", "2026-09-12"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "record me") || !strings.Contains(string(data), "existing freeform notes") {
		t.Fatalf("headerless changelog or new fragment was lost: %q, %v", data, err)
	}
}

func TestRenderPendingJournalRejectsMissingFragmentDirectory(t *testing.T) {
	root, fragment, _ := interruptedRender(t)
	if err := os.Remove(fragment); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "changelog.d")); err != nil {
		t.Fatal(err)
	}
	if err := RenderReleaseContext(t.Context(), root, "1.2.3", "2026-09-12"); err == nil {
		t.Fatal("missing fragment directory falsely completed pending recovery")
	}
	if _, err := os.Stat(filepath.Join(root, renderJournalName)); err != nil {
		t.Fatalf("recovery journal was lost: %v", err)
	}
}

func TestRenderPublishedReadbackRejectsChangedOutput(t *testing.T) {
	root, fragment, _ := interruptedRender(t)
	pinned, err := contextopt.OpenDirectory(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := pinned.Close(); err != nil {
			t.Error(err)
		}
	}()
	journal, _, err := loadRenderJournal(t.Context(), pinned)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CHANGELOG.md"), []byte("external edit after publication\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyPublishedChangelog(t.Context(), pinned, journal); err == nil {
		t.Fatal("changed published content accepted for cleanup")
	}
	if _, err := os.Stat(fragment); err != nil {
		t.Fatalf("readback changed source fragments: %v", err)
	}
}

// readOnlyDirectoryBlocksRemoval measures whether a directory without write permission
// stops a file inside it from being removed on this host, and says why not when it does not.
//
// interruptedRender provokes a publication whose cleanup fails by making changelog.d
// read-only. POSIX refuses the unlink; Windows ignores the directory mode and removes the
// file, so the render completed cleanly and every case built on the helper failed with
// "expected cleanup failure after publication" -- reporting a missing failure the platform
// could never have produced. The answer is measured on a throwaway directory rather than
// inferred from the platform name, because a POSIX process running as root cannot provoke
// it either, and should skip for the same stated reason rather than fail.
func readOnlyDirectoryBlocksRemoval(t *testing.T) (bool, string) {
	t.Helper()
	dir := t.TempDir()
	victim := filepath.Join(dir, "probe")
	if err := os.WriteFile(victim, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Errorf("restore probe directory mode: %v", err)
		}
	})
	if err := os.Remove(victim); err != nil {
		return true, ""
	}
	return false, "this host removes a file from a read-only directory, so an interrupted " +
		"publication cannot be provoked here; covered on the Linux leg of the platform matrix"
}

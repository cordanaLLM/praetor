package util

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestConfinePath_Positive(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nested", "deep"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cases := []struct {
		name string
		rel  string
		want string
	}{
		{"plain file", "file.txt", filepath.Join(root, "file.txt")},
		{"nested existing dir", filepath.Join("nested", "deep"), filepath.Join(root, "nested", "deep")},
		{"not yet existing leaf", filepath.Join("nested", "deep", "new.json"), filepath.Join(root, "nested", "deep", "new.json")},
		{"interior dot dot stays inside", filepath.Join("nested", "..", "ok.txt"), filepath.Join(root, "ok.txt")},
		{"empty rel is the root", "", filepath.Clean(root)},
	}

	for _, tc := range cases {
		got, err := ConfinePath(root, tc.rel)
		if err != nil {
			t.Errorf("%s: ConfinePath(%q, %q) returned error: %v", tc.name, root, tc.rel, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: ConfinePath(%q, %q) = %q, want %q", tc.name, root, tc.rel, got, tc.want)
		}
	}
}

func TestConfinePath_Negative(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("s"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	// Lexical traversal.
	if _, err := ConfinePath(root, filepath.Join("..", "escape.txt")); !errors.Is(err, ErrPathEscapesRoot) {
		t.Errorf("expected ErrPathEscapesRoot for ../escape.txt, got %v", err)
	}
	if _, err := ConfinePath(root, filepath.Join("a", "..", "..", "..", "etc", "passwd")); !errors.Is(err, ErrPathEscapesRoot) {
		t.Errorf("expected ErrPathEscapesRoot for deep traversal, got %v", err)
	}

	// Absolute member path.
	if _, err := ConfinePath(root, filepath.Join(outside, "secret.txt")); !errors.Is(err, ErrAbsoluteRelPath) {
		t.Errorf("expected ErrAbsoluteRelPath for absolute rel, got %v", err)
	}

	// Symlink escape: link inside root pointing at a directory outside root.
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}
	if _, err := ConfinePath(root, filepath.Join("link", "secret.txt")); !errors.Is(err, ErrPathEscapesRoot) {
		t.Errorf("expected ErrPathEscapesRoot through symlink, got %v", err)
	}

	// Empty root.
	if _, err := ConfinePath("  ", "x"); !errors.Is(err, ErrEmptyRoot) {
		t.Errorf("expected ErrEmptyRoot, got %v", err)
	}
}

func TestConfinePath_Boundary(t *testing.T) {
	root := t.TempDir()

	// Boundary: a sibling directory sharing the root's name prefix must not pass.
	sibling := filepath.Clean(root) + "-evil"
	if err := os.MkdirAll(sibling, 0o700); err != nil {
		t.Fatalf("mkdir sibling: %v", err)
	}
	t.Cleanup(func() {
		if rmErr := os.RemoveAll(sibling); rmErr != nil {
			t.Logf("cleanup %s: %v", sibling, rmErr)
		}
	})
	rel, err := filepath.Rel(root, filepath.Join(sibling, "x"))
	if err != nil {
		t.Fatalf("rel: %v", err)
	}
	if _, err := ConfinePath(root, rel); !errors.Is(err, ErrPathEscapesRoot) {
		t.Errorf("expected ErrPathEscapesRoot for prefix sibling %q, got %v", rel, err)
	}

	// Boundary: "." resolves to the root itself.
	got, err := ConfinePath(root, ".")
	if err != nil || got != filepath.Clean(root) {
		t.Errorf(`ConfinePath(root, ".") = (%q, %v), want (%q, nil)`, got, err, filepath.Clean(root))
	}

	// Boundary: a symlink that stays inside the root is accepted.
	inner := filepath.Join(root, "inner")
	if err := os.MkdirAll(inner, 0o700); err != nil {
		t.Fatalf("mkdir inner: %v", err)
	}
	if err := os.Symlink(inner, filepath.Join(root, "alias")); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}
	if _, err := ConfinePath(root, filepath.Join("alias", "f.txt")); err != nil {
		t.Errorf("expected inbound symlink to be accepted, got %v", err)
	}
}

// TestConfinePath_Positive_FilesystemRoot pins BUG-825: a root of "/" (a volume root on
// Windows) already ends in a separator, and withinRoot used to demand a doubled one, so
// every path under it was refused.
func TestConfinePath_Positive_FilesystemRoot(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	volumeRoot := filepath.VolumeName(dir) + string(filepath.Separator)
	want := filepath.Join(dir, "ledger.json")
	rel, err := filepath.Rel(volumeRoot, want)
	if err != nil {
		t.Fatalf("rel: %v", err)
	}
	got, err := ConfinePath(volumeRoot, rel)
	if err != nil {
		t.Fatalf("ConfinePath(%q, %q): %v", volumeRoot, rel, err)
	}
	if got != want {
		t.Errorf("ConfinePath(%q, %q) = %q, want %q", volumeRoot, rel, got, want)
	}
	if got, err := ConfinePath(volumeRoot, "."); err != nil || got != volumeRoot {
		t.Errorf(`ConfinePath(%q, ".") = (%q, %v), want (%q, nil)`, volumeRoot, got, err, volumeRoot)
	}
}

func TestWithinRoot_Boundary_SeparatorTerminatedRoot(t *testing.T) {
	sep := string(filepath.Separator)
	repo := sep + "repo"
	cases := []struct {
		root, path string
		want       bool
	}{
		{sep, sep, true},
		{sep, sep + "etc", true},
		{repo, repo, true},
		{repo, repo + sep + "file", true},
		{repo, repo + "-evil", false},
		{repo, sep, false},
		{repo + sep + "a", repo, false},
	}
	for _, tc := range cases {
		if got := withinRoot(tc.root, tc.path); got != tc.want {
			t.Errorf("withinRoot(%q, %q) = %v, want %v", tc.root, tc.path, got, tc.want)
		}
	}
}

// TestConfinePath_Negative_SymlinkedAncestor pins BUG-826's check: a final element linking
// back into the root used to vouch for a parent directory outside it. It also pins the
// lexical result: callers relate two results (privatePlanningOutput takes the Rel of
// ConfinePath(root, ".workingdir") and an output directory below it), so a result must
// never be rewritten to the link's target.
func TestConfinePath_Negative_SymlinkedAncestor(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	inner := filepath.Join(root, "inner")
	if err := os.MkdirAll(inner, 0o700); err != nil {
		t.Fatalf("mkdir inner: %v", err)
	}
	if err := os.Symlink(inner, filepath.Join(root, "alias")); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	// A path through an in-root link is accepted and returned as the lexical join.
	got, err := ConfinePath(root, filepath.Join("alias", "sub", "ledger.json"))
	if err != nil {
		t.Fatalf("ConfinePath through an in-root link: %v", err)
	}
	if want := filepath.Join(root, "alias", "sub", "ledger.json"); got != want {
		t.Errorf("ConfinePath through an in-root link = %q, want the lexical %q", got, want)
	}
	private, err := ConfinePath(root, "alias")
	if err != nil {
		t.Fatalf("ConfinePath(root, alias): %v", err)
	}
	if rel, err := filepath.Rel(private, got); err != nil || rel != filepath.Join("sub", "ledger.json") {
		t.Errorf("Rel(%q, %q) = (%q, %v), want the results to share the alias prefix", private, got, rel, err)
	}

	// A final element that is itself a link stays visible to no-follow callers.
	victim := filepath.Join(inner, "victim.txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatalf("seed victim: %v", err)
	}
	if err := os.Symlink(victim, filepath.Join(root, "leaf.json")); err != nil {
		t.Fatalf("symlink leaf: %v", err)
	}
	leaf, err := ConfinePath(root, "leaf.json")
	if err != nil || leaf != filepath.Join(root, "leaf.json") {
		t.Fatalf("ConfinePath(root, leaf.json) = (%q, %v), want the unresolved link", leaf, err)
	}
	if err := WriteFileNoFollow(leaf, []byte("clobber"), 0o600); !errors.Is(err, ErrSymlinkDestination) {
		t.Errorf("expected the confined link to be refused, got %v", err)
	}

	// A parent directory outside the root is refused even when the final element links back in.
	if err := os.Symlink(outside, filepath.Join(root, "out")); err != nil {
		t.Fatalf("symlink out: %v", err)
	}
	if err := os.Symlink(victim, filepath.Join(outside, "back.json")); err != nil {
		t.Fatalf("symlink back: %v", err)
	}
	if got, err := ConfinePath(root, filepath.Join("out", "back.json")); !errors.Is(err, ErrPathEscapesRoot) {
		t.Errorf("expected ErrPathEscapesRoot for a parent outside the root, got (%q, %v)", got, err)
	}
	if data, err := os.ReadFile(victim); err != nil || string(data) != "keep" { // #nosec G304 -- test-local path from t.TempDir
		t.Errorf("victim = (%q, %v), want it untouched", data, err)
	}
}

// confinedFixture returns a root holding a real directory "inner", a relative in-root link
// "alias" to it and a link "out" to a directory outside the root, plus that outside
// directory. It skips where the platform cannot create symbolic links.
func confinedFixture(t *testing.T) (root, outside string) {
	t.Helper()
	root = t.TempDir()
	outside = t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "inner"), 0o700); err != nil {
		t.Fatalf("mkdir inner: %v", err)
	}
	if err := os.Symlink("inner", filepath.Join(root, "alias")); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "out")); err != nil {
		t.Fatalf("symlink out: %v", err)
	}
	return root, outside
}

// assertEmptyDir fails when dir holds any entry: nothing may have been created there.
func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir %s: %v", dir, err)
	}
	if len(entries) != 0 {
		t.Errorf("expected nothing created in %s, got %v", dir, entries)
	}
}

// swapForLink replaces the directory at path with a relative symbolic link to target, the
// swap a hostile repository makes between a confinement check and the write that follows
// it. The link is relative so that only its escape, not an absolute target, gets it
// refused.
func swapForLink(t *testing.T, path, target string) {
	t.Helper()
	link, err := filepath.Rel(filepath.Dir(path), target)
	if err != nil {
		t.Fatalf("relate %s to %s: %v", target, path, err)
	}
	if err := os.Rename(path, path+".orig"); err != nil {
		t.Fatalf("move %s aside: %v", path, err)
	}
	if err := os.Symlink(link, path); err != nil {
		t.Fatalf("symlink %s: %v", path, err)
	}
}

func TestMkdirConfined_Positive(t *testing.T) {
	root, _ := confinedFixture(t)
	if err := MkdirConfined(root, filepath.Join("alias", "new", "leaf"), 0o700); err != nil {
		t.Fatalf("MkdirConfined through an in-root link: %v", err)
	}
	info, err := os.Stat(filepath.Join(root, "inner", "new", "leaf"))
	if err != nil || !info.IsDir() {
		t.Fatalf("leaf missing at the link target: %v", err)
	}
	if runtime.GOOS == "windows" {
		return // POSIX permission bits are not available on Windows.
	}
	if info.Mode().Perm()&^0o700 != 0 {
		t.Errorf("new leaf mode = %#o, want within 0700", info.Mode().Perm())
	}
	existing := filepath.Join(root, "inner", "open")
	if err := os.Mkdir(existing, 0o700); err != nil {
		t.Fatalf("mkdir existing: %v", err)
	}
	if err := os.Chmod(existing, 0o755); err != nil {
		t.Fatalf("chmod existing: %v", err)
	}
	if err := MkdirConfined(root, filepath.Join("inner", "open"), 0o750); err != nil {
		t.Fatalf("MkdirConfined on an existing leaf: %v", err)
	}
	info, err = os.Stat(existing)
	if err != nil {
		t.Fatalf("stat existing: %v", err)
	}
	if info.Mode().Perm() != 0o750 {
		t.Errorf("existing leaf mode = %v, want tightened to 0750", info.Mode())
	}
}

func TestMkdirConfined_Negative(t *testing.T) {
	root, outside := confinedFixture(t)
	cases := []struct {
		rel  string
		perm os.FileMode
		want error
	}{
		{filepath.Join("out", "planted"), 0o700, ErrPathEscapesRoot},
		{filepath.Join("..", "sibling"), 0o700, ErrPathEscapesRoot},
		{filepath.Join(outside, "abs"), 0o700, ErrAbsoluteRelPath},
		{"ok", 0o777, ErrInsecurePerm},
	}
	for _, tc := range cases {
		if err := MkdirConfined(root, tc.rel, tc.perm); !errors.Is(err, tc.want) {
			t.Errorf("MkdirConfined(%q, %#o) = %v, want %v", tc.rel, tc.perm, err, tc.want)
		}
	}
	assertEmptyDir(t, outside)
	if err := MkdirConfined(filepath.Join(root, "missing-root"), "sub", 0o700); err == nil {
		t.Errorf("expected a missing root to be refused")
	}
}

// TestMkdirConfined_Boundary_RootAndSwapAfterCheck pins the two edges of the confined
// create: a rel naming the root changes nothing, and a directory swapped for an escaping
// link after ConfinePath's check is refused by the pinned handle instead of followed,
// which MkdirSecure on the checked path does not do (BUG-826).
func TestMkdirConfined_Boundary_RootAndSwapAfterCheck(t *testing.T) {
	root, outside := confinedFixture(t)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(root, 0o755); err != nil {
			t.Fatalf("chmod root: %v", err)
		}
	}
	before, err := os.Stat(root)
	if err != nil {
		t.Fatalf("stat root: %v", err)
	}
	if err := MkdirConfined(root, ".", 0o700); err != nil {
		t.Fatalf(`MkdirConfined(root, "."): %v`, err)
	}
	after, err := os.Stat(root)
	if err != nil {
		t.Fatalf("stat root: %v", err)
	}
	if after.Mode() != before.Mode() {
		t.Errorf("root mode changed: before %v, after %v", before.Mode(), after.Mode())
	}

	absRoot, inside, err := confineBelow(root, filepath.Join("inner", "ledgers"))
	if err != nil {
		t.Fatalf("confineBelow: %v", err)
	}
	swapForLink(t, filepath.Join(root, "inner"), outside)
	if err := mkdirConfined(absRoot, inside, 0o700); !errors.Is(err, ErrPathEscapesRoot) {
		t.Errorf("directory swapped for an escaping link = %v, want ErrPathEscapesRoot like the check reports", err)
	}
	assertEmptyDir(t, outside)
}

// TestClassifyEscape_Boundary_OnlyAnEscapeIsLabelled pins classifyEscape's two
// pass-through edges: success stays nil, and a failure the re-run check does not see as an
// escape (a missing directory) is returned untouched instead of mislabelled.
func TestClassifyEscape_Boundary_OnlyAnEscapeIsLabelled(t *testing.T) {
	root, _ := confinedFixture(t)
	if err := classifyEscape(root, "inner", nil); err != nil {
		t.Errorf("classifyEscape(nil) = %v, want nil", err)
	}
	missing := errors.New("missing directory")
	if err := classifyEscape(root, filepath.Join("missing", "x"), missing); err != missing { //nolint:errorlint // identity is the contract under test
		t.Errorf("classifyEscape(non-escape) = %v, want the operation error unchanged", err)
	}
	escaped := classifyEscape(root, filepath.Join("out", "x"), missing)
	if !errors.Is(escaped, ErrPathEscapesRoot) || !errors.Is(escaped, missing) {
		t.Errorf("classifyEscape(escape) = %v, want both ErrPathEscapesRoot and the operation error", escaped)
	}
}

func TestWriteFileSecure_Positive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "receipt.key")

	if err := WriteFileSecure(path, []byte("payload"), 0o600); err != nil {
		t.Fatalf("WriteFileSecure: %v", err)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- test-local path from t.TempDir
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "payload" {
		t.Errorf("content = %q, want %q", string(data), "payload")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if ModeIsProtection() && info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %#o, want 0600", info.Mode().Perm())
	}
}

func TestWriteFileSecure_Negative(t *testing.T) {
	dir := t.TempDir()

	if err := WriteFileSecure(filepath.Join(dir, "ww.txt"), []byte("x"), 0o666); !errors.Is(err, ErrInsecurePerm) {
		t.Errorf("expected ErrInsecurePerm for world-writable mode, got %v", err)
	}
	if err := WriteFileSecure(filepath.Join(dir, "suid.txt"), []byte("x"), os.ModeSetuid|0o600); !errors.Is(err, ErrInsecurePerm) {
		t.Errorf("expected ErrInsecurePerm for setuid mode, got %v", err)
	}
	if err := WriteFileSecure(filepath.Join(dir, "missing", "x.txt"), []byte("x"), 0o600); err == nil {
		t.Error("expected error writing into a non-existent directory, got nil")
	}
}

func TestWriteFileSecure_Boundary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pre-existing.txt")

	// Boundary: the file already exists with loose permissions and must be tightened.
	if err := os.WriteFile(path, []byte("old and long"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if err := WriteFileSecure(path, []byte("new"), 0); err != nil {
		t.Fatalf("WriteFileSecure with default perm: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if ModeIsProtection() && info.Mode().Perm() != SecureFilePerm {
		t.Errorf("mode = %#o, want %#o", info.Mode().Perm(), SecureFilePerm)
	}
	if info.Size() != int64(len("new")) {
		t.Errorf("size = %d, want %d (file must be truncated)", info.Size(), len("new"))
	}

	// Boundary: zero-length payload.
	empty := filepath.Join(dir, "empty.txt")
	if err := WriteFileSecure(empty, []byte{}, 0o600); err != nil {
		t.Fatalf("WriteFileSecure empty: %v", err)
	}
	if info, err := os.Stat(empty); err != nil || info.Size() != 0 {
		t.Errorf("expected a zero-byte file, got size=%v err=%v", info, err)
	}
}

func TestWriteFileAtomic_Positive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")

	if err := WriteFileAtomic(path, []byte("payload"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- test-local path from t.TempDir
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "payload" {
		t.Errorf("content = %q, want %q", string(data), "payload")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if ModeIsProtection() && info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %#o, want 0644", info.Mode().Perm())
	}

	// No temp file survives a successful write.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "catalog.json" {
		t.Fatalf("expected exactly catalog.json in %s, got %v", dir, entries)
	}
}

func TestWriteFileAtomic_Negative(t *testing.T) {
	dir := t.TempDir()

	if err := WriteFileAtomic(filepath.Join(dir, "ww.txt"), []byte("x"), 0o666); !errors.Is(err, ErrInsecurePerm) {
		t.Errorf("expected ErrInsecurePerm for world-writable mode, got %v", err)
	}

	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permission bits are not available on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the directory permission this case relies on")
	}

	// Negative: a failed write must never touch a pre-existing target. A directory that
	// cannot be written to blocks the temp file WriteFileAtomic needs to create, before
	// it ever reaches path -- unlike WriteFileSecure's truncate-in-place, which does not
	// need directory-write permission to damage an existing file it can already open.
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	target := filepath.Join(locked, "catalog.json")
	if err := os.WriteFile(target, []byte("previous, valid catalog"), 0o644); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatalf("chmod locked dir: %v", err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) }) //nolint:errcheck // best-effort; only unblocks t.TempDir()'s own cleanup, a failure here would just leave this one test's temp dir behind

	err := WriteFileAtomic(target, []byte("new content that must never land"), 0o644)
	if err == nil {
		t.Fatal("expected an error writing into a read-only directory")
	}
	data, readErr := os.ReadFile(target) // #nosec G304 -- test-local path from t.TempDir
	if readErr != nil {
		t.Fatalf("target must survive the failed write: %v", readErr)
	}
	if string(data) != "previous, valid catalog" {
		t.Errorf("target content = %q, want the untouched previous content", string(data))
	}
	entries, readDirErr := os.ReadDir(locked)
	if readDirErr != nil {
		t.Fatalf("readdir: %v", readDirErr)
	}
	if len(entries) != 1 {
		t.Errorf("expected only the pre-existing file in %s, got %v", locked, entries)
	}
}

func TestWriteFileAtomic_Boundary(t *testing.T) {
	dir := t.TempDir()

	// Boundary: default perm (0) selects SecureFilePerm.
	path := filepath.Join(dir, "defaulted.txt")
	if err := WriteFileAtomic(path, []byte("new"), 0); err != nil {
		t.Fatalf("WriteFileAtomic with default perm: %v", err)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatalf("stat: %v", err)
	} else if ModeIsProtection() && info.Mode().Perm() != SecureFilePerm {
		t.Errorf("mode = %#o, want %#o", info.Mode().Perm(), SecureFilePerm)
	}

	// Boundary: replacing an existing file is atomic, and the new content -- not a
	// merge of old and new -- is what a reader sees afterward.
	if err := os.WriteFile(path, []byte("much longer previous content"), 0o600); err != nil {
		t.Fatalf("reseed: %v", err)
	}
	if err := WriteFileAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic replace: %v", err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() != int64(len("new")) {
		t.Errorf("expected the file truncated to len(\"new\"), got size=%v err=%v", info, err)
	}

	// Boundary: zero-length payload.
	empty := filepath.Join(dir, "empty.txt")
	if err := WriteFileAtomic(empty, []byte{}, 0o600); err != nil {
		t.Fatalf("WriteFileAtomic empty: %v", err)
	}
	if info, err := os.Stat(empty); err != nil || info.Size() != 0 {
		t.Errorf("expected a zero-byte file, got size=%v err=%v", info, err)
	}
}

func TestMkdirSecure_3D(t *testing.T) {
	root := t.TempDir()

	// Positive: nested creation with an explicit mode.
	nested := filepath.Join(root, "a", "b", "c")
	if err := MkdirSecure(nested, 0o700); err != nil {
		t.Fatalf("MkdirSecure: %v", err)
	}
	info, err := os.Stat(nested)
	if err != nil || !info.IsDir() {
		t.Fatalf("expected directory at %s, got %v (%v)", nested, info, err)
	}
	if ModeIsProtection() && info.Mode().Perm() != 0o700 {
		t.Errorf("mode = %#o, want 0700", info.Mode().Perm())
	}

	// Negative: world-writable request is refused.
	if err := MkdirSecure(filepath.Join(root, "ww"), 0o777); !errors.Is(err, ErrInsecurePerm) {
		t.Errorf("expected ErrInsecurePerm, got %v", err)
	}

	// Negative: path already exists as a file.
	filePath := filepath.Join(root, "file")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := MkdirSecure(filePath, 0o700); err == nil {
		t.Error("expected error creating a directory over an existing file, got nil")
	}

	// Boundary: perm 0 selects SecureDirPerm, and re-creating an existing directory
	// re-applies the mode.
	def := filepath.Join(root, "defaulted")
	if err := os.MkdirAll(def, 0o755); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	if err := MkdirSecure(def, 0); err != nil {
		t.Fatalf("MkdirSecure default perm: %v", err)
	}
	if info, err := os.Stat(def); err != nil || (ModeIsProtection() && info.Mode().Perm() != SecureDirPerm) {
		t.Errorf("mode = %v (%v), want %#o", info, err, SecureDirPerm)
	}
}

func TestSecurePermissions_NeverWidenExisting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not available on Windows")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "private.txt")
	if err := os.WriteFile(file, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileSecure(file, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertSecureMode(t, file, 0o600)
	if err := MkdirSecure(root, 0o755); err != nil {
		t.Fatal(err)
	}
	assertSecureMode(t, root, 0o700)
	nested := filepath.Join(root, "new", "leaf")
	if err := MkdirSecure(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	assertSecureMode(t, root, 0o700)
}

func TestSecurePermissions_IntersectRequestedBits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not available on Windows")
	}
	root := t.TempDir()
	file := filepath.Join(root, "shared.txt")
	if err := os.WriteFile(file, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0o660); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileSecure(file, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertSecureMode(t, file, 0o640)
	if err := os.Chmod(root, 0o770); err != nil {
		t.Fatal(err)
	}
	if err := MkdirSecure(root, 0o755); err != nil {
		t.Fatal(err)
	}
	assertSecureMode(t, root, 0o750)
}

func assertSecureMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); ModeIsProtection() && got != want {
		t.Errorf("%s mode = %#o, want %#o", filepath.Base(path), got, want)
	}
}

func TestValidateExecArg_3D(t *testing.T) {
	// Positive: ordinary values a caller may forward to a subprocess.
	valid := []string{"main", "v1.2.3", "internal/util", "feature_branch", "a.b-c", "HEAD~1", "owner/repo"}
	for _, v := range valid {
		if err := ValidateExecArg(v); err != nil {
			t.Errorf("ValidateExecArg(%q) = %v, want nil", v, err)
		}
	}

	// Negative: option injection and shell metacharacters.
	negatives := []struct {
		arg  string
		want error
	}{
		{"", ErrEmptyExecArg},
		{"--upload-pack=touch /tmp/pwn", ErrExecArgOption},
		{"-rf", ErrExecArgOption},
		{"a;rm -rf /", ErrExecArgMeta},
		{"a&&b", ErrExecArgMeta},
		{"a|b", ErrExecArgMeta},
		{"$(id)", ErrExecArgMeta},
		{"`id`", ErrExecArgMeta},
		{"a>b", ErrExecArgMeta},
		{"a<b", ErrExecArgMeta},
		{`a"b`, ErrExecArgMeta},
		{"a'b", ErrExecArgMeta},
		{`a\b`, ErrExecArgMeta},
		{"a\nb", ErrExecArgMeta},
		{"a\x00b", ErrExecArgMeta},
	}
	for _, tc := range negatives {
		err := ValidateExecArg(tc.arg)
		if !errors.Is(err, tc.want) {
			t.Errorf("ValidateExecArg(%q) = %v, want %v", tc.arg, err, tc.want)
		}
	}

	// Boundary: exactly at and one past the length limit.
	atLimit := strings.Repeat("a", MaxExecArgLen)
	if err := ValidateExecArg(atLimit); err != nil {
		t.Errorf("ValidateExecArg(len=%d) = %v, want nil", MaxExecArgLen, err)
	}
	overLimit := strings.Repeat("a", MaxExecArgLen+1)
	if err := ValidateExecArg(overLimit); !errors.Is(err, ErrExecArgTooLong) {
		t.Errorf("ValidateExecArg(len=%d) = %v, want ErrExecArgTooLong", MaxExecArgLen+1, err)
	}
	// Boundary: a single "-" is still an option.
	if err := ValidateExecArg("-"); !errors.Is(err, ErrExecArgOption) {
		t.Errorf(`ValidateExecArg("-") = %v, want ErrExecArgOption`, err)
	}
}

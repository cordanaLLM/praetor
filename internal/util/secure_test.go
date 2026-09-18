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

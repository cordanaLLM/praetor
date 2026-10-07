package paperclip

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

func TestSynthesizeHarness_Negative_NilContext(t *testing.T) {
	if _, err := SynthesizeHarness(nil, t.TempDir(), unknownFacts); err == nil { //nolint:staticcheck // exercising the nil-context contract
		t.Fatal("nil context must be rejected")
	}
}

// identifiedRepo returns a repository whose .standards.yaml declares acme/widget, the
// identity every test that is not about identity resolution synthesizes for, on Forgejo: it has
// no origin remote, so its forge needs repository.forge, and Forgejo's harness carries the AGit
// push the push-protocol tests run (forge_rows_test.go covers the other forges).
func identifiedRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeRepoFile(t, repo, ".standards.yaml", "repository:\n  owner: acme\n  name: widget\n  forge: forgejo\n")
	return repo
}

// writeRepoFile writes body to rel under repo, creating parent directories.
func writeRepoFile(t *testing.T, repo, rel, body string) {
	t.Helper()
	path := filepath.Join(repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// layoutShapedRepo returns <tmp>/<owner>/<name> with no manifest and no origin remote: the
// shape util.ResolveRepoIdentity's directory fallback reads as owner/name.
func layoutShapedRepo(t *testing.T, owner, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), owner, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Positive: the origin remote names the platform, not the directory the checkout sits in.
func TestSynthesizeHarness_Positive_PlatformFromOriginRemote(t *testing.T) {
	dir := layoutShapedRepo(t, "parent-dir", "checkout-dir")
	testsupport.InitGitRepoWithOrigin(t, dir, "https://github.com/acme/widget.git")
	h, err := SynthesizeHarness(t.Context(), dir, unknownFacts)
	if err != nil {
		t.Fatal(err)
	}
	if h.Platform != "acme/widget" {
		t.Fatalf("platform = %q, want the origin remote's acme/widget", h.Platform)
	}
}

// Negative (BUG-852): with no manifest identity and no origin remote there is no platform to
// write. The checkout layout is not an identity and no cordanaLLM owner is substituted.
func TestSynthesizeHarness_Negative_NoIdentityIsAnError(t *testing.T) {
	dir := layoutShapedRepo(t, "acme", "widget")
	h, err := SynthesizeHarness(t.Context(), dir, unknownFacts)
	if !errors.Is(err, util.ErrRepoIdentityUnresolved) || !errors.Is(err, config.ErrOwnerUnknown) || h != nil {
		t.Fatalf("SynthesizeHarness = (%v, %v), want no harness, ErrRepoIdentityUnresolved and ErrOwnerUnknown", h, err)
	}
}

// Boundary: a cancelled context leaves the remote unread. That is a failed read, not an
// answer that the repository has no identity, and it never degrades to a guessed platform.
func TestSynthesizeHarness_Boundary_CancelledContextIsNotUnresolved(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	h, err := SynthesizeHarness(ctx, layoutShapedRepo(t, "acme", "widget"), unknownFacts)
	if err == nil || errors.Is(err, util.ErrRepoIdentityUnresolved) || h != nil {
		t.Fatalf("SynthesizeHarness = (%v, %v), want no harness and a read error", h, err)
	}
}

func TestWriteHarness_Negative_NilHarness(t *testing.T) {
	if err := WriteHarness(nil, t.TempDir()); err == nil {
		t.Fatal("nil harness must be rejected")
	}
}

func TestWriteHarness_Negative_EscapingPaperclipDirIsRefused(t *testing.T) {
	repo := identifiedRepo(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(repo, ".paperclip")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	h, err := SynthesizeHarness(context.Background(), repo, unknownFacts)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteHarness(h, repo); !errors.Is(err, util.ErrPathEscapesRoot) {
		t.Fatalf("expected ErrPathEscapesRoot, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "harness.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("nothing may be written through the symlinked .paperclip directory")
	}
}

// Boundary: a manifest naming only an owner declares no repository name, so the name comes
// from the origin remote while the declared owner outranks the remote's (ADR-0014 §3,
// config.ResolveRepositoryIdentity); without a remote it is unresolved rather than
// cordanaLLM/<basename> or the directory layout.
func TestResolvePlatform_Boundary_IncompleteManifestFallsThrough(t *testing.T) {
	dir := layoutShapedRepo(t, "acme", "repo")
	writeRepoFile(t, dir, ".standards.yaml", "repository:\n  owner: only-owner\n")
	got, err := resolvePlatform(t.Context(), dir)
	if !errors.Is(err, util.ErrRepoIdentityUnresolved) || !errors.Is(err, config.ErrRepositoryNameUnknown) || got != "" {
		t.Fatalf("resolvePlatform = (%q, %v), want unresolved with the name named as missing", got, err)
	}
	testsupport.InitGitRepoWithOrigin(t, dir, "git@github.com:remote-owner/remote-repo.git")
	if got, err := resolvePlatform(t.Context(), dir); err != nil || got != "only-owner/remote-repo" {
		t.Fatalf("resolvePlatform = (%q, %v), want the manifest owner and the remote's name", got, err)
	}
}

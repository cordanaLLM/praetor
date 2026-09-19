package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/lockdown"

	"github.com/cordanaLLM/praetor/internal/util"
)

// gateFixture is a hermetic git repository carrying a pinned key and a signed receipt.
type gateFixture struct {
	dir    string
	head   string
	env    []string
	pub    ed25519.PublicKey
	priv   ed25519.PrivateKey
	output []byte
}

// newGateFixture builds an isolated git repository with one commit. It never reads or
// writes the developer's git configuration, HOME or the real repository.
func newGateFixture(t *testing.T) *gateFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+filepath.Join(dir, "no-such-gitconfig"),
		"GIT_CONFIG_SYSTEM="+filepath.Join(dir, "no-such-gitconfig"),
		"GIT_AUTHOR_NAME=praetor-test", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=praetor-test", "GIT_COMMITTER_EMAIL=test@example.invalid",
	)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "README.md"},
		{"commit", "-q", "-m", "fixture"},
	} {
		if out, err := runFixtureGit(t, dir, env, args...); err != nil {
			t.Skipf("git %v failed in sandbox: %v (%s)", args, err, out)
		}
	}
	head, err := runFixtureGit(t, dir, env, "rev-parse", "HEAD")
	if err != nil {
		t.Skipf("git rev-parse failed: %v", err)
	}

	pub, priv, err := lockdown.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	return &gateFixture{
		dir:    dir,
		head:   strings.TrimSpace(head),
		env:    env,
		pub:    pub,
		priv:   priv,
		output: []byte("praetor-gate-output/v1\nstage\tPrefetch & Lockfiles\ttrue\t\n"),
	}
}

func runFixtureGit(t *testing.T, dir string, env []string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// pin writes a .standards.yaml carrying the given public key.
func (f *gateFixture) pin(t *testing.T, pub ed25519.PublicKey) {
	t.Helper()
	body := "version: 1\nreceipt:\n  public_key: \"" + hex.EncodeToString(pub) + "\"\n"
	if err := os.WriteFile(filepath.Join(f.dir, ".standards.yaml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

// mintReceipt writes a receipt signed by priv for the given commit, attesting repository
// "acme/widget".
func (f *gateFixture) mintReceipt(t *testing.T, priv ed25519.PrivateKey, commit string) {
	t.Helper()
	f.mintReceiptRepo(t, priv, commit, "acme/widget")
}

// mintReceiptRepo writes a receipt signed by priv for the given commit and repository.
func (f *gateFixture) mintReceiptRepo(t *testing.T, priv ed25519.PrivateKey, commit, repo string) {
	t.Helper()
	receipt, err := lockdown.CreateReceipt(gating.ReceiptCommand, 0, f.output, commit, repo, priv)
	if err != nil {
		t.Fatalf("CreateReceipt: %v", err)
	}
	path := filepath.Join(f.dir, gating.ReceiptFileName)
	rf := &lockdown.ReceiptFile{ExecutionReceipt: *receipt, GateOutput: string(f.output)}
	if err := lockdown.SaveReceiptFile(path, rf, 0o644); err != nil {
		t.Fatalf("SaveReceiptFile: %v", err)
	}
}

// setOrigin configures f's origin remote so util.ResolveRepoIdentity resolves to owner/repo,
// matching how a real fork checkout carries its identity.
func (f *gateFixture) setOrigin(t *testing.T, owner, repo string) {
	t.Helper()
	url := "https://github.com/" + owner + "/" + repo + ".git"
	if out, err := runFixtureGit(t, f.dir, f.env, "remote", "add", "origin", url); err != nil {
		t.Fatalf("git remote add origin: %v (%s)", err, out)
	}
}

func TestRunGateVerify_Positive(t *testing.T) {
	f := newGateFixture(t)
	f.pin(t, f.pub)
	f.mintReceipt(t, f.priv, f.head)

	if err := runGate([]string{"verify", "--path", f.dir}); err != nil {
		t.Fatalf("gate verify failed: %v", err)
	}
}

func TestRunGateVerify_Negative(t *testing.T) {
	// A receipt minted with a foreign key must be rejected even though it verifies
	// against the key embedded in the receipt itself.
	forged := newGateFixture(t)
	_, otherPriv, err := lockdown.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	forged.pin(t, forged.pub)
	forged.mintReceipt(t, otherPriv, forged.head)
	err = runGate([]string{"verify", "--path", forged.dir})
	if !errors.Is(err, lockdown.ErrKeyNotPinned) {
		t.Errorf("expected ErrKeyNotPinned, got %v", err)
	}

	// A receipt for a different commit must be rejected.
	stale := newGateFixture(t)
	stale.pin(t, stale.pub)
	stale.mintReceipt(t, stale.priv, "0000000000000000000000000000000000000000")
	if err := runGate([]string{"verify", "--path", stale.dir}); err == nil ||
		!strings.Contains(err.Error(), "HEAD is") {
		t.Errorf("expected a commit mismatch error, got %v", err)
	}

	// A tampered gate output must be rejected: the receipt signs its SHA-256.
	tampered := newGateFixture(t)
	tampered.pin(t, tampered.pub)
	tampered.mintReceipt(t, tampered.priv, tampered.head)
	receiptPath := filepath.Join(tampered.dir, gating.ReceiptFileName)
	rf, err := lockdown.LoadReceiptFile(receiptPath)
	if err != nil {
		t.Fatalf("LoadReceiptFile: %v", err)
	}
	rf.GateOutput += "stage\tRace-Detector Tests\ttrue\t\n"
	if err := lockdown.SaveReceiptFile(receiptPath, rf, 0o644); err != nil {
		t.Fatalf("SaveReceiptFile: %v", err)
	}
	if err := runGate([]string{"verify", "--path", tampered.dir}); !errors.Is(err, lockdown.ErrOutputMismatch) {
		t.Errorf("expected ErrOutputMismatch for a tampered gate output, got %v", err)
	}
}

func TestRunGateVerify_Boundary(t *testing.T) {
	// Boundary: no receipt on disk at all.
	f := newGateFixture(t)
	f.pin(t, f.pub)
	if err := runGate([]string{"verify", "--path", f.dir}); err == nil {
		t.Error("expected an error when the receipt is missing")
	}

	// Boundary: a receipt exists but no key is pinned yet.
	unpinned := newGateFixture(t)
	if err := os.WriteFile(filepath.Join(unpinned.dir, ".standards.yaml"), []byte("version: 1\n"), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	unpinned.mintReceipt(t, unpinned.priv, unpinned.head)
	if err := runGate([]string{"verify", "--path", unpinned.dir}); !errors.Is(err, lockdown.ErrNoPinnedKey) {
		t.Errorf("expected ErrNoPinnedKey, got %v", err)
	}

	// Boundary: an explicit --receipt path outside the repository still verifies.
	elsewhere := newGateFixture(t)
	elsewhere.pin(t, elsewhere.pub)
	elsewhere.mintReceipt(t, elsewhere.priv, elsewhere.head)
	moved := filepath.Join(t.TempDir(), "receipt.json")
	data, err := os.ReadFile(filepath.Join(elsewhere.dir, gating.ReceiptFileName))
	if err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	if err := os.WriteFile(moved, data, 0o600); err != nil {
		t.Fatalf("write moved receipt: %v", err)
	}
	if err := os.Remove(filepath.Join(elsewhere.dir, gating.ReceiptFileName)); err != nil {
		t.Fatalf("remove receipt: %v", err)
	}
	if err := runGate([]string{"verify", "--path", elsewhere.dir, "--receipt", moved}); err != nil {
		t.Errorf("expected an out-of-tree receipt to verify, got %v", err)
	}
}

// TestRunGateVerify_PublicKeyFlag_Positive covers refs #99's F2: a note-shaped receipt
// (no manifest at all, exactly what `git notes show refs/notes/praetor/receipts` yields)
// verifies against a supplied --public-key, once the repository it attests matches the
// origin the checkout resolves to.
func TestRunGateVerify_PublicKeyFlag_Positive(t *testing.T) {
	f := newGateFixture(t)
	f.setOrigin(t, "acme", "widget")
	f.mintReceipt(t, f.priv, f.head)

	if err := runGate([]string{"verify", "--path", f.dir, "--public-key", hex.EncodeToString(f.pub)}); err != nil {
		t.Fatalf("gate verify --public-key failed: %v", err)
	}
}

func TestRunGateVerify_PublicKeyFlag_Negative(t *testing.T) {
	// A receipt verified against the wrong supplied key must be rejected, even though it
	// verifies against the key embedded in the receipt itself.
	wrongKey := newGateFixture(t)
	_, otherPriv, err := lockdown.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	wrongKey.mintReceipt(t, otherPriv, wrongKey.head)
	err = runGate([]string{"verify", "--path", wrongKey.dir, "--public-key", hex.EncodeToString(wrongKey.pub)})
	if !errors.Is(err, lockdown.ErrKeyNotPinned) {
		t.Errorf("expected ErrKeyNotPinned, got %v", err)
	}

	// A receipt for a different commit must be rejected.
	wrongCommit := newGateFixture(t)
	wrongCommit.mintReceipt(t, wrongCommit.priv, "0000000000000000000000000000000000000000")
	err = runGate([]string{"verify", "--path", wrongCommit.dir, "--public-key", hex.EncodeToString(wrongCommit.pub)})
	if err == nil || !strings.Contains(err.Error(), "HEAD is") {
		t.Errorf("expected a commit mismatch error, got %v", err)
	}

	// A receipt attesting a repository other than the one --path resolves to must be
	// rejected: a supplied key has no manifest binding it to one repository.
	wrongRepo := newGateFixture(t)
	wrongRepo.setOrigin(t, "acme", "widget")
	wrongRepo.mintReceiptRepo(t, wrongRepo.priv, wrongRepo.head, "acme/other-widget")
	err = runGate([]string{"verify", "--path", wrongRepo.dir, "--public-key", hex.EncodeToString(wrongRepo.pub)})
	if err == nil || !strings.Contains(err.Error(), "attests repository acme/other-widget") {
		t.Errorf("expected a repository mismatch error, got %v", err)
	}

	// A tampered gate output must be rejected: the receipt signs its SHA-256.
	tampered := newGateFixture(t)
	tampered.setOrigin(t, "acme", "widget")
	tampered.mintReceipt(t, tampered.priv, tampered.head)
	receiptPath := filepath.Join(tampered.dir, gating.ReceiptFileName)
	rf, err := lockdown.LoadReceiptFile(receiptPath)
	if err != nil {
		t.Fatalf("LoadReceiptFile: %v", err)
	}
	rf.GateOutput += "stage\tRace-Detector Tests\ttrue\t\n"
	if err := lockdown.SaveReceiptFile(receiptPath, rf, 0o644); err != nil {
		t.Fatalf("SaveReceiptFile: %v", err)
	}
	err = runGate([]string{"verify", "--path", tampered.dir, "--public-key", hex.EncodeToString(tampered.pub)})
	if !errors.Is(err, lockdown.ErrOutputMismatch) {
		t.Errorf("expected ErrOutputMismatch for a tampered gate output, got %v", err)
	}
}

// TestRunGateVerify_PublicKeyFlag_Boundary proves the supplied key wins over a present,
// mismatching manifest-pinned key only when --public-key is given explicitly: the same
// fixture state fails without the flag and passes with it.
func TestRunGateVerify_PublicKeyFlag_Boundary(t *testing.T) {
	f := newGateFixture(t)
	f.setOrigin(t, "acme", "widget")
	manifestPub, _, err := lockdown.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	f.pin(t, manifestPub)
	f.mintReceipt(t, f.priv, f.head)

	// Without --public-key: the manifest-pinned key is used, and it does not match the
	// key that actually signed the receipt.
	if err := runGate([]string{"verify", "--path", f.dir}); !errors.Is(err, lockdown.ErrKeyNotPinned) {
		t.Errorf("expected ErrKeyNotPinned without --public-key, got %v", err)
	}

	// With --public-key: the supplied key wins over the manifest, ignoring the mismatch.
	if err := runGate([]string{"verify", "--path", f.dir, "--public-key", hex.EncodeToString(f.pub)}); err != nil {
		t.Errorf("expected --public-key to override the manifest-pinned key, got %v", err)
	}
}

func TestRunGateKeygen_3D(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// APPDATA and LOCALAPPDATA are set alongside the POSIX pair because
	// os.UserConfigDir reads APPDATA on Windows. Without them this sandbox held on
	// POSIX only, and a keygen case wrote to the real per-user key file -- silently
	// destroying a developer's signing key on every test run (HISS-21).
	t.Setenv("APPDATA", home)
	t.Setenv("LOCALAPPDATA", home)

	// Positive: an explicit path receives a 0600 key that loads back.
	keyPath := filepath.Join(t.TempDir(), "nested", "receipt.key")
	if err := runGate([]string{"keygen", "--key", keyPath}); err != nil {
		t.Fatalf("gate keygen failed: %v", err)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	if util.ModeIsProtection() && info.Mode().Perm() != lockdown.SigningKeyPerm {
		t.Errorf("key mode = %#o, want %#o", info.Mode().Perm(), lockdown.SigningKeyPerm)
	}
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	if _, err := lockdown.ParseSigningKey(string(raw)); err != nil {
		t.Errorf("generated key does not parse: %v", err)
	}

	// Negative: a second keygen never silently replaces an existing key.
	if err := runGate([]string{"keygen", "--key", keyPath}); err == nil {
		t.Error("expected keygen to refuse overwriting an existing key")
	}

	// Boundary: the default location lands under the sandboxed config home.
	if err := runGate([]string{"keygen"}); err != nil {
		t.Fatalf("gate keygen (default path) failed: %v", err)
	}
	defaultPath, err := lockdown.DefaultSigningKeyPath()
	if err != nil {
		t.Fatalf("DefaultSigningKeyPath: %v", err)
	}
	if !strings.HasPrefix(defaultPath, home) {
		t.Fatalf("default key path %q escaped the sandbox %q", defaultPath, home)
	}
	if _, err := os.Stat(defaultPath); err != nil {
		t.Errorf("default key was not written: %v", err)
	}
}

func TestSplitGateSubcommand_3D(t *testing.T) {
	cases := []struct {
		args     []string
		wantSub  string
		wantRest int
	}{
		{[]string{"verify", "--path", "."}, "verify", 2},
		{[]string{"keygen"}, "keygen", 0},
		{[]string{"run", "--dry-run"}, "run", 1},
		// Backwards compatibility: flags with no subcommand still mean "run".
		{[]string{"--path=."}, "run", 1},
		// Boundary: no arguments at all.
		{nil, "run", 0},
	}
	for _, tc := range cases {
		sub, rest := splitGateSubcommand(tc.args)
		if sub != tc.wantSub || len(rest) != tc.wantRest {
			t.Errorf("splitGateSubcommand(%v) = (%q, %v), want (%q, %d args)", tc.args, sub, rest, tc.wantSub, tc.wantRest)
		}
	}

	// Negative: an unknown subcommand is refused rather than silently treated as a run.
	if err := runGate([]string{"frobnicate"}); err == nil {
		t.Error("expected an error for an unknown gate subcommand")
	}
}

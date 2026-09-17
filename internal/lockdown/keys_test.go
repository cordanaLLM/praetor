package lockdown

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sandboxConfigDir points os.UserConfigDir at a temp directory so no test ever reads or
// writes the developer's real per-user configuration directory.
func sandboxConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	// APPDATA and LOCALAPPDATA are set alongside the POSIX pair because
	// os.UserConfigDir reads APPDATA on Windows. Without them this sandbox held on
	// POSIX only, and a keygen case wrote to the real per-user key file -- silently
	// destroying a developer's signing key on every test run (HISS-21).
	t.Setenv("APPDATA", dir)
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv(SigningKeyEnv, "")
	return dir
}

func TestLoadSigningKey_Positive(t *testing.T) {
	configHome := sandboxConfigDir(t)

	_, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	// Positive 1: the environment variable carries a hex seed.
	t.Setenv(SigningKeyEnv, hex.EncodeToString(priv.Seed()))
	fromEnv, err := LoadSigningKey()
	if err != nil {
		t.Fatalf("LoadSigningKey from seed env: %v", err)
	}
	if !fromEnv.Equal(priv) {
		t.Error("key loaded from the hex seed does not match the generated key")
	}

	// Positive 2: the environment variable carries a full hex private key.
	t.Setenv(SigningKeyEnv, hex.EncodeToString(priv))
	fromEnvFull, err := LoadSigningKey()
	if err != nil {
		t.Fatalf("LoadSigningKey from private key env: %v", err)
	}
	if !fromEnvFull.Equal(priv) {
		t.Error("key loaded from the hex private key does not match the generated key")
	}

	// Positive 3: the per-user key file is used when the environment is unset.
	t.Setenv(SigningKeyEnv, "")
	path, err := DefaultSigningKeyPath()
	if err != nil {
		t.Fatalf("DefaultSigningKeyPath: %v", err)
	}
	if !strings.HasPrefix(path, configHome) {
		t.Fatalf("DefaultSigningKeyPath %q escaped the sandbox %q", path, configHome)
	}
	if err := SaveSigningKey(path, priv); err != nil {
		t.Fatalf("SaveSigningKey: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	if info.Mode().Perm() != SigningKeyPerm {
		t.Errorf("key mode = %#o, want %#o", info.Mode().Perm(), SigningKeyPerm)
	}
	fromFile, err := LoadSigningKey()
	if err != nil {
		t.Fatalf("LoadSigningKey from file: %v", err)
	}
	if !fromFile.Equal(priv) {
		t.Error("key loaded from disk does not match the generated key")
	}
}

func TestLoadSigningKey_Negative(t *testing.T) {
	sandboxConfigDir(t)

	// No environment key and no key file: fail closed, never an ephemeral key.
	if _, err := LoadSigningKey(); !errors.Is(err, ErrNoSigningKey) {
		t.Errorf("expected ErrNoSigningKey, got %v", err)
	}

	// Malformed environment key.
	t.Setenv(SigningKeyEnv, "not-hex")
	if _, err := LoadSigningKey(); !errors.Is(err, ErrMalformedSigningKey) {
		t.Errorf("expected ErrMalformedSigningKey for non-hex, got %v", err)
	}
	t.Setenv(SigningKeyEnv, hex.EncodeToString([]byte("too-short")))
	if _, err := LoadSigningKey(); !errors.Is(err, ErrMalformedSigningKey) {
		t.Errorf("expected ErrMalformedSigningKey for a short key, got %v", err)
	}

	// A 64-byte key whose tail does not match its own seed is rejected.
	_, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	tampered := make([]byte, len(priv))
	copy(tampered, priv)
	tampered[ed25519.PrivateKeySize-1] ^= 0xff
	t.Setenv(SigningKeyEnv, hex.EncodeToString(tampered))
	if _, err := LoadSigningKey(); !errors.Is(err, ErrMalformedSigningKey) {
		t.Errorf("expected ErrMalformedSigningKey for an inconsistent private key, got %v", err)
	}

	// A group-readable key file is refused.
	t.Setenv(SigningKeyEnv, "")
	path, err := DefaultSigningKeyPath()
	if err != nil {
		t.Fatalf("DefaultSigningKeyPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(priv.Seed())), 0o644); err != nil {
		t.Fatalf("write loose key: %v", err)
	}
	if _, err := LoadSigningKey(); !errors.Is(err, ErrInsecureKeyPerm) {
		t.Errorf("expected ErrInsecureKeyPerm for a 0644 key file, got %v", err)
	}
}

func TestSaveSigningKey_Boundary(t *testing.T) {
	sandboxConfigDir(t)
	_, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	path, err := DefaultSigningKeyPath()
	if err != nil {
		t.Fatalf("DefaultSigningKeyPath: %v", err)
	}

	// Boundary: a wrong-sized key is refused before touching the filesystem.
	if err := SaveSigningKey(path, make([]byte, 10)); !errors.Is(err, ErrMalformedSigningKey) {
		t.Errorf("expected ErrMalformedSigningKey, got %v", err)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("a refused save must not create the key file")
	}

	// Positive: first save succeeds and round-trips.
	if err := SaveSigningKey(path, priv); err != nil {
		t.Fatalf("SaveSigningKey: %v", err)
	}
	// Boundary: a second save never silently replaces an existing key.
	if err := SaveSigningKey(path, priv); err == nil {
		t.Error("expected SaveSigningKey to refuse overwriting an existing key")
	}

	// Boundary: the key directory is owner-only.
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat key dir: %v", err)
	}
	if info.Mode().Perm() != SigningKeyDirPerm {
		t.Errorf("key dir mode = %#o, want %#o", info.Mode().Perm(), SigningKeyDirPerm)
	}
}

func TestPinnedPublicKey_3D(t *testing.T) {
	dir := t.TempDir()
	pub, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	// Positive: a pinned key is read from the receipt section.
	good := filepath.Join(dir, "good.yaml")
	body := "version: 1\nreceipt:\n  public_key: \"" + hex.EncodeToString(pub) + "\"\n"
	if err := os.WriteFile(good, []byte(body), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	loaded, err := PinnedPublicKey(good)
	if err != nil {
		t.Fatalf("PinnedPublicKey: %v", err)
	}
	if !loaded.Equal(pub) {
		t.Error("pinned key does not match the generated public key")
	}

	// Negative: no receipt section at all.
	bare := filepath.Join(dir, "bare.yaml")
	if err := os.WriteFile(bare, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if _, err := PinnedPublicKey(bare); !errors.Is(err, ErrNoPinnedKey) {
		t.Errorf("expected ErrNoPinnedKey, got %v", err)
	}

	// Negative: a missing manifest is an error, not an empty key.
	if _, err := PinnedPublicKey(filepath.Join(dir, "absent.yaml")); err == nil {
		t.Error("expected an error for a missing manifest")
	}

	// Boundary: hex of the wrong length and non-hex input.
	for name, value := range map[string]string{
		"short":   hex.EncodeToString(pub[:16]),
		"non-hex": "zzzz",
	} {
		path := filepath.Join(dir, name+".yaml")
		if err := os.WriteFile(path, []byte("receipt:\n  public_key: \""+value+"\"\n"), 0o600); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
		if _, err := PinnedPublicKey(path); !errors.Is(err, ErrMalformedPinnedKey) {
			t.Errorf("%s: expected ErrMalformedPinnedKey, got %v", name, err)
		}
	}
}

func TestVerifyPinnedReceipt_3D(t *testing.T) {
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	output := []byte("Prefetch & Lockfiles\tpassed\n")
	receipt, err := CreateReceipt("praetorctl gate", 0, output, "deadbeef", "acme/widget", priv)
	if err != nil {
		t.Fatalf("CreateReceipt: %v", err)
	}

	// Positive: signed by the pinned key with the matching output.
	if err := VerifyPinnedReceipt(receipt, pub, output); err != nil {
		t.Fatalf("VerifyPinnedReceipt: %v", err)
	}

	// Negative: a receipt minted with a foreign key is rejected even though the receipt
	// verifies against the key it carries.
	foreignPub, foreignPriv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	forged, err := CreateReceipt("praetorctl gate", 0, output, "deadbeef", "acme/widget", foreignPriv)
	if err != nil {
		t.Fatalf("CreateReceipt: %v", err)
	}
	if err := VerifyReceipt(forged); err != nil {
		t.Fatalf("self-signed receipt should verify against its own key: %v", err)
	}
	if err := VerifyPinnedReceipt(forged, pub, output); !errors.Is(err, ErrKeyNotPinned) {
		t.Errorf("expected ErrKeyNotPinned for a foreign key, got %v", err)
	}
	if err := VerifyPinnedReceipt(forged, foreignPub, output); err != nil {
		t.Errorf("receipt should verify against its own pinned key: %v", err)
	}

	// Negative: output that does not match the signed hash.
	if err := VerifyPinnedReceipt(receipt, pub, []byte("other output")); !errors.Is(err, ErrOutputMismatch) {
		t.Errorf("expected ErrOutputMismatch, got %v", err)
	}

	// Boundary: nil receipt and a malformed pinned key.
	if err := VerifyPinnedReceipt(nil, pub, output); !errors.Is(err, ErrNilReceipt) {
		t.Errorf("expected ErrNilReceipt, got %v", err)
	}
	if err := VerifyPinnedReceipt(receipt, pub[:16], output); !errors.Is(err, ErrMalformedPinnedKey) {
		t.Errorf("expected ErrMalformedPinnedKey, got %v", err)
	}

	// Boundary: a receipt whose embedded key is structurally invalid.
	broken := *receipt
	broken.PublicKey = "not-hex"
	if err := VerifyPinnedReceipt(&broken, pub, output); !errors.Is(err, ErrInvalidPubKey) {
		t.Errorf("expected ErrInvalidPubKey, got %v", err)
	}
}

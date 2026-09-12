package lockdown

import (
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// SigningKeyEnv holds a hex-encoded Ed25519 seed (32 bytes) or private key (64 bytes)
	// used to sign Exit-0 receipts. It takes precedence over the on-disk key.
	SigningKeyEnv = "PRAETOR_RECEIPT_KEY"
	// SigningKeyDirName is the per-user configuration directory holding the signing key.
	SigningKeyDirName = "praetor"
	// SigningKeyFileName is the on-disk signing key file inside SigningKeyDirName.
	SigningKeyFileName = "receipt.key"
	// SigningKeyPerm is the only accepted mode for the on-disk signing key.
	SigningKeyPerm os.FileMode = 0o600
	// SigningKeyDirPerm is the mode applied to the signing key directory.
	SigningKeyDirPerm os.FileMode = 0o700
	// maxKeyFileBytes bounds how much of the key file is read (HISS-02).
	maxKeyFileBytes = 4096
	// maxManifestBytes bounds how much of .standards.yaml is parsed for the pinned key.
	maxManifestBytes = 1 << 20
)

var (
	// ErrNoSigningKey is returned when neither the environment nor the per-user key file
	// provides a receipt signing key.
	ErrNoSigningKey = errors.New("no Ed25519 receipt signing key available: set " +
		SigningKeyEnv + " or run 'praetorctl gate keygen'")
	// ErrInsecureKeyPerm is returned when the on-disk signing key is group- or
	// world-accessible.
	ErrInsecureKeyPerm = errors.New("receipt signing key must be mode 0600")
	// ErrMalformedSigningKey is returned for a key that is not a hex-encoded Ed25519 seed
	// or private key.
	ErrMalformedSigningKey = errors.New("malformed Ed25519 receipt signing key: expected a " +
		"64-character hex seed or a 128-character hex private key")
	// ErrNoPinnedKey is returned when .standards.yaml carries no receipt.public_key.
	ErrNoPinnedKey = errors.New("no pinned receipt public key: set receipt.public_key in .standards.yaml " +
		"to the output of 'praetorctl gate keygen'")
	// ErrMalformedPinnedKey is returned for a pinned key that is not 32 bytes of hex.
	ErrMalformedPinnedKey = errors.New("malformed pinned receipt public key: expected 64 hex characters")
	// ErrKeyNotPinned is returned when a receipt was signed by a key other than the pinned one.
	ErrKeyNotPinned = errors.New("receipt was not signed by the pinned receipt.public_key")
)

// manifestReceiptSection is the minimal view of .standards.yaml required to read the
// pinned receipt public key without importing the full manifest schema.
type manifestReceiptSection struct {
	Receipt struct {
		PublicKey string `yaml:"public_key"`
	} `yaml:"receipt"`
}

// DefaultSigningKeyPath returns the per-user signing key location
// (~/.config/praetor/receipt.key on Linux, honouring XDG_CONFIG_HOME).
func DefaultSigningKeyPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(configDir, SigningKeyDirName, SigningKeyFileName), nil
}

// LoadSigningKey resolves the Ed25519 receipt signing key from the SigningKeyEnv
// environment variable, falling back to the per-user key file, which must be mode 0600.
// It fails closed: a missing or malformed key is an error, never an ephemeral keypair.
func LoadSigningKey() (ed25519.PrivateKey, error) {
	if raw := strings.TrimSpace(os.Getenv(SigningKeyEnv)); raw != "" {
		key, err := ParseSigningKey(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", SigningKeyEnv, err)
		}
		return key, nil
	}

	path, err := DefaultSigningKeyPath()
	if err != nil {
		return nil, err
	}
	raw, err := readKeyFile(path)
	if err != nil {
		return nil, err
	}
	key, err := ParseSigningKey(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return key, nil
}

// readKeyFile reads at most maxKeyFileBytes from the per-user key file after verifying
// that its mode excludes group and other access.
func readKeyFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w (looked in %s)", ErrNoSigningKey, path)
		}
		return "", fmt.Errorf("stat receipt signing key %s: %w", path, err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%w: %s is %#o", ErrInsecureKeyPerm, path, info.Mode().Perm())
	}

	// #nosec G304 -- path is derived from os.UserConfigDir(), never from user input.
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open receipt signing key %s: %w", path, err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxKeyFileBytes))
	if cerr := file.Close(); cerr != nil && readErr == nil {
		readErr = cerr
	}
	if readErr != nil {
		return "", fmt.Errorf("read receipt signing key %s: %w", path, readErr)
	}
	return string(data), nil
}

// ParseSigningKey decodes a hex-encoded Ed25519 seed (32 bytes) or full private key
// (64 bytes) and validates that a full private key is internally consistent.
func ParseSigningKey(raw string) (ed25519.PrivateKey, error) {
	trimmed := strings.TrimSpace(raw)
	decoded, err := hex.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformedSigningKey, err)
	}

	switch len(decoded) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(decoded), nil
	case ed25519.PrivateKeySize:
		derived := ed25519.NewKeyFromSeed(decoded[:ed25519.SeedSize])
		if subtle.ConstantTimeCompare(derived, decoded) != 1 {
			return nil, fmt.Errorf("%w: private key does not match its own seed", ErrMalformedSigningKey)
		}
		return ed25519.PrivateKey(decoded), nil
	default:
		return nil, fmt.Errorf("%w: decoded %d bytes", ErrMalformedSigningKey, len(decoded))
	}
}

// SaveSigningKey writes the 32-byte seed of priv to path with mode 0600, creating the
// parent directory with mode 0700. It never overwrites an existing key.
func SaveSigningKey(path string, priv ed25519.PrivateKey) error {
	if len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: private key is %d bytes", ErrMalformedSigningKey, len(priv))
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("receipt signing key already exists at %s: refusing to overwrite", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	if err := util.MkdirSecure(filepath.Dir(path), SigningKeyDirPerm); err != nil {
		return fmt.Errorf("create receipt key directory: %w", err)
	}
	seed := hex.EncodeToString(priv.Seed()) + "\n"
	if err := util.WriteFileSecure(path, []byte(seed), SigningKeyPerm); err != nil {
		return fmt.Errorf("write receipt signing key: %w", err)
	}
	return nil
}

// PinnedPublicKey reads receipt.public_key from the .standards.yaml manifest at
// manifestPath. The pinned key is the only trust anchor a receipt may be verified
// against; the public key embedded inside a receipt is never trusted on its own.
func PinnedPublicKey(manifestPath string) (ed25519.PublicKey, error) {
	info, err := os.Stat(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read standards manifest %s: %w", manifestPath, err)
	}
	if info.Size() > maxManifestBytes {
		return nil, fmt.Errorf("standards manifest %s exceeds %d bytes", manifestPath, maxManifestBytes)
	}

	// #nosec G304 -- manifestPath is the repository's own .standards.yaml, resolved by the caller.
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read standards manifest %s: %w", manifestPath, err)
	}

	var section manifestReceiptSection
	if err := yaml.Unmarshal(data, &section); err != nil {
		return nil, fmt.Errorf("parse standards manifest %s: %w", manifestPath, err)
	}
	return ParsePinnedPublicKey(section.Receipt.PublicKey)
}

// ParsePinnedPublicKey decodes a hex-encoded Ed25519 public key pinned in a manifest.
func ParsePinnedPublicKey(raw string) (ed25519.PublicKey, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, ErrNoPinnedKey
	}
	decoded, err := hex.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformedPinnedKey, err)
	}
	if len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: decoded %d bytes", ErrMalformedPinnedKey, len(decoded))
	}
	return ed25519.PublicKey(decoded), nil
}

// VerifyPinnedReceipt verifies that receipt was signed by the pinned public key and that
// its output hash matches output. It is the only verification entry point a gate should
// use, because VerifyReceipt alone trusts the key carried inside the receipt.
func VerifyPinnedReceipt(receipt *ExecutionReceipt, pinned ed25519.PublicKey, output []byte) error {
	if receipt == nil {
		return ErrNilReceipt
	}
	if len(pinned) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: pinned key is %d bytes", ErrMalformedPinnedKey, len(pinned))
	}

	embedded, err := hex.DecodeString(receipt.PublicKey)
	if err != nil || len(embedded) != ed25519.PublicKeySize {
		return ErrInvalidPubKey
	}
	if subtle.ConstantTimeCompare(embedded, pinned) != 1 {
		return fmt.Errorf("%w: receipt key %s", ErrKeyNotPinned, receipt.PublicKey)
	}
	return VerifyReceiptWithOutput(receipt, output)
}

package lockdown

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

const ReceiptVersion = "v1"

var (
	ErrNonZeroExit     = errors.New("cannot generate Exit-0 receipt: execution exit code is non-zero")
	ErrNilReceipt      = errors.New("execution receipt cannot be nil")
	ErrInvalidPrivKey  = errors.New("invalid Ed25519 private key size")
	ErrInvalidPubKey   = errors.New("invalid Ed25519 public key in receipt")
	ErrInvalidSig      = errors.New("invalid Ed25519 signature in receipt")
	ErrSigVerification = errors.New("receipt Ed25519 signature verification failed")
	ErrOutputMismatch  = errors.New("execution output hash does not match receipt output hash")
)

// ExecutionReceipt represents an Ed25519-signed verification receipt certifying an Exit-0 run.
type ExecutionReceipt struct {
	Version    string    `json:"version"`
	Command    string    `json:"command"`
	ExitCode   int       `json:"exit_code"`
	Timestamp  time.Time `json:"timestamp"`
	CommitSHA  string    `json:"commit_sha,omitempty"`
	Repository string    `json:"repository,omitempty"`
	OutputHash string    `json:"output_hash"`
	PublicKey  string    `json:"public_key"`
	Signature  string    `json:"signature"`
}

// GenerateKeyPair generates a cryptographically secure Ed25519 keypair for signing receipts.
func GenerateKeyPair() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed generating Ed25519 keypair: %w", err)
	}
	return pub, priv, nil
}

// BuildCanonicalPayload constructs the deterministic byte sequence signed by Ed25519.
func BuildCanonicalPayload(version, command string, exitCode int, ts time.Time, commitSHA, repo, outputHash string) string {
	return fmt.Sprintf("%s\n%s\n%d\n%s\n%s\n%s\n%s",
		version,
		command,
		exitCode,
		ts.UTC().Format(time.RFC3339Nano),
		commitSHA,
		repo,
		outputHash,
	)
}

// CreateReceipt creates and signs an Exit-0 ExecutionReceipt using an Ed25519 private key.
func CreateReceipt(command string, exitCode int, output []byte, commitSHA, repository string, privKey ed25519.PrivateKey) (*ExecutionReceipt, error) {
	if exitCode != 0 {
		return nil, fmt.Errorf("%w: exit code is %d", ErrNonZeroExit, exitCode)
	}
	if len(privKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidPrivKey, len(privKey), ed25519.PrivateKeySize)
	}

	hash := sha256.Sum256(output)
	outputHash := hex.EncodeToString(hash[:])
	now := time.Now().UTC()

	payload := BuildCanonicalPayload(ReceiptVersion, command, exitCode, now, commitSHA, repository, outputHash)
	sig := ed25519.Sign(privKey, []byte(payload))

	pubKey, ok := privKey.Public().(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%w: derived public key is not Ed25519", ErrInvalidPrivKey)
	}

	return &ExecutionReceipt{
		Version:    ReceiptVersion,
		Command:    command,
		ExitCode:   exitCode,
		Timestamp:  now,
		CommitSHA:  commitSHA,
		Repository: repository,
		OutputHash: outputHash,
		PublicKey:  hex.EncodeToString(pubKey),
		Signature:  hex.EncodeToString(sig),
	}, nil
}

// VerifyReceipt cryptographically verifies an ExecutionReceipt.
func VerifyReceipt(receipt *ExecutionReceipt) error {
	if receipt == nil {
		return ErrNilReceipt
	}
	if receipt.ExitCode != 0 {
		return fmt.Errorf("%w: recorded exit code is %d", ErrNonZeroExit, receipt.ExitCode)
	}

	pubKeyBytes, err := hex.DecodeString(receipt.PublicKey)
	if err != nil || len(pubKeyBytes) != ed25519.PublicKeySize {
		return ErrInvalidPubKey
	}

	sigBytes, err := hex.DecodeString(receipt.Signature)
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return ErrInvalidSig
	}

	payload := BuildCanonicalPayload(
		receipt.Version,
		receipt.Command,
		receipt.ExitCode,
		receipt.Timestamp,
		receipt.CommitSHA,
		receipt.Repository,
		receipt.OutputHash,
	)

	if !ed25519.Verify(pubKeyBytes, []byte(payload), sigBytes) {
		return ErrSigVerification
	}

	return nil
}

// ReceiptFile is the on-disk .standards-receipt.json envelope: the signed receipt plus
// the verbatim gate output whose SHA-256 the receipt certifies. Embedding inlines the
// receipt fields, so the file stays readable as a plain ExecutionReceipt.
type ReceiptFile struct {
	ExecutionReceipt
	GateOutput string `json:"gate_output"`
}

// LoadReceiptFile reads and parses an on-disk receipt envelope.
func LoadReceiptFile(path string) (*ReceiptFile, error) {
	// #nosec G304 -- path is the repository's own receipt location or an operator-supplied
	// --receipt argument; the file is only parsed as JSON, never executed.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read receipt %s: %w", path, err)
	}
	var rf ReceiptFile
	if err := json.Unmarshal(data, &rf); err != nil {
		return nil, fmt.Errorf("parse receipt %s: %w", path, err)
	}
	return &rf, nil
}

// SaveReceiptFile writes a receipt envelope with an explicit file mode.
func SaveReceiptFile(path string, rf *ReceiptFile, perm os.FileMode) error {
	if rf == nil {
		return ErrNilReceipt
	}
	data, err := json.MarshalIndent(rf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal receipt: %w", err)
	}
	if err := util.WriteFileSecure(path, append(data, '\n'), perm); err != nil {
		return fmt.Errorf("write receipt %s: %w", path, err)
	}
	return nil
}

// VerifyReceiptWithOutput verifies the receipt signature and verifies that the output matches.
func VerifyReceiptWithOutput(receipt *ExecutionReceipt, output []byte) error {
	if err := VerifyReceipt(receipt); err != nil {
		return err
	}

	hash := sha256.Sum256(output)
	expectedHash := hex.EncodeToString(hash[:])
	if receipt.OutputHash != expectedHash {
		return fmt.Errorf("%w: expected %s, got %s", ErrOutputMismatch, receipt.OutputHash, expectedHash)
	}

	return nil
}

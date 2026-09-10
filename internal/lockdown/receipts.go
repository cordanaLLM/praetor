package lockdown

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

const ReceiptVersion = "v1"

var (
	ErrNonZeroExit     = errors.New("cannot generate Exit-0 receipt: execution exit code is non-zero")
	ErrNilReceipt      = errors.New("execution receipt cannot be nil")
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
		return nil, errors.New("invalid Ed25519 private key size")
	}

	hash := sha256.Sum256(output)
	outputHash := hex.EncodeToString(hash[:])
	now := time.Now().UTC()

	payload := BuildCanonicalPayload(ReceiptVersion, command, exitCode, now, commitSHA, repository, outputHash)
	sig := ed25519.Sign(privKey, []byte(payload))

	pubKey := privKey.Public().(ed25519.PublicKey)

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
		return fmt.Errorf("invalid Exit-0 receipt: recorded exit code is %d", receipt.ExitCode)
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

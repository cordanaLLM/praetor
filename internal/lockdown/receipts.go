package lockdown

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

const ReceiptVersion = "v1"

const (
	// GateOutputVersion heads the gate output an Exit-0 receipt certifies: the first line of
	// gating.PipelineReport.StageOutput. v2 records each stage's verdict (passed, failed,
	// skipped or not_applicable) where v1 recorded a passed bool that read a skipped stage as
	// passed.
	GateOutputVersion = "praetor-gate-output/v2"
	// maxVersionEcho bounds how much of an unrecognised gate output header an error repeats.
	// The output can come from a pull request body, so it is untrusted and unbounded.
	maxVersionEcho = 64
)

var (
	ErrNonZeroExit     = errors.New("cannot generate Exit-0 receipt: execution exit code is non-zero")
	ErrNilReceipt      = errors.New("execution receipt cannot be nil")
	ErrInvalidPrivKey  = errors.New("invalid Ed25519 private key size")
	ErrInvalidPubKey   = errors.New("invalid Ed25519 public key in receipt")
	ErrInvalidSig      = errors.New("invalid Ed25519 signature in receipt")
	ErrSigVerification = errors.New("receipt Ed25519 signature verification failed")
	ErrOutputMismatch  = errors.New("execution output hash does not match receipt output hash")
	// ErrCommitMismatch is returned when a receipt attests a commit other than the checked-out HEAD.
	ErrCommitMismatch = errors.New("receipt commit does not match HEAD")
	// ErrGateOutputVersion reports a receipt whose certified gate output does not open with
	// GateOutputVersion.
	ErrGateOutputVersion = errors.New("receipt certifies an unsupported gate output version")
	// ErrWorktreeNotClean reports a receipt whose signed gate output says the scanned working
	// tree was not clean: the scan stages read files HEAD does not carry.
	ErrWorktreeNotClean = errors.New("receipt certifies a scan of a working tree that was not clean")
	// ErrWorktreeUnrecorded reports gate output that does not state the scanned tree's
	// cleanliness exactly once, so nothing binds the receipt to a scan of HEAD alone.
	ErrWorktreeUnrecorded = errors.New("receipt gate output does not record whether the scanned working tree was clean")
)

// gateWorktreeCleanKey names the gate-output header field recording whether the scanned
// working tree was clean.
const gateWorktreeCleanKey = "worktree_clean"

// maxGateOutputHeaderLines bounds the header scan (HISS-02). The gate writes five header lines
// before its first stage line.
const maxGateOutputHeaderLines = 32

// maxReceiptFileBytes bounds how much of a receipt envelope LoadReceiptFile reads (HISS-02).
// An envelope is a few hundred bytes of signed fields plus the gate output: five header lines
// and one line per stage. 1 MiB leaves room for long stage messages without allocating a
// planted multi-GB file in full.
const maxReceiptFileBytes = 1 << 20

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
//
// The read is the bounded, root-anchored one the manifest readers share
// (util.ReadConfinedLimited, anchored at the receipt's own directory): only a regular file
// is opened, so a FIFO planted at the receipt path fails instead of blocking the verifier
// past every deadline; a link at the path that resolves outside that directory is refused;
// and at most maxReceiptFileBytes are read. json.Unmarshal refuses anything after the first
// JSON value, so a second envelope appended to the file is an error rather than ignored:
// the single-document rule the manifest readers apply (BUG-857).
func LoadReceiptFile(path string) (*ReceiptFile, error) {
	data, err := util.ReadConfinedLimited(filepath.Dir(path), filepath.Base(path), maxReceiptFileBytes)
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
//
// The write is util.WriteFileConfined anchored at the receipt's own directory, the
// repository root the gate mints it into: a link planted at the receipt path is refused
// instead of written through, and the envelope is replaced atomically, so LoadReceiptFile
// never reads a torn one (BUG-826).
func SaveReceiptFile(path string, rf *ReceiptFile, perm os.FileMode) error {
	if rf == nil {
		return ErrNilReceipt
	}
	data, err := json.MarshalIndent(rf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal receipt: %w", err)
	}
	if err := util.WriteFileConfined(filepath.Dir(path), filepath.Base(path), append(data, '\n'), perm); err != nil {
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

// VerifyReceiptCommit binds a receipt to the commit checked out in repoPath. A signature proves
// who minted a receipt, not where it applies: without this binding any receipt the pinned key
// ever signed would verify against any later HEAD. It runs one local git query under ctx and
// makes no network call. An empty receiptSHA never matches, so a receipt minted without a
// commit fails closed.
func VerifyReceiptCommit(ctx context.Context, repoPath, receiptSHA string) error {
	head, err := util.RunGit(ctx, repoPath, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("cannot resolve HEAD in %s: %w", repoPath, err)
	}
	if head != receiptSHA {
		return fmt.Errorf("%w: receipt attests commit %s but HEAD is %s", ErrCommitMismatch, receiptSHA, head)
	}
	return nil
}

// checkGateOutputVersion fails closed unless output opens with the GateOutputVersion line.
func checkGateOutputVersion(output string) error {
	header, _, _ := strings.Cut(output, "\n")
	if header == GateOutputVersion {
		return nil
	}
	return fmt.Errorf("%w: gate output opens with %.*q, want %q; re-mint the receipt with `praetorctl gate run`",
		ErrGateOutputVersion, maxVersionEcho, header, GateOutputVersion)
}

// WorktreeCleanLine renders the gate-output header line recording whether the scanned working
// tree was clean. The gate writes it and RequireCleanWorktree reads it, so both share one
// format.
func WorktreeCleanLine(clean bool) string {
	return fmt.Sprintf("%s\t%t", gateWorktreeCleanKey, clean)
}

// RequireCleanWorktree fails unless gateOutput's header -- the lines before the first stage
// line -- records worktree_clean true exactly once. A receipt's signature proves only who
// signed the output; this is what proves the scan it certifies read HEAD's tree and nothing
// else. Call it on output the receipt's hash has already been verified against.
func RequireCleanWorktree(gateOutput string) error {
	lines := strings.SplitN(gateOutput, "\n", maxGateOutputHeaderLines+1)
	values := make([]string, 0, 1)
	for i := 0; i < len(lines) && i < maxGateOutputHeaderLines; i++ {
		key, value, _ := strings.Cut(lines[i], "\t")
		if key == "stage" {
			break
		}
		if key == gateWorktreeCleanKey {
			values = append(values, value)
		}
	}
	if len(values) != 1 {
		return fmt.Errorf("%w: the header holds %d %s lines, want 1", ErrWorktreeUnrecorded, len(values), gateWorktreeCleanKey)
	}
	if values[0] != "true" {
		return fmt.Errorf("%w: %s is %q", ErrWorktreeNotClean, gateWorktreeCleanKey, values[0])
	}
	return nil
}

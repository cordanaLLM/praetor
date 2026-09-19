// Package devsync copies a development workstation's project folders to an rclone remote as
// one gzip-compressed tar archive per repository and brings them back on another machine. It
// is a development stopgap, not a backup product: it keeps no history and resolves no
// conflicts.
package devsync

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// DefaultRemoteName is the rclone crypt remote init creates.
	DefaultRemoteName = "praetor-sync"
	// DefaultBase is the folder the crypt remote encrypts into: praetor-sync/ at the Drive root.
	DefaultBase = "gdrive:praetor-sync"
	// DefaultRemote is where push, pull and ls read and write.
	DefaultRemote = DefaultRemoteName + ":"
	// keyBytes is the length of each generated crypt key before encoding.
	keyBytes = 32
)

// ErrRemoteExists reports that init would overwrite an existing rclone remote.
var ErrRemoteExists = errors.New("devsync: rclone remote already exists")

// InitOptions configures Init.
type InitOptions struct {
	// RemoteName is the crypt remote to create; empty selects DefaultRemoteName.
	RemoteName string
	// Base is the remote folder the crypt remote encrypts into; empty selects DefaultBase.
	Base   string
	Rclone Rclone
}

// Init creates an rclone crypt remote over Base with two freshly generated keys, through
// rclone's own "config create --obscure". It refuses to replace a remote of the same name.
func Init(ctx context.Context, opts InitOptions) error {
	name := defaultString(opts.RemoteName, DefaultRemoteName)
	base := defaultString(opts.Base, DefaultBase)
	if err := validateRemoteName(name); err != nil {
		return err
	}
	if err := util.ValidateExecPathArg(base); err != nil {
		return fmt.Errorf("base %q: %w", base, err)
	}
	remotes, err := opts.Rclone.listRemotes(ctx)
	if err != nil {
		return err
	}
	if slices.Contains(remotes, name) {
		return fmt.Errorf("%w: %s", ErrRemoteExists, name)
	}
	password, err := generateKey()
	if err != nil {
		return err
	}
	salt, err := generateKey()
	if err != nil {
		return err
	}
	out, err := opts.Rclone.run(ctx, "config", "create", name, "crypt", "remote="+base,
		"password="+password, "password2="+salt, "--obscure", "--non-interactive")
	if err != nil {
		return err
	}
	return configCreateResult(out)
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func validateRemoteName(name string) error {
	if strings.ContainsAny(name, ":/\\") {
		return fmt.Errorf("remote name %q must not contain ':', '/' or '\\'", name)
	}
	if err := util.ValidateExecArg(name); err != nil {
		return fmt.Errorf("remote name %q: %w", name, err)
	}
	return nil
}

func generateKey() (string, error) {
	key := make([]byte, keyBytes)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("generate crypt key: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(key), nil
}

// configCreateResult reads rclone's non-interactive answer. A pending question or an error
// means the remote was not fully configured.
func configCreateResult(out []byte) error {
	var result struct {
		State string
		Error string
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return fmt.Errorf("decode rclone config create: %w", err)
	}
	if result.Error != "" || result.State != "" {
		return fmt.Errorf("rclone config create did not finish: state %q, error %q", result.State, result.Error)
	}
	return nil
}

// ListOptions configures List.
type ListOptions struct {
	// Remote is the remote root, for example "praetor-sync:".
	Remote string
	Rclone Rclone
}

// List returns every archive on the remote with its host, size and time. A remote that holds
// nothing yet lists as empty.
func List(ctx context.Context, opts ListOptions) ([]RemoteArchive, error) {
	archives, err := opts.Rclone.listArchives(ctx, opts.Remote)
	if errors.Is(err, ErrRemoteMissing) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for i := range archives {
		archives[i].Host, _, _ = strings.Cut(archives[i].Path, "/")
	}
	return archives, nil
}

// Outcome is the result for one archive of a push or pull.
type Outcome struct {
	// Archive is the archive's path below the remote, for example host/dev/org/repo.tar.gz.
	Archive string
	Status  string
	// Bytes is the compressed size uploaded or downloaded, or the source size when skipped.
	Bytes int64
	Note  string
	Err   error
}

// Outcome statuses.
const (
	StatusUploaded    = "uploaded"
	StatusSkipped     = "skipped"
	StatusWouldUpload = "would-upload"
	StatusRestored    = "restored"
	StatusFailed      = "failed"
	// StatusTooLarge reports a unit whose measured source exceeds PushOptions.MaxArchiveSize;
	// push never streamed it. Skipping for size is not a failure.
	StatusTooLarge = "too-large"
)

func failed(outcome Outcome, err error) Outcome {
	outcome.Status, outcome.Err = StatusFailed, err
	return outcome
}

// report prints one outcome line.
func report(w io.Writer, o Outcome) error {
	if o.Err != nil {
		_, err := fmt.Fprintf(w, "%-12s %s: %v\n", o.Status, o.Archive, o.Err)
		return err
	}
	note := ""
	if o.Note != "" {
		note = " (" + o.Note + ")"
	}
	_, err := fmt.Fprintf(w, "%-12s %s %s%s\n", o.Status, o.Archive, FormatBytes(o.Bytes), note)
	return err
}

// failures summarises failed outcomes as one error.
func failures(outcomes []Outcome) error {
	count := 0
	for _, o := range outcomes {
		if o.Status == StatusFailed {
			count++
		}
	}
	if count == 0 {
		return nil
	}
	return fmt.Errorf("%d of %d archives failed", count, len(outcomes))
}

// reportSizeSummary prints how many archives Push skipped because their measured source
// exceeded MaxArchiveSize, and their combined size, so an oversized archive is never left off
// the remote without the operator being told. It always prints, including a zero count, so the
// line's absence is never mistaken for the check not having run.
func reportSizeSummary(w io.Writer, outcomes []Outcome) error {
	var count int
	var total int64
	for _, o := range outcomes {
		if o.Status == StatusTooLarge {
			count++
			total += o.Bytes
		}
	}
	_, err := fmt.Fprintf(w, "skipped for size: %d archive(s), %s\n", count, FormatBytes(total))
	return err
}

// FormatBytes renders a byte count with a binary unit, for example "12.3 MiB".
func FormatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value, suffix := float64(n)/unit, "KiB"
	for _, next := range []string{"MiB", "GiB", "TiB"} {
		if value < unit {
			break
		}
		value, suffix = value/unit, next
	}
	return fmt.Sprintf("%.1f %s", value, suffix)
}

// sizeUnits maps a byte-size suffix, uppercased, to its multiplier. The empty suffix and "B"
// both mean plain bytes; the rest are the binary units FormatBytes itself prints.
var sizeUnits = map[string]int64{
	"":    1,
	"B":   1,
	"KIB": 1 << 10,
	"MIB": 1 << 20,
	"GIB": 1 << 30,
	"TIB": 1 << 40,
}

// ParseSize parses a byte count such as devsync's --max-archive-size flag: a plain integer
// number of bytes, or a non-negative decimal number followed by a binary unit (B, KiB, MiB,
// GiB, TiB), the unit matched case-insensitively and optionally separated by a space. "0" and
// "none", in any case, both parse as 0, which callers treat as no cap at all.
func ParseSize(s string) (int64, error) {
	trimmed := strings.TrimSpace(s)
	if strings.EqualFold(trimmed, "none") {
		return 0, nil
	}
	number, unit := splitSizeUnit(trimmed)
	multiplier, ok := sizeUnits[strings.ToUpper(unit)]
	if !ok {
		return 0, fmt.Errorf("size %q: unknown unit %q", s, unit)
	}
	value, err := strconv.ParseFloat(number, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("size %q: not a non-negative number", s)
	}
	bytes := value * float64(multiplier)
	if bytes > math.MaxInt64 {
		return 0, fmt.Errorf("size %q: too large", s)
	}
	return int64(bytes), nil
}

// splitSizeUnit splits s into its leading number and trailing letter unit, for example
// "500 MiB" into "500" and "MiB".
func splitSizeUnit(s string) (number, unit string) {
	i := len(s)
	for i > 0 && isASCIILetter(s[i-1]) {
		i--
	}
	return strings.TrimSpace(s[:i]), s[i:]
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func writerOrDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

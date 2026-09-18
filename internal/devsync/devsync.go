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
	"slices"
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
)

func failed(outcome Outcome, err error) Outcome {
	outcome.Status, outcome.Err = StatusFailed, err
	return outcome
}

// report prints one outcome line.
func report(w io.Writer, o Outcome) {
	if o.Err != nil {
		fmt.Fprintf(w, "%-12s %s: %v\n", o.Status, o.Archive, o.Err)
		return
	}
	note := ""
	if o.Note != "" {
		note = " (" + o.Note + ")"
	}
	fmt.Fprintf(w, "%-12s %s %s%s\n", o.Status, o.Archive, FormatBytes(o.Bytes), note)
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

func writerOrDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

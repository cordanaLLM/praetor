package devsync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// PullOptions configures Pull.
type PullOptions struct {
	// Remote is the remote root, for example "praetor-sync:".
	Remote string
	// Host is the workstation whose archives are restored.
	Host string
	// Into receives the archives below <Into>/<Host>; see DefaultPullDir.
	Into string
	// DevDir is this machine's dev folder; a target inside it is refused.
	DevDir string
	Rclone Rclone
	// Out receives one line per archive as it completes; nil discards them.
	Out io.Writer
}

// Pull downloads every archive of Host and unpacks each into <Into>/<Host>/<archive path
// without .tar.gz>. The target must be empty or absent and must not lie inside DevDir, so a
// pull never overwrites working copies.
func Pull(ctx context.Context, opts PullOptions) ([]Outcome, error) {
	if err := validateHost(opts.Host); err != nil {
		return nil, err
	}
	if opts.Remote == "" || opts.Into == "" || opts.DevDir == "" {
		return nil, errors.New("pull needs a remote, a target folder and the dev folder")
	}
	target, err := pullTarget(ctx, filepath.Join(opts.Into, opts.Host), opts.DevDir)
	if err != nil {
		return nil, err
	}
	archives, err := hostArchives(ctx, opts)
	if err != nil {
		return nil, err
	}
	if err := util.MkdirSecure(target, util.SecureDirPerm); err != nil {
		return nil, err
	}
	out := writerOrDiscard(opts.Out)
	outcomes := make([]Outcome, 0, len(archives))
	var printErr error
	for _, archive := range archives {
		outcome := pullArchive(ctx, opts, target, archive)
		printErr = errors.Join(printErr, report(out, outcome))
		outcomes = append(outcomes, outcome)
	}
	return outcomes, errors.Join(failures(outcomes), printErr)
}

// hostArchives lists the archives of opts.Host; a host with none is an error.
func hostArchives(ctx context.Context, opts PullOptions) ([]RemoteArchive, error) {
	archives, err := opts.Rclone.listArchives(ctx, remotePath(opts.Remote, opts.Host))
	if errors.Is(err, ErrRemoteMissing) || (err == nil && len(archives) == 0) {
		return nil, fmt.Errorf("no archives for host %q on %s", opts.Host, opts.Remote)
	}
	return archives, err
}

// pullTarget resolves target and refuses one inside devDir or one that already holds files.
func pullTarget(ctx context.Context, target, devDir string) (string, error) {
	resolved, err := util.ResolveExistingPath(ctx, target)
	if err != nil {
		return "", err
	}
	dev, err := util.ResolveExistingPath(ctx, devDir)
	if err != nil {
		return "", err
	}
	if rel, err := filepath.Rel(dev, resolved); err == nil && (rel == "." || filepath.IsLocal(rel)) {
		return "", fmt.Errorf("refusing to pull into %s: it is inside the dev folder %s", resolved, dev)
	}
	entries, err := os.ReadDir(resolved)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if len(entries) > 0 {
		return "", fmt.Errorf("refusing to pull into %s: it is not empty", resolved)
	}
	return resolved, nil
}

func pullArchive(ctx context.Context, opts PullOptions, target string, archive RemoteArchive) Outcome {
	outcome := Outcome{Archive: path.Join(opts.Host, archive.Path), Bytes: archive.Size}
	rel, err := entryName(strings.TrimSuffix(archive.Path, archiveSuffix))
	if err != nil {
		return failed(outcome, err)
	}
	dir := filepath.Join(target, rel)
	if err := util.MkdirSecure(dir, util.SecureDirPerm); err != nil {
		return failed(outcome, err)
	}
	err = opts.Rclone.download(ctx, remotePath(opts.Remote, opts.Host, archive.Path), func(r io.Reader) error {
		return extractArchive(ctx, r, dir)
	})
	if err != nil {
		return failed(outcome, err)
	}
	outcome.Status = StatusRestored
	return outcome
}

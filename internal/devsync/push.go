package devsync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/harvester"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// agentStateArchive is the per-host archive of the harvested agent state bundle.
	agentStateArchive = "agent-state" + archiveSuffix
	// devFolder is the per-host folder holding the project archives.
	devFolder = "dev"
	// bundleTimeout bounds harvesting the agent state bundle.
	bundleTimeout = 10 * time.Minute
)

// PushOptions configures Push.
type PushOptions struct {
	// DevDir is the folder holding the projects, normally <home>/dev.
	DevDir string
	// HomeDir is the home whose agent state is bundled.
	HomeDir string
	// Remote is the remote root, for example "praetor-sync:".
	Remote string
	// Host names this workstation's folder on the remote.
	Host string
	// StatePath is the local file recording what was uploaded; see DefaultStatePath.
	StatePath string
	// DryRun reports what would be uploaded without uploading or recording anything.
	DryRun bool
	Rclone Rclone
	// Out receives one line per archive as it completes; nil discards them.
	Out io.Writer
}

// Push uploads one archive per repository and per top-level non-repository folder below
// DevDir, skipping those unchanged since the last push, then the agent state bundle.
func Push(ctx context.Context, opts PushOptions) ([]Outcome, error) {
	if err := validatePush(opts); err != nil {
		return nil, err
	}
	state, err := loadState(opts.StatePath)
	if err != nil {
		return nil, err
	}
	units, err := discoverUnits(ctx, opts.DevDir)
	if err != nil {
		return nil, err
	}
	out := writerOrDiscard(opts.Out)
	outcomes := make([]Outcome, 0, len(units)+1)
	var printErr error
	for _, u := range units {
		outcome := pushUnit(ctx, opts, state, u)
		printErr = errors.Join(printErr, report(out, outcome))
		outcomes = append(outcomes, outcome)
	}
	outcome := pushAgentState(ctx, opts)
	printErr = errors.Join(printErr, report(out, outcome))
	outcomes = append(outcomes, outcome)
	return outcomes, errors.Join(failures(outcomes), printErr)
}

func validatePush(opts PushOptions) error {
	if err := validateHost(opts.Host); err != nil {
		return err
	}
	if opts.Remote == "" || opts.StatePath == "" || opts.HomeDir == "" {
		return errors.New("push needs a remote, a state path and a home directory")
	}
	info, err := os.Stat(opts.DevDir)
	if err != nil {
		return fmt.Errorf("dev folder: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("dev folder %s is not a directory", opts.DevDir)
	}
	return nil
}

// validateHost accepts a host name usable as one remote folder name.
func validateHost(host string) error {
	if host == "" || host == "." || host == ".." || strings.ContainsAny(host, ":/\\") {
		return fmt.Errorf("host %q must be one folder name", host)
	}
	if err := util.ValidateExecArg(host); err != nil {
		return fmt.Errorf("host %q: %w", host, err)
	}
	return nil
}

func pushUnit(ctx context.Context, opts PushOptions, state *pushState, u unit) Outcome {
	archive := path.Join(opts.Host, devFolder, filepath.ToSlash(u.rel)) + archiveSuffix
	outcome := Outcome{Archive: archive}
	key := remotePath(opts.Remote, archive)
	if !u.folder {
		u, outcome.Note = withGitIgnores(ctx, u)
	}
	current, err := fingerprintUnit(ctx, u)
	switch {
	case err != nil:
		return failed(outcome, err)
	case u.folder && current.Files == 0:
		outcome.Status, outcome.Note = StatusSkipped, "empty"
		return outcome
	case state.Archives[key] == current:
		outcome.Status, outcome.Bytes, outcome.Note = StatusSkipped, current.Bytes, "unchanged"
		return outcome
	case opts.DryRun:
		outcome.Status, outcome.Bytes, outcome.Note = StatusWouldUpload, current.Bytes, "before compression"
		return outcome
	}
	var written fingerprint
	n, err := opts.Rclone.upload(ctx, key, func(w io.Writer) error {
		var writeErr error
		written, writeErr = writeArchive(ctx, u, w)
		return writeErr
	})
	if err != nil {
		return failed(outcome, err)
	}
	outcome.Status, outcome.Bytes = StatusUploaded, n
	state.Archives[key] = written
	if err := state.save(opts.StatePath); err != nil {
		return failed(outcome, fmt.Errorf("uploaded, but recording it failed: %w", err))
	}
	return outcome
}

func pushAgentState(ctx context.Context, opts PushOptions) Outcome {
	outcome := Outcome{Archive: path.Join(opts.Host, agentStateArchive)}
	if opts.DryRun {
		outcome.Status = StatusWouldUpload
		return outcome
	}
	n, err := uploadAgentState(ctx, opts, remotePath(opts.Remote, outcome.Archive))
	if err != nil {
		return failed(outcome, err)
	}
	outcome.Status, outcome.Bytes = StatusUploaded, n
	return outcome
}

// uploadAgentState harvests the agent state bundle into a private temporary folder and
// uploads it as one archive. The bundle routinely carries credentials; it is removed again
// whatever the outcome.
func uploadAgentState(ctx context.Context, opts PushOptions, dest string) (n int64, err error) {
	temp, err := os.MkdirTemp("", "praetor-devsync-")
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(temp)) }()
	bundleDir := filepath.Join(temp, "agent-state")
	bundleCtx, cancel := context.WithTimeout(ctx, bundleTimeout)
	defer cancel()
	bundle := harvester.BundleOptions{WorkstationName: opts.Host, OutputDir: bundleDir, HomeDir: opts.HomeDir}
	if _, err := harvester.BundleWorkstation(bundleCtx, bundle); err != nil {
		return 0, fmt.Errorf("bundle agent state: %w", err)
	}
	u := unit{rel: "agent-state", dir: bundleDir, keepCaches: true}
	return opts.Rclone.upload(ctx, dest, func(w io.Writer) error {
		_, writeErr := writeArchive(ctx, u, w)
		return writeErr
	})
}

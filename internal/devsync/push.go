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
	// MaxArchiveSize caps every archive's measured source size in bytes, the agent state
	// bundle's included: push skips an archive whose fingerprint exceeds it rather than
	// streaming it. Zero means no cap. It is measured before compression, from the same
	// fingerprint recorded in StatePath, not from the archive itself.
	MaxArchiveSize int64
	Rclone         Rclone
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
	printErr = errors.Join(printErr, reportSizeSummary(out, outcomes))
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
	case exceedsCap(current.Bytes, opts.MaxArchiveSize):
		return tooLarge(outcome, current.Bytes, opts.MaxArchiveSize)
	case state.Archives[key] == current:
		outcome.Status, outcome.Bytes, outcome.Note = StatusSkipped, current.Bytes, "unchanged"
		return outcome
	case opts.DryRun:
		return wouldUpload(outcome, current.Bytes)
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

// exceedsCap reports whether an archive whose source measures bytes is over limit, the
// PushOptions.MaxArchiveSize every archive is held to. A limit of zero or less is no cap.
func exceedsCap(bytes, limit int64) bool {
	return limit > 0 && bytes > limit
}

// tooLarge reports an archive skipped because its source measures bytes, over limit.
func tooLarge(outcome Outcome, bytes, limit int64) Outcome {
	outcome.Status, outcome.Bytes = StatusTooLarge, bytes
	outcome.Note = fmt.Sprintf("%s exceeds cap %s", FormatBytes(bytes), FormatBytes(limit))
	return outcome
}

// wouldUpload reports, for a dry run, an archive whose source measures bytes.
func wouldUpload(outcome Outcome, bytes int64) Outcome {
	outcome.Status, outcome.Bytes, outcome.Note = StatusWouldUpload, bytes, "before compression"
	return outcome
}

// pushAgentState harvests the agent state bundle into a private temporary folder, then
// measures and uploads it through pushBundle; a dry run harvests and measures it too, without
// reaching the remote. The bundle routinely carries credentials; it is removed again whatever
// the outcome, and a failed removal fails the archive.
func pushAgentState(ctx context.Context, opts PushOptions) (outcome Outcome) {
	outcome = Outcome{Archive: path.Join(opts.Host, agentStateArchive)}
	temp, err := os.MkdirTemp("", "praetor-devsync-")
	if err != nil {
		return failed(outcome, fmt.Errorf("agent state temporary folder: %w", err))
	}
	defer func() {
		if removeErr := os.RemoveAll(temp); removeErr != nil {
			outcome = failed(outcome, errors.Join(outcome.Err, fmt.Errorf("remove agent state bundle: %w", removeErr)))
		}
	}()
	u, err := bundleAgentState(ctx, opts, filepath.Join(temp, "agent-state"))
	if err != nil {
		return failed(outcome, err)
	}
	return pushBundle(ctx, opts, outcome, u)
}

// bundleAgentState harvests the agent state bundle into dir, bounded by bundleTimeout, and
// returns it as the unit to archive.
func bundleAgentState(ctx context.Context, opts PushOptions, dir string) (unit, error) {
	bundleCtx, cancel := context.WithTimeout(ctx, bundleTimeout)
	defer cancel()
	bundle := harvester.BundleOptions{WorkstationName: opts.Host, OutputDir: dir, HomeDir: opts.HomeDir}
	if _, err := harvester.BundleWorkstation(bundleCtx, bundle); err != nil {
		return unit{}, fmt.Errorf("bundle agent state: %w", err)
	}
	return unit{rel: "agent-state", dir: dir, keepCaches: true}, nil
}

// pushBundle measures the harvested bundle u and holds it to MaxArchiveSize exactly as
// pushUnit holds a project, then uploads it as outcome.Archive unless it is too large or
// this is a dry run. The bundle is uploaded on every push; no state records it.
func pushBundle(ctx context.Context, opts PushOptions, outcome Outcome, u unit) Outcome {
	current, err := fingerprintUnit(ctx, u)
	switch {
	case err != nil:
		return failed(outcome, fmt.Errorf("measure agent state: %w", err))
	case exceedsCap(current.Bytes, opts.MaxArchiveSize):
		return tooLarge(outcome, current.Bytes, opts.MaxArchiveSize)
	case opts.DryRun:
		return wouldUpload(outcome, current.Bytes)
	}
	n, err := opts.Rclone.upload(ctx, remotePath(opts.Remote, outcome.Archive), func(w io.Writer) error {
		_, writeErr := writeArchive(ctx, u, w)
		return writeErr
	})
	if err != nil {
		return failed(outcome, err)
	}
	outcome.Status, outcome.Bytes = StatusUploaded, n
	return outcome
}

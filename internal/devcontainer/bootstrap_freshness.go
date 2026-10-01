package devcontainer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// freshnessGitBytes bounds one git answer the freshness check reads: a commit id with a
	// timestamp, a count, or a boolean.
	freshnessGitBytes = 4096
	// freshnessGitBound bounds one git read. Finding the newest commit that changed the bundle
	// walks history, so its cost grows with the repository (util.RunGitProbeWithin).
	freshnessGitBound = 30 * time.Second
)

// ErrBundleStale reports a committed bundle whose source is further behind the working tree
// than its freshness bounds allow.
var ErrBundleStale = errors.New("devcontainer bundle is past its freshness bound")

// FreshnessBounds are how far a committed bundle may fall behind the working tree before the
// freshness check fails: MaxCommits commits since the bundle last changed, and MaxAge since that
// commit. A bundle exactly at a bound passes.
type FreshnessBounds struct {
	MaxCommits int
	MaxAge     time.Duration
}

// FreshnessReport is one measurement of a committed bundle against the working tree. Commit,
// CommitTime, Behind and Age are measured only when the source differs (Fresh is false).
type FreshnessReport struct {
	// Recorded is the sourceSHA256 the configuration records.
	Recorded string
	// Current is the digest of the source recaptured from the working tree.
	Current string
	// Commit is the newest commit reachable from HEAD that changed a bundle file.
	Commit string
	// CommitTime is Commit's committer date.
	CommitTime time.Time
	// Behind counts the commits from Commit to HEAD.
	Behind int
	// Age is the time from CommitTime to the measurement, zero when the clock is behind it.
	Age    time.Duration
	Bounds FreshnessBounds
}

// Fresh reports whether the committed bundle carries exactly the source the working tree holds.
func (r *FreshnessReport) Fresh() bool {
	return r.Recorded == r.Current
}

// Exceeded reports whether the bundle is past either bound.
func (r *FreshnessReport) Exceeded() bool {
	return !r.Fresh() && (r.Behind > r.Bounds.MaxCommits || r.Age > r.Bounds.MaxAge)
}

// Err returns ErrBundleStale with the measurement when the bundle is past a bound, else nil.
func (r *FreshnessReport) Err() error {
	if !r.Exceeded() {
		return nil
	}
	return fmt.Errorf("%w: %d commits behind (bound %d) and %s old (bound %s) since bundle commit %s; "+
		"regenerate it with praetorctl devcontainer generate --source-root <Praetor checkout> --force",
		ErrBundleStale, r.Behind, r.Bounds.MaxCommits, formatDays(r.Age), formatDays(r.Bounds.MaxAge), util.ShortCommit(r.Commit))
}

// String renders the measurement as one line.
func (r *FreshnessReport) String() string {
	if r.Fresh() {
		return fmt.Sprintf("DevContainer bundle source %s matches the working tree", r.Recorded)
	}
	return fmt.Sprintf("DevContainer bundle source %s differs from the working tree (%s): %d commits and %s behind since bundle commit %s; bounds %d commits and %s",
		r.Recorded, r.Current, r.Behind, formatDays(r.Age), util.ShortCommit(r.Commit), r.Bounds.MaxCommits, formatDays(r.Bounds.MaxAge))
}

// CheckFreshness measures the bundle whose configuration is at path against the Praetor source
// in the working tree at root (#338). It recaptures the source the way preparation does
// (captureBootstrapSource) and compares its digest with the recorded sourceSHA256. When they
// differ it measures how far behind the bundle is: the commits from the newest commit that
// changed a bundle file to HEAD, and that commit's age at now.
//
// It is a separate check on purpose. Verify, through validateReadyBootstrap, checks a bundle
// against its own records, which a bundle cut a hundred commits ago passes exactly as one cut a
// minute ago; this check is what notices the difference, so it never runs inside that path.
//
// A missing or unavailable bundle, a root that is not a Praetor checkout, a shallow clone, a
// bundle no commit records, and any git failure are errors, never a fresh report.
func CheckFreshness(ctx context.Context, root, path string, bounds FreshnessBounds, now time.Time) (*FreshnessReport, error) {
	if ctx == nil {
		return nil, errors.New("devcontainer freshness requires context")
	}
	if bounds.MaxCommits < 1 || bounds.MaxAge <= 0 {
		return nil, fmt.Errorf("devcontainer freshness bounds must be positive; got %d commits and %s", bounds.MaxCommits, bounds.MaxAge)
	}
	ctx, cancel := context.WithTimeout(ctx, bootstrapBound)
	defer cancel()
	spec, err := readRecordedSource(ctx, path)
	if err != nil {
		return nil, err
	}
	files, err := captureBootstrapSource(ctx, root)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("devcontainer freshness compares the bundle with a Praetor source checkout; %s holds no go.mod", root)
	}
	report := &FreshnessReport{Recorded: spec.SourceSHA256, Current: bootstrapSourceDigest(files), Bounds: bounds}
	if report.Fresh() {
		return report, nil
	}
	if err := measureBundleLag(ctx, root, path, spec, now, report); err != nil {
		return nil, err
	}
	return report, nil
}

// readRecordedSource returns the ready bootstrap specification the configuration at path
// records.
func readRecordedSource(ctx context.Context, path string) (*BootstrapSpec, error) {
	_, dc, err := readBootstrapConfig(ctx, path)
	if err != nil {
		return nil, err
	}
	spec := (&Bundle{Config: dc}).Spec()
	if spec == nil {
		return nil, fmt.Errorf("devcontainer at %s records no bootstrap bundle", path)
	}
	if err := validateBootstrapSpec(spec); err != nil {
		return nil, fmt.Errorf("devcontainer at %s: %w", path, err)
	}
	if spec.State != BootstrapReady {
		return nil, fmt.Errorf("%w at %s: an unavailable bundle carries no source to compare", ErrBootstrapUnavailable, path)
	}
	return spec, nil
}

// measureBundleLag fills report with the newest commit that changed a bundle file, the commits
// from it to HEAD, and its age at now.
func measureBundleLag(ctx context.Context, root, path string, spec *BootstrapSpec, now time.Time, report *FreshnessReport) error {
	pathspecs, err := bundlePathspecs(root, path, spec)
	if err != nil {
		return err
	}
	shallow, err := freshnessGit(ctx, root, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return err
	}
	if shallow != "false" {
		return errors.New("devcontainer freshness cannot count commits in a shallow clone; fetch the full history (actions/checkout fetch-depth: 0)")
	}
	answer, err := freshnessGit(ctx, root, append([]string{"--literal-pathspecs", "log", "-1", "--format=%H %ct", "HEAD", "--"}, pathspecs...)...)
	if err != nil {
		return err
	}
	commit, committed, err := parseBundleCommit(answer, path)
	if err != nil {
		return err
	}
	count, err := freshnessGit(ctx, root, "rev-list", "--count", commit+"..HEAD")
	if err != nil {
		return err
	}
	behind, err := strconv.Atoi(count)
	if err != nil || behind < 0 {
		return fmt.Errorf("devcontainer freshness: git rev-list answered %q, not a commit count", count)
	}
	report.Commit, report.CommitTime, report.Behind = commit, committed, behind
	report.Age = max(now.Sub(committed), 0)
	return nil
}

// parseBundleCommit reads git log's "<commit> <committer timestamp>" answer. An empty answer
// means no commit reachable from HEAD records the bundle.
func parseBundleCommit(answer, path string) (string, time.Time, error) {
	if answer == "" {
		return "", time.Time{}, fmt.Errorf("devcontainer freshness: no commit records the bundle at %s; commit it before measuring how far behind it is", path)
	}
	commit, stamp, found := strings.Cut(answer, " ")
	seconds, err := strconv.ParseInt(stamp, 10, 64)
	if !found || err != nil || commit == "" {
		return "", time.Time{}, fmt.Errorf("devcontainer freshness: git log answered %q, not a commit and timestamp", answer)
	}
	return commit, time.Unix(seconds, 0), nil
}

// bundlePathspecs names every file of the bundle at path, the configuration and its companions,
// relative to root, as git pathspecs.
func bundlePathspecs(root, path string, spec *BootstrapSpec) ([]string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	relConfig, err := filepath.Rel(absRoot, absPath)
	if err != nil || relConfig == ".." || strings.HasPrefix(relConfig, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("devcontainer freshness: the bundle at %s lies outside the source root %s", path, root)
	}
	dir := filepath.Dir(relConfig)
	names := []string{relConfig, filepath.Join(dir, bootstrapDockerfile)}
	for i := 0; i < spec.ArchiveParts && i < maxBootstrapParts; i++ {
		names = append(names, filepath.Join(dir, bootstrapPartName(i)))
	}
	for i := range names {
		names[i] = filepath.ToSlash(names[i])
	}
	return names, nil
}

// freshnessGit runs one bounded read-only git inspection at root and returns its trimmed
// standard output.
func freshnessGit(ctx context.Context, root string, args ...string) (string, error) {
	result, err := util.RunGitProbeWithin(ctx, root, freshnessGitBytes, freshnessGitBound, args...)
	if err != nil {
		return "", fmt.Errorf("devcontainer freshness: git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

// formatDays renders a duration in days with one decimal.
func formatDays(d time.Duration) string {
	return strconv.FormatFloat(d.Hours()/24, 'f', 1, 64) + " days"
}

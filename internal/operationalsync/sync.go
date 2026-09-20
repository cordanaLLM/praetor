// Package operationalsync prepares local operational forks from reviewed commits.
package operationalsync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Options binds a run to reviewed local commits; it never fetches remote content.
type Options struct {
	OwnerPath   string `json:"owner_path"`
	SourcePath  string `json:"source_path"`
	OwnerSHA    string `json:"owner_sha"`
	BaseSHA     string `json:"base_sha"`
	SourceSHA   string `json:"source_sha"`
	Destination string `json:"destination,omitempty"`
	// Owner is the operational repository owner. Only init takes it; later stages read the manifest.
	Owner string `json:"owner,omitempty"`
}

// Report describes a structural plan or uncommitted merge, never a release receipt.
type Report struct {
	Version      int      `json:"version"`
	Stage        string   `json:"stage"`
	Status       string   `json:"status"`
	Error        string   `json:"error,omitempty"`
	Options      Options  `json:"options"`
	Owner        string   `json:"owner"`
	Source       string   `json:"source"`
	ChangedPaths []string `json:"changed_paths"`
	// OwnerOnlyPaths lists the operator files accepted under the owner-only prefixes; never nil.
	OwnerOnlyPaths []string `json:"owner_only_paths"`
	Candidate      string   `json:"candidate,omitempty"`
	MergePending   bool     `json:"merge_pending"`
	UpToDate       bool     `json:"up_to_date"`
	Scope          string   `json:"scope"`
}

type operation struct {
	git              *gitRunner
	opts             Options
	owner            identity
	origin, upstream string
	expected         map[string][]byte
	ownerOnly        []string
}

var commitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)
var identityPart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)

// Run validates plan/prepare, or applies the first owner overlay for init. Prepare creates only
// Destination and leaves a normal merge for review; init writes the four overlay files and stages nothing.
func Run(ctx context.Context, stage string, opts Options) (*Report, error) {
	if ctx == nil {
		return nil, errors.New("operational sync requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := validateOptions(stage, &opts); err != nil {
		return nil, err
	}
	g, err := newGit(ctx)
	if err != nil {
		return nil, err
	}
	if stage == "init" {
		return runInit(ctx, g, opts)
	}
	return runSync(ctx, g, stage, opts)
}

func runSync(ctx context.Context, g *gitRunner, stage string, opts Options) (*Report, error) {
	op := operation{git: g, opts: opts}
	if err := op.validate(ctx); err != nil {
		return nil, err
	}
	report := &Report{Version: 1, Stage: stage, Status: "planned", Options: opts, Owner: op.origin, Source: op.upstream,
		ChangedPaths: append([]string(nil), ownerPaths...), OwnerOnlyPaths: append([]string{}, op.ownerOnly...),
		Scope: "Identity, reviewed ancestry, exact engine tree, owner-only configuration overlay and owner-only operator paths; no tests, publication or bot activation"}
	if stage == "plan" {
		return report, nil
	}
	report.Candidate = opts.Destination
	report.Status = "preparing"
	if err := op.prepare(ctx, report); err != nil {
		report.Status, report.Error = "failed", err.Error()
		return report, err
	}
	report.Status = "prepared"
	if report.UpToDate {
		report.Status = "up-to-date"
	}
	return report, nil
}

func validateOptions(stage string, opts *Options) error {
	switch stage {
	case "init":
		return validateInitOptions(opts)
	case "plan", "prepare":
		return validateSyncOptions(stage, opts)
	}
	return errors.New("supported stages: init, plan, prepare")
}

func validateSyncOptions(stage string, opts *Options) error {
	for _, sha := range []string{opts.OwnerSHA, opts.BaseSHA, opts.SourceSHA} {
		if !commitSHA.MatchString(sha) {
			return errors.New("owner, base and source require full reviewed lowercase 40-character commit SHAs")
		}
	}
	for _, path := range []*string{&opts.OwnerPath, &opts.SourcePath} {
		if err := normalizeCheckoutPath(path); err != nil {
			return err
		}
	}
	if opts.Owner != "" {
		return errors.New("owner is only valid for init; later stages read it from the reviewed manifest")
	}
	if stage == "plan" && opts.Destination != "" {
		return errors.New("destination is only valid for prepare")
	}
	if stage == "prepare" {
		return validateDestination(opts)
	}
	return nil
}

func normalizeCheckoutPath(path *string) error {
	if *path == "" {
		return errors.New("owner and source checkout paths are required")
	}
	absolute, err := filepath.Abs(*path)
	if err != nil {
		return err
	}
	*path = absolute
	return util.ValidateExecPathArg(absolute)
}

func validateDestination(opts *Options) error {
	if opts.Destination == "" {
		return errors.New("prepare requires a new destination")
	}
	abs, err := filepath.Abs(opts.Destination)
	if err != nil {
		return err
	}
	opts.Destination = abs
	if err := util.ValidateExecPathArg(abs); err != nil {
		return err
	}
	if _, err := os.Lstat(abs); !os.IsNotExist(err) {
		return errors.New("destination must not exist")
	}
	// The directory is created with a single os.Mkdir (prepare.go), never MkdirAll, so the
	// parent is the only ancestor this call actually writes beneath, and Lstat on that one
	// entry is the confinement question: is it a real directory, or does prepare's Mkdir
	// follow a symlink to somewhere else. Resolving the whole chain with EvalSymlinks and
	// demanding literal equality (the previous check) asked a broader question than that and
	// answered it wrong: macOS ships /var as a symlink to /private/var, so any destination
	// under the platform's own TMPDIR -- including every t.TempDir() in this package's own
	// tests -- had a resolved ancestry that could never equal its literal spelling. That
	// failed the whole operationalsync suite on macOS (#135) for a symlink no attacker placed
	// and this call never walks through. The parent itself is still refused when it is one.
	parentInfo, err := os.Lstat(filepath.Dir(abs))
	if err != nil {
		return fmt.Errorf("destination parent: %w", err)
	}
	if !parentInfo.IsDir() {
		return errors.New("destination parent must be a directory, never a symlink")
	}
	for _, source := range []string{opts.OwnerPath, opts.SourcePath} {
		resolved, err := filepath.EvalSymlinks(source)
		if err != nil {
			return err
		}
		if abs == resolved || strings.HasPrefix(abs, resolved+string(filepath.Separator)) {
			return errors.New("destination must be outside input repositories")
		}
	}
	return nil
}

func (op *operation) validate(ctx context.Context) error {
	o := op.opts
	if err := op.checkCheckoutRoots(ctx); err != nil {
		return err
	}
	if err := op.checkInputConfiguration(ctx); err != nil {
		return err
	}
	if err := verifyInputsUnchanged(ctx, op); err != nil {
		return err
	}
	base, err := op.readFiles(ctx, o.SourcePath, o.BaseSHA)
	if err != nil {
		return err
	}
	source, err := op.validateExisting(ctx, base)
	if err != nil {
		return err
	}
	if err := op.git.ancestor(ctx, o.OwnerPath, o.BaseSHA, o.OwnerSHA); err != nil {
		return err
	}
	if err := op.git.ancestor(ctx, o.SourcePath, o.BaseSHA, o.SourceSHA); err != nil {
		return err
	}
	next, err := op.readFiles(ctx, o.SourcePath, o.SourceSHA)
	if err != nil {
		return err
	}
	_, nextID, err := manifest(next[ownerPaths[0]])
	if err != nil {
		return err
	}
	if nextID != source {
		return errors.New("reviewed upstream repository identity changed")
	}
	op.expected, err = overlay(next, op.owner)
	return err
}

// inputDirs names the checkouts a stage reads: init has no source checkout.
func (op *operation) inputDirs() []string {
	if op.opts.SourcePath == "" {
		return []string{op.opts.OwnerPath}
	}
	return []string{op.opts.OwnerPath, op.opts.SourcePath}
}

func (op *operation) checkCheckoutRoots(ctx context.Context) error {
	for _, path := range op.inputDirs() {
		root, err := op.git.text(ctx, path, "rev-parse", "--show-toplevel")
		if err != nil {
			return err
		}
		// "is this the same directory?" has one implementation (HISS-19): util.SameDirectory,
		// deciding by inode identity. String equality answered it wrong on both non-Linux legs
		// and could only ever be patched per symptom. git reports the top level with forward
		// slashes on every platform, resolved through its own real_path(), so on Windows it
		// prints "C:/Users/..." where the caller holds "C:\Users\..." and the drive letter's
		// case is not fixed either; on macOS the caller's TMPDIR reaches /var while git answers
		// /private/var. Compared as strings the two never matched and every checkout root was
		// refused as a subdirectory. FromSlash normalises git's separator before the stat and
		// cannot let a subdirectory pass: a subdirectory is a different inode from its root.
		// SameDirectory fails closed, so a path neither side can stat is refused here too.
		if !util.SameDirectory(filepath.FromSlash(root), path) {
			return errors.New("owner and source paths must name checkout roots, not subdirectories")
		}
	}
	return nil
}

func (op *operation) validateExisting(ctx context.Context, base map[string][]byte) (identity, error) {
	o := op.opts
	current, err := op.readFiles(ctx, o.OwnerPath, o.OwnerSHA)
	if err != nil {
		return identity{}, err
	}
	_, op.owner, err = manifest(current[ownerPaths[0]])
	if err != nil {
		return identity{}, err
	}
	_, source, err := manifest(base[ownerPaths[0]])
	if err != nil {
		return identity{}, err
	}
	if err := op.checkIdentity(ctx, source); err != nil {
		return identity{}, err
	}
	if err := op.checkOverlay(ctx, base, current); err != nil {
		return identity{}, err
	}
	op.ownerOnly, err = op.checkPaths(ctx, o.OwnerPath, o.BaseSHA, o.OwnerSHA)
	if err != nil {
		return identity{}, err
	}
	return source, nil
}

func (op *operation) readFiles(ctx context.Context, dir, sha string) (map[string][]byte, error) {
	files := make(map[string][]byte, 4)
	for _, path := range ownerPaths {
		raw, err := op.git.blob(ctx, dir, sha, path)
		if err != nil {
			return nil, err
		}
		if len(raw) > 1<<20 {
			return nil, fmt.Errorf("%s exceeds 1 MiB", path)
		}
		files[path] = raw
	}
	return files, nil
}

// checkOverlay compares the owner tree's four overlay files against what the current engine
// would generate. .standards.yaml uses equivalentManifestOverlay rather than plain equivalent:
// an owner manifest overlaid before a field existed (#255/#258 added repository.source) must
// still pass here so plan/prepare stay usable, while a manifest carrying a present-but-wrong
// value stays refused (#263).
func (op *operation) checkOverlay(ctx context.Context, base, current map[string][]byte) error {
	expected, err := overlay(base, op.owner)
	if err != nil {
		return err
	}
	for _, path := range ownerPaths {
		if path == ownerPaths[0] {
			ok, err := equivalentManifestOverlay(expected[path], current[path])
			if err != nil {
				return fmt.Errorf("unexpected owner override in %s: %w", path, err)
			}
			if !ok {
				return fmt.Errorf("unexpected owner override in %s", path)
			}
			continue
		}
		if !equivalent(path, expected[path], current[path]) {
			return fmt.Errorf("unexpected owner override in %s", path)
		}
	}
	return nil
}

// checkPaths refuses any difference outside the overlay and the owner-only prefixes and returns the
// accepted owner-only paths. Rename detection stays off: a rename reports only its destination and
// would hide the engine file an owner moved under an owner-only prefix.
func (op *operation) checkPaths(ctx context.Context, dir, base, current string) ([]string, error) {
	out, err := op.git.run(ctx, dir, "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--name-only", "-z", base, current, "--")
	if err != nil {
		return nil, err
	}
	ownerOnly := make([]string, 0)
	for _, path := range strings.Split(string(out), "\x00") {
		switch {
		case path == "" || allowedPath(path):
		case ownerOnlyPath(path):
			ownerOnly = append(ownerOnly, path)
		default:
			return nil, fmt.Errorf("unexpected owner tree difference: %q", path)
		}
	}
	slices.Sort(ownerOnly)
	return ownerOnly, op.checkOwnerOnly(ctx, dir, current, ownerOnly)
}

func allowedPath(path string) bool {
	for _, allowed := range ownerPaths {
		if path == allowed {
			return true
		}
	}
	return false
}

// Package operationalsync prepares local operational forks from reviewed commits.
package operationalsync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	Candidate    string   `json:"candidate,omitempty"`
	MergePending bool     `json:"merge_pending"`
	UpToDate     bool     `json:"up_to_date"`
	Scope        string   `json:"scope"`
}

type operation struct {
	git              *gitRunner
	opts             Options
	owner            identity
	origin, upstream string
	expected         map[string][]byte
}

var commitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)
var identityPart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)

// Run validates plan/prepare. Prepare creates only Destination and leaves a normal merge for review.
func Run(ctx context.Context, stage string, opts Options) (*Report, error) {
	if ctx == nil {
		return nil, errors.New("operational sync requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if stage != "plan" && stage != "prepare" {
		return nil, errors.New("supported stages: plan, prepare")
	}
	if err := validateOptions(stage, &opts); err != nil {
		return nil, err
	}
	g, err := newGit(ctx)
	if err != nil {
		return nil, err
	}
	op := operation{git: g, opts: opts}
	if err := op.validate(ctx); err != nil {
		return nil, err
	}
	report := &Report{Version: 1, Stage: stage, Status: "planned", Options: opts, Owner: op.origin, Source: op.upstream,
		ChangedPaths: append([]string(nil), ownerPaths...), Scope: "Identity, reviewed ancestry, exact engine tree and owner-only configuration overlay; no tests, publication or bot activation"}
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
	for _, sha := range []string{opts.OwnerSHA, opts.BaseSHA, opts.SourceSHA} {
		if !commitSHA.MatchString(sha) {
			return errors.New("owner, base and source require full reviewed lowercase 40-character commit SHAs")
		}
	}
	for _, path := range []*string{&opts.OwnerPath, &opts.SourcePath} {
		if *path == "" {
			return errors.New("owner and source checkout paths are required")
		}
		absolute, err := filepath.Abs(*path)
		if err != nil {
			return err
		}
		*path = absolute
		if err := util.ValidateExecPathArg(*path); err != nil {
			return err
		}
	}
	if stage == "plan" && opts.Destination != "" {
		return errors.New("destination is only valid for prepare")
	}
	if stage == "prepare" {
		return validateDestination(opts)
	}
	return nil
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
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return err
	}
	if parent != filepath.Dir(abs) {
		return errors.New("destination ancestors must not be symlinks")
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

func (op *operation) checkCheckoutRoots(ctx context.Context) error {
	for _, path := range []string{op.opts.OwnerPath, op.opts.SourcePath} {
		root, err := op.git.text(ctx, path, "rev-parse", "--show-toplevel")
		if err != nil {
			return err
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		// git reports the top level with forward slashes on every platform, so on Windows it
		// prints "C:/Users/..." while EvalSymlinks returns "C:\Users\...". Compared as strings
		// the two never matched and every checkout root was refused as a subdirectory.
		// FromSlash is a no-op where the separator is already '/', and normalising the
		// separator cannot let a subdirectory pass: it still differs from its root.
		if filepath.Clean(filepath.FromSlash(root)) != resolved {
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
	if err := op.checkPaths(ctx, o.OwnerPath, o.BaseSHA, o.OwnerSHA); err != nil {
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

func (op *operation) checkOverlay(ctx context.Context, base, current map[string][]byte) error {
	expected, err := overlay(base, op.owner)
	if err != nil {
		return err
	}
	for _, path := range ownerPaths {
		if !equivalent(path, expected[path], current[path]) {
			return fmt.Errorf("unexpected owner override in %s", path)
		}
	}
	return nil
}

func (op *operation) checkPaths(ctx context.Context, dir, base, current string) error {
	out, err := op.git.run(ctx, dir, "diff", "--no-ext-diff", "--no-textconv", "--name-only", "-z", base, current, "--")
	if err != nil {
		return err
	}
	for _, path := range strings.Split(string(out), "\x00") {
		if path == "" {
			continue
		}
		if !allowedPath(path) {
			return fmt.Errorf("unexpected owner tree difference: %q", path)
		}
	}
	return nil
}

func allowedPath(path string) bool {
	for _, allowed := range ownerPaths {
		if path == allowed {
			return true
		}
	}
	return false
}

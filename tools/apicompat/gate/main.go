//go:build apicompatgate

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Command gate is the Go API compatibility gate of the api:public-contract facet. The
// workflow .github/workflows/praetor-api.yml runs it on every pull request and push:
//
//	go run tools/apicompat/gate/main.go [-base=<rev>] [-policy=auto|warn|reject] [-checker=<path>]
//
// It compares the exported API of every public Go module between a base revision and the
// commit checked out (HEAD) with the pinned checker (checkerModule), run once per module from
// that module's directory against the repository root, so a nested module is compared as
// itself and never skipped by a root-only run.
//
// A module is a go.mod git tracks at the revision, outside the directories the go command
// skips in ./... patterns (testdata, vendor, and names starting with "." or "_"), whose module
// path has no internal element. A module at both revisions is compared; one only at HEAD is
// reported as added; one only at the base is a removed module, which is an incompatible change.
// More than maxModules modules, or a revision listing more than maxTreeEntries paths, fails
// the gate instead of comparing a truncated set.
//
// -base defaults to the newest root release tag, vMAJOR.MINOR.PATCH with no directory prefix,
// merged into HEAD. A nested module's tag carries its directory (dir/v1.2.3) and is never
// selected. With neither -base nor such a tag there is no published API to compare, and the
// gate passes saying so.
//
// Compatibility policy and execution policy are separate. -policy=auto rejects incompatible
// changes once the newest root release tag merged into HEAD is v1 or later and reports them as
// warnings before that; warn and reject fix the policy. A comparison that did not run fails the
// gate whatever the policy: a git or discovery error, a malformed go.mod, a compared module
// whose packages do not build at HEAD, a checker that cannot be installed, that misses the
// removed function of a canary module (verifyChecker), that exits with any status but 0 or 1,
// or that prints an error.
//
// Exit status: 0 compatible, or incompatible under the warn policy; 1 incompatible under the
// reject policy; 2 the comparison did not run. The checker checks each revision out in the
// working tree, restores HEAD afterwards and refuses a dirty tree, so run the gate in a clean
// checkout.
//
// praetorctl adopt writes this file and praetorctl audit locks it: change it in Praetor's
// tools/apicompat, never in an adopted repository.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	exitCompatible = 0
	exitRejected   = 1
	exitNotRun     = 2

	// checkerModule pins the checker, github.com/joelanford/go-apidiff, to commit c3e0953fa2fd
	// of its main branch (2026-09-10). Its newest release, v0.8.3, builds against
	// golang.org/x/tools v0.33.0, which cannot decode the export data of Go 1.27 and reads every
	// package as empty, so it passes every module; this commit only moves the dependencies
	// forward (x/tools v0.49.0). The CLI is "go-apidiff <oldCommit> [newCommit]
	// --repo-path=<root>": it loads ./... from its working directory and exits 0 when
	// compatible, 1 when it found an incompatible change and 2 when the comparison failed. A
	// flag or argument error is printed with cobra's "Error: " prefix while the status stays 0,
	// which classifyChecker reads as a failed run.
	checkerModule = "github.com/joelanford/go-apidiff@v0.8.4-0.20260910211158-c3e0953fa2fd"
	checkerBinary = "go-apidiff"

	maxTreeEntries   = 1 << 21
	maxModules       = 512
	maxTags          = 1 << 16
	maxRecordBytes   = 1 << 16
	maxGoModBytes    = 1 << 20
	maxGoModLines    = 1 << 16
	maxPathElements  = 256
	maxDiagnostics   = 1 << 20
	maxRevisionBytes = 4096

	runTimeout     = 3 * time.Hour
	gitTimeout     = 2 * time.Minute
	installTimeout = 10 * time.Minute
	moduleTimeout  = 30 * time.Minute
)

// releaseTagPattern matches a root release tag. A nested module's tags carry the module's
// directory as a prefix (Go modules reference, "Mapping versions to commits"), and a
// pre-release or build suffix does not publish a release, so neither matches.
var releaseTagPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// The canary module verifyChecker compares: its second commit removes the exported function
// its first declares.
const (
	canaryGoMod  = "module apicompat.invalid/canary\n\ngo 1.21\n"
	canaryBefore = "package canary\n\n// Removed is deleted by the second commit.\nfunc Removed() {}\n"
	canaryAfter  = "package canary\n"
)

var errOutputLimit = errors.New("output exceeds its bound")

// gitRepositoryVariables bind git to a repository other than the one its working directory
// names. A gate run from a git hook inherits them, and the canary's git init and commits would
// then write into that repository. internal/util/command_environment.go strips the same set
// from Praetor's own child processes; this standalone program cannot import it.
var gitRepositoryVariables = [...]string{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_CONFIG", "GIT_DIR", "GIT_GRAFT_FILE",
	"GIT_IMPLICIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_NO_REPLACE_OBJECTS", "GIT_OBJECT_DIRECTORY",
	"GIT_PREFIX", "GIT_REPLACE_REF_BASE", "GIT_SHALLOW_FILE", "GIT_WORK_TREE",
}

func main() {
	for _, name := range gitRepositoryVariables {
		if err := os.Unsetenv(name); err != nil {
			fmt.Fprintf(os.Stderr, "error: the API compatibility comparison did not run: unset %s: %v\n", name, err)
			os.Exit(exitNotRun)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	status := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	cancel()
	os.Exit(status)
}

// run executes the gate and returns its exit status. Every error is an execution failure, and
// so is a report the gate could not write.
func run(ctx context.Context, args []string, out, errOut io.Writer) int {
	stdout, stderr := &printer{w: out}, &printer{w: errOut}
	status, err := runGate(ctx, args, stdout, stderr)
	if err != nil {
		stderr.annotate("error", "the API compatibility comparison did not run: "+err.Error())
		status = exitNotRun
	}
	if stdout.failed != nil || stderr.failed != nil {
		return exitNotRun
	}
	return status
}

func runGate(ctx context.Context, args []string, stdout, stderr *printer) (int, error) {
	opts, err := parseOptions(args, stderr.w)
	if err != nil {
		return exitNotRun, err
	}
	return gate(ctx, opts, stdout, stderr)
}

// printer writes the gate's report and keeps the first write error.
type printer struct {
	w      io.Writer
	failed error
}

func (p *printer) printf(format string, args ...any) {
	if p.failed == nil {
		_, p.failed = fmt.Fprintf(p.w, format, args...)
	}
}

// annotate prints one message as a GitHub Actions workflow command, which renders as an
// annotation, when GITHUB_ACTIONS is "true", and as a plain "level: message" line otherwise.
func (p *printer) annotate(level, message string) {
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		p.printf("::%s::%s\n", level, strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(message))
		return
	}
	p.printf("%s: %s\n", level, message)
}

// options are the gate's flags.
type options struct {
	repo, base, policy, checker string
}

func parseOptions(args []string, stderr io.Writer) (options, error) {
	flags := flag.NewFlagSet("gate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var opts options
	flags.StringVar(&opts.repo, "repo", ".", "any directory inside the repository to compare")
	flags.StringVar(&opts.base, "base", "", "base revision; empty selects the newest root release tag merged into HEAD")
	flags.StringVar(&opts.policy, "policy", "auto", "compatibility policy: auto, warn or reject")
	flags.StringVar(&opts.checker, "checker", "", "go-apidiff binary; empty installs "+checkerModule)
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected arguments %q", flags.Args())
	}
	if !slices.Contains([]string{"auto", "warn", "reject"}, opts.policy) {
		return options{}, fmt.Errorf("-policy must be auto, warn or reject, got %q", opts.policy)
	}
	if strings.HasPrefix(opts.base, "-") || len(opts.base) > maxRevisionBytes {
		return options{}, fmt.Errorf("-base %q is not a revision name", opts.base)
	}
	return opts, nil
}

// gate resolves the revisions and the policy, discovers the modules at both revisions and
// compares them, returning the exit status for the verdict.
func gate(ctx context.Context, opts options, stdout, stderr *printer) (int, error) {
	repo, err := resolveRepository(ctx, opts)
	if err != nil {
		return exitNotRun, err
	}
	if repo.base == "" {
		stdout.printf("No root release tag vMAJOR.MINOR.PATCH is merged into HEAD %s and -base is unset: "+
			"no published API to compare.\n", short(repo.head))
		return exitCompatible, nil
	}
	if repo.base == repo.head {
		stdout.printf("Base %s is HEAD %s: nothing to compare.\n", repo.baseName, short(repo.head))
		return exitCompatible, nil
	}
	plan, err := planModules(ctx, repo)
	if err != nil {
		return exitNotRun, err
	}
	results, err := compareModules(ctx, repo, plan, opts.checker, stdout, stderr)
	if err != nil {
		return exitNotRun, err
	}
	return verdict(repo, plan, results, stdout, stderr), nil
}

// repository is what one gate run compares: the root, the two commits and the policy.
type repository struct {
	root     string
	head     string
	base     string
	baseName string
	policy   policy
}

// policy is the compatibility policy: whether an incompatible change fails the gate, and why.
type policy struct {
	reject bool
	reason string
}

// releaseTag is the newest root release tag merged into HEAD.
type releaseTag struct {
	name    string
	version [3]int
	found   bool
}

func resolveRepository(ctx context.Context, opts options) (repository, error) {
	top, err := gitCapture(ctx, opts.repo, maxRevisionBytes, "rev-parse", "--show-toplevel")
	if err != nil {
		return repository{}, err
	}
	repo := repository{root: filepath.Clean(filepath.FromSlash(strings.TrimSpace(string(top))))}
	if repo.head, err = commitOf(ctx, repo.root, "HEAD"); err != nil {
		return repository{}, err
	}
	newest, err := newestReleaseTag(ctx, repo.root, repo.head)
	if err != nil {
		return repository{}, err
	}
	repo.policy = resolvePolicy(opts.policy, newest)
	switch {
	case opts.base != "":
		repo.baseName = opts.base
		repo.base, err = commitOf(ctx, repo.root, opts.base)
	case newest.found:
		repo.baseName = newest.name
		repo.base, err = commitOf(ctx, repo.root, "refs/tags/"+newest.name)
	}
	return repo, err
}

// commitOf resolves revision to the full name of the commit it names.
func commitOf(ctx context.Context, root, revision string) (string, error) {
	out, err := gitCapture(ctx, root, maxRevisionBytes, "rev-parse", "--verify", "--quiet", revision+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("revision %q does not name a commit: %w", revision, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// newestReleaseTag returns the highest root release tag merged into head.
func newestReleaseTag(ctx context.Context, root, head string) (releaseTag, error) {
	tags, err := gitRecords(ctx, root, '\n', maxTags, func(string) bool { return true },
		"tag", "--merged", head, "--list", "v*")
	if err != nil {
		return releaseTag{}, err
	}
	var newest releaseTag
	for index := 0; index < len(tags) && index < maxTags; index++ {
		candidate, ok, err := parseReleaseTag(tags[index])
		if err != nil {
			return releaseTag{}, err
		}
		if ok && (!newest.found || slices.Compare(candidate.version[:], newest.version[:]) > 0) {
			newest = candidate
		}
	}
	return newest, nil
}

// parseReleaseTag reads name as a root release tag; ok is false for any other tag.
func parseReleaseTag(name string) (releaseTag, bool, error) {
	match := releaseTagPattern.FindStringSubmatch(name)
	if match == nil {
		return releaseTag{}, false, nil
	}
	tag := releaseTag{name: name, found: true}
	for index := range tag.version {
		value, err := strconv.Atoi(match[index+1])
		if err != nil {
			return releaseTag{}, false, fmt.Errorf("release tag %s: %w", name, err)
		}
		tag.version[index] = value
	}
	return tag, true, nil
}

// resolvePolicy turns the -policy flag into the compatibility policy. auto follows the
// release line: a v1 or later root release promises a stable API, a v0 line or no release
// does not.
func resolvePolicy(flagValue string, newest releaseTag) policy {
	switch {
	case flagValue == "warn":
		return policy{reason: "-policy=warn"}
	case flagValue == "reject":
		return policy{reject: true, reason: "-policy=reject"}
	case !newest.found:
		return policy{reason: "auto: no root release tag, so the API is pre-v1"}
	case newest.version[0] >= 1:
		return policy{reject: true, reason: "auto: newest root release tag " + newest.name + " is v1 or later"}
	default:
		return policy{reason: "auto: newest root release tag " + newest.name + " is pre-v1"}
	}
}

// module is one go.mod a revision tracks.
type module struct {
	dir  string
	path string
}

// modulePlan sorts the modules of both revisions by directory: compared at both, added at HEAD
// only, removed from HEAD, and not public because the module path has an internal element.
type modulePlan struct {
	compared  []module
	added     []module
	removed   []module
	notPublic []module
}

func (p modulePlan) empty() bool {
	return len(p.compared)+len(p.added)+len(p.removed)+len(p.notPublic) == 0
}

func planModules(ctx context.Context, repo repository) (modulePlan, error) {
	base, err := discoverModules(ctx, repo.root, repo.base)
	if err != nil {
		return modulePlan{}, err
	}
	head, err := discoverModules(ctx, repo.root, repo.head)
	if err != nil {
		return modulePlan{}, err
	}
	var plan modulePlan
	base, plan.notPublic = splitPublic(base, nil)
	head, plan.notPublic = splitPublic(head, plan.notPublic)
	for index := 0; index < len(head) && index < maxModules; index++ {
		if containsDir(base, head[index].dir) {
			plan.compared = append(plan.compared, head[index])
		} else {
			plan.added = append(plan.added, head[index])
		}
	}
	for index := 0; index < len(base) && index < maxModules; index++ {
		if !containsDir(head, base[index].dir) {
			plan.removed = append(plan.removed, base[index])
		}
	}
	return plan, nil
}

// splitPublic returns the modules whose path has no internal element, and appends the others
// to notPublic once per directory.
func splitPublic(modules, notPublic []module) (public, skipped []module) {
	skipped = notPublic
	for index := 0; index < len(modules) && index < maxModules; index++ {
		if !slices.Contains(strings.Split(modules[index].path, "/"), "internal") {
			public = append(public, modules[index])
		} else if !containsDir(skipped, modules[index].dir) {
			skipped = append(skipped, modules[index])
		}
	}
	return public, skipped
}

func containsDir(modules []module, dir string) bool {
	return slices.ContainsFunc(modules, func(m module) bool { return m.dir == dir })
}

// discoverModules lists the modules revision tracks, in directory order.
func discoverModules(ctx context.Context, root, revision string) ([]module, error) {
	files, err := gitRecords(ctx, root, 0, maxTreeEntries, isModuleFile,
		"ls-tree", "-r", "-z", "--full-tree", "--name-only", revision)
	if err != nil {
		return nil, fmt.Errorf("list the files of %s: %w", short(revision), err)
	}
	if len(files) > maxModules {
		return nil, fmt.Errorf("%s tracks %d Go modules, more than the %d one run compares", short(revision), len(files), maxModules)
	}
	slices.Sort(files)
	modules := make([]module, 0, len(files))
	for index := 0; index < len(files) && index < maxModules; index++ {
		data, err := gitCapture(ctx, root, maxGoModBytes, "cat-file", "blob", revision+":"+files[index])
		if err != nil {
			return nil, err
		}
		modulePath, err := declaredModulePath(string(data))
		if err != nil {
			return nil, fmt.Errorf("%s:%s: %w", short(revision), files[index], err)
		}
		modules = append(modules, module{dir: path.Dir(files[index]), path: modulePath})
	}
	return modules, nil
}

// isModuleFile reports whether a tracked path is a go.mod the go command would build: one in no
// directory a ./... pattern skips.
func isModuleFile(name string) bool {
	if path.Base(name) != "go.mod" {
		return false
	}
	elements := strings.Split(path.Dir(name), "/")
	if len(elements) > maxPathElements {
		return false
	}
	return !slices.ContainsFunc(elements, skippedDirectory)
}

// skippedDirectory reports whether the go command skips a directory of this name in a ./...
// pattern (go help packages).
func skippedDirectory(name string) bool {
	if name == "." {
		return false
	}
	return name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// declaredModulePath returns the path of a go.mod's module directive: bare, double-quoted or
// back-quoted, with an optional trailing comment.
func declaredModulePath(gomod string) (string, error) {
	lines := strings.Split(gomod, "\n")
	for index := 0; index < len(lines) && index < maxGoModLines; index++ {
		value, found := moduleDirective(lines[index])
		if !found {
			continue
		}
		modulePath, err := unquotedModulePath(value)
		if err != nil {
			return "", fmt.Errorf("module directive %q: %w", strings.TrimSpace(lines[index]), err)
		}
		return modulePath, nil
	}
	return "", errors.New("go.mod declares no module directive")
}

// moduleDirective returns the argument of line when line is a module directive.
func moduleDirective(line string) (string, bool) {
	line, _, _ = strings.Cut(line, "//")
	rest, found := strings.CutPrefix(strings.TrimSpace(line), "module")
	if !found || rest == "" || !strings.ContainsRune(" \t\"`", rune(rest[0])) {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// unquotedModulePath reads a module directive's argument as one module path.
func unquotedModulePath(value string) (string, error) {
	if strings.HasPrefix(value, "\"") || strings.HasPrefix(value, "`") {
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			return "", err
		}
		value = unquoted
	}
	if value == "" || strings.ContainsAny(value, " \t()") {
		return "", errors.New("names no module path")
	}
	return value, nil
}

// result is the checker's verdict on one compared module.
type result struct {
	module       module
	incompatible bool
}

// compareModules verifies the checker, then builds and compares each compared module in
// directory order. The first failure stops the comparison: the checker may have left another
// revision checked out.
func compareModules(
	ctx context.Context, repo repository, plan modulePlan, explicit string, stdout, stderr *printer,
) (results []result, err error) {
	if len(plan.compared) == 0 {
		return nil, nil
	}
	checker, cleanup, err := resolveChecker(ctx, explicit, stderr.w)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, cleanup()) }()
	if err := verifyChecker(ctx, checker, stderr.w); err != nil {
		return nil, err
	}
	return compareEach(ctx, repo, plan, checker, stdout, stderr)
}

// compareEach builds and compares each compared module in directory order, stopping at the
// first failure.
func compareEach(ctx context.Context, repo repository, plan modulePlan, checker string, stdout, stderr *printer) ([]result, error) {
	results := make([]result, 0, len(plan.compared))
	for index := 0; index < len(plan.compared) && index < maxModules; index++ {
		target := plan.compared[index]
		stdout.printf("Comparing %s (%s) from %s to %s\n", target.dir, target.path, short(repo.base), short(repo.head))
		moduleDir := filepath.Join(repo.root, filepath.FromSlash(target.dir))
		incompatible, err := compareModule(ctx, checker, repo, moduleDir, stdout.w, stderr.w)
		if err != nil {
			return nil, fmt.Errorf("module %s (%s): %w", target.dir, target.path, err)
		}
		results = append(results, result{module: target, incompatible: incompatible})
	}
	return results, nil
}

// compareModule builds the module's packages at HEAD, then runs the checker on it. The checker
// reads a package it cannot type-check as empty without saying so, so a module that does not
// build would otherwise read as compatible; go list -export builds what the checker loads and
// writes nothing into the working tree.
func compareModule(ctx context.Context, checker string, repo repository, moduleDir string, stdout, stderr io.Writer) (bool, error) {
	if err := runTool(ctx, moduleDir, io.Discard, stderr, "go", "list", "-export", "./..."); err != nil {
		return false, fmt.Errorf("its packages do not build at HEAD, so the checker cannot read them: %w", err)
	}
	return runChecker(ctx, checker, repo.root, moduleDir, repo.base, repo.head, stdout, stderr)
}

// resolveChecker returns the checker binary: the -checker path, or checkerModule installed into
// a temporary directory the returned cleanup removes. The install runs outside the repository,
// so no go.mod, go.work or vendor directory of the repository changes what it builds.
func resolveChecker(ctx context.Context, explicit string, stderr io.Writer) (string, func() error, error) {
	if explicit != "" {
		return explicit, func() error { return nil }, nil
	}
	dir, err := os.MkdirTemp("", "apicompat-checker-")
	if err != nil {
		return "", nil, fmt.Errorf("create the checker directory: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()
	install := exec.CommandContext(ctx, "go", "install", checkerModule)
	install.Dir = dir
	install.Env = append(os.Environ(), "GOBIN="+dir)
	install.Stdout, install.Stderr = stderr, stderr
	if err := install.Run(); err != nil {
		return "", nil, errors.Join(fmt.Errorf("install %s: %w", checkerModule, err), os.RemoveAll(dir))
	}
	name := checkerBinary
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, name), func() error { return os.RemoveAll(dir) }, nil
}

// verifyChecker runs the checker on a canary repository whose second commit removes an
// exported function, and fails unless it reports that change as incompatible. A checker built
// against a golang.org/x/tools too old for the running toolchain's export data reads every
// package as empty and passes every module; the canary turns that silent pass into a failure.
func verifyChecker(ctx context.Context, checker string, stderr io.Writer) (err error) {
	dir, err := os.MkdirTemp("", "apicompat-canary-")
	if err != nil {
		return fmt.Errorf("create the canary repository: %w", err)
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()
	repo := filepath.Join(dir, "repo")
	base, head, err := commitCanary(ctx, repo, filepath.Join(dir, "no-hooks"))
	if err != nil {
		return fmt.Errorf("create the canary repository: %w", err)
	}
	incompatible, err := runChecker(ctx, checker, repo, repo, base, head, io.Discard, stderr)
	if err != nil {
		return fmt.Errorf("the checker failed on its canary: %w", err)
	}
	if !incompatible {
		return errors.New("the checker reported the canary's removed exported function as compatible, " +
			"so it cannot read the packages this Go toolchain builds")
	}
	return nil
}

// commitCanary creates the canary repository in repo with two commits and returns them. The
// commits run with a fixed identity, no signing and a hooks directory that does not exist, so
// no configuration of the machine changes or blocks them.
func commitCanary(ctx context.Context, repo, hooks string) (base, head string, err error) {
	if err := os.MkdirAll(repo, 0o700); err != nil {
		return "", "", err
	}
	commit := []string{"-c", "user.name=apicompat", "-c", "user.email=apicompat@example.invalid",
		"-c", "commit.gpgSign=false", "-c", "core.hooksPath=" + hooks, "commit", "--quiet", "--all", "--message"}
	steps := []func() error{
		func() error { return runTool(ctx, repo, io.Discard, io.Discard, "git", "init", "--quiet") },
		func() error { return writeCanary(repo, canaryBefore) },
		func() error { return runTool(ctx, repo, io.Discard, io.Discard, "git", "add", "--all") },
		func() error { return runTool(ctx, repo, io.Discard, io.Discard, "git", append(commit, "base")...) },
		func() error { base, err = commitOf(ctx, repo, "HEAD"); return err },
		func() error { return os.WriteFile(filepath.Join(repo, "canary.go"), []byte(canaryAfter), 0o600) },
		func() error { return runTool(ctx, repo, io.Discard, io.Discard, "git", append(commit, "head")...) },
		func() error { head, err = commitOf(ctx, repo, "HEAD"); return err },
	}
	for index := 0; index < len(steps); index++ {
		if err := steps[index](); err != nil {
			return "", "", err
		}
	}
	return base, head, nil
}

// writeCanary writes the canary module's go.mod and its source.
func writeCanary(repo, source string) error {
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte(canaryGoMod), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(repo, "canary.go"), []byte(source), 0o600)
}

// runChecker compares the module in moduleDir between base and head of the repository at root,
// and reports whether the checker found an incompatible change.
func runChecker(ctx context.Context, checker, root, moduleDir, base, head string, stdout, stderr io.Writer) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, moduleTimeout)
	defer cancel()
	diagnostics := &boundedBuffer{limit: maxDiagnostics}
	command := exec.CommandContext(ctx, checker, base, head, "--repo-path="+root)
	command.Dir = moduleDir
	command.Stdout = stdout
	command.Stderr = io.MultiWriter(stderr, diagnostics)
	return classifyChecker(command.Run(), diagnostics.String())
}

// classifyChecker reads one checker run: status 0 is compatible unless the run printed an
// error, status 1 is incompatible, and anything else is a comparison that did not run.
func classifyChecker(runErr error, diagnostics string) (bool, error) {
	var exit *exec.ExitError
	switch {
	case runErr == nil && reportedError(diagnostics):
		return false, errors.New("the checker printed an error and exited 0, so it compared nothing")
	case runErr == nil:
		return false, nil
	case errors.As(runErr, &exit) && exit.ExitCode() == exitRejected:
		return true, nil
	default:
		return false, fmt.Errorf("the checker failed: %w", runErr)
	}
}

// reportedError reports whether diagnostics hold a line in cobra's error format.
func reportedError(diagnostics string) bool {
	lines := strings.Split(diagnostics, "\n")
	for index := 0; index < len(lines) && index < maxGoModLines; index++ {
		if strings.HasPrefix(strings.TrimSpace(lines[index]), "Error: ") {
			return true
		}
	}
	return false
}

// verdict prints every module's outcome and the policy, and returns the exit status.
func verdict(repo repository, plan modulePlan, results []result, stdout, stderr *printer) int {
	if plan.empty() {
		stdout.printf("No Go module is tracked at %s or HEAD %s: nothing to compare.\n", repo.baseName, short(repo.head))
		return exitCompatible
	}
	stdout.printf("\nGo API compatibility from %s (%s) to HEAD %s:\n", repo.baseName, short(repo.base), short(repo.head))
	incompatible := len(plan.removed)
	for index := 0; index < len(results) && index < maxModules; index++ {
		state := "compatible"
		if results[index].incompatible {
			state = "incompatible"
			incompatible++
		}
		printModule(stdout, state, results[index].module, "")
	}
	printModules(stdout, "added", plan.added, "new at HEAD, nothing to compare")
	printModules(stdout, "removed", plan.removed, "every package of the module is gone")
	printModules(stdout, "not public", plan.notPublic, "the module path has an internal element")
	if incompatible == 0 {
		stdout.printf("Every compared module is compatible (policy %s).\n", repo.policy.reason)
		return exitCompatible
	}
	message := fmt.Sprintf("%d incompatible module change(s) from %s to HEAD %s", incompatible, repo.baseName, short(repo.head))
	if repo.policy.reject {
		stderr.annotate("error", message+" rejected ("+repo.policy.reason+")")
		return exitRejected
	}
	stdout.annotate("warning", message+" reported, not rejected ("+repo.policy.reason+")")
	return exitCompatible
}

func printModules(stdout *printer, state string, modules []module, note string) {
	for index := 0; index < len(modules) && index < maxModules; index++ {
		printModule(stdout, state, modules[index], note)
	}
}

func printModule(stdout *printer, state string, target module, note string) {
	if note != "" {
		note = ": " + note
	}
	stdout.printf("  %-12s %s (%s)%s\n", state, target.dir, target.path, note)
}

// short abbreviates a commit name for display.
func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// runTool runs name args in dir under moduleTimeout.
func runTool(ctx context.Context, dir string, stdout, stderr io.Writer, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, moduleTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = dir
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

// gitCapture runs git in dir and returns its standard output, refusing more than limit bytes.
func gitCapture(ctx context.Context, dir string, limit int, args ...string) ([]byte, error) {
	var out []byte
	err := runGit(ctx, dir, func(stdout io.Reader) error {
		data, err := io.ReadAll(io.LimitReader(stdout, int64(limit)+1))
		if err == nil && len(data) > limit {
			err = errOutputLimit
		}
		out = data
		return err
	}, args...)
	return out, err
}

// gitRecords runs git in dir and returns the records of its output, split at sep, that keep
// selects. More than limit records fail rather than truncate the list.
func gitRecords(ctx context.Context, dir string, sep byte, limit int, keep func(string) bool, args ...string) ([]string, error) {
	var kept []string
	err := runGit(ctx, dir, func(stdout io.Reader) error {
		var err error
		kept, err = scanRecords(bufio.NewReader(stdout), sep, limit, keep)
		return err
	}, args...)
	return kept, err
}

// scanRecords reads at most limit records ending in sep; a final record without sep counts.
func scanRecords(reader *bufio.Reader, sep byte, limit int, keep func(string) bool) ([]string, error) {
	var kept []string
	for count := 0; count <= limit; count++ {
		record, err := reader.ReadString(sep)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if len(record) > maxRecordBytes {
			return nil, fmt.Errorf("an output record exceeds %d bytes", maxRecordBytes)
		}
		if record = strings.TrimSuffix(record, string(sep)); record != "" && keep(record) {
			kept = append(kept, record)
		}
		if errors.Is(err, io.EOF) {
			return kept, nil
		}
	}
	return nil, fmt.Errorf("output exceeds %d records", limit)
}

// runGit runs git -C dir args under gitTimeout and hands its standard output to consume. When
// consume fails, git is stopped; standard error, bounded, is carried in a failure.
func runGit(ctx context.Context, dir string, consume func(io.Reader) error, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	diagnostics := &boundedBuffer{limit: maxDiagnostics}
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	command.Stderr = diagnostics
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("git %s: %w", args[0], err)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("git %s: %w", args[0], err)
	}
	if err := consume(stdout); err != nil {
		cancel()
		return errors.Join(fmt.Errorf("git %s: %w", args[0], err), command.Wait())
	}
	if err := command.Wait(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(diagnostics.String()))
	}
	return nil
}

// boundedBuffer keeps the first limit bytes written to it and drops the rest, reporting every
// write as complete so a writer sharing an io.MultiWriter with it still receives all output.
type boundedBuffer struct {
	data  []byte
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - len(b.data); room > 0 {
		b.data = append(b.data, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	return string(b.data)
}

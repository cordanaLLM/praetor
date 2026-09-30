// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package devcontainer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// ConfigPath is where a repository keeps the devcontainer the gate builds, relative to its root.
const ConfigPath = ".devcontainer/devcontainer.json"

const (
	// CLIName is the reference devcontainer CLI (@devcontainers/cli). It is the only builder that
	// applies a configuration's features: a docker or podman build of the Dockerfile ignores them.
	CLIName = "devcontainer"
	// ImageBuildTimeout bounds one image build, pull and inspection (HISS-02). A first build pulls
	// the base images and installs the features; later builds reuse the runtime's layer cache.
	ImageBuildTimeout = 15 * time.Minute
	// RuntimeProbeTimeout bounds the `<runtime> info` call that asks one container runtime on
	// PATH whether it can serve a build (HISS-02): a stopped daemon or podman machine answers
	// within it with an error, and a hung one is cut off by it.
	RuntimeProbeTimeout = 10 * time.Second
	// RuntimeProbeBudget is the longest planning spends asking runtimes: one probe per runtime.
	RuntimeProbeBudget = RuntimeProbeTimeout * time.Duration(len(containerRuntimes))
	// probeExcerptBytes bounds how much of a failed probe's diagnostic a refusal quotes.
	probeExcerptBytes = 200
)

// containerRuntimes are the container CLIs an image is built and run with, in preference order.
// docker comes first because it is the devcontainer CLI's default.
var containerRuntimes = [...]string{"docker", "podman"}

// imageID is the form a runtime's image ID takes once normalizeImageID has prefixed it.
var imageID = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// ErrImageUnavailable reports a devcontainer this host cannot build as its configuration declares
// it. Its message says why; a caller runs the work on the host instead and says so.
var ErrImageUnavailable = errors.New("devcontainer image unavailable here")

// CommandRunner runs name with args in dir and returns its standard output, carrying standard
// error in the error. util.RunCommand is the production runner.
type CommandRunner func(ctx context.Context, dir, name string, args ...string) (string, error)

// Host is what planning an image reads from the machine: its operating system, its PATH, and
// the runner that asks each container runtime on PATH whether it answers.
type Host struct {
	GOOS     string
	LookPath func(string) (string, error)
	// Run starts the `<runtime> info` probe. util.RunCommand is the production runner.
	Run CommandRunner
}

// Runtime is a container CLI resolved on PATH.
type Runtime struct {
	Name string
	Path string
}

// ImagePlan is a devcontainer image this host can build: the checked configuration, the runtime
// that builds and runs it, and the devcontainer CLI when the configuration declares features.
type ImagePlan struct {
	RepoDir    string
	ConfigPath string
	Config     *DevContainer
	Runtime    Runtime
	// CLI is the devcontainer CLI's path, set when the configuration declares features.
	CLI string
	// dockerfile and context are the confined absolute build inputs, empty for an image-only
	// configuration.
	dockerfile, context string
}

// ImageRefs are the two names a build gives its image. Stable is the repository's lasting tag: the
// next build reuses the layers it keeps, and moving it to a newer image leaves the old one dangling
// for the runtime's own prune, so one tag per repository is all that accumulates. Run is unique to
// one gate run: the image ID is read through it, so a concurrent build that moves Stable cannot
// hand this run another image, and the run removes it when it finishes (Image.ReleaseArgs).
type ImageRefs struct {
	Stable, Run string
}

// Image is a built devcontainer image, identified by the ID its runtime assigned.
type Image struct {
	Runtime Runtime
	// Ref is the image's lasting name: the stable tag of a build, the configured image of a pull.
	Ref string
	// RunRef is the per-run tag the run removes when it finishes; empty for a pulled image.
	RunRef string
	ID     string
	// Builder says what produced the image: the devcontainer CLI, a runtime build or a pull.
	Builder string
}

// RunOptions describes one command run in a disposable container of an image.
type RunOptions struct {
	// Name names the container, so one whose CLI was killed can still be removed.
	Name string
	// Mounts are host directories, each bind-mounted at its own path.
	Mounts []string
	// Env holds KEY=VALUE pairs set in the container.
	Env     []string
	Workdir string
	// UID and GID are the host identity the command runs as; negative on a host without one.
	UID, GID int
}

// unavailable wraps ErrImageUnavailable with the reason.
func unavailable(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrImageUnavailable, fmt.Sprintf(format, args...))
}

// PlanImage checks that repoDir's devcontainer can be built here, writing nothing and running only
// the runtime probe (FindRuntime). It refuses, with ErrImageUnavailable and the reason, a Windows
// host (a Linux container cannot mount a Windows path at its own path), a repository without
// ConfigPath, a host where neither docker nor podman is on PATH and answers, a configuration
// outside the managed schema, a recorded Praetor bootstrap whose companions fail verification or
// that is unavailable, build inputs outside the repository, and features without the devcontainer
// CLI to apply them.
func PlanImage(ctx context.Context, repoDir string, host Host) (*ImagePlan, error) {
	if host.GOOS == "windows" {
		return nil, unavailable("a Linux container cannot mount a Windows checkout at its own path")
	}
	root, err := util.ResolveExistingPath(ctx, repoDir)
	if err != nil {
		return nil, unavailable("the repository path cannot be resolved: %v", err)
	}
	path := filepath.Join(root, filepath.FromSlash(ConfigPath))
	if !util.FileExists(path) {
		return nil, unavailable("the repository has no %s", ConfigPath)
	}
	runtime, err := FindRuntime(ctx, root, host)
	if err != nil {
		return nil, err
	}
	dc, err := LoadDevContainer(ctx, path)
	if err != nil {
		return nil, unavailable("%s is not a configuration praetor builds: %v", ConfigPath, err)
	}
	plan := &ImagePlan{RepoDir: root, ConfigPath: path, Config: dc, Runtime: runtime}
	if err := plan.resolveBuild(); err != nil {
		return nil, err
	}
	if err := plan.resolveCLI(host.LookPath); err != nil {
		return nil, err
	}
	// Last, because it decodes the whole source archive: every cheaper refusal comes first.
	if err := verifyRecordedCompanions(ctx, path, dc); err != nil {
		return nil, unavailable("%s: %v", ConfigPath, err)
	}
	return plan, nil
}

// FindRuntime returns the first container runtime, in containerRuntimes order, that is on PATH and
// answers `<runtime> info`, run in dir under RuntimeProbeTimeout. A CLI on PATH is not a working
// runtime: its daemon may be stopped, its socket closed to this user, or its podman machine down,
// and a plan made with it would fail every build. A runtime that does not answer is passed over
// for the next, and when none answers the refusal names each one and why.
func FindRuntime(ctx context.Context, dir string, host Host) (Runtime, error) {
	if host.Run == nil {
		return Runtime{}, unavailable("no command runner was given to ask docker or podman whether it answers")
	}
	notes := make([]string, 0, len(containerRuntimes))
	onPath := 0
	for i := 0; i < len(containerRuntimes); i++ {
		name := containerRuntimes[i]
		path, err := host.LookPath(name)
		if err != nil {
			notes = append(notes, name+" is not on PATH")
			continue
		}
		onPath++
		if err := probeRuntime(ctx, host.Run, dir, path); err != nil {
			notes = append(notes, fmt.Sprintf("%s is on PATH but `%s info` failed: %s", name, name,
				util.TruncateExcerpt(err.Error(), probeExcerptBytes)))
			continue
		}
		return Runtime{Name: name, Path: path}, nil
	}
	if onPath == 0 {
		return Runtime{}, unavailable("neither docker nor podman is on PATH")
	}
	return Runtime{}, unavailable("no container runtime answers: %s", strings.Join(notes, "; "))
}

// probeRuntime asks the runtime at path for its system information under RuntimeProbeTimeout. It
// fails when the daemon or machine behind the CLI cannot be reached.
func probeRuntime(ctx context.Context, run CommandRunner, dir, path string) error {
	pCtx, cancel := context.WithTimeout(ctx, RuntimeProbeTimeout)
	defer cancel()
	_, err := run(pCtx, dir, path, "info")
	return err
}

// resolveBuild confines the configuration's build inputs to the repository. The dockerfile and
// context are relative to the configuration's directory, as the devcontainer specification reads
// them; the context defaults to that directory.
func (p *ImagePlan) resolveBuild() error {
	build := p.Config.Build
	if build == nil {
		if strings.TrimSpace(p.Config.Image) == "" {
			return unavailable("%s names neither an image nor a build", ConfigPath)
		}
		return nil
	}
	if strings.TrimSpace(build.Dockerfile) == "" {
		return unavailable("%s build names no dockerfile", ConfigPath)
	}
	configDir := filepath.Dir(filepath.FromSlash(ConfigPath))
	dockerfile, err := util.ConfinePath(p.RepoDir, filepath.Join(configDir, filepath.FromSlash(build.Dockerfile)))
	if err != nil {
		return unavailable("%s build.dockerfile %q is outside the repository: %v", ConfigPath, build.Dockerfile, err)
	}
	buildContext, err := util.ConfinePath(p.RepoDir, filepath.Join(configDir, filepath.FromSlash(build.Context)))
	if err != nil {
		return unavailable("%s build.context %q is outside the repository: %v", ConfigPath, build.Context, err)
	}
	p.dockerfile, p.context = dockerfile, buildContext
	return nil
}

// resolveCLI finds the devcontainer CLI when the configuration declares features. A runtime build
// of the Dockerfile would leave them out, and an image without its declared features is not the
// repository's devcontainer, so without the CLI there is no plan.
func (p *ImagePlan) resolveCLI(lookPath func(string) (string, error)) error {
	if len(p.Config.Features) == 0 {
		return nil
	}
	cli, err := lookPath(CLIName)
	if err != nil {
		return unavailable("%s declares features (%s), which only the devcontainer CLI applies, and %s is not on PATH",
			ConfigPath, strings.Join(featureRefs(p.Config.Features), ", "), CLIName)
	}
	p.CLI = cli
	return nil
}

// featureRefs lists the declared feature references in sorted order.
func featureRefs(features map[string]interface{}) []string {
	refs := make([]string, 0, len(features))
	for ref := range features {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs
}

// Build builds the planned image under ImageBuildTimeout, tagged with both refs, and returns it
// with the ID its runtime assigned, read through refs.Run: through the devcontainer CLI when the
// configuration declares features, else a runtime build of the Dockerfile, else a pull of the
// named image when it is not present, which is tagged with neither ref.
func (p *ImagePlan) Build(ctx context.Context, run CommandRunner, refs ImageRefs) (Image, error) {
	bCtx, cancel := context.WithTimeout(ctx, ImageBuildTimeout)
	defer cancel()
	img := Image{Runtime: p.Runtime, Ref: refs.Stable, RunRef: refs.Run}
	var err error
	switch {
	case p.CLI != "":
		img.Builder = CLIName + " build"
		_, err = run(bCtx, p.RepoDir, p.CLI, "build", "--workspace-folder", p.RepoDir, "--config", p.ConfigPath,
			"--image-name", refs.Stable, "--image-name", refs.Run, "--docker-path", p.Runtime.Path)
	case p.dockerfile != "":
		img.Builder = p.Runtime.Name + " build"
		_, err = run(bCtx, p.RepoDir, p.Runtime.Path, p.buildArgs(refs)...)
	default:
		img.Ref, img.RunRef, img.Builder = p.Config.Image, "", p.Runtime.Name+" pull"
		err = p.pullIfMissing(bCtx, run)
	}
	if err != nil {
		return Image{}, buildError(bCtx, img.Builder, err)
	}
	if img.ID, err = inspectID(bCtx, run, p.RepoDir, img); err != nil {
		return Image{}, err
	}
	return img, nil
}

// inspectID reads the ID of the image a build or pull produced: through its per-run tag when it
// has one, else its configured name.
func inspectID(ctx context.Context, run CommandRunner, dir string, img Image) (string, error) {
	ref := img.RunRef
	if ref == "" {
		ref = img.Ref
	}
	id, err := run(ctx, dir, img.Runtime.Path, "image", "inspect", "--format", "{{.Id}}", ref)
	if err != nil {
		return "", buildError(ctx, img.Runtime.Name+" image inspect", err)
	}
	return normalizeImageID(id)
}

// buildArgs is the runtime build command line: the Dockerfile, both tags, the build arguments in
// key order and the context.
func (p *ImagePlan) buildArgs(refs ImageRefs) []string {
	args := []string{"build", "--file", p.dockerfile, "--tag", refs.Stable, "--tag", refs.Run}
	keys := make([]string, 0, len(p.Config.Build.Args))
	for key := range p.Config.Build.Args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for i := 0; i < len(keys) && i < MaxLoopLimit; i++ {
		args = append(args, "--build-arg", keys[i]+"="+p.Config.Build.Args[keys[i]])
	}
	return append(args, p.context)
}

// pullIfMissing pulls an image-only configuration's image unless the runtime already holds it.
func (p *ImagePlan) pullIfMissing(ctx context.Context, run CommandRunner) error {
	if _, err := run(ctx, p.RepoDir, p.Runtime.Path, "image", "inspect", "--format", "{{.Id}}", p.Config.Image); err == nil {
		return nil
	}
	_, err := run(ctx, p.RepoDir, p.Runtime.Path, "pull", p.Config.Image)
	return err
}

// buildError names the step that failed, and the build bound when it is what stopped the step.
func buildError(ctx context.Context, step string, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%s did not finish within the %s image build bound: %w", step, ImageBuildTimeout, err)
	}
	return fmt.Errorf("%s failed: %w", step, err)
}

// normalizeImageID returns an image ID as sha256:<hex>. docker prints it with the prefix and
// podman without.
func normalizeImageID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if !strings.HasPrefix(id, "sha256:") {
		id = "sha256:" + id
	}
	if !imageID.MatchString(id) {
		return "", fmt.Errorf("the runtime reported image ID %q, not a sha256 digest", util.TruncateExcerpt(raw, 96))
	}
	return id, nil
}

// RunArgs is the runtime command line that runs name with args in a disposable container of the
// image, by its ID so the command runs in exactly the image that was inspected. docker runs it as
// the host UID:GID; rootless podman maps the host user into the container with keep-id, which
// --user alone does not. Either way files it writes into a mount stay the host user's.
func (img Image) RunArgs(opts RunOptions, name string, args ...string) []string {
	argv := []string{"run", "--rm", "--name", opts.Name}
	if opts.UID >= 0 && opts.GID >= 0 {
		if img.Runtime.Name == "podman" {
			argv = append(argv, "--userns=keep-id")
		} else {
			argv = append(argv, "--user", strconv.Itoa(opts.UID)+":"+strconv.Itoa(opts.GID))
		}
	}
	for i := 0; i < len(opts.Mounts) && i < MaxLoopLimit; i++ {
		argv = append(argv, "--mount", "type=bind,src="+opts.Mounts[i]+",dst="+opts.Mounts[i])
	}
	for i := 0; i < len(opts.Env) && i < MaxLoopLimit; i++ {
		argv = append(argv, "--env", opts.Env[i])
	}
	argv = append(argv, "--workdir", opts.Workdir, img.ID, name)
	return append(argv, args...)
}

// RemoveArgs is the runtime command line that removes a named container, running or not. Both
// runtimes exit zero when the container is already gone.
func (img Image) RemoveArgs(container string) []string {
	return []string{"rm", "--force", container}
}

// ReleaseArgs is the runtime command line that removes the image's per-run tag, nil for an image
// without one. While the stable tag still names the image this only untags it. Once a newer build
// has moved the stable tag the image itself goes, and --no-prune keeps its untagged parent layers,
// which podman's layer cache is made of, for the runtime's own prune to decide.
func (img Image) ReleaseArgs() []string {
	if img.RunRef == "" {
		return nil
	}
	return []string{"image", "rm", "--no-prune", img.RunRef}
}

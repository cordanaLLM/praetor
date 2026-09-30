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

// Host is what planning an image reads from the machine: its operating system and its PATH.
type Host struct {
	GOOS     string
	LookPath func(string) (string, error)
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

// Image is a built devcontainer image, identified by the ID its runtime assigned.
type Image struct {
	Runtime Runtime
	Ref     string
	ID      string
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

// PlanImage checks that repoDir's devcontainer can be built here, writing and running nothing. It
// refuses, with ErrImageUnavailable and the reason, a Windows host (a Linux container cannot mount
// a Windows path at its own path), a repository without ConfigPath, a host without docker or
// podman, a configuration outside the managed schema, a recorded Praetor bootstrap whose
// companions fail verification or that is unavailable, build inputs outside the repository, and
// features without the devcontainer CLI to apply them.
func PlanImage(ctx context.Context, repoDir string, host Host) (*ImagePlan, error) {
	if host.GOOS == "windows" {
		return nil, unavailable("a Linux container cannot mount a Windows checkout at its own path")
	}
	root, err := resolvedDir(repoDir)
	if err != nil {
		return nil, unavailable("the repository path cannot be resolved: %v", err)
	}
	path := filepath.Join(root, filepath.FromSlash(ConfigPath))
	if !util.FileExists(path) {
		return nil, unavailable("the repository has no %s", ConfigPath)
	}
	runtime, err := findRuntime(host.LookPath)
	if err != nil {
		return nil, err
	}
	dc, err := LoadDevContainer(ctx, path)
	if err != nil {
		return nil, unavailable("%s is not a configuration praetor builds: %v", ConfigPath, err)
	}
	if err := verifyRecordedCompanions(ctx, path, dc); err != nil {
		return nil, unavailable("%s: %v", ConfigPath, err)
	}
	plan := &ImagePlan{RepoDir: root, ConfigPath: path, Config: dc, Runtime: runtime}
	if err := plan.resolveBuild(); err != nil {
		return nil, err
	}
	if err := plan.resolveCLI(host.LookPath); err != nil {
		return nil, err
	}
	return plan, nil
}

// resolvedDir returns dir as an absolute path with its symbolic links resolved.
func resolvedDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// findRuntime returns the first container CLI on PATH, in containerRuntimes order.
func findRuntime(lookPath func(string) (string, error)) (Runtime, error) {
	for i := 0; i < len(containerRuntimes); i++ {
		if path, err := lookPath(containerRuntimes[i]); err == nil {
			return Runtime{Name: containerRuntimes[i], Path: path}, nil
		}
	}
	return Runtime{}, unavailable("neither docker nor podman is on PATH")
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

// Build builds the planned image under ImageBuildTimeout, tagged ref, and returns it with the ID
// its runtime assigned: through the devcontainer CLI when the configuration declares features,
// else a runtime build of the Dockerfile, else a pull of the named image when it is not present.
func (p *ImagePlan) Build(ctx context.Context, run CommandRunner, ref string) (Image, error) {
	bCtx, cancel := context.WithTimeout(ctx, ImageBuildTimeout)
	defer cancel()
	img := Image{Runtime: p.Runtime, Ref: ref}
	var err error
	switch {
	case p.CLI != "":
		img.Builder = CLIName + " build"
		_, err = run(bCtx, p.RepoDir, p.CLI, "build", "--workspace-folder", p.RepoDir,
			"--config", p.ConfigPath, "--image-name", ref, "--docker-path", p.Runtime.Path)
	case p.dockerfile != "":
		img.Builder = p.Runtime.Name + " build"
		_, err = run(bCtx, p.RepoDir, p.Runtime.Path, p.buildArgs(ref)...)
	default:
		img.Ref, img.Builder = p.Config.Image, p.Runtime.Name+" pull"
		err = p.pullIfMissing(bCtx, run)
	}
	if err != nil {
		return Image{}, buildError(bCtx, img.Builder, err)
	}
	id, err := run(bCtx, p.RepoDir, p.Runtime.Path, "image", "inspect", "--format", "{{.Id}}", img.Ref)
	if err != nil {
		return Image{}, buildError(bCtx, p.Runtime.Name+" image inspect", err)
	}
	if img.ID, err = normalizeImageID(id); err != nil {
		return Image{}, err
	}
	return img, nil
}

// buildArgs is the runtime build command line: the Dockerfile, the tag, the build arguments in
// key order and the context.
func (p *ImagePlan) buildArgs(ref string) []string {
	args := []string{"build", "--file", p.dockerfile, "--tag", ref}
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

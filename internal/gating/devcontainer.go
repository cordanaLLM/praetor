// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cordanaLLM/praetor/internal/devcontainer"
	"github.com/cordanaLLM/praetor/internal/lockdown"
	"github.com/cordanaLLM/praetor/internal/topology"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// DevcontainerEnv selects where the Go toolchain stages run. Unset or "auto" runs them in the
	// repository's devcontainer when this host can build it; "off" runs them on the host. Any other
	// value runs them on the host too, and the recorded reason names the value.
	DevcontainerEnv = "PRAETOR_GATE_DEVCONTAINER"
	// DevcontainerStage names the stage a run records when the devcontainer it planned could not be
	// built or entered. The run is rejected: a planned devcontainer that fails is reported, never
	// replaced by the host.
	DevcontainerStage = "Devcontainer Image"
	// devcontainerHomeRel is the container's HOME below the user's cache directory
	// (os.UserCacheDir): outside every checkout, so Go's build and module caches outlive the run
	// and the checkout. The pre-push hook gates a fresh clone in a temporary directory, so a HOME
	// kept below the checkout or its git common dir would start empty on every push and be deleted
	// after it. One HOME serves every repository, as the host's own Go caches do: both caches are
	// content-addressed and safe for concurrent builds.
	devcontainerHomeRel = "praetor/devcontainer-home"
	// devcontainerToolsRel is the tool cache below the user's cache directory: the pinned Node and
	// devcontainer CLI a configuration with features is built with (devcontainer.PinnedCLI). Like
	// the HOME it outlives every checkout, so only the first build on a machine fetches them.
	devcontainerToolsRel = "praetor/devcontainer-cli"
	// devcontainerPlanTimeout bounds planning: the git and file reads, plus one probe per runtime.
	devcontainerPlanTimeout = GitQueryTimeout + devcontainer.RuntimeProbeBudget
	// localImageKey keys the stable image tag of a repository without an origin remote. All such
	// repositories share one tag, so a clone without an origin cannot add a tag per push.
	localImageKey = "local"
	// devcontainerProbe asks a container's shell for a command's path. It exits zero either way,
	// so an empty answer means absent and a failure means the container could not run the shell.
	devcontainerProbe = `command -v "$1" || true`

	executionHost         = "host"
	executionDevcontainer = "devcontainer"
)

// devcontainerStages are the stages whose Go part runs in the devcontainer. The HISS scan, the
// security scanners, the flavor audit, the Cargo parts and the receipt still run on the host.
var devcontainerStages = []string{stagePrefetch, stageTests}

// forwardedEnv are the host variables that decide how the container's go commands reach modules:
// the module proxy and checksum database settings, the private-module patterns, the toolchain
// selection and the HTTP proxies in both spellings. The container starts from the image's
// environment, so without them a host that needs a proxy or private modules would pass the host
// gate and fail the container's prefetch. GOFLAGS is forwarded too, joined with the gate's own
// flag (containerGoFlags). GONOSUMCHECK is read by no current go command (Go 1.27's cmd/go has no
// such variable) and is carried for wrappers that still honour it. Credentials are not forwarded:
// HOME is the gate's, so ~/.netrc, git credential helpers and SSH keys do not reach the container.
var forwardedEnv = [...]string{
	"GOPROXY", "GOPRIVATE", "GONOPROXY", "GONOSUMDB", "GONOSUMCHECK", "GOSUMDB", "GOINSECURE", "GOTOOLCHAIN",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
}

// errNotOnPath reports a command a toolchain does not have on its PATH.
var errNotOnPath = errors.New("not on PATH")

// containerSeq keeps the names of containers started within one nanosecond apart.
var containerSeq atomic.Uint64

// Execution records where a run's toolchain stages ran. Environment is "devcontainer", with the
// runtime, the image ID, what built it and the stages that ran in it, or "host", with the reason.
// The receipt certifies it as the execution header line (lockdown.ExecutionLine). Cleanup is
// outside that line: it says what the run could not remove after its stages, such as its per-run
// image tag, and is reported, not signed.
type Execution struct {
	Environment string   `json:"environment"`
	Runtime     string   `json:"runtime,omitempty"`
	Image       string   `json:"image,omitempty"`
	ImageID     string   `json:"image_id,omitempty"`
	Builder     string   `json:"builder,omitempty"`
	Stages      []string `json:"stages,omitempty"`
	Reason      string   `json:"reason,omitempty"`
	Cleanup     string   `json:"cleanup,omitempty"`
}

// Line renders the execution header line of the signed gate output.
func (e *Execution) Line() string {
	if e.Environment == executionDevcontainer {
		return lockdown.ExecutionLine(executionDevcontainer, "runtime="+e.Runtime, "image="+e.ImageID,
			"builder="+e.Builder, "stages="+strings.Join(e.Stages, ", ")+" (go)")
	}
	return lockdown.ExecutionLine(executionHost, "reason="+e.Reason)
}

// String is the report form of the execution: where the stages ran, the image or the reason, and
// what the run could not clean up.
func (e *Execution) String() string {
	where := "host: " + e.Reason
	if e.Environment == executionDevcontainer {
		where = fmt.Sprintf("devcontainer %s (%s, %s) for the Go part of %s", e.ImageID, e.Runtime, e.Builder,
			strings.Join(e.Stages, ", "))
	}
	if e.Cleanup != "" {
		return where + "; cleanup: " + e.Cleanup
	}
	return where
}

// hostExecution records a run whose toolchain stages run on the host, and why.
func hostExecution(reason string) *Execution {
	return &Execution{Environment: executionHost, Reason: reason}
}

// hostMachine is the real machine a plan is made for.
func hostMachine() devcontainer.Host {
	return devcontainer.Host{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, LookPath: exec.LookPath, Run: util.RunCommand,
		Tools: devcontainer.ToolCache{Fetch: devcontainer.HTTPFetch}}
}

// planDevcontainer decides, building nothing, whether the run's Go toolchain stages run in the
// repository's devcontainer, and otherwise why they run on the host: the operator opted out, a dry
// run builds nothing, the repository has no go.mod, the user has no cache directory to keep the
// container's HOME and the pinned devcontainer CLI in (praetorCacheDir), or devcontainer.PlanImage
// refused the host or the configuration. EnvRunBudget reserves the image build exactly when this
// returns a plan.
func planDevcontainer(ctx context.Context, repoDir string, dryRun bool, host devcontainer.Host) (*devcontainer.ImagePlan, string) {
	switch mode := strings.TrimSpace(os.Getenv(DevcontainerEnv)); mode {
	case "", "auto":
	case "off":
		return nil, DevcontainerEnv + "=off"
	default:
		return nil, fmt.Sprintf("%s=%q is neither auto nor off", DevcontainerEnv, mode)
	}
	if dryRun {
		return nil, "dry run: no devcontainer image is built and no toolchain stage runs"
	}
	if !util.FileExists(filepath.Join(repoDir, "go.mod")) {
		return nil, "no go.mod: the devcontainer runs the Go toolchain stages only"
	}
	tools, err := praetorCacheDir(devcontainerToolsRel)
	if err != nil {
		return nil, fmt.Sprintf("no user cache directory to keep the devcontainer's HOME and tools in: %v", err)
	}
	host.Tools.Dir = tools
	pCtx, cancel := context.WithTimeout(ctx, devcontainerPlanTimeout)
	defer cancel()
	plan, err := devcontainer.PlanImage(pCtx, repoDir, host)
	if err != nil {
		return nil, err.Error()
	}
	return plan, ""
}

// resolveExecution decides where the run's Go toolchain stages run, before any stage does. Without
// a plan they run on the host and the report records why. With one the image is built and must
// carry go; an image without go is recorded as the reason they run on the host. A plan whose build
// or setup fails rejects the run through a failed DevcontainerStage, with the opt-out named. A run
// that entered the devcontainer releases its image tag through releaseDevcontainer.
func resolveExecution(ctx context.Context, cfg *stageConfig) error {
	host := devcontainer.Host{GOOS: cfg.goos, GOARCH: cfg.goarch, LookPath: cfg.lookPath, Run: devcontainer.CommandRunner(cfg.run),
		Tools: devcontainer.ToolCache{Fetch: cfg.fetch}}
	plan, reason := planDevcontainer(ctx, cfg.repoDir, cfg.dryRun, host)
	if plan == nil {
		cfg.rep.Execution = hostExecution(reason)
		return nil
	}
	start := time.Now()
	entered, cleanup, err := enterDevcontainer(ctx, cfg, plan)
	if err != nil {
		err = fmt.Errorf("the devcontainer that %s declares could not be used: %w; fix it, or set %s=off to "+
			"run the toolchain stages on the host", devcontainer.ConfigPath, err, DevcontainerEnv)
		cfg.rep.Stages = append(cfg.rep.Stages, StageResult{Name: DevcontainerStage, Status: StageFailed,
			Duration: time.Since(start), Message: err.Error()})
		return err
	}
	if entered == nil {
		cfg.rep.Execution = hostExecution(fmt.Sprintf("the devcontainer image built from %s has no go on its PATH",
			devcontainer.ConfigPath))
		cfg.rep.Execution.Cleanup = cleanup
		return nil
	}
	cfg.container = entered
	cfg.rep.Execution = &Execution{Environment: executionDevcontainer, Runtime: entered.image.Runtime.Name,
		Image: entered.image.Ref, ImageID: entered.image.ID, Builder: entered.image.Builder, Stages: devcontainerStages}
	return nil
}

// enterDevcontainer builds the planned image and prepares how commands run in it. It returns nil
// and no error when the image has no go on its PATH. An image it built but did not enter has its
// per-run tag released here: a failure to do so joins the error, or, with no error, is returned
// as the cleanup note.
func enterDevcontainer(ctx context.Context, cfg *stageConfig, plan *devcontainer.ImagePlan) (entered *devcontainerExec, cleanup string, err error) {
	img, err := plan.Build(ctx, devcontainer.CommandRunner(cfg.run), imageRefs(ctx, plan.RepoDir))
	if err != nil {
		return nil, "", err
	}
	defer func() {
		if entered != nil {
			return
		}
		if relErr := releaseImage(ctx, cfg.run, plan.RepoDir, img); relErr != nil && err != nil {
			err = errors.Join(err, relErr)
		} else if relErr != nil {
			cleanup = relErr.Error()
		}
	}()
	dc, err := newDevcontainerExec(ctx, plan.RepoDir, img)
	if err != nil {
		return nil, "", err
	}
	probeCtx, cancel := context.WithTimeout(ctx, GitQueryTimeout)
	defer cancel()
	err = dc.toolchain(cfg.run).have(probeCtx, plan.RepoDir, "go")
	if errors.Is(err, errNotOnPath) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("probing image %s for go failed: %w", img.ID, err)
	}
	return dc, "", nil
}

// imageRefs names the image a run builds. The stable tag is keyed by the repository's origin
// remote (host and path), so every clone and worktree of one repository, the pre-push hook's
// temporary clone included, reuses one tag instead of adding one per checkout; a repository
// without an origin shares localImageKey. The per-run tag adds the process and the time.
func imageRefs(ctx context.Context, repoDir string) devcontainer.ImageRefs {
	key := localImageKey
	gitCtx, cancel := context.WithTimeout(ctx, GitQueryTimeout)
	defer cancel()
	if remote, err := util.ReadOriginRemote(gitCtx, repoDir); err == nil {
		key = remote.Host + "/" + remote.Path
	}
	sum := sha256.Sum256([]byte(key))
	name := "praetor-gate-" + hex.EncodeToString(sum[:6])
	return devcontainer.ImageRefs{
		Stable: name + ":latest",
		Run:    fmt.Sprintf("%s:run-%d-%d-%d", name, os.Getpid(), time.Now().UnixNano(), containerSeq.Add(1)),
	}
}

// releaseImage removes the image's per-run tag under CleanupTimeout, which runs on even when the
// run's own context has ended. An image without one, a pull, needs nothing.
func releaseImage(ctx context.Context, run commandRunner, dir string, img devcontainer.Image) error {
	args := img.ReleaseArgs()
	if args == nil {
		return nil
	}
	rmCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), CleanupTimeout)
	defer cancel()
	if _, err := run(rmCtx, dir, img.Runtime.Path, args...); err != nil {
		return fmt.Errorf("could not remove the per-run image tag %s: %w", img.RunRef, err)
	}
	return nil
}

// releaseDevcontainer removes the per-run tag of the image a run entered, once its stages are
// done, and records a failure as the execution's cleanup note. A run on the host has none.
func releaseDevcontainer(ctx context.Context, cfg *stageConfig) {
	if cfg.container == nil {
		return
	}
	if err := releaseImage(ctx, cfg.run, cfg.repoDir, cfg.container.image); err != nil && cfg.rep.Execution != nil {
		cfg.rep.Execution.Cleanup = err.Error()
	}
}

// devcontainerExec is how a run's commands reach its devcontainer: the image, the host directories
// mounted at their own paths (the checkout and its git common dir), the environment, and the host
// user they run as.
type devcontainerExec struct {
	image    devcontainer.Image
	mounts   []string
	env      []string
	uid, gid int
}

// newDevcontainerExec mounts the checkout and, when it lies elsewhere, its git common dir, each at
// its own path, so the stage worktrees' gitdir links resolve inside the container as they do on
// the host. HOME is the persistent devcontainerHome, mounted at its own path too; the environment
// is containerEnv's.
func newDevcontainerExec(ctx context.Context, repoDir string, img devcontainer.Image) (*devcontainerExec, error) {
	gitCtx, cancel := context.WithTimeout(ctx, GitQueryTimeout)
	defer cancel()
	common, err := topology.GitCommonDir(gitCtx, repoDir)
	if err != nil {
		return nil, fmt.Errorf("resolve the git common dir to mount: %w", err)
	}
	if common, err = util.ResolveExistingPath(gitCtx, common); err != nil {
		return nil, fmt.Errorf("resolve the git common dir to mount: %w", err)
	}
	home, err := devcontainerHome(gitCtx)
	if err != nil {
		return nil, err
	}
	mounts := []string{repoDir}
	outside := [...]string{common, home}
	for i := 0; i < len(outside); i++ {
		if !util.WithinRoot(repoDir, outside[i]) {
			mounts = append(mounts, outside[i])
		}
	}
	for i := 0; i < len(mounts); i++ {
		if strings.ContainsAny(mounts[i], ",\n\r") {
			return nil, fmt.Errorf("%q cannot be bind-mounted: a comma or line break splits the mount option", mounts[i])
		}
	}
	return &devcontainerExec{image: img, mounts: mounts, uid: os.Getuid(), gid: os.Getgid(), env: containerEnv(home, os.LookupEnv)}, nil
}

// containerEnv is the environment the container's commands get beyond the image's own: HOME, with
// GOPATH below it, overriding an image's shared /go; GOFLAGS (containerGoFlags); the gate off, so a
// gate a test starts inside never looks for a runtime there; and each forwardedEnv variable the
// host sets, read through lookup, even when set empty, since an empty GOPROXY or NO_PROXY is a
// setting too.
func containerEnv(home string, lookup func(string) (string, bool)) []string {
	env := make([]string, 0, 4+len(forwardedEnv))
	env = append(env, "HOME="+home, "GOPATH="+filepath.Join(home, "go"), "GOFLAGS="+containerGoFlags(lookup), DevcontainerEnv+"=off")
	for i := 0; i < len(forwardedEnv); i++ {
		if value, ok := lookup(forwardedEnv[i]); ok {
			env = append(env, forwardedEnv[i]+"="+value)
		}
	}
	return env
}

// containerGoFlags is the host's GOFLAGS, build tags and all, followed by -modcacherw, which leaves
// the module cache in HOME writable so it can be deleted without first restoring write permission.
func containerGoFlags(lookup func(string) (string, bool)) string {
	host, _ := lookup("GOFLAGS")
	return strings.TrimSpace(host + " -modcacherw")
}

// praetorCacheDir is rel below the user's cache directory (os.UserCacheDir), confined to it. It
// creates nothing.
func praetorCacheDir(rel string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("no user cache directory: %w", err)
	}
	return util.ConfinePath(cache, filepath.FromSlash(rel))
}

// devcontainerHome creates the container's HOME below the user's cache directory, on the host so
// it belongs to the host user, and returns its resolved path, which is where it is mounted.
func devcontainerHome(ctx context.Context) (string, error) {
	home, err := praetorCacheDir(devcontainerHomeRel)
	if err != nil {
		return "", fmt.Errorf("place the devcontainer home: %w", err)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", fmt.Errorf("create the devcontainer home %s: %w", home, err)
	}
	if home, err = util.ResolveExistingPath(ctx, home); err != nil {
		return "", fmt.Errorf("resolve the devcontainer home to mount: %w", err)
	}
	return home, nil
}

// toolchain is a Go toolchain whose commands run in the devcontainer, started through base.
func (d *devcontainerExec) toolchain(base commandRunner) toolchain {
	run := d.runner(base)
	return toolchain{
		run: run,
		have: func(ctx context.Context, dir, name string) error {
			out, err := run(ctx, dir, "sh", "-c", devcontainerProbe, "sh", name)
			if err != nil {
				return err
			}
			if strings.TrimSpace(out) == "" {
				return errNotOnPath
			}
			return nil
		},
		where: " in the devcontainer",
	}
}

// runner runs each command in a disposable container of the image, in dir, which must lie inside
// a mounted directory. A container whose command failed is removed by name: a killed runtime CLI
// does not stop the container its daemon runs, and --rm removes only a container that exited.
func (d *devcontainerExec) runner(base commandRunner) commandRunner {
	return func(ctx context.Context, dir, name string, args ...string) (string, error) {
		workdir, err := d.workdir(ctx, dir)
		if err != nil {
			return "", err
		}
		container := fmt.Sprintf("praetor-gate-%d-%d-%d", os.Getpid(), time.Now().UnixNano(), containerSeq.Add(1))
		argv := d.image.RunArgs(devcontainer.RunOptions{Name: container, Mounts: d.mounts, Env: d.env,
			Workdir: workdir, UID: d.uid, GID: d.gid}, name, args...)
		out, err := base(ctx, workdir, d.image.Runtime.Path, argv...)
		if err == nil {
			return out, nil
		}
		if rmErr := d.remove(ctx, base, workdir, container); rmErr != nil {
			err = errors.Join(err, rmErr)
		}
		return out, err
	}
}

// workdir resolves dir to the path the container sees it at, which is its own resolved path, and
// refuses one outside every mount.
func (d *devcontainerExec) workdir(ctx context.Context, dir string) (string, error) {
	abs, err := util.ResolveExistingPath(ctx, dir)
	if err != nil {
		return "", fmt.Errorf("resolve %s for the devcontainer: %w", dir, err)
	}
	for i := 0; i < len(d.mounts); i++ {
		if util.WithinRoot(d.mounts[i], abs) {
			return abs, nil
		}
	}
	return "", fmt.Errorf("%s is outside the directories the devcontainer mounts (%s)", abs, strings.Join(d.mounts, ", "))
}

// remove removes a named container under a cleanup bound of its own, since the stage context is
// typically what ended the command.
func (d *devcontainerExec) remove(ctx context.Context, base commandRunner, dir, container string) error {
	rmCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), CleanupTimeout)
	defer cancel()
	if _, err := base(rmCtx, dir, d.image.Runtime.Path, d.image.RemoveArgs(container)...); err != nil {
		return fmt.Errorf("leaked devcontainer %s: %w", container, err)
	}
	return nil
}

// toolchain is where a Go toolchain stage runs its commands: on the host, or in the devcontainer.
type toolchain struct {
	run commandRunner
	// have reports errNotOnPath when name is not on the toolchain's PATH, asked from dir, and any
	// other error when the toolchain could not be asked.
	have func(ctx context.Context, dir, name string) error
	// where is empty on the host and names the devcontainer otherwise, for messages.
	where string
}

// unanswered is a race-detector probe the toolchain did not answer. On the host it is the reason
// the race tests are skipped, as it always was. In the devcontainer it is an error: the container
// failed a command, and the stage fails with it rather than skipping.
func (tc toolchain) unanswered(what string, err error) (bool, string, error) {
	if tc.where == "" {
		return false, fmt.Sprintf("%s: %v", what, err), nil
	}
	return false, "", fmt.Errorf("%s%s: %w", what, tc.where, err)
}

// goToolchain returns where the run's Go toolchain stages run: the devcontainer the run entered, or
// the host.
func (c *stageConfig) goToolchain() toolchain {
	if c.container != nil {
		return c.container.toolchain(c.run)
	}
	return toolchain{
		run: c.run,
		have: func(_ context.Context, _, name string) error {
			if _, err := c.lookPath(name); err != nil {
				return fmt.Errorf("%w: %w", errNotOnPath, err)
			}
			return nil
		},
	}
}

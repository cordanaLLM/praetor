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
	return devcontainer.Host{GOOS: runtime.GOOS, LookPath: exec.LookPath, Run: util.RunCommand}
}

// planDevcontainer decides, building nothing, whether the run's Go toolchain stages run in the
// repository's devcontainer, and otherwise why they run on the host: the operator opted out, a dry
// run builds nothing, the repository has no go.mod, the user has no cache directory to keep the
// container's HOME in (devcontainerHome), or devcontainer.PlanImage refused the host or the
// configuration. EnvRunBudget reserves the image build exactly when this returns a plan.
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
	if _, err := os.UserCacheDir(); err != nil {
		return nil, fmt.Sprintf("no user cache directory to keep the devcontainer's HOME in: %v", err)
	}
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
	host := devcontainer.Host{GOOS: cfg.goos, LookPath: cfg.lookPath, Run: devcontainer.CommandRunner(cfg.run)}
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
// the host. HOME is the persistent devcontainerHome, mounted at its own path too. GOPATH follows
// it, overriding an image's shared /go, and GOFLAGS leaves the module cache writable so the cache
// can be deleted without first restoring write permission. The container runs the gate off, so a
// gate a test starts inside never looks for a runtime there.
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
	return &devcontainerExec{image: img, mounts: mounts, uid: os.Getuid(), gid: os.Getgid(), env: []string{
		"HOME=" + home, "GOPATH=" + filepath.Join(home, "go"), "GOFLAGS=-modcacherw", DevcontainerEnv + "=off",
	}}, nil
}

// devcontainerHome creates the container's HOME below the user's cache directory, on the host so
// it belongs to the host user, and returns its resolved path, which is where it is mounted.
func devcontainerHome(ctx context.Context) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("place the devcontainer home: no user cache directory: %w", err)
	}
	home, err := util.ConfinePath(cache, filepath.FromSlash(devcontainerHomeRel))
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

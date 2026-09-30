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
	// devcontainerHomeRel is the container's HOME below the git common dir, where the cargo stage
	// keeps its target directory too: outside every working tree, shared by the clone's worktrees,
	// so Go's build and module caches outlive the run.
	devcontainerHomeRel = "praetor/devcontainer-home"
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
// The receipt certifies it as the execution header line (lockdown.ExecutionLine).
type Execution struct {
	Environment string   `json:"environment"`
	Runtime     string   `json:"runtime,omitempty"`
	Image       string   `json:"image,omitempty"`
	ImageID     string   `json:"image_id,omitempty"`
	Builder     string   `json:"builder,omitempty"`
	Stages      []string `json:"stages,omitempty"`
	Reason      string   `json:"reason,omitempty"`
}

// Line renders the execution header line of the signed gate output.
func (e *Execution) Line() string {
	if e.Environment == executionDevcontainer {
		return lockdown.ExecutionLine(executionDevcontainer, "runtime="+e.Runtime, "image="+e.ImageID,
			"builder="+e.Builder, "stages="+strings.Join(e.Stages, ", ")+" (go)")
	}
	return lockdown.ExecutionLine(executionHost, "reason="+e.Reason)
}

// String is the report form of the execution: where the stages ran, and the image or the reason.
func (e *Execution) String() string {
	if e.Environment == executionDevcontainer {
		return fmt.Sprintf("devcontainer %s (%s, %s) for the Go part of %s", e.ImageID, e.Runtime, e.Builder,
			strings.Join(e.Stages, ", "))
	}
	return "host: " + e.Reason
}

// hostExecution records a run whose toolchain stages run on the host, and why.
func hostExecution(reason string) *Execution {
	return &Execution{Environment: executionHost, Reason: reason}
}

// hostMachine is the real machine a plan is made for.
func hostMachine() devcontainer.Host {
	return devcontainer.Host{GOOS: runtime.GOOS, LookPath: exec.LookPath}
}

// planDevcontainer decides, building nothing, whether the run's Go toolchain stages run in the
// repository's devcontainer, and otherwise why they run on the host: the operator opted out, a dry
// run builds nothing, the repository has no go.mod, or devcontainer.PlanImage refused the host or
// the configuration. EnvRunBudget reserves the image build exactly when this returns a plan.
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
	pCtx, cancel := context.WithTimeout(ctx, GitQueryTimeout)
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
// or setup fails rejects the run through a failed DevcontainerStage, with the opt-out named.
func resolveExecution(ctx context.Context, cfg *stageConfig) error {
	plan, reason := planDevcontainer(ctx, cfg.repoDir, cfg.dryRun, devcontainer.Host{GOOS: cfg.goos, LookPath: cfg.lookPath})
	if plan == nil {
		cfg.rep.Execution = hostExecution(reason)
		return nil
	}
	start := time.Now()
	entered, err := enterDevcontainer(ctx, cfg, plan)
	if err != nil {
		err = fmt.Errorf("the devcontainer %s declares could not be used: %w; fix it, or set %s=off to run "+
			"the toolchain stages on the host", devcontainer.ConfigPath, err, DevcontainerEnv)
		cfg.rep.Stages = append(cfg.rep.Stages, StageResult{Name: DevcontainerStage, Status: StageFailed,
			Duration: time.Since(start), Message: err.Error()})
		return err
	}
	if entered == nil {
		cfg.rep.Execution = hostExecution(fmt.Sprintf("the devcontainer image built from %s has no go on its PATH",
			devcontainer.ConfigPath))
		return nil
	}
	cfg.container = entered
	cfg.rep.Execution = &Execution{Environment: executionDevcontainer, Runtime: entered.image.Runtime.Name,
		Image: entered.image.Ref, ImageID: entered.image.ID, Builder: entered.image.Builder, Stages: devcontainerStages}
	return nil
}

// enterDevcontainer builds the planned image and prepares how commands run in it. It returns nil
// and no error when the image has no go on its PATH.
func enterDevcontainer(ctx context.Context, cfg *stageConfig, plan *devcontainer.ImagePlan) (*devcontainerExec, error) {
	img, err := plan.Build(ctx, devcontainer.CommandRunner(cfg.run), imageRef(plan.RepoDir))
	if err != nil {
		return nil, err
	}
	entered, err := newDevcontainerExec(ctx, plan.RepoDir, img)
	if err != nil {
		return nil, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, GitQueryTimeout)
	defer cancel()
	err = entered.toolchain(cfg.run).have(probeCtx, plan.RepoDir, "go")
	if errors.Is(err, errNotOnPath) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("probing image %s for go failed: %w", img.ID, err)
	}
	return entered, nil
}

// imageRef tags a repository's gate image by its resolved path, so two clones never overwrite each
// other's tag. The image the stages run in is named by its ID, not by this tag.
func imageRef(repoDir string) string {
	sum := sha256.Sum256([]byte(repoDir))
	return "praetor-gate-" + hex.EncodeToString(sum[:6]) + ":latest"
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
// the host. HOME is a persistent directory below the git common dir, created on the host so it
// belongs to the host user. GOPATH follows it, overriding an image's shared /go, and GOFLAGS
// leaves the module cache writable so removing the clone removes it. The container runs the gate
// off, so a gate a test starts inside never looks for a runtime there.
func newDevcontainerExec(ctx context.Context, repoDir string, img devcontainer.Image) (*devcontainerExec, error) {
	gitCtx, cancel := context.WithTimeout(ctx, GitQueryTimeout)
	defer cancel()
	common, err := topology.GitCommonDir(gitCtx, repoDir)
	if err != nil {
		return nil, fmt.Errorf("resolve the git common dir to mount: %w", err)
	}
	if common, err = filepath.EvalSymlinks(common); err != nil {
		return nil, fmt.Errorf("resolve the git common dir to mount: %w", err)
	}
	home, err := util.ConfinePath(common, filepath.FromSlash(devcontainerHomeRel))
	if err != nil {
		return nil, fmt.Errorf("place the devcontainer home: %w", err)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, fmt.Errorf("create the devcontainer home %s: %w", home, err)
	}
	mounts := []string{repoDir}
	if !util.WithinRoot(repoDir, common) {
		mounts = append(mounts, common)
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
		workdir, err := d.workdir(dir)
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
func (d *devcontainerExec) workdir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve %s for the devcontainer: %w", dir, err)
	}
	if abs, err = filepath.EvalSymlinks(abs); err != nil {
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

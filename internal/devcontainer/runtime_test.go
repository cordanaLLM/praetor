// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package devcontainer

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// onPath answers a PATH lookup for exactly the named commands.
func onPath(names ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		if slices.Contains(names, name) {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New(name + " not found")
	}
}

// answers is a runner whose every command succeeds, as a runtime whose daemon answers.
func answers(context.Context, string, string, ...string) (string, error) { return "", nil }

// noFetch is a fetcher for plans that must fetch nothing.
func noFetch(context.Context, string, io.Writer) error {
	return errors.New("this test fetches nothing")
}

// hostWith is a host on goos/amd64 with exactly the named commands on PATH, each runtime
// answering, and a tool cache that fetches nothing.
func hostWith(goos string, names ...string) Host {
	return Host{GOOS: goos, GOARCH: "amd64", LookPath: onPath(names...), Run: answers,
		Tools: ToolCache{Dir: "/nonexistent/praetor-tools", Fetch: noFetch}}
}

// writeConfigRepo writes a repository holding config as its devcontainer.json, plus each extra
// file relative to the configuration's directory, and returns the repository's resolved path.
func writeConfigRepo(t *testing.T, config string, extra map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{"devcontainer.json": config}
	for name, body := range extra {
		files[name] = body
	}
	for name, body := range files {
		path := filepath.Join(root, ".devcontainer", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

const dockerfileConfig = `{"name": "fixture", "build": {"dockerfile": "Dockerfile", "context": ".", "args": {"B": "2", "A": "1"}}}`

var linux = hostWith("linux", "docker", "podman")

// Positive: a Dockerfile configuration without features plans a runtime build of confined inputs,
// with docker preferred over podman, and plans no devcontainer CLI.
func TestPlanImagePlansARuntimeBuild(t *testing.T) {
	root := writeConfigRepo(t, dockerfileConfig, map[string]string{"Dockerfile": "FROM scratch\n"})
	plan, err := PlanImage(t.Context(), root, linux)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.Runtime.Name != "docker" || plan.CLI != nil || plan.RepoDir != root {
		t.Errorf("plan = %+v, want docker, no CLI, repo %s", plan, root)
	}
	if want := filepath.Join(root, ".devcontainer", "Dockerfile"); plan.dockerfile != want {
		t.Errorf("dockerfile = %s, want %s", plan.dockerfile, want)
	}
	if want := filepath.Join(root, ".devcontainer"); plan.context != want {
		t.Errorf("context = %s, want %s", plan.context, want)
	}
	podman, err := PlanImage(t.Context(), root, hostWith("darwin", "podman"))
	if err != nil || podman.Runtime.Name != "podman" {
		t.Errorf("a host with podman alone must plan with it: %+v %v", podman, err)
	}
}

// Negative: every host or configuration the gate cannot build from is refused with
// ErrImageUnavailable and a reason naming the cause.
func TestPlanImageRefusesWithAReason(t *testing.T) {
	built := writeConfigRepo(t, dockerfileConfig, nil)
	withFeatures := writeConfigRepo(t, `{"name": "x", "image": "a@sha256:`+strings.Repeat("0", 64)+
		`", "features": {"ghcr.io/devcontainers/features/node:1": {}, "ghcr.io/devcontainers/features/go:1": {}}}`, nil)
	riscv := hostWith("linux", "docker")
	riscv.GOARCH = "riscv64"
	noCache := hostWith("linux", "docker")
	noCache.Tools.Dir = ""
	cases := []struct {
		name, root string
		host       Host
		want       string
	}{
		{"windows", built, hostWith("windows", "docker", "podman"), "Windows"},
		{"no config", t.TempDir(), linux, "has no " + ConfigPath},
		{"no runtime", built, hostWith("linux"), "neither docker nor podman"},
		{"unmanaged key", writeConfigRepo(t, `{"name": "x", "image": "a@sha256:`+strings.Repeat("0", 64)+`", "runArgs": []}`, nil), linux, "runArgs"},
		{"neither image nor build", writeConfigRepo(t, `{"name": "x"}`, nil), linux, "neither an image nor a build"},
		{"dockerfile escapes", writeConfigRepo(t, `{"name": "x", "build": {"dockerfile": "../../outside", "context": "."}}`, nil), linux, "outside the repository"},
		{"context escapes", writeConfigRepo(t, `{"name": "x", "build": {"dockerfile": "Dockerfile", "context": "../.."}}`, nil), linux, "build.context"},
		{"features on a platform without a pinned Node", withFeatures, riscv, "(ghcr.io/devcontainers/features/go:1, ghcr.io/devcontainers/features/node:1), " +
			"which only the devcontainer CLI applies, and the gate cannot run its pinned CLI here: the gate pins Node for " +
			"darwin-arm64, darwin-x64, linux-arm64, linux-x64 only, not linux/riscv64"},
		{"features without a tool cache", withFeatures, noCache, "no tool cache"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := PlanImage(t.Context(), tc.root, tc.host)
			if !errors.Is(err, ErrImageUnavailable) || plan != nil {
				t.Fatalf("PlanImage = %+v, %v; want ErrImageUnavailable", plan, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("reason %q does not mention %q", err, tc.want)
			}
		})
	}
}

// Boundary: a recorded Praetor bootstrap declares features, so it plans the pinned devcontainer
// CLI, and its companions are verified first: one edited byte in the Dockerfile refuses the plan
// through the same check Verify applies.
func TestPlanImageVerifiesARecordedBootstrap(t *testing.T) {
	path := writeRecordedBundle(t, BootstrapOptions{SourceRoot: bootstrapSourceFixture(t)})
	root := filepath.Dir(filepath.Dir(path))
	plan, err := PlanImage(t.Context(), root, linux)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.CLI == nil || plan.CLI.Platform != "linux-x64" || plan.dockerfile == "" {
		t.Errorf("a recorded bootstrap with features must plan a pinned devcontainer CLI build: %+v", plan)
	}
	dockerfile := filepath.Join(filepath.Dir(path), bootstrapDockerfile)
	data, err := os.ReadFile(dockerfile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dockerfile, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanImage(t.Context(), root, linux); !errors.Is(err, ErrImageUnavailable) || !strings.Contains(err.Error(), "Dockerfile") {
		t.Errorf("an edited bootstrap Dockerfile must refuse the plan, got %v", err)
	}
}

// Praetor's own devcontainer declares features (#652). It plans the pinned devcontainer CLI on a
// host with docker alone, no CLI on PATH, and on a platform the Node pins do not cover it runs the
// gate on the host and names the features.
func TestPlanImageNamesPraetorsOwnFeatures(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		host := hostWith("linux", "docker")
		host.GOARCH = arch
		plan, err := PlanImage(t.Context(), filepath.Join("..", ".."), host)
		if err != nil || plan.CLI == nil || plan.CLI.Platform != "linux-"+map[string]string{"amd64": "x64", "arm64": "arm64"}[arch] {
			t.Fatalf("praetor's devcontainer on linux/%s must plan the pinned CLI with docker alone: %+v %v", arch, plan, err)
		}
	}
	host := hostWith("darwin", "podman")
	host.GOARCH = "386"
	_, err := PlanImage(t.Context(), filepath.Join("..", ".."), host)
	if !errors.Is(err, ErrImageUnavailable) || !strings.Contains(err.Error(), "ghcr.io/devcontainers/features/go:1") ||
		!strings.Contains(err.Error(), "not darwin/386") {
		t.Fatalf("praetor's devcontainer where no Node is pinned: %v", err)
	}
}

// recordingRunner records every command and answers image inspection with id.
type recordingRunner struct {
	commands [][]string
	id       string
	fail     map[string]error
}

func (r *recordingRunner) run(_ context.Context, _, name string, args ...string) (string, error) {
	r.commands = append(r.commands, append([]string{filepath.Base(name)}, args...))
	if err := r.fail[filepath.Base(name)+" "+args[0]]; err != nil {
		return "", err
	}
	if len(args) > 1 && args[0] == "image" && args[1] == "inspect" {
		return r.id + "\n", nil
	}
	return "", nil
}

var testRefs = ImageRefs{Stable: "praetor-gate-x:latest", Run: "praetor-gate-x:run-1"}

// Positive and boundary: each plan builds through its own builder, tagging both refs and reading
// the ID through the per-run one, and the ID is normalized whichever runtime printed it: docker's
// sha256:<hex> and podman's bare hex alike.
func TestBuildUsesThePlannedBuilder(t *testing.T) {
	hexID := strings.Repeat("ab", 32)
	root := writeConfigRepo(t, dockerfileConfig, map[string]string{"Dockerfile": "FROM scratch\n"})
	plan, err := PlanImage(t.Context(), root, hostWith("linux", "podman"))
	if err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{id: hexID}
	img, err := plan.Build(t.Context(), runner.run, testRefs)
	if err != nil {
		t.Fatal(err)
	}
	if img.ID != "sha256:"+hexID || img.Builder != "podman build" || img.Ref != testRefs.Stable || img.RunRef != testRefs.Run {
		t.Errorf("image = %+v", img)
	}
	want := []string{"podman", "build", "--file", plan.dockerfile, "--tag", testRefs.Stable, "--tag", testRefs.Run,
		"--build-arg", "A=1", "--build-arg", "B=2", plan.context}
	if !slices.Equal(runner.commands[0], want) {
		t.Errorf("build command = %q, want %q", runner.commands[0], want)
	}
	if inspect := runner.commands[1]; inspect[len(inspect)-1] != testRefs.Run {
		t.Errorf("the ID must be read through the per-run tag, got %q", inspect)
	}

	plan.CLI = provisionedCLI(t)
	runner = &recordingRunner{id: "sha256:" + hexID}
	if img, err = plan.Build(t.Context(), runner.run, testRefs); err != nil || img.Builder != plan.CLI.String() ||
		!strings.HasPrefix(img.Builder, CLIName+" build (@devcontainers/cli ") {
		t.Fatalf("CLI build: %+v %v", img, err)
	}
	wantCLI := []string{"node", cliScript(plan.CLI.installDir()), "build", "--workspace-folder", root, "--config", plan.ConfigPath,
		"--no-lockfile", "--image-name", testRefs.Stable, "--image-name", testRefs.Run, "--docker-path", "/usr/bin/podman"}
	if !slices.Equal(runner.commands[0], wantCLI) {
		t.Errorf("CLI build command = %q, want %q", runner.commands[0], wantCLI)
	}

	// Boundary: a committed feature lockfile is enforced, never rewritten, and the CLI still
	// writes nothing into the checkout.
	lock := filepath.Join(root, ".devcontainer", featureLockfile)
	if err := os.WriteFile(lock, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner = &recordingRunner{id: hexID}
	if _, err := plan.Build(t.Context(), runner.run, testRefs); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(runner.commands[0], "--frozen-lockfile") || slices.Contains(runner.commands[0], "--no-lockfile") {
		t.Errorf("a committed lockfile must be built --frozen-lockfile: %q", runner.commands[0])
	}
}

// Negative: a failed build, a pull of an absent image that fails and an ID that is not a digest
// each fail the build, naming the step.
func TestBuildFailsNamingTheStep(t *testing.T) {
	image := "registry.example/app@sha256:" + strings.Repeat("c", 64)
	root := writeConfigRepo(t, `{"name": "x", "image": "`+image+`"}`, nil)
	plan, err := PlanImage(t.Context(), root, hostWith("linux", "docker"))
	if err != nil {
		t.Fatal(err)
	}
	pullFails := &recordingRunner{fail: map[string]error{"docker image": errors.New("no such image"), "docker pull": errors.New("denied")}}
	if _, err := plan.Build(t.Context(), pullFails.run, testRefs); err == nil || !strings.Contains(err.Error(), "docker pull failed") {
		t.Errorf("a failed pull must fail the build, got %v", err)
	}
	notDigest := &recordingRunner{id: "latest"}
	if _, err := plan.Build(t.Context(), notDigest.run, testRefs); err == nil || !strings.Contains(err.Error(), "not a sha256 digest") {
		t.Errorf("an ID that is not a digest must fail the build, got %v", err)
	}
	present := &recordingRunner{id: strings.Repeat("d", 64)}
	img, err := plan.Build(t.Context(), present.run, testRefs)
	if err != nil || img.Ref != image || img.RunRef != "" || len(present.commands) != 2 {
		t.Errorf("a present image must be inspected, not pulled or tagged: %+v %v %q", img, err, present.commands)
	}
	if args := img.ReleaseArgs(); args != nil {
		t.Errorf("a pulled image has no per-run tag to release, got %q", args)
	}
}

// RunArgs maps the host user per runtime, starts the runtime's init as PID 1 on every runtime so
// orphaned processes are reaped, puts /tmp on a tmpfs unless the checkout is mounted there, and
// names the image by ID; a host without a user, as on Windows, passes no mapping.
func TestRunArgsMapTheHostUserPerRuntime(t *testing.T) {
	id := "sha256:" + strings.Repeat("e", 64)
	opts := RunOptions{Name: "c1", Mounts: []string{"/repo"}, Env: []string{"HOME=/repo/h"}, Workdir: "/repo/wt", UID: 1000, GID: 1001}
	docker := Image{Runtime: Runtime{Name: "docker"}, ID: id}.RunArgs(opts, "go", "test")
	want := []string{"run", "--rm", "--init", "--name", "c1", "--user", "1000:1001", "--tmpfs", "/tmp:exec", "--mount", "type=bind,src=/repo,dst=/repo",
		"--env", "HOME=/repo/h", "--workdir", "/repo/wt", id, "go", "test"}
	if !slices.Equal(docker, want) {
		t.Errorf("docker run = %q, want %q", docker, want)
	}
	podman := Image{Runtime: Runtime{Name: "podman"}, ID: id}.RunArgs(opts, "go")
	if !slices.Contains(podman, "--userns=keep-id") || slices.Contains(podman, "--user") || !slices.Contains(podman, "--init") {
		t.Errorf("podman must map the user with keep-id and start its init, got %q", podman)
	}
	// Rootless docker maps the container's root to the host user: as the host UID the command
	// would be a subordinate user inside, unable to write the checkout or HOME.
	rootless := Image{Runtime: Runtime{Name: "docker", Rootless: true}, ID: id}.RunArgs(opts, "go")
	if at := slices.Index(rootless, "--user"); at < 0 || rootless[at+1] != "0:0" || slices.Contains(rootless, "1000:1001") {
		t.Errorf("rootless docker must run as the container root, the host user, got %q", rootless)
	}
	// A checkout mounted at /tmp itself keeps it: the tmpfs would hide the mount. One below /tmp
	// still gets the tmpfs, since the runtime mounts the checkout over it.
	atTmp := Image{Runtime: Runtime{Name: "docker"}, ID: id}.RunArgs(RunOptions{Name: "c1", Mounts: []string{"/tmp"}, Workdir: "/tmp", UID: 1, GID: 1}, "go")
	belowTmp := Image{Runtime: Runtime{Name: "docker"}, ID: id}.RunArgs(RunOptions{Name: "c1", Mounts: []string{"/tmp/hook/repo"}, Workdir: "/tmp/hook/repo", UID: 1, GID: 1}, "go")
	if slices.Contains(atTmp, "--tmpfs") || !slices.Contains(belowTmp, "--tmpfs") {
		t.Errorf("only a mount at /tmp itself keeps /tmp off the tmpfs: at %q, below %q", atTmp, belowTmp)
	}
	opts.UID, opts.GID = -1, -1
	none := Image{Runtime: Runtime{Name: "docker"}, ID: id}.RunArgs(opts, "go")
	if slices.Contains(none, "--user") || slices.Contains(none, "--userns=keep-id") {
		t.Errorf("a host without a user must pass no mapping, got %q", none)
	}
	if got := (Image{}).RemoveArgs("c1"); !slices.Equal(got, []string{"rm", "--force", "c1"}) {
		t.Errorf("RemoveArgs = %q", got)
	}
	if got := (Image{RunRef: testRefs.Run}).ReleaseArgs(); !slices.Equal(got, []string{"image", "rm", "--no-prune", testRefs.Run}) {
		t.Errorf("ReleaseArgs = %q", got)
	}
}

// probingRunner answers `<runtime> info` per runtime: an error for the named ones, success for the
// rest, and records each probe with the time its context allowed.
type probingRunner struct {
	answer string
	down   map[string]error
	probed []string
	bounds []time.Duration
}

func (r *probingRunner) run(ctx context.Context, _, name string, args ...string) (string, error) {
	base := filepath.Base(name)
	r.probed = append(r.probed, base+" "+args[0])
	if deadline, ok := ctx.Deadline(); ok {
		r.bounds = append(r.bounds, time.Until(deadline))
	}
	return r.answer, r.down[base]
}

// Positive: a runtime counts only once `<runtime> info` answers, docker before podman, each probe
// under RuntimeProbeTimeout; a docker whose daemon does not answer is passed over for podman.
func TestFindRuntimeProbesEachRuntime(t *testing.T) {
	up := &probingRunner{}
	rt, err := FindRuntime(t.Context(), t.TempDir(), Host{GOOS: "linux", LookPath: onPath("docker", "podman"), Run: up.run})
	if err != nil || rt.Name != "docker" || !slices.Equal(up.probed, []string{"docker info"}) {
		t.Fatalf("an answering docker = %+v %v, probes %q", rt, err, up.probed)
	}
	if len(up.bounds) != 1 || up.bounds[0] > RuntimeProbeTimeout {
		t.Errorf("the probe must run under RuntimeProbeTimeout, got %v", up.bounds)
	}
	daemonDown := &probingRunner{down: map[string]error{"docker": errors.New("exit status 1: failed to connect to the docker API")}}
	rt, err = FindRuntime(t.Context(), t.TempDir(), Host{GOOS: "linux", LookPath: onPath("docker", "podman"), Run: daemonDown.run})
	if err != nil || rt.Name != "podman" || !slices.Equal(daemonDown.probed, []string{"docker info", "podman info"}) {
		t.Errorf("a stopped docker must fall through to podman: %+v %v, probes %q", rt, err, daemonDown.probed)
	}
}

// Positive, negative and boundary: docker's security options say whether its daemon is rootless. A
// daemon reporting name=rootless plans a rootless runtime, one without it a rootful one, an empty
// answer carries no options, and an answer that is not a JSON list refuses docker, which is then
// passed over like a daemon that does not answer.
func TestFindRuntimeDetectsRootlessDocker(t *testing.T) {
	for _, tc := range []struct {
		answer   string
		rootless bool
	}{
		{`["name=seccomp,profile=builtin","name=rootless","name=cgroupns"]`, true},
		{`["name=seccomp,profile=builtin","name=cgroupns"]`, false},
		{`["name=rootlessish"]`, false},
		{"", false},
		{"null\n", false},
	} {
		runner := &probingRunner{answer: tc.answer}
		rt, err := FindRuntime(t.Context(), t.TempDir(), Host{GOOS: "linux", LookPath: onPath("docker"), Run: runner.run})
		if err != nil || rt.Name != "docker" || rt.Rootless != tc.rootless {
			t.Errorf("security options %q: runtime %+v %v, want rootless=%v", tc.answer, rt, err, tc.rootless)
		}
	}
	garbled := &probingRunner{answer: "Security Options: rootless"}
	if _, err := FindRuntime(t.Context(), t.TempDir(), Host{GOOS: "linux", LookPath: onPath("docker"), Run: garbled.run}); err == nil ||
		!strings.Contains(err.Error(), "not a JSON list") {
		t.Errorf("an answer that is not a JSON list must refuse docker, got %v", err)
	}
	var asked []string
	record := func(_ context.Context, _, name string, args ...string) (string, error) {
		asked = append(asked, filepath.Base(name)+" "+strings.Join(args, " "))
		return "", nil
	}
	if _, err := FindRuntime(t.Context(), t.TempDir(), Host{GOOS: "linux", LookPath: onPath("docker"), Run: record}); err != nil ||
		!slices.Equal(asked, []string{"docker info --format {{json .SecurityOptions}}"}) {
		t.Errorf("docker must be asked for its security options, asked %q (%v)", asked, err)
	}
}

// Negative: runtimes on PATH that do not answer, or no runner to ask them, refuse the plan with
// each runtime named, so the gate runs on the host and says why instead of failing every build.
func TestFindRuntimeRefusesRuntimesThatDoNotAnswer(t *testing.T) {
	root := writeConfigRepo(t, dockerfileConfig, map[string]string{"Dockerfile": "FROM scratch\n"})
	allDown := &probingRunner{down: map[string]error{
		"docker": errors.New("exit status 1: permission denied while trying to connect to the docker API"),
		"podman": errors.New("exit status 125: Cannot connect to Podman"),
	}}
	plan, err := PlanImage(t.Context(), root, Host{GOOS: "darwin", LookPath: onPath("docker", "podman"), Run: allDown.run})
	if !errors.Is(err, ErrImageUnavailable) || plan != nil {
		t.Fatalf("no answering runtime must refuse the plan, got %+v %v", plan, err)
	}
	for _, want := range []string{"no container runtime answers", "docker is on PATH but `docker info` failed: exit status 1: permission denied",
		"podman is on PATH but `podman info` failed: exit status 125"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("reason %q does not mention %q", err, want)
		}
	}
	dockerOnly := &probingRunner{down: map[string]error{"docker": errors.New("exit status 1")}}
	if _, err := FindRuntime(t.Context(), root, Host{GOOS: "linux", LookPath: onPath("docker"), Run: dockerOnly.run}); err == nil ||
		!strings.Contains(err.Error(), "podman is not on PATH") {
		t.Errorf("a lone docker that does not answer must say podman is absent, got %v", err)
	}
	if _, err := FindRuntime(t.Context(), root, Host{GOOS: "linux", LookPath: onPath("docker")}); !errors.Is(err, ErrImageUnavailable) {
		t.Errorf("a host without a runner cannot ask a runtime, got %v", err)
	}
}

// Boundary: a runtime that hangs is cut off when its probe's context ends rather than holding the
// plan, and a probe diagnostic is quoted up to probeExcerptBytes.
func TestFindRuntimeBoundsAHungProbe(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	hung := func(ctx context.Context, _, _ string, _ ...string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	_, err := FindRuntime(ctx, t.TempDir(), Host{GOOS: "linux", LookPath: onPath("docker"), Run: hung})
	if !errors.Is(err, ErrImageUnavailable) || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("a hung probe must end at its bound and refuse, got %v", err)
	}
	long := &probingRunner{down: map[string]error{"docker": errors.New(strings.Repeat("x", 4*probeExcerptBytes))}}
	_, err = FindRuntime(t.Context(), t.TempDir(), Host{GOOS: "linux", LookPath: onPath("docker"), Run: long.run})
	if err == nil || strings.Count(err.Error(), "x") > probeExcerptBytes+1 || !strings.Contains(err.Error(), "[truncated]") {
		t.Errorf("a long diagnostic must be truncated to %d bytes, got %d", probeExcerptBytes, len(err.Error()))
	}
}

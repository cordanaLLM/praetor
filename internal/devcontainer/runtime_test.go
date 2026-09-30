// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package devcontainer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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

var linux = Host{GOOS: "linux", LookPath: onPath("docker", "podman", CLIName)}

// Positive: a Dockerfile configuration without features plans a runtime build of confined inputs,
// with docker preferred over podman, and needs no devcontainer CLI.
func TestPlanImagePlansARuntimeBuild(t *testing.T) {
	root := writeConfigRepo(t, dockerfileConfig, map[string]string{"Dockerfile": "FROM scratch\n"})
	plan, err := PlanImage(t.Context(), root, linux)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.Runtime.Name != "docker" || plan.CLI != "" || plan.RepoDir != root {
		t.Errorf("plan = %+v, want docker, no CLI, repo %s", plan, root)
	}
	if want := filepath.Join(root, ".devcontainer", "Dockerfile"); plan.dockerfile != want {
		t.Errorf("dockerfile = %s, want %s", plan.dockerfile, want)
	}
	if want := filepath.Join(root, ".devcontainer"); plan.context != want {
		t.Errorf("context = %s, want %s", plan.context, want)
	}
	podman, err := PlanImage(t.Context(), root, Host{GOOS: "darwin", LookPath: onPath("podman")})
	if err != nil || podman.Runtime.Name != "podman" {
		t.Errorf("a host with podman alone must plan with it: %+v %v", podman, err)
	}
}

// Negative: every host or configuration the gate cannot build from is refused with
// ErrImageUnavailable and a reason naming the cause.
func TestPlanImageRefusesWithAReason(t *testing.T) {
	built := writeConfigRepo(t, dockerfileConfig, nil)
	cases := []struct {
		name, root string
		host       Host
		want       string
	}{
		{"windows", built, Host{GOOS: "windows", LookPath: linux.LookPath}, "Windows"},
		{"no config", t.TempDir(), linux, "has no " + ConfigPath},
		{"no runtime", built, Host{GOOS: "linux", LookPath: onPath(CLIName)}, "neither docker nor podman"},
		{"unmanaged key", writeConfigRepo(t, `{"name": "x", "image": "a@sha256:`+strings.Repeat("0", 64)+`", "runArgs": []}`, nil), linux, "runArgs"},
		{"neither image nor build", writeConfigRepo(t, `{"name": "x"}`, nil), linux, "neither an image nor a build"},
		{"dockerfile escapes", writeConfigRepo(t, `{"name": "x", "build": {"dockerfile": "../../outside", "context": "."}}`, nil), linux, "outside the repository"},
		{"context escapes", writeConfigRepo(t, `{"name": "x", "build": {"dockerfile": "Dockerfile", "context": "../.."}}`, nil), linux, "build.context"},
		{"features without the CLI", writeConfigRepo(t, `{"name": "x", "image": "a@sha256:`+strings.Repeat("0", 64)+`", "features": {"ghcr.io/devcontainers/features/node:1": {}, "ghcr.io/devcontainers/features/go:1": {}}}`, nil),
			Host{GOOS: "linux", LookPath: onPath("docker")}, "(ghcr.io/devcontainers/features/go:1, ghcr.io/devcontainers/features/node:1), which only the devcontainer CLI applies"},
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

// Boundary: a recorded Praetor bootstrap declares features, so it plans only where the
// devcontainer CLI is on PATH, and its companions are verified first: one edited byte in the
// Dockerfile refuses the plan through the same check Verify applies.
func TestPlanImageVerifiesARecordedBootstrap(t *testing.T) {
	path := writeRecordedBundle(t, BootstrapOptions{SourceRoot: bootstrapSourceFixture(t)})
	root := filepath.Dir(filepath.Dir(path))
	plan, err := PlanImage(t.Context(), root, linux)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.CLI != "/usr/bin/"+CLIName || plan.dockerfile == "" {
		t.Errorf("a recorded bootstrap with features must plan a devcontainer CLI build: %+v", plan)
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

// Praetor's own devcontainer declares features, so a host without the devcontainer CLI runs the
// gate on the host and says which features need it.
func TestPlanImageNamesPraetorsOwnFeatures(t *testing.T) {
	_, err := PlanImage(t.Context(), filepath.Join("..", ".."), Host{GOOS: "linux", LookPath: onPath("docker")})
	if !errors.Is(err, ErrImageUnavailable) || !strings.Contains(err.Error(), "ghcr.io/devcontainers/features/go:1") {
		t.Fatalf("praetor's devcontainer without the CLI: %v", err)
	}
	if _, err := PlanImage(t.Context(), filepath.Join("..", ".."), linux); err != nil {
		t.Fatalf("praetor's devcontainer must plan where the CLI is present: %v", err)
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

// Positive and boundary: each plan builds through its own builder, and the ID is normalized
// whichever runtime printed it: docker's sha256:<hex> and podman's bare hex alike.
func TestBuildUsesThePlannedBuilder(t *testing.T) {
	hexID := strings.Repeat("ab", 32)
	root := writeConfigRepo(t, dockerfileConfig, map[string]string{"Dockerfile": "FROM scratch\n"})
	plan, err := PlanImage(t.Context(), root, Host{GOOS: "linux", LookPath: onPath("podman")})
	if err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{id: hexID}
	img, err := plan.Build(t.Context(), runner.run, "praetor-gate-x:latest")
	if err != nil {
		t.Fatal(err)
	}
	if img.ID != "sha256:"+hexID || img.Builder != "podman build" || img.Ref != "praetor-gate-x:latest" {
		t.Errorf("image = %+v", img)
	}
	want := []string{"podman", "build", "--file", plan.dockerfile, "--tag", "praetor-gate-x:latest",
		"--build-arg", "A=1", "--build-arg", "B=2", plan.context}
	if !slices.Equal(runner.commands[0], want) {
		t.Errorf("build command = %q, want %q", runner.commands[0], want)
	}

	plan.CLI = "/usr/bin/" + CLIName
	runner = &recordingRunner{id: "sha256:" + hexID}
	if img, err = plan.Build(t.Context(), runner.run, "ref:latest"); err != nil || img.Builder != CLIName+" build" {
		t.Fatalf("CLI build: %+v %v", img, err)
	}
	wantCLI := []string{CLIName, "build", "--workspace-folder", root, "--config", plan.ConfigPath,
		"--image-name", "ref:latest", "--docker-path", "/usr/bin/podman"}
	if !slices.Equal(runner.commands[0], wantCLI) {
		t.Errorf("CLI build command = %q, want %q", runner.commands[0], wantCLI)
	}
}

// Negative: a failed build, a pull of an absent image that fails and an ID that is not a digest
// each fail the build, naming the step.
func TestBuildFailsNamingTheStep(t *testing.T) {
	image := "registry.example/app@sha256:" + strings.Repeat("c", 64)
	root := writeConfigRepo(t, `{"name": "x", "image": "`+image+`"}`, nil)
	plan, err := PlanImage(t.Context(), root, Host{GOOS: "linux", LookPath: onPath("docker")})
	if err != nil {
		t.Fatal(err)
	}
	pullFails := &recordingRunner{fail: map[string]error{"docker image": errors.New("no such image"), "docker pull": errors.New("denied")}}
	if _, err := plan.Build(t.Context(), pullFails.run, "unused"); err == nil || !strings.Contains(err.Error(), "docker pull failed") {
		t.Errorf("a failed pull must fail the build, got %v", err)
	}
	notDigest := &recordingRunner{id: "latest"}
	if _, err := plan.Build(t.Context(), notDigest.run, "unused"); err == nil || !strings.Contains(err.Error(), "not a sha256 digest") {
		t.Errorf("an ID that is not a digest must fail the build, got %v", err)
	}
	present := &recordingRunner{id: strings.Repeat("d", 64)}
	img, err := plan.Build(t.Context(), present.run, "unused")
	if err != nil || img.Ref != image || len(present.commands) != 2 {
		t.Errorf("a present image must be inspected, not pulled: %+v %v %q", img, err, present.commands)
	}
}

// RunArgs maps the host user per runtime and names the image by ID; a host without a user, as on
// Windows, passes no mapping.
func TestRunArgsMapTheHostUserPerRuntime(t *testing.T) {
	id := "sha256:" + strings.Repeat("e", 64)
	opts := RunOptions{Name: "c1", Mounts: []string{"/repo"}, Env: []string{"HOME=/repo/h"}, Workdir: "/repo/wt", UID: 1000, GID: 1001}
	docker := Image{Runtime: Runtime{Name: "docker"}, ID: id}.RunArgs(opts, "go", "test")
	want := []string{"run", "--rm", "--name", "c1", "--user", "1000:1001", "--mount", "type=bind,src=/repo,dst=/repo",
		"--env", "HOME=/repo/h", "--workdir", "/repo/wt", id, "go", "test"}
	if !slices.Equal(docker, want) {
		t.Errorf("docker run = %q, want %q", docker, want)
	}
	podman := Image{Runtime: Runtime{Name: "podman"}, ID: id}.RunArgs(opts, "go")
	if !slices.Contains(podman, "--userns=keep-id") || slices.Contains(podman, "--user") {
		t.Errorf("podman must map the user with keep-id, got %q", podman)
	}
	opts.UID, opts.GID = -1, -1
	none := Image{Runtime: Runtime{Name: "docker"}, ID: id}.RunArgs(opts, "go")
	if slices.Contains(none, "--user") || slices.Contains(none, "--userns=keep-id") {
		t.Errorf("a host without a user must pass no mapping, got %q", none)
	}
	if got := (Image{}).RemoveArgs("c1"); !slices.Equal(got, []string{"rm", "--force", "c1"}) {
		t.Errorf("RemoveArgs = %q", got)
	}
}

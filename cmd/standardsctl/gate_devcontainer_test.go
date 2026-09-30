// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/devcontainer"
	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/lockdown"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// gate verify prints where the certified toolchain stages ran (positive), says a receipt minted
// before the gate recorded it carries none (negative) while still verifying it, and calls a
// receipt holding two such lines ambiguous (boundary).
func TestRunGateVerify_PrintsTheCertifiedExecution(t *testing.T) {
	f := newGateFixture(t)
	f.pin(t, f.pub)
	f.mintReceipt(t, f.priv, f.head)
	out, err := captureStdout(t, func() error { return runGate([]string{"verify", "--path", f.dir}) })
	if err != nil {
		t.Fatalf("gate verify: %v", err)
	}
	mustContain(t, out, "Execution:  not recorded")

	image := "sha256:" + strings.Repeat("9", 64)
	f.output = []byte(lockdown.GateOutputVersion + "\n" + lockdown.WorktreeCleanLine(true) + "\n" +
		lockdown.ExecutionLine("devcontainer", "runtime=docker", "image="+image) + "\nstage\tRace-Detector Tests\tpassed\t\n")
	f.mintReceipt(t, f.priv, f.head)
	out, err = captureStdout(t, func() error { return runGate([]string{"verify", "--path", f.dir}) })
	if err != nil {
		t.Fatalf("gate verify of a receipt recording its devcontainer: %v", err)
	}
	mustContain(t, out, "Execution:  devcontainer runtime=docker image="+image)

	// Boundary: signed output holding two execution lines, which the gate never writes, is
	// reported as ambiguous with the count, never mislabelled as a receipt minted before the line.
	f.output = []byte(lockdown.GateOutputVersion + "\n" + lockdown.WorktreeCleanLine(true) + "\n" +
		lockdown.ExecutionLine("host", "reason=a") + "\n" + lockdown.ExecutionLine("devcontainer", "image="+image) +
		"\nstage\tRace-Detector Tests\tpassed\t\n")
	f.mintReceipt(t, f.priv, f.head)
	out, err = captureStdout(t, func() error { return runGate([]string{"verify", "--path", f.dir}) })
	if err != nil {
		t.Fatalf("gate verify of a receipt with two execution lines: %v", err)
	}
	mustContain(t, out, "Execution:  ambiguous (")
	mustContain(t, out, "2 execution lines")
	if strings.Contains(out, "not recorded") {
		t.Errorf("an ambiguous receipt must not read as one minted before the line:\n%s", out)
	}
}

// Boundary: gate deadline reports the image build it reserves for a Go repository with a
// devcontainer and docker on PATH, and omits the field when the operator opts out.
func TestGateDeadline_ReportsTheDevcontainerBuild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a Windows host runs the toolchain stages on the host, so no image build is reserved there")
	}
	dir := writeMarkers(t, "go.mod")
	writeFixtureFile(t, dir, ".devcontainer/devcontainer.json", `{"name": "x", "build": {"dockerfile": "Dockerfile"}}`)
	bin := t.TempDir()
	testsupport.BuildExecutable(t, bin, "docker", "package main\n\nfunc main() {}\n")
	t.Setenv("PATH", bin)
	t.Setenv(gating.TestStageTimeoutEnv, "")
	for _, tc := range []struct{ mode, want string }{{"", devcontainer.ImageBuildTimeout.String()}, {"off", ""}} {
		t.Setenv(gating.DevcontainerEnv, tc.mode)
		out, err := captureStdout(t, func() error { return runGate([]string{"deadline", "--json", "--path", dir}) })
		if err != nil {
			t.Fatalf("gate deadline: %v", err)
		}
		var got gateDeadlineReport
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("gate deadline printed non-JSON %q: %v", out, err)
		}
		if got.DevcontainerImageBuild != tc.want {
			t.Errorf("%s=%q: devcontainer_image_build = %q, want %q", gating.DevcontainerEnv, tc.mode, got.DevcontainerImageBuild, tc.want)
		}
	}
}

// Negative: gate run --dry-run builds no image, so its deadline reserves no image build, while
// the same repository run for real reserves it.
func TestGateRun_DryRunReservesNoImageBuild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a Windows host runs the toolchain stages on the host, so no image build is reserved there")
	}
	stubPipeline(t)
	dir := writeMarkers(t, "go.mod")
	writeFixtureFile(t, dir, ".devcontainer/devcontainer.json", `{"name": "x", "build": {"dockerfile": "Dockerfile"}}`)
	bin := t.TempDir()
	testsupport.BuildExecutable(t, bin, "docker", "package main\n\nfunc main() {}\n")
	t.Setenv("PATH", bin)
	t.Setenv(gating.TestStageTimeoutEnv, "")
	t.Setenv(gating.DevcontainerEnv, "")
	for _, tc := range []struct {
		args  []string
		image bool
	}{{[]string{"run", "--dry-run", "--path=" + dir}, false}, {[]string{"run", "--path=" + dir}, true}} {
		out, err := captureStdout(t, func() error { return runGate(tc.args) })
		if err != nil {
			t.Fatalf("gate %q: %v", tc.args, err)
		}
		if got := strings.Contains(out, "to build the devcontainer image"); got != tc.image {
			t.Errorf("gate %q reserves the image build = %v, want %v:\n%s", tc.args, got, tc.image, out)
		}
	}
}

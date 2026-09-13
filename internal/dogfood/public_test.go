package dogfood

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

func TestRunPublicLoopAppliesAndRechecksImmutableFixture(t *testing.T) {
	opts, sha := publicLoopFixture(t)
	opts.Apply = true
	report, err := RunPublicLoop(context.Background(), opts)
	if err != nil {
		t.Fatalf("public loop failed: %v (%+v)", err, report)
	}
	if !report.Verified || len(report.Results) != 1 {
		t.Fatalf("unverified report: %+v", report)
	}
	result := report.Results[0]
	if result.SourceSHA != sha || result.Status != "verified" || len(result.Attempts) != 2 {
		t.Fatalf("wrong retained source/rechecks: %+v", result)
	}
	if result.Attempts[0].TreeDigest != result.Attempts[1].TreeDigest {
		t.Fatal("repeat apply changed bytes")
	}
	if _, err := os.Stat(filepath.Join(report.RunDir, "report.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(result.Checkout, ".standards.lock")); err != nil {
		t.Fatal(err)
	}
	ctx, err := publicCommandContext(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := util.RunGit(ctx, result.Checkout, "config", "--local", "--get", "core.hooksPath"); err == nil {
		t.Fatal("disposable loop activated hooks")
	}
	if contents := publicRead(t, filepath.Join(result.Checkout, "fixture.go")); contents != "package fixture\n" {
		t.Fatal("upstream source changed")
	}
}

func TestRunPublicLoopDryRunIsOnlyPlanned(t *testing.T) {
	opts, _ := publicLoopFixture(t)
	report, err := RunPublicLoop(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	result := report.Results[0]
	if report.Verified || result.Status != "planned" || len(result.Attempts) != 0 {
		t.Fatalf("simulation claimed verification: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(result.Checkout, ".standards.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run wrote lock: %v", err)
	}
}

func TestRunPublicLoopRetainsApplyFailureAndStops(t *testing.T) {
	opts, _ := publicLoopFixture(t, map[string]string{".workingdir": "this is a file, not a ledger directory\n"})
	opts.Apply = true
	report, err := RunPublicLoop(context.Background(), opts)
	if !errors.Is(err, ErrPublicLoopFailed) {
		t.Fatalf("missing source did not fail truthfully: %v", err)
	}
	result := report.Results[0]
	if report.Verified || result.Status != "failed" || result.Error == "" || len(result.Attempts) != 1 {
		t.Fatalf("failure retried/hidden: %+v", result)
	}
	if _, err := os.Stat(result.Checkout); err != nil {
		t.Fatalf("failure evidence deleted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(report.RunDir, "report.json")); err != nil {
		t.Fatal(err)
	}
}

func TestPublicLoopValidationAndCancellation(t *testing.T) {
	opts, _ := publicLoopFixture(t)
	cases := []PublicLoopOptions{opts, opts, opts, opts}
	cases[0].Repositories = []string{"https://user:secret@github.com/spf13/cobra"}
	cases[1].Repositories = make([]string, MaxPublicRepositories+1)
	cases[2].MaxAttempts = MaxPublicAttempts + 1
	cases[3].ArtifactDir = ""
	for _, invalid := range cases {
		if _, err := RunPublicLoop(context.Background(), invalid); err == nil {
			t.Fatalf("invalid options accepted: %+v", invalid)
		}
	}
	var nilContext context.Context
	if _, err := RunPublicLoop(nilContext, opts); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RunPublicLoop(ctx, opts); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if _, err := os.Stat(opts.ArtifactDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid run wrote artifacts: %v", err)
	}
}

func TestPublicCommandEnvironmentIgnoresWorkstation(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "/private/socket")
	t.Setenv("GIT_CONFIG_VALUE_0", "credential-helper-secret")
	t.Setenv("PRAETOR_TEST_SECRET", "must-not-copy")
	dir := t.TempDir()
	ctx, err := publicCommandContext(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := util.RunCommand(ctx, "", "/usr/bin/env")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"SSH_AUTH_SOCK", "credential-helper-secret", "must-not-copy"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("ambient state leaked: %s", forbidden)
		}
	}
	for _, required := range []string{"HOME=" + filepath.Join(dir, "home"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ALLOW_PROTOCOL=https", "GIT_CONFIG_KEY_0=credential.helper", "GIT_CONFIG_VALUE_0=\n"} {
		if !strings.Contains(output, required) {
			t.Fatalf("isolation missing %s", required)
		}
	}
}

func TestPublicAdoptionErrorsCannotPass(t *testing.T) {
	if publicAdoptionError(&adopt.AdoptReport{Errors: []string{"failed write"}}, nil) == nil {
		t.Fatal("soft adoption error accepted")
	}
	if publicAdoptionError(nil, nil) == nil {
		t.Fatal("nil adoption report accepted")
	}
	if publicAdoptionError(&adopt.AdoptReport{}, nil) != nil {
		t.Fatal("valid adoption rejected")
	}
}

func TestPublicEvidenceFailurePreservesApplyError(t *testing.T) {
	opts, _ := publicLoopFixture(t, map[string]string{".workingdir": "blocked ledger"})
	opts.Apply = true
	wrapper, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	script := publicRead(t, wrapper)
	script = strings.Replace(script, "if [ \"$1\" = clone ]; then", "if [ \"$1\" = clone ]; then\n for arg do target=$arg; done\n mkdir \"${target%/*}/result.json\" || exit $?", 1)
	publicWrite(t, wrapper, script, 0o700)
	report, err := RunPublicLoop(context.Background(), opts)
	if !errors.Is(err, ErrPublicLoopFailed) {
		t.Fatalf("write failure did not fail run: %v", err)
	}
	message := report.Results[0].Error
	if !strings.Contains(message, "incomplete work") || !strings.Contains(message, "retain evidence") {
		t.Fatalf("lost one of two failures: %s", message)
	}
}

func publicLoopFixture(t *testing.T, extraFiles ...map[string]string) (PublicLoopOptions, string) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	fixture := t.TempDir()
	ctx, err := publicCommandContext(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "--template=", fixture}, {"-C", fixture, "config", "user.name", "Fixture"}, {"-C", fixture, "config", "user.email", "fixture@example.invalid"}} {
		if output, err := util.RunCommand(ctx, "", git, args...); err != nil {
			t.Fatalf("fixture git: %v %s", err, output)
		}
	}
	publicWrite(t, filepath.Join(fixture, "fixture.go"), "package fixture\n", 0o600)
	for _, files := range extraFiles {
		for name, data := range files {
			publicWrite(t, filepath.Join(fixture, name), data, 0o600)
		}
	}
	if _, err := util.RunCommand(ctx, fixture, git, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := util.RunCommand(ctx, fixture, git, "-c", "commit.gpgsign=false", "commit", "-m", "fixture"); err != nil {
		t.Fatal(err)
	}
	sha, err := util.RunCommand(ctx, fixture, git, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	installPublicGitFixture(t, git, fixture)
	source, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return PublicLoopOptions{Repositories: []string{"https://github.com/spf13/cobra"}, SourceRoot: source, ArtifactDir: filepath.Join(t.TempDir(), "evidence")}, strings.TrimSpace(sha)
}

func installPublicGitFixture(t *testing.T, git, fixture string) {
	t.Helper()
	tools := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = clone ]; then\n for arg do target=$arg; done\n GIT_ALLOW_PROTOCOL=file %s clone --template= --no-hardlinks --no-checkout %s \"$target\" || exit $?\n exec %s -C \"$target\" remote set-url origin https://github.com/spf13/cobra\nfi\nexec %s \"$@\"\n", publicShellQuote(git), publicShellQuote(fixture), publicShellQuote(git), publicShellQuote(git))
	publicWrite(t, filepath.Join(tools, "git"), script, 0o700)
	t.Setenv("PATH", tools+":/usr/bin:/bin")
}

func publicShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func publicWrite(t *testing.T, path, data string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
}

func publicRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

package adopt

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

func verificationFixture(t *testing.T, files map[string]string) (string, *VerificationPlan) {
	t.Helper()
	root := t.TempDir()
	for path, data := range files {
		mustWrite(t, filepath.Join(root, path), data)
	}
	plan, err := resolveVerificationPlan(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	return root, plan
}

func TestVerificationMixedProjectCommands(t *testing.T) {
	_, plan := verificationFixture(t, map[string]string{
		"Plugin.sln": "", "src/Plugin.csproj": `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework></PropertyGroup></Project>`,
		"tests/Plugin.Tests.csproj": `<Project><ItemGroup><PackageReference Include="Microsoft.NET.Test.Sdk" Version="18.0.0" /></ItemGroup></Project>`,
		"global.json":               `{"sdk":{"version":"10.0.400"},"test":{"runner":"Microsoft.Testing.Platform"}}`,
		"package.json":              `{"packageManager":"npm@11.15.0","scripts":{"build":"fixture build","check":"fixture check","test":"fixture test"}}`,
		".python-version":           "3.14.7\n", "tests/python/test_release.py": "import unittest\n",
	})
	if plan.Status != verificationDeclared || !reflect.DeepEqual(plan.Runtimes, []string{"node", "dotnet", "python"}) {
		t.Fatalf("mixed project plan incorrect: %+v", plan)
	}
	if !reflect.DeepEqual(plan.Build[0], []string{"npm", "run", "build"}) {
		t.Fatal("frontend must build before .NET resources")
	}
	for _, want := range [][]string{
		{"dotnet", "restore", "./src/Plugin.csproj", "--locked-mode"},
		{"dotnet", "build", "./src/Plugin.csproj", "-c", "Release", "--no-restore", "-warnaserror"},
		{"dotnet", "test", "--project", "./tests/Plugin.Tests.csproj", "-c", "Release", "--no-build"},
		{"npm", "run", "test"}, {"python3", "-m", "unittest", "discover", "-s", "tests/python"},
	} {
		assertVerificationCommand(t, plan, want)
	}
	if strings.Contains(buildMakefile(plan), "'go'") || strings.Contains(buildMakefile(plan), "meson") {
		t.Fatal("governance guessed an unrelated toolchain")
	}
}

func assertVerificationCommand(t *testing.T, plan *VerificationPlan, want []string) {
	t.Helper()
	for _, group := range [][][]string{plan.Build, plan.Test} {
		for _, command := range group {
			if reflect.DeepEqual(command, want) {
				return
			}
		}
	}
	t.Fatalf("command %q absent from %+v", want, plan)
}

func TestVerificationMissingAndAmbiguousGatesFail(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"empty": {}, "solution-without-project": {"App.sln": ""},
		"dotnet-no-test-project": {"App.csproj": "<Project/>"},
		"node-no-scripts":        {"package.json": "{}"},
		"node-no-test":           {"package.json": `{"scripts":{"build":"true"}}`},
		"node-empty-test":        {"package.json": `{"scripts":{"build":"true","test":"  "}}`},
		"node-npm-init-test":     {"package.json": `{"scripts":{"build":"true","test":"echo \"Error: no test specified\" && exit 1"}}`},
		"node-no-build":          {"package.json": `{"scripts":{"test":"true"}}`},
		"node-other-manager":     {"package.json": `{"packageManager":"pnpm@10.0.0","scripts":{"build":"true","test":"true"}}`},
		"python-unknown-runner":  {"pyproject.toml": "[project]\n"},
		"python-no-build":        {"pytest.ini": "[pytest]\n"},
		"python-old-discovery":   {".python-version": "3.13.5", "tests/test_something.py": "import unittest\n"},
		"meson-needs-setup":      {"meson.build": "project('fixture', 'c')\n"},
		"cmake-needs-setup":      {"CMakeLists.txt": "project(fixture)\n"},
	} {
		t.Run(name, func(t *testing.T) {
			root, plan := verificationFixture(t, files)
			if plan.Status != verificationUnavailable || len(plan.Reasons) == 0 {
				t.Fatalf("missing gate claimed usable: %+v", plan)
			}
			mustWrite(t, filepath.Join(root, "Makefile"), buildMakefile(plan))
			if _, err := util.RunCommand(t.Context(), root, "make", "--no-print-directory", "test"); err == nil {
				t.Fatal("unavailable test recipe succeeded")
			}
			text := verificationTestText(plan)
			if !strings.Contains(text, "exit 1") {
				t.Fatal("harness omits unavailable failure")
			}
		})
	}
}

func TestVerificationRejectsMalformedMetadataBeforeMutation(t *testing.T) {
	for name, data := range map[string]string{
		"null": "null", "array": "[]", "trailing": "{} {}", "scripts-null": `{"scripts":null}`,
		"null-script": `{"scripts":{"test":null}}`, "numeric-script": `{"scripts":{"test":42}}`,
		"duplicate":    `{"scripts":{},"scripts":{"test":"true"}}`,
		"case-alias":   `{"Scripts":{"test":"true"}}`,
		"script-alias": `{"scripts":{"Test":"true"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			root := newTestRepo(t, name)
			mustWrite(t, filepath.Join(root, "package.json"), data)
			before := snapshotTree(t, root)
			report, err := Adopt(t.Context(), AdoptOptions{Path: root, DryRun: false})
			if err == nil || report == nil || len(report.Errors) == 0 || len(report.CreatedFiles) != 0 {
				t.Fatalf("bad metadata accepted or partial state lost: %+v %v", report, err)
			}
			assertTreeUnchanged(t, before, snapshotTree(t, root))
		})
	}
}

// argumentRecorder writes each argument it receives on its own line to "received" in its working
// directory, as printf '%s\n' "$@" > received did.
const argumentRecorder = `package main

import (
	"os"
	"strings"
)

func main() {
	data := ""
	if len(os.Args) > 1 {
		data = strings.Join(os.Args[1:], "\n") + "\n"
	}
	if err := os.WriteFile("received", []byte(data), 0o600); err != nil {
		os.Exit(1)
	}
}
`

func TestVerificationCommandArgumentsRemainInertThroughMake(t *testing.T) {
	root := t.TempDir()
	// The recorder is a native program, so what it records is what make delivered. As a
	// "#!/bin/sh" script it was itself an MSYS program on Windows, and the MSYS runtime re-parses
	// the command line it is started with: project'quote.csproj arrived merged with the
	// arguments after it and ~ as the home directory. A native recorder measured under GNU Make
	// 4.4.1 for Windows32 received all nine arguments exactly, with and without sh on PATH.
	record := testsupport.BuildExecutable(t, root, "record", argumentRecorder)
	arguments := []string{"project`touch INJECTED_BACKTICK`.csproj", "project$(touch INJECTED_DOLLAR).csproj", "#not-comment", "project'quote.csproj", "~", "$HOME", "semi;touch INJECTED_SEMI", "back\\slash", ""}
	command := append([]string{record}, arguments...)
	plan := &VerificationPlan{Status: verificationDeclared, Build: [][]string{command}, Test: [][]string{command}}
	mustWrite(t, filepath.Join(root, "Makefile"), "test:\n"+verificationRecipe(plan, plan.Test))
	if out, err := util.RunCommand(t.Context(), root, "make", "--no-print-directory", "test"); err != nil {
		t.Fatalf("synthetic argv execution: %s %v", out, err)
	}
	if got := mustRead(t, filepath.Join(root, "received")); got != strings.Join(arguments, "\n")+"\n" {
		t.Fatalf("arguments changed: %q", got)
	}
	for _, sentinel := range []string{"INJECTED_BACKTICK", "INJECTED_DOLLAR", "INJECTED_SEMI"} {
		if _, err := os.Stat(filepath.Join(root, sentinel)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("shell expanded an argument: %s %v", sentinel, err)
		}
	}
}

func TestVerificationEmptyRecipesNeverSucceed(t *testing.T) {
	for _, status := range []string{verificationDeclared, verificationUnavailable, verificationPreserved} {
		root := t.TempDir()
		plan := &VerificationPlan{Status: status, Build: [][]string{{}}, Test: [][]string{{}}}
		mustWrite(t, filepath.Join(root, "Makefile"), buildMakefile(plan))
		if _, err := util.RunCommand(t.Context(), root, "make", "--no-print-directory", "test"); err == nil {
			t.Fatalf("empty %s recipe succeeded", status)
		}
	}
}

func TestVerificationActualScaffoldBuildAndTestFailurePropagates(t *testing.T) {
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Fatalf("make is required for scaffold execution: %v", err)
	}
	for _, failure := range []string{"none", "build", "test", "audit"} {
		t.Run(failure, func(t *testing.T) {
			root, plan := verificationFixture(t, map[string]string{"go.mod": "module fixture\n"})
			stubs := t.TempDir()
			writeStub(t, stubs, "go", "printf '%s\\n' \"$*\" >> calls\n[ \"$1\" != '"+failure+"' ]\n")
			writeStub(t, stubs, "standardsctl", "printf '%s\\n' \"$*\" >> calls\n[ \"$1\" != '"+failure+"' ]\n")
			hermeticPath(t, stubs)
			mustWrite(t, filepath.Join(root, "Makefile"), buildMakefile(plan))
			// -j1 pins serial execution: this assertion compares an exact call order, and
			// make inherits parallelism through MAKEFLAGS from whatever invoked the test.
			// Run under `make verify-all` with a jobserver the targets interleave and the
			// order assertion fails for a scaffold that is correct.
			_, err := util.RunCommand(context.Background(), root, makePath, "--no-print-directory", "-j1", "verify-all")
			if (err == nil) != (failure == "none") {
				t.Fatalf("failure %q did not propagate: %v", failure, err)
			}
			calls := mustRead(t, filepath.Join(root, "calls"))
			if failure == "none" && calls != "compile-context --verify\naudit\nbuild -v ./...\ntest -v -race ./...\n" {
				t.Fatalf("scaffold did not exercise required commands: %q", calls)
			}
		})
	}
}

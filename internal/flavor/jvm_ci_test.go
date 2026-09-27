package flavor_test

// BUG-1009: jvm-service detects a gradlew or a Gradle build file, but the scaffolded build step
// ran a wrapper only when its executable bit was set and never ran a Gradle build without one.
// A gradlew committed without the bit, or a build.gradle with no wrapper, fell through to "no
// Maven or Gradle build found" inside a required check. The step is executed here against each
// layout, with stub build tools on a PATH that holds nothing else, so the host's own Maven or
// Gradle never decides a case.

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const jvmCIPath = ".github/workflows/ci.yml"

// jvmStepTimeout bounds one execution of the build step (HISS-02).
const jvmStepTimeout = 30 * time.Second

// jvmBuildStep returns the run: body of the scaffolded JVM workflow's "Build and verify" step.
func jvmBuildStep(t *testing.T) string {
	t.Helper()
	body := scaffoldInto(t, "jvm-service", jvmCIPath)[jvmCIPath]
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(body), &workflow); err != nil {
		t.Fatalf("parse workflow: %v\n%s", err, body)
	}
	for _, job := range workflow.Jobs {
		for i := 0; i < len(job.Steps) && i < maxWorkflowSteps; i++ {
			if job.Steps[i].Name == "Build and verify" {
				return job.Steps[i].Run
			}
		}
	}
	t.Fatalf("the scaffolded JVM workflow has no \"Build and verify\" step:\n%s", body)
	return ""
}

// jvmLayout is one repository shape the build step meets.
type jvmLayout struct {
	name string
	// files are written 0644; executable names the ones written 0755.
	files      map[string]string
	executable map[string]bool
	// tools are the stub build tools on the runner's PATH.
	tools []string
	// ran is what the step must run; fails is a fragment of the error it must stop with instead.
	ran   string
	fails string
}

// stubTool prints its name and arguments, standing in for a wrapper or an installed tool.
func stubTool(name string) string {
	return "#!/bin/sh\necho \"" + name + " $*\"\n"
}

func jvmLayouts() []jvmLayout {
	gradleBuild := "plugins { java }\n"
	return []jvmLayout{
		// Positive: an executable wrapper runs as itself.
		{name: "executable-gradlew", files: map[string]string{"gradlew": stubTool("gradlew"), "build.gradle": gradleBuild}, executable: map[string]bool{"gradlew": true}, ran: "gradlew check"},
		{name: "executable-mvnw", files: map[string]string{"mvnw": stubTool("mvnw"), "pom.xml": "<project/>\n"}, executable: map[string]bool{"mvnw": true}, ran: "mvnw -B verify"},
		// Positive: the row's case, a gradlew committed without its executable bit.
		{name: "gradlew-without-exec-bit", files: map[string]string{"gradlew": stubTool("gradlew"), "build.gradle.kts": gradleBuild}, ran: "gradlew check"},
		// Boundary: a non-executable Maven wrapper still wins over the runner's mvn.
		{name: "mvnw-without-exec-bit", files: map[string]string{"mvnw": stubTool("mvnw"), "pom.xml": "<project/>\n"}, tools: []string{"mvn"}, ran: "mvnw -B verify"},
		{name: "pom-only", files: map[string]string{"pom.xml": "<project/>\n"}, tools: []string{"mvn"}, ran: "mvn -B verify"},
		// Positive: the row's other case, a Gradle build with no wrapper, where the runner has gradle.
		{name: "gradle-build-no-wrapper", files: map[string]string{"build.gradle.kts": gradleBuild}, tools: []string{"gradle"}, ran: "gradle check"},
		// Boundary: a settings file alone marks a Gradle build too.
		{name: "gradle-settings-only", files: map[string]string{"settings.gradle": "rootProject.name = 'widget'\n"}, tools: []string{"gradle"}, ran: "gradle check"},
		// Negative: no wrapper and no gradle names the missing wrapper, not a missing build.
		{name: "gradle-build-no-wrapper-no-gradle", files: map[string]string{"build.gradle": gradleBuild}, fails: "no gradlew wrapper"},
		// Negative: nothing to build is still reported as such.
		{name: "no-build", files: map[string]string{"README.md": "# widget\n"}, tools: []string{"mvn", "gradle"}, fails: "no Maven or Gradle build found"},
	}
}

// jvmFixture writes the layout's repository and a PATH directory holding sh and its stub tools.
func jvmFixture(t *testing.T, shell string, layout jvmLayout) (repo, path string) {
	t.Helper()
	repo, path = t.TempDir(), t.TempDir()
	for name, body := range layout.files {
		mode := os.FileMode(0o644)
		if layout.executable[name] {
			mode = 0o755
		}
		writeMode(t, filepath.Join(repo, name), body, mode)
	}
	for _, tool := range layout.tools {
		writeMode(t, filepath.Join(path, tool), stubTool(tool), 0o755)
	}
	if err := os.Symlink(shell, filepath.Join(path, "sh")); err != nil {
		t.Fatalf("link sh into the fixture PATH: %v", err)
	}
	return repo, path
}

// writeMode writes a file and then sets its mode, which the umask cannot narrow.
func writeMode(t *testing.T, name, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(name, []byte(body), mode); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	if err := os.Chmod(name, mode); err != nil {
		t.Fatalf("chmod %s: %v", name, err)
	}
}

// runJVMStep executes the step in repo with only path on PATH.
func runJVMStep(t *testing.T, shell, step, repo, path string) (stdout, stderr string, err error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), jvmStepTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-c", step)
	cmd.Dir = repo
	cmd.Env = []string{"PATH=" + path}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	return out.String(), errOut.String(), err
}

// jvmShell returns sh, or skips where the step's shell and file modes cannot be reproduced.
func jvmShell(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the step runs under sh on an ubuntu runner and hinges on POSIX file modes, which a Windows host has neither of")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("sh is not available to execute the build step: %v", err)
	}
	return shell
}

func TestJVMBuildStepRunsTheRepositorysBuild(t *testing.T) {
	shell := jvmShell(t)
	step := jvmBuildStep(t)
	for _, layout := range jvmLayouts() {
		t.Run(layout.name, func(t *testing.T) {
			repo, path := jvmFixture(t, shell, layout)
			stdout, stderr, err := runJVMStep(t, shell, step, repo, path)
			if layout.fails != "" {
				if err == nil || !strings.Contains(stderr, layout.fails) {
					t.Fatalf("want a failure naming %q, got err %v, stdout %q, stderr %q", layout.fails, err, stdout, stderr)
				}
				return
			}
			if err != nil || strings.TrimSpace(stdout) != layout.ran {
				t.Fatalf("want the step to run %q, got err %v, stdout %q, stderr %q", layout.ran, err, stdout, stderr)
			}
		})
	}
}

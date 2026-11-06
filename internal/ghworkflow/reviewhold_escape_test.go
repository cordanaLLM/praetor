// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ghworkflow

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// reviewTextModeJQ emulates native jq.exe's LF expansion unless binary mode is requested.
const reviewTextModeJQ = `jq() {
  if [[ "${1:-}" == --binary ]]; then
    command jq "$@"
  else
    command jq "$@" | sed $'s/$/\r/'
  fi
}
`

// TestReviewHoldPortableSubstitutions refuses quoting that Bash 3.2 preserves literally.
func TestReviewHoldPortableSubstitutions(t *testing.T) {
	quoted := regexp.MustCompile(`\$\{[^}\n]*//[^}\n]*['"][^}\n]*\}`)
	for name, rendered := range map[string]string{
		"hold": RenderReviewHoldStep(false), "release": RenderReviewHoldReleaseWorkflow(),
	} {
		if match := quoted.FindString(rendered); match != "" {
			t.Errorf("%s: Bash 3.2 retains substitution quotes: %s", name, match)
		}
	}
	for _, planted := range []string{`${title//':'/%3A}`, `${title//:/'%3A'}`, `${title//":"/"%3A"}`} {
		if !quoted.MatchString(planted) {
			t.Errorf("quoted substitution escaped static check: %s", planted)
		}
	}
	if quoted.MatchString(`${title//:/%3A}`) {
		t.Error("portable substitution refused")
	}
}

// TestReviewHoldAnnotationTitle decodes the annotation emitted by the rendered Bash fixture.
func TestReviewHoldAnnotationTitle(t *testing.T) {
	spec, err := Parse([]byte("jobs:\n  hold:\n    steps:\n" + RenderReviewHoldStep(false)))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"installed jq", "Windows text mode"} {
		t.Run(mode, func(t *testing.T) {
			dir := reviewFixtures(t)
			writeReviewFixture(t, dir, "checks.json", `{"check_runs":[{"name":"CodeRabbit %0D\r\nnext","status":"queued"}]}`)
			writeReviewFixture(t, dir, "expired-after-sleep", "")
			out, err := runReviewScript(t, dir, reviewJQMode(t, mode)+spec.Jobs["hold"].Steps[0].Run)
			assertReviewAnnotation(t, out, err)
		})
	}
}

// reviewJQMode enables the text-mode emulator only when its stream editor is available.
func reviewJQMode(t *testing.T, mode string) string {
	t.Helper()
	if mode != "Windows text mode" {
		return ""
	}
	if _, err := exec.LookPath("sed"); err != nil {
		t.Skip("sed absent: native jq.exe text-mode emulation requires sed")
	}
	return reviewTextModeJQ
}

// assertReviewAnnotation requires exact title/message round trips without extra annotation lines.
func assertReviewAnnotation(t *testing.T, out string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("open review passed: %s", out)
	}
	line := strings.SplitN(strings.TrimSpace(out), "\n", 2)[0]
	property, message, found := strings.Cut(strings.TrimPrefix(line, "::error title="), "::")
	decode := strings.NewReplacer("%3A", ":", "%0D", "\r", "%0A", "\n", "%25", "%")
	if !found || !strings.HasPrefix(line, "::error title=") || decode.Replace(property) != "Gate held: review open" {
		t.Fatalf("annotation=%q; want decoded title Gate held: review open", line)
	}
	if !strings.HasSuffix(decode.Replace(message), "bots=CodeRabbit %0D\r\nnext") || strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Fatalf("annotation message escaped incorrectly: %q", out)
	}
}

// TestReviewHoldReleaseTextModeJQ preserves scalar values and paths from native jq.exe.
func TestReviewHoldReleaseTextModeJQ(t *testing.T) {
	dir := reviewFixtures(t)
	out, err := runReviewScript(t, dir, reviewJQMode(t, "Windows text mode")+reviewHoldReleaseScript)
	if err != nil {
		t.Fatalf("release failed: %v; %s", err, out)
	}
	want := append([][]string{reviewRESTCall("pulls?state=open&sort=created&direction=asc", 1)}, reviewReleaseCalls("1", 5)...)
	assertReviewCalls(t, dir, want)
	reruns, err := readReviewFixture(t, dir, "reruns")
	if err != nil || string(reruns) != "42\n" {
		t.Fatalf("reruns=%q err=%v", reruns, err)
	}
}

// TestReviewScriptLiteralInput preserves long scripts and backslashes across the Bash boundary.
func TestReviewScriptLiteralInput(t *testing.T) {
	dir := reviewFixtures(t)
	want := strings.Repeat(`literal\\backslash `, 1024)
	out, err := runReviewScript(t, dir, "value='"+want+"'\nprintf '%s' \"$value\"\n")
	if err != nil || out != want {
		t.Fatalf("script transport: bytes=%d want=%d err=%v", len(out), len(want), err)
	}
}

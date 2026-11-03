// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ghworkflow

import (
	"regexp"
	"strings"
	"testing"
)

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
	dir := reviewFixtures(t)
	writeReviewFixture(t, dir, "checks.json", `{"check_runs":[{"name":"CodeRabbit %0D\r\nnext","status":"queued"}]}`)
	out, err := runReviewScript(t, dir, spec.Jobs["hold"].Steps[0].Run)
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

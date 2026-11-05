// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// TestOwnReleasePublisherPolicy plants missing, inherited and overridden package settings.
func TestOwnReleasePublisherPolicy(t *testing.T) {
	cases := []struct {
		name, config, publisher, want string
	}{
		{"missing key", `{"packages":{".":{}}}`, "      - uses: jellysin/release-helper/actions/publish@v1\n", "FAIL"},
		{"top level", `{"skip-github-release":true,"packages":{".":{},"lib":{}}}`, "      - uses: softprops/action-gh-release@v2\n", "PASS"},
		{"every package", `{"packages":{".":{"skip-github-release":true},"lib":{"skip-github-release":true}}}`, "      - uses: ncipollo/release-action@v1\n", "PASS"},
		{"mixed packages", `{"packages":{".":{"skip-github-release":true},"lib":{}}}`, "      - run: gh release create v1\n", "FAIL"},
		{"false overrides true", `{"skip-github-release":true,"packages":{".":{},"lib":{"skip-github-release":false}}}`, "      - run: gh release upload v1 dist/*\n", "FAIL"},
		{"true overrides false", `{"skip-github-release":false,"packages":{".":{"skip-github-release":true}}}`, "      - run: gh release edit v1 --draft=false\n", "PASS"},
		{"no publisher", `{"packages":{".":{}}}`, "      - run: echo ready\n", skipReleaseOwnNoPublisher},
		{"no config", "", "      - run: gh release create v1\n", skipReleaseOwnNoConfig},
	}
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			assertOwnReleaseAudit(t, "release-please-config.json", tc.config, tc.publisher, tc.want)
		})
	}
}

// assertOwnReleaseAudit exercises the shared CLI/MCP audit entry point using local fixtures.
func assertOwnReleaseAudit(t *testing.T, path, config, steps, want string) {
	t.Helper()
	files := map[string]string{
		".github/workflows/release-please.yml": passingReleaseWorkflow,
	}
	files[".github/workflows/publish.yml"] = releaseSteps(steps)
	if config != "" {
		files[path] = config
	}
	out, err := AuditReleaseToken(context.Background(), ReleaseTokenOptions{Root: supplyChainRepo(t, files)})
	if err != nil {
		out = err.Error()
	}
	if (err != nil) != (want == "FAIL") {
		t.Fatalf("audit error = %v; want %s; output: %s", err, want, out)
	}
	if strings.HasPrefix(want, "[SKIP]") {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q: %s", want, out)
		}
		return
	}
	if want == "" {
		if strings.Contains(out, "release-own-publisher") {
			t.Fatalf("unexpected own-publisher finding: %s", out)
		}
		return
	}
	for _, text := range []string{"[" + want + "] release-own-publisher:", path, ".github/workflows/publish.yml", "skip-github-release"} {
		if !strings.Contains(out, text) {
			t.Errorf("output missing %q: %s", text, out)
		}
	}
}

// TestOwnReleasePublisherCommands covers publishers without matching printed or draft-only text.
func TestOwnReleasePublisherCommands(t *testing.T) {
	cases := []struct{ name, script, want string }{
		{"create", "gh release create v1 --draft", "FAIL"},
		{"upload", "gh release upload v1 dist/*", "FAIL"},
		{"publish edit", "gh release edit v1 --draft=false", "FAIL"},
		{"publish continued", "gh release edit v1 \\\n            --draft=false", "FAIL"},
		{"publish after check", "test -f dist/a && gh release create v1 dist/a", "FAIL"},
		{"print", "echo 'gh release create v1'", skipReleaseOwnNoPublisher},
		{"comment", "# gh release upload v1 dist/*", skipReleaseOwnNoPublisher},
		{"draft edit", "gh release edit v1 --draft=true", skipReleaseOwnNoPublisher},
		{"view", "gh release view v1", skipReleaseOwnNoPublisher},
	}
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			steps := "      - run: |\n          " + strings.ReplaceAll(tc.script, "\n", "\n          ") + "\n"
			assertOwnReleaseAudit(t, "release-please-config.json", `{"packages":{".":{}}}`, steps, tc.want)
		})
	}
}

// TestOwnReleasePublisherConfigBoundaries covers every known path and malformed settings.
func TestOwnReleasePublisherConfigBoundaries(t *testing.T) {
	cases := []struct{ name, path, config, want string }{
		{"github config", ".github/release-please-config.json", `{"packages":{".":{}}}`, "FAIL"},
		{"github passing config", ".github/release-please-config.json", `{"skip-github-release":true,"packages":{".":{}}}`, "PASS"},
		{"root manifest alone", ".release-please-manifest.json", `{".":"1.0.0"}`, "FAIL"},
		{"github manifest alone", ".github/.release-please-manifest.json", `{".":"1.0.0"}`, "FAIL"},
		{"invalid json", "release-please-config.json", `{`, "FAIL"},
		{"duplicate setting", "release-please-config.json", `{"skip-github-release":false,"skip-github-release":true,"packages":{".":{}}}`, "FAIL"},
		{"non boolean", "release-please-config.json", `{"skip-github-release":"true","packages":{".":{}}}`, "FAIL"},
		{"non object package", "release-please-config.json", `{"skip-github-release":true,"packages":{".":null}}`, "FAIL"},
		{"empty packages", "release-please-config.json", `{"skip-github-release":true,"packages":{}}`, "FAIL"},
		{"null inherits", "release-please-config.json", `{"skip-github-release":true,"packages":{".":{"skip-github-release":null}}}`, "PASS"},
		{"legacy root", "release-please-config.json", `{"skip-github-release":true}`, "PASS"},
		{"case sensitive packages", "release-please-config.json", `{"skip-github-release":true,"packages":{"A":{},"a":{}}}`, "PASS"},
	}
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			assertOwnReleaseAudit(t, tc.path, tc.config, "      - uses: softprops/action-gh-release@v2\n", tc.want)
		})
	}
}

// TestOwnReleasePublisherWorkflowBoundaries excludes inactive steps and unrelated action paths.
func TestOwnReleasePublisherWorkflowBoundaries(t *testing.T) {
	cases := []struct{ name, steps string }{
		{"disabled step", "      - if: false\n        uses: softprops/action-gh-release@v2\n"},
		{"similar action", "      - uses: softprops/action-gh-release-other@v2\n"},
		{"uses in text", "      - run: echo softprops/action-gh-release@v2\n"},
	}
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			assertOwnReleaseAudit(t, "release-please-config.json", `{"packages":{".":{}}}`, tc.steps, skipReleaseOwnNoPublisher)
		})
	}
}

// TestOwnReleasePublisherManifestWithConfig proves a version manifest is not read as config.
func TestOwnReleasePublisherManifestWithConfig(t *testing.T) {
	files := map[string]string{
		".release-please-manifest.json": `{".":"1.0.0"}`,
	}
	files["release-please-config.json"] = `{"skip-github-release":true,"packages":{".":{}}}`
	files[".github/workflows/publish.yml"] = releaseSteps("      - run: gh release create v1\n")
	out, err := AuditReleaseToken(context.Background(), ReleaseTokenOptions{Root: supplyChainRepo(t, files)})
	if err != nil || !strings.Contains(out, "[PASS] release-own-publisher:") {
		t.Fatalf("audit = %q, %v; want own publisher pass without a release-please step", out, err)
	}
}

// TestOwnReleasePublisherPackageBound refuses truncation and checks the last allowed package.
func TestOwnReleasePublisherPackageBound(t *testing.T) {
	cases := []struct {
		name, last, want string
		count            int
	}{
		{"at cap", `{}`, "PASS", maxReleasePackages},
		{"last package false", `{"skip-github-release":false}`, "FAIL", maxReleasePackages},
		{"over cap", `{}`, "FAIL", maxReleasePackages + 1},
	}
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			packages := make([]string, 0, tc.count)
			for j := 0; j < tc.count && j <= maxReleasePackages; j++ {
				value := `{}`
				if j == tc.count-1 {
					value = tc.last
				}
				packages = append(packages, fmt.Sprintf(`"p%04d":%s`, j, value))
			}
			config := `{"skip-github-release":true,"packages":{` + strings.Join(packages, ",") + `}}`
			assertOwnReleaseAudit(t, "release-please-config.json", config, "      - run: gh release upload v1 dist/*\n", tc.want)
		})
	}
}

// TestOwnReleasePublisherAllConfigs checks every configuration and reports every publisher.
func TestOwnReleasePublisherAllConfigs(t *testing.T) {
	files := map[string]string{"release-please-config.json": `{"skip-github-release":true,"packages":{".":{}}}`}
	files[".github/release-please-config.json"] = `{"packages":{".":{}}}`
	files[".github/workflows/first.yml"] = releaseSteps("      - run: gh release create v1\n")
	files[".github/workflows/second.yaml"] = releaseSteps("      - uses: ncipollo/release-action@v1\n")
	_, err := AuditReleaseToken(context.Background(), ReleaseTokenOptions{Root: supplyChainRepo(t, files)})
	if err == nil {
		t.Fatal("audit passed; want failure for second config")
	}
	for _, text := range []string{"[FAIL] release-own-publisher:", ".github/release-please-config.json", "first.yml", "second.yaml"} {
		if !strings.Contains(err.Error(), text) {
			t.Errorf("error missing %q: %v", text, err)
		}
	}
}

// TestAuditOwnReleasePublisherSkip verifies direct skip reports for missing config and absent publisher.
func TestAuditOwnReleasePublisherSkip(t *testing.T) {
	t.Run("no config", func(t *testing.T) {
		root := supplyChainRepo(t, map[string]string{
			".github/workflows/publish.yml": releaseSteps("      - run: gh release create v1\n"),
		})
		out, err := AuditOwnReleasePublisher(context.Background(), root)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != skipReleaseOwnNoConfig {
			t.Errorf("out = %q; want %q", out, skipReleaseOwnNoConfig)
		}
	})

	t.Run("no publisher", func(t *testing.T) {
		root := supplyChainRepo(t, map[string]string{
			"release-please-config.json": `{"packages":{".":{}}}`,
			".github/workflows/ci.yml":   releaseSteps("      - run: echo ready\n"),
		})
		out, err := AuditOwnReleasePublisher(context.Background(), root)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != skipReleaseOwnNoPublisher {
			t.Errorf("out = %q; want %q", out, skipReleaseOwnNoPublisher)
		}
	})
}

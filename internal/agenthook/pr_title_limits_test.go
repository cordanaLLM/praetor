// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// TestForgeSettingsSelection preserves selected empty settings and refuses selection errors.
func TestForgeSettingsSelection(t *testing.T) {
	selected := config.ForgeSettings{
		PRTitleLimits:    []config.PRTitleLimit{{Repo: "example/repo", Max: 64}},
		OwnOrganizations: []string{"fleet-org"},
	}
	cause := errors.New("selected settings unavailable")
	for _, tc := range []struct {
		name string
		in   Invocation
		want config.ForgeSettings
		err  error
	}{
		{"selected without environment", Invocation{Forge: &selected}, selected, nil},
		{"selected overrides environment", Invocation{Forge: &selected, Getenv: titleLimitEnvironment(t, "")}, selected, nil},
		{"selected empty overrides environment", Invocation{Forge: &config.ForgeSettings{}, Getenv: titleLimitEnvironment(t, "")}, config.ForgeSettings{}, nil},
		{"selection error", Invocation{Forge: &selected, SettingsError: cause}, config.ForgeSettings{}, cause},
		{"unselected error", Invocation{SettingsError: cause}, config.ForgeSettings{}, cause},
		{"unselected without environment", Invocation{}, config.ForgeSettings{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := loadOperatorForgeSettings(t.Context(), tc.in)
			if !errors.Is(err, tc.err) || !slices.Equal(got.PRTitleLimits, tc.want.PRTitleLimits) ||
				!slices.Equal(got.OwnOrganizations, tc.want.OwnOrganizations) {
				t.Fatalf("forge settings %+v, error %v; want %+v, %v", got, err, tc.want, tc.err)
			}
		})
	}
}

// titleLimitEnvironment supplies only a fixture operator file and optional GH_REPO.
func titleLimitEnvironment(t *testing.T, ghRepo string) func(string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fleet.yaml")
	writeTestFixtureFile(t, path, "forge: {pr_title_limits: [{repo: example/widget, max: 64}]}\n")
	return func(key string) string {
		if key == config.FleetConfigEnv {
			return path
		}
		if key == "GH_REPO" {
			return ghRepo
		}
		return ""
	}
}

// TestPRTitleLimitsPreToolPlants65And64 exercises the actual pre-tool boundary.
func TestPRTitleLimitsPreToolPlants65And64(t *testing.T) {
	root := repository(t, true)
	getenv := titleLimitEnvironment(t, "")
	t.Run("65 denied", func(t *testing.T) {
		title := strings.Repeat("x", 65)
		response := runPreToolCommand(t, root, `gh pr create --repo example/widget --title "`+title+`"`, getenv)
		assertPRTitle65Denied(t, response, title)
	})
	t.Run("64 allowed", func(t *testing.T) {
		title := strings.Repeat("x", 64)
		response := runPreToolCommand(t, root, `gh pr create --repo example/widget --title "`+title+`"`, getenv)
		if response.ExitCode != 0 {
			t.Fatalf("64 must pass: %+v", response)
		}
	})
}

// assertPRTitle65Denied requires the rule, target, measured length, cap and offending title.
func assertPRTitle65Denied(t *testing.T, response Response, title string) {
	t.Helper()
	reason := string(response.Stderr)
	if response.ExitCode != 2 {
		t.Fatalf("65 must deny, got exit %d: %s", response.ExitCode, reason)
	}
	for _, want := range []string{"pr-title-limit", "example/widget", "65", "64", title} {
		if !strings.Contains(reason, want) {
			t.Errorf("refusal missing %q: %s", want, reason)
		}
	}
}

// TestPRTitleLimitsResolutionAndForms makes every repository flag override a conflicting origin and environment.
func TestPRTitleLimitsResolutionAndForms(t *testing.T) {
	root := repository(t, false)
	testsupport.RunFixtureGit(t, root, []string{"remote", "add", "origin", "https://github.com/example/other.git"})
	long := strings.Repeat("é", 65)
	for _, form := range []string{
		"gh pr create --repo example/widget", "gh pr create -R EXAMPLE/WIDGET",
		"gh -R example/widget pr create", "gh --repo=example/widget pr create",
		"gh -Rexample/widget pr create", "gh -R=example/widget pr create",
		"gh pr -R example/widget create", "gh pr create --repo=example/widget",
		"gh pr create -R=example/widget", "gh pr create -Rexample/widget",
		"gh pr create -R github.com/example/widget", "gh pr create -R https://github.com/example/widget",
		"gh pr create -R http://github.com/example/widget",
		"GH_REPO=example/other gh -Rexample/widget pr create",
		"gh --hostname github.com -R example/widget pr create",
		"gh --hostname=github.com pr create -R example/widget",
		"gh -Rexample/widget --hostname github.com pr create",
		"gh pr --hostname github.com create -R example/widget",
	} {
		t.Run(form, func(t *testing.T) {
			got := assertTitleLimitVerdict(t, root, form+` --title="`+long+`"`, "example/other", true)
			if !strings.Contains(got.Reason, "title length 65 exceeds limit 64") {
				t.Fatalf("wrong title measurement: %+v", got)
			}
		})
	}
}

// assertTitleLimitVerdict checks the rule directly so another hook cannot mask a missed title.
func assertTitleLimitVerdict(t *testing.T, root, command, inherited string, deny bool) Verdict {
	t.Helper()
	got := evaluatePRTitleLimits(t.Context(), Canonical{Command: command}, Invocation{
		WorkDir: root, Getenv: titleLimitEnvironment(t, inherited),
	})
	if (got.Outcome == Deny) != deny || (deny && !strings.Contains(got.Reason, "pr-title-limit")) {
		t.Fatalf("deny=%v: %+v", deny, got)
	}
	return got
}

// TestPRTitleLimitsRepositorySources plants each precedence source above a conflicting origin.
func TestPRTitleLimitsRepositorySources(t *testing.T) {
	long := ` -t "` + strings.Repeat("x", 65) + `"`
	for _, tc := range []struct{ name, origin, resolved, inherited, command string }{
		{"origin", "widget", "", "", "gh pr create"},
		{"inherited", "other", "", "example/widget", "gh pr create"},
		{"inherited URL", "other", "", "https://github.com/example/widget", "gh pr create"},
		{"assignment", "other", "", "example/other", "GH_REPO=example/widget gh pr create"},
		{"env", "other", "", "example/other", "env GH_REPO=example/widget gh pr create"},
		{"quoted assignment", "other", "", "example/other", "GH_REPO='example/widget' gh pr create"},
		{"env URL", "other", "", "example/other", "env GH_REPO=http://github.com/example/widget gh pr create"},
		{"empty local", "widget", "", "example/other", "GH_REPO= gh pr create"},
		{"default base", "other", "base", "", "gh pr create"},
		{"default name", "other", "example/widget", "", "gh pr create"},
		{"inherited above default", "other", "example/other", "example/widget", "gh pr create"},
		{"local above default", "other", "example/other", "example/other", "GH_REPO=example/widget gh pr create"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := repository(t, false)
			testsupport.RunFixtureGit(t, root, []string{"remote", "add", "origin", "https://github.com/example/" + tc.origin + ".git"})
			if tc.resolved != "" {
				testsupport.RunFixtureGit(t, root,
					[]string{"remote", "add", "upstream", "https://github.com/example/widget.git"},
					[]string{"config", "remote.upstream.gh-resolved", tc.resolved})
			}
			got := assertTitleLimitVerdict(t, root, tc.command+long, tc.inherited, true)
			if !strings.Contains(got.Reason, "title length 65 exceeds limit 64") {
				t.Fatalf("wrong source: %+v", got)
			}
		})
	}
}

// TestPRTitleLimitsTitleData distinguishes literal values, omitted titles and shell expansion.
func TestPRTitleLimitsTitleData(t *testing.T) {
	root := repository(t, false)
	long, boundary := strings.Repeat("x", 65), strings.Repeat("x", 64)
	for _, tc := range []struct {
		name, args string
		deny       bool
	}{
		{"separate", `-t "` + long + `"`, true},
		{"inline", `--title="` + long + `"`, true},
		{"short equals", `-t="` + long + `"`, true},
		{"attached", `-t"` + long + `"`, true},
		{"boundary", `-t "` + boundary + `"`, false},
		{"unicode", `-t "` + strings.Repeat("é", 65) + `"`, true},
		{"decode inline once", `'--title=` + boundary + `"'`, true},
		{"decode attached once", `'-t` + boundary + `"'`, true},
		{"decode separate once", `-t '` + boundary + `"'`, true},
		{"body value", `--body '--title' -t "` + long + `"`, true},
		{"template", `--template ` + long, false},
		{"template title value", `--template --title`, false},
		{"inline template", `--template=` + long, false},
		{"body file title value", `--body-file --title`, false},
		{"fill", `--fill`, false},
		{"interactive", ``, false},
		{"parameter", `-t "$TITLE"`, true},
		{"braced parameter", `--title="${TITLE}"`, true},
		{"substitution", `-t "$(printf title)"`, true},
		{"backtick", "-t \"`printf title`\"", true},
		{"glob", `-t *`, true},
		{"literal parameter", `-t '$TITLE'`, false},
		{"escaped parameter", `-t "\$TITLE"`, false},
		{"quoted glob", `-t '*'`, false},
		{"last title wins", `-t "` + long + `" -t ok`, false},
		{"last expanded title", `-t ok -t "$TITLE"`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := assertTitleLimitVerdict(t, root, "gh pr create "+tc.args, "example/widget", tc.deny)
			if strings.HasPrefix(tc.name, "decode") && !strings.Contains(got.Reason, "title length 65 exceeds limit 64") {
				t.Fatalf("literal quote lost: %+v", got)
			}
		})
	}
}

// TestPRTitleLimitsNoLimitsSkipsShellReader plants unrelated commands above each title-reader bound.
func TestPRTitleLimitsNoLimitsSkipsShellReader(t *testing.T) {
	root := repository(t, false)
	got := evaluatePRTitleLimits(t.Context(), Canonical{Command: "echo " + strings.Repeat("x ", maxForgeTextWords+1)},
		Invocation{WorkDir: root, Forge: &config.ForgeSettings{}})
	if got.Outcome != Allow {
		t.Fatalf("no limits must skip parsing: %+v", got)
	}
	response := runPreToolCommand(t, root, strings.Repeat("echo ok;", maxForgeTextSegments+1), noEnvironment)
	if response.ExitCode != 0 {
		t.Fatalf("ungoverned command affected: %+v", response)
	}
}

// TestPRTitleLimitsURLAndCommandBoundaries pins valid URL targets and non-create commands.
func TestPRTitleLimitsURLAndCommandBoundaries(t *testing.T) {
	root := repository(t, false)
	long := strings.Repeat("x", 65)
	for _, tc := range []struct {
		command, inherited string
		deny               bool
	}{
		{`gh pr create -R https://github.com/example/widget -t ok`, "example/other", false},
		{`gh pr create -R http://github.com/example/widget -t ok`, "example/other", false},
		{`gh pr create -t ok`, "https://github.com/example/widget", false},
		{`GH_REPO=http://github.com/example/widget gh pr create -t ok`, "example/other", false},
		{`gh pr create -R example/other -t "$TITLE"`, "example/widget", false},
		{`gh pr create -t ok; gh pr create -t "` + long + `"`, "example/widget", true},
		{`gh pr edit 1 --title "` + long + `"`, "example/widget", false},
		{`gh issue create --title "` + long + `"`, "example/widget", false},
		{`gh pr create -R invalid -t ok`, "example/widget", true},
		{`gh pr create --title ok -- --title "` + long + `"`, "example/widget", false},
		{`gh pr create -t "` + long + `" GH_REPO=example/other`, "example/widget", true},
	} {
		t.Run(tc.command, func(t *testing.T) {
			assertTitleLimitVerdict(t, root, tc.command, tc.inherited, tc.deny)
		})
	}
}

// TestPRTitleLimitsEveryPreToolClient checks every registered client and ungoverned checkout.
func TestPRTitleLimitsEveryPreToolClient(t *testing.T) {
	root := repository(t, false)
	for _, client := range []string{"claude", "codex", "gemini", "lefthook", "agy"} {
		t.Run(client, func(t *testing.T) {
			command := `gh pr create -R example/widget -t "` + strings.Repeat("x", 65) + `"`
			payload := map[string]any{"tool_input": map[string]any{"command": command}}
			if client == "agy" {
				payload = map[string]any{"toolCall": map[string]any{"name": "run_command", "args": map[string]any{"CommandLine": command}}}
			}
			response := Run(t.Context(), Invocation{Client: client, Event: "pre-tool", WorkDir: root,
				Stdin: bytes.NewReader(commandPayload(t, payload)), Getenv: titleLimitEnvironment(t, ""), Policy: policy(t)})
			if client == "agy" {
				if !strings.Contains(string(response.Stdout), `"decision":"deny"`) {
					t.Fatalf("agy allowed: %+v", response)
				}
			} else if response.ExitCode != denyExit(client) {
				t.Fatalf("client %s exit %d: %s", client, response.ExitCode, response.Stderr)
			}
			if !strings.Contains(string(response.Stdout)+string(response.Stderr), "pr-title-limit") {
				t.Fatalf("wrong rule: %+v", response)
			}
		})
	}
}

// TestPRTitleLimitsReadFailures denies missing settings and unresolved targets by name.
func TestPRTitleLimitsReadFailures(t *testing.T) {
	root := repository(t, false)
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	response := runPreToolCommand(t, root, `gh pr create -R example/widget -t ok`, func(key string) string {
		if key == config.FleetConfigEnv {
			return missing
		}
		return ""
	})
	for _, want := range []string{"pr-title-limit", "operator forge settings unavailable", "missing.yaml"} {
		if response.ExitCode != 2 || !strings.Contains(string(response.Stderr), want) {
			t.Fatalf("missing %s: %+v", want, response)
		}
	}
	response = runPreToolCommand(t, root, `gh pr create -t ok`, titleLimitEnvironment(t, ""))
	if response.ExitCode != 2 || !strings.Contains(string(response.Stderr), "target repository unavailable") {
		t.Fatalf("unresolved target: %+v", response)
	}
	response = runPreToolCommand(t, root, `gh pr create -t ok`, noEnvironment)
	if response.ExitCode != 0 {
		t.Fatalf("no configured limits: %+v", response)
	}
}

// TestPRTitleLimitsCommandDirectory reuses scoped cd tracking and canonical command cwd.
func TestPRTitleLimitsCommandDirectory(t *testing.T) {
	outer, inner := repository(t, true), repository(t, false)
	testsupport.RunFixtureGit(t, inner, []string{"remote", "add", "origin", "https://github.com/example/widget.git"})
	command := `cd "` + inner + `" && gh pr create -t "` + strings.Repeat("x", 65) + `"`
	response := runPreToolCommand(t, outer, command, titleLimitEnvironment(t, ""))
	if response.ExitCode != 2 || !strings.Contains(string(response.Stderr), "pr-title-limit") {
		t.Fatalf("cd origin: %+v", response)
	}
	verdict := evaluatePRTitleLimits(context.Background(), Canonical{Command: `gh pr create -t "` + strings.Repeat("x", 65) + `"`, CommandDir: inner}, Invocation{WorkDir: outer, Getenv: titleLimitEnvironment(t, "")})
	if verdict.Outcome != Deny {
		t.Fatalf("canonical cwd: %+v", verdict)
	}
}

// TestPRTitleLimitsEngineCheckout reads back the hook from this engine checkout without writes.
func TestPRTitleLimitsEngineCheckout(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "internal", "agenthook", "evaluate.go")); err != nil {
		t.Fatal(err)
	}
	for _, count := range []int{65, 64} {
		response := runPreToolCommand(t, root, `gh pr create -R example/widget -t "`+strings.Repeat("x", count)+`"`, titleLimitEnvironment(t, ""))
		if (response.ExitCode == 2) != (count == 65) || (count == 64 && response.ExitCode != 0) {
			t.Fatalf("engine checkout, length %d: %+v", count, response)
		}
		t.Logf("engine checkout %s, length %d, exit %d, refusal %s", root, count, response.ExitCode, response.Stderr)
	}
}

// TestPRTitleLimitsShortClusters plants pflag clusters and their value boundaries.
func TestPRTitleLimitsShortClusters(t *testing.T) {
	root := repository(t, false)
	for _, prefix := range []string{"-dt ", "-dt", "-dt=", "-eft ", "-wt ", "-ht ", "-dR example/widget -dt "} {
		t.Run(prefix, func(t *testing.T) {
			assertPRTitleBoundary(t, root, "gh pr create -R example/widget "+prefix, " --body ok", "example/other")
		})
	}
	for _, args := range []string{"-db --title -t ", "-db--title -t ", "-d=false -t "} {
		t.Run(args, func(t *testing.T) {
			assertPRTitleBoundary(t, root, "gh pr create -R example/widget "+args, "", "example/other")
		})
	}
}

// assertPRTitleBoundary requires the cap to pass and the next Unicode length to fail.
func assertPRTitleBoundary(t *testing.T, root, prefix, suffix, inherited string) {
	t.Helper()
	for _, count := range []int{64, 65} {
		title := strings.Repeat("é", count)
		got := assertTitleLimitVerdict(t, root, prefix+`"`+title+`"`+suffix, inherited, count == 65)
		if count == 65 && !strings.Contains(got.Reason, "title length 65 exceeds limit 64") {
			t.Fatalf("wrong target or measurement: %+v", got)
		}
	}
}

// TestPRTitleLimitsUnknownCluster refuses an unknown shorthand only for a capped target.
func TestPRTitleLimitsUnknownCluster(t *testing.T) {
	root := repository(t, false)
	for _, repo := range []string{"example/widget", "example/other"} {
		for _, count := range []int{64, 65} {
			command := "gh pr create -R " + repo + ` -dzt "` + strings.Repeat("x", count) + `" --body ok`
			got := assertTitleLimitVerdict(t, root, command, "", repo == "example/widget")
			if repo == "example/widget" && !strings.Contains(got.Reason, `unknown shorthand "-z"`) {
				t.Fatalf("unknown shorthand not named: %+v", got)
			}
		}
	}
	assertTitleLimitVerdict(t, root, "gh pr create -R example/widget -dz --body ok", "", true)
}

// TestPRTitleLimitsRemotePrecedence plants conflicting remotes without a gh-resolved default.
func TestPRTitleLimitsRemotePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		remotes [][2]string
	}{
		{"upstream before origin", [][2]string{{"origin", "other"}, {"upstream", "widget"}}},
		{"github before origin", [][2]string{{"origin", "other"}, {"github", "widget"}}},
		{"upstream before github", [][2]string{{"github", "other"}, {"upstream", "widget"}}},
		{"case insensitive upstream", [][2]string{{"origin", "other"}, {"UPSTREAM", "widget"}}},
		{"origin before others", [][2]string{{"another", "other"}, {"origin", "widget"}}},
		{"other remote", [][2]string{{"another", "widget"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := repository(t, false)
			for _, remote := range tc.remotes {
				testsupport.RunFixtureGit(t, root, []string{"remote", "add", remote[0], "https://github.com/example/" + remote[1] + ".git"})
			}
			assertPRTitleBoundary(t, root, "gh pr create -t ", " --body ok", "")
		})
	}
	root := repository(t, false)
	testsupport.RunFixtureGit(t, root,
		[]string{"remote", "add", "origin", "https://github.com/example/widget.git"},
		[]string{"remote", "add", "upstream", "https://github.com/example/other.git"})
	assertTitleLimitVerdict(t, root, `gh pr create -t "`+strings.Repeat("x", 65)+`"`, "", false)
}

// TestPRTitleLimitsNormalizedTargets plants suffix and transport variants at both lengths.
func TestPRTitleLimitsNormalizedTargets(t *testing.T) {
	root := repository(t, false)
	testsupport.RunFixtureGit(t, root, []string{"remote", "add", "origin", "https://github.com/example/other.git"})
	for _, target := range []string{
		"https://github.com/example/widget.git", "https://github.com/example/widget/",
		"https://github.com/example/widget.git/", "http://github.com/example/widget.git/",
		"git@github.com:example/widget.git", "git@github.com:example/widget/",
		"git@github.com:example/widget.git/", "ssh://git@github.com/example/widget.git",
		"ssh://git@github.com/example/widget/", "ssh://git@github.com/example/widget.git/",
	} {
		t.Run(target, func(t *testing.T) {
			assertPRTitleBoundary(t, root, "gh pr create -R "+target+" -t ", "", "example/other")
			assertPRTitleBoundary(t, root, "gh pr create -t ", "", target)
		})
	}
}

// TestPRTitleLimitsEnvRemoval prevents inherited GH_REPO from returning after env removes it.
func TestPRTitleLimitsEnvRemoval(t *testing.T) {
	root := repository(t, false)
	testsupport.RunFixtureGit(t, root, []string{"remote", "add", "origin", "https://github.com/example/widget.git"})
	for _, wrapper := range []string{
		"env -u GH_REPO", "env --unset=GH_REPO", "env --unset GH_REPO",
		"env -u 'GH_REPO'", "GH_REPO=example/other env -u GH_REPO",
		"env -u GH_REPO GH_REPO=example/widget", "env --unset=OTHER",
	} {
		t.Run(wrapper, func(t *testing.T) {
			inherited := "example/other"
			if wrapper == "env --unset=OTHER" {
				inherited = "example/widget"
			}
			assertPRTitleBoundary(t, root, wrapper+" gh pr create -t ", " --body ok", inherited)
		})
	}
	assertTitleLimitVerdict(t, root, `env -u GH_REPO GH_REPO=example/other gh pr create -t "`+strings.Repeat("x", 65)+`"`, "example/widget", false)
}

// Guide clauses pin the documented precedence to executable resolution cases.
const (
	prTitleGuideFlags      = "1. Use an explicit repository flag (`--repo VALUE`, `--repo=VALUE`, `-R VALUE`, `-RVALUE` or `-R=VALUE`) before any environment or checkout source."
	prTitleGuidePrefix     = "2. Use a nonempty command-prefix `GH_REPO` assignment before the inherited environment."
	prTitleGuideRemoval    = "3. An `env` removal of `GH_REPO` suppresses its inherited value; continue at step 6."
	prTitleGuideEmpty      = "4. An empty command-prefix assignment also suppresses inherited `GH_REPO`; continue at step 6."
	prTitleGuideInherited  = "5. Otherwise, use inherited `GH_REPO` when nonempty, before checkout configuration."
	prTitleGuideDefault    = "6. Read the command directory's local `remote.<name>.gh-resolved` keys. Use the first nonempty value: `base` selects that named remote's repository, and any other value names the repository directly."
	prTitleGuideRemotes    = "7. Without a default, rank network remotes case-insensitively: `upstream`, then `github`, then `origin`, then other names. Equal ranks keep the first name returned by `git remote`."
	prTitleGuideUnresolved = "8. If nothing resolves, refuse an explicit-title creation when any title limits are configured. With no limits, allow this check without resolving the target."
)

// prTitleRepositoryCase binds a documented selection step to conflicting policy targets.
type prTitleRepositoryCase struct {
	name, command, inherited string
	remotes                  [][2]string
	resolved                 [2]string
	want, guide              string
	noLimits                 bool
}

// prTitleRepositoryCases covers every guide step and adjacent precedence boundary.
func prTitleRepositoryCases() []prTitleRepositoryCase {
	all := [][2]string{{"upstream", "upstream"}, {"github", "github"}, {"origin", "origin"}, {"another", "another"}}
	defaults := [2]string{"origin", "example/default"}
	return []prTitleRepositoryCase{
		{"flag over prefix", "GH_REPO=example/local gh pr create --repo example/flag", "example/inherited", all, defaults, "example/flag", prTitleGuideFlags, false},
		{"short flag", "gh pr create -R example/flag", "example/inherited", all, defaults, "example/flag", prTitleGuideFlags, false},
		{"long equals flag", "gh pr create --repo=example/flag", "example/inherited", all, defaults, "example/flag", prTitleGuideFlags, false},
		{"attached flag", "gh pr create -Rexample/flag", "example/inherited", all, defaults, "example/flag", prTitleGuideFlags, false},
		{"short equals flag", "gh pr create -R=example/flag", "example/inherited", all, defaults, "example/flag", prTitleGuideFlags, false},
		{"prefix over inherited", "GH_REPO=example/local gh pr create", "example/inherited", all, defaults, "example/local", prTitleGuidePrefix, false},
		{"env prefix", "env GH_REPO=example/local gh pr create", "example/inherited", all, defaults, "example/local", prTitleGuidePrefix, false},
		{"env removal to default", "env -u GH_REPO gh pr create", "example/inherited", all, defaults, "example/default", prTitleGuideRemoval, false},
		{"env removal to remotes", "env --unset=GH_REPO gh pr create", "example/inherited", all, [2]string{}, "example/upstream", prTitleGuideRemoval, false},
		{"empty prefix to default", "GH_REPO= gh pr create", "example/inherited", all, defaults, "example/default", prTitleGuideEmpty, false},
		{"empty prefix to remotes", "GH_REPO='' gh pr create", "example/inherited", all, [2]string{}, "example/upstream", prTitleGuideEmpty, false},
		{"inherited over default", "gh pr create", "example/inherited", all, defaults, "example/inherited", prTitleGuideInherited, false},
		{"default name over ranked", "gh pr create", "", all, defaults, "example/default", prTitleGuideDefault, false},
		{"default base over ranked", "gh pr create", "", all, [2]string{"origin", "base"}, "example/origin", prTitleGuideDefault, false},
		{"empty default to ranked", "gh pr create", "", all, [2]string{"origin", ""}, "example/upstream", prTitleGuideDefault, false},
		{"upstream over github", "gh pr create", "", all, [2]string{}, "example/upstream", prTitleGuideRemotes, false},
		{"github over origin", "gh pr create", "", all[1:], [2]string{}, "example/github", prTitleGuideRemotes, false},
		{"origin over other", "gh pr create", "", all[2:], [2]string{}, "example/origin", prTitleGuideRemotes, false},
		{"other remote", "gh pr create", "", all[3:], [2]string{}, "example/another", prTitleGuideRemotes, false},
		{"case insensitive rank", "gh pr create", "", [][2]string{{"UPSTREAM", "upstream"}, {"github", "github"}}, [2]string{}, "example/upstream", prTitleGuideRemotes, false},
		{"equal rank first", "gh pr create", "", [][2]string{{"zeta", "zeta"}, {"alpha", "alpha"}}, [2]string{}, "example/alpha", prTitleGuideRemotes, false},
		{"unresolved with limits", "gh pr create", "", nil, [2]string{}, "", prTitleGuideUnresolved, false},
		{"unresolved without limits", "gh pr create", "", nil, [2]string{}, "", prTitleGuideUnresolved, true},
	}
}

// TestPRTitleLimitsRepositoryOrder binds the numbered guide to selected repositories and caps.
func TestPRTitleLimitsRepositoryOrder(t *testing.T) {
	guide, err := os.ReadFile(filepath.Join("..", "..", "docs", "guides", "agent-hooks.md"))
	if err != nil {
		t.Fatal(err)
	}
	cases := prTitleRepositoryCases()
	for i := 0; i < len(cases) && i < 32; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(string(guide), tc.guide) {
				t.Errorf("guide missing selection step %q", tc.guide)
			}
			assertPRTitleRepositoryCase(t, tc)
		})
	}
}

// assertPRTitleRepositoryCase resolves a real fixture and checks selected and unrelated caps.
func assertPRTitleRepositoryCase(t *testing.T, tc prTitleRepositoryCase) {
	t.Helper()
	root := repository(t, false)
	for i := 0; i < len(tc.remotes) && i < 8; i++ {
		remote := tc.remotes[i]
		testsupport.RunFixtureGit(t, root, []string{"remote", "add", remote[0], "https://github.com/example/" + remote[1] + ".git"})
	}
	if tc.resolved[0] != "" {
		testsupport.RunFixtureGit(t, root, []string{"config", "remote." + tc.resolved[0] + ".gh-resolved", tc.resolved[1]})
	}
	ctx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	command := tc.command + ` --title="` + strings.Repeat("x", 65) + `"`
	calls, err := collectGHWriteCalls(command, root)
	if err != nil {
		t.Fatal(err)
	}
	candidates := explicitPRTitleCalls(calls)
	if len(candidates) != 1 {
		t.Fatalf("title calls = %d; want 1", len(candidates))
	}
	candidate := candidates[0]
	getenv := titleLimitEnvironment(t, tc.inherited)
	got, err := resolvePRTitleRepository(ctx, candidate.call, candidate.repo, getenv)
	if got != tc.want || (err != nil) != (tc.want == "") {
		t.Fatalf("repository = %q, error %v; want %q", got, err, tc.want)
	}
	assertPRTitleRepositoryCaps(t, ctx, candidate, getenv, tc.want)
	if tc.noLimits {
		assertPRTitleNoLimits(t, ctx, root, command)
	}
}

// assertPRTitleNoLimits checks that empty operator limits bypass target resolution.
func assertPRTitleNoLimits(t *testing.T, ctx context.Context, root, command string) {
	t.Helper()
	got := evaluatePRTitleLimits(ctx, Canonical{Command: command}, Invocation{WorkDir: root, Forge: &config.ForgeSettings{}})
	if got.Outcome != Allow {
		t.Fatalf("no limits: %+v", got)
	}
}

// assertPRTitleRepositoryCaps proves only the selected repository's limit judges a title.
func assertPRTitleRepositoryCaps(t *testing.T, ctx context.Context, candidate ghPRTitleCall, getenv func(string) string, want string) {
	t.Helper()
	other := config.PRTitleLimit{Repo: "example/unrelated", Max: 20}
	got := judgePRTitle(ctx, candidate, []config.PRTitleLimit{other}, getenv)
	if (got.Outcome == Deny) != (want == "") {
		t.Fatalf("unrelated limit judged target %q: %+v", want, got)
	}
	if want == "" {
		if !strings.Contains(got.Reason, "target repository unavailable") {
			t.Fatalf("unresolved target not named: %+v", got)
		}
		return
	}
	got = judgePRTitle(ctx, candidate, []config.PRTitleLimit{other, {Repo: want, Max: 64}}, getenv)
	if got.Outcome != Deny || !strings.Contains(got.Reason, "repository "+want+": title length 65 exceeds limit 64") {
		t.Fatalf("selected limit not applied: %+v", got)
	}
}

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func repositoryWithManifest(t *testing.T, manifestYAML string) string {
	t.Helper()
	dir := repository(t, false)
	if err := os.WriteFile(filepath.Join(dir, manifestName), []byte(manifestYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeTestFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeRefusedFormFixtures(t *testing.T, root string) {
	t.Helper()
	writeTestFixtureFile(t, filepath.Join(root, "pr-body.md"),
		" summary\nSee https://github.com/outside-org/project/pull/77 for details.\n")
	writeTestFixtureFile(t, filepath.Join(root, "comment.json"),
		`{"body":" tracks outside-org/project#88 in upstream"}`)
	writeTestFixtureFile(t, filepath.Join(root, "field-body.md"),
		" tracks outside-org/project!99\n")
	writeTestFixtureFile(t, filepath.Join(root, "sub", "release-notes.md"),
		"Release notes mentioning outside-org/project#105\n")
	writeTestFixtureFile(t, filepath.Join(root, "cat-body.md"),
		"Body from cat mentioning outside-org/project#204\n")
}

func runPreToolCommand(t *testing.T, workDir, command string, getenv func(string) string) Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if getenv == nil {
		getenv = noEnvironment
	}
	payload := commandPayload(t, map[string]any{
		"tool_name":  "Bash",
		"tool_input": map[string]any{"command": command},
	})
	return Run(ctx, Invocation{
		Client:  "claude",
		Event:   "pre-tool",
		Stdin:   bytes.NewReader(payload),
		Getenv:  getenv,
		WorkDir: workDir,
		Policy:  policy(t),
	})
}

func assertForgeTextDenied(t *testing.T, resp Response, wantSpan string) {
	t.Helper()
	stderr := string(resp.Stderr)
	if resp.ExitCode != 2 {
		t.Fatalf("expected deny exit 2, got %d (stderr=%q)", resp.ExitCode, stderr)
	}
	if !strings.Contains(stderr, forgeTextRefRuleName) {
		t.Errorf("refusal missing rule name %q: %q", forgeTextRefRuleName, stderr)
	}
	if !strings.Contains(stderr, wantSpan) {
		t.Errorf("refusal missing offending span %q: %q", wantSpan, stderr)
	}
	if !strings.Contains(stderr, forgeTextAllowedForms) {
		t.Errorf("refusal missing allowed forms line %q: %q", forgeTextAllowedForms, stderr)
	}
}

func TestForgeTextRefsRefusedPerForm(t *testing.T) {
	root := repositoryWithManifest(t, "version: 1\nrepository:\n  owner: repo-owner\n  name: governed-repo\nforge:\n  own_organizations:\n    - fleet-org\n    - second-fleet\n")
	writeRefusedFormFixtures(t, root)

	cases := []struct{ name, command, wantSpan string }{
		{"inline owner/repo#N in --body", `gh issue create --title "Bug report" --body "Reproduces outside-org/project#42 on Linux"`, `outside-org/project#42`},
		{"github.com/<outside>/<repo>/issues/N in --title", `gh pr create --title "Follow up on https://github.com/outside-org/project/issues/19" --body "Clean fix"`, `https://github.com/outside-org/project/issues/19`},
		{"github.com/<outside>/<repo>/pull/N in --body-file", `gh pr edit 12 --body-file pr-body.md`, `https://github.com/outside-org/project/pull/77`},
		{"owner/repo#N in gh api -f body=", `gh api repos/fleet-org/governed-repo/issues/5/comments -f body="Upstream issue outside-org/project#63"`, `outside-org/project#63`},
		{"owner/repo#N in gh api --input JSON file", `gh api repos/fleet-org/governed-repo/pulls/5/comments --input comment.json`, `outside-org/project#88`},
		{"owner/repo#N in $(cat <<EOF here-document", "gh issue comment 9 --body \"$(cat <<'EOF'\nContext:\nSee outside-org/project#51 for background.\nEOF\n)\"", `outside-org/project#51`},
		{"owner/repo#N in unquoted here-document on standard input", "gh issue comment 9 --body-file - <<EOF\nContext:\nSee outside-org/project#52 for background.\nEOF\n", `outside-org/project#52`},
		{"owner/repo!N in gh api -F body=@file", `gh api -X POST repos/fleet-org/governed-repo/issues/9/comments -F body=@field-body.md`, `outside-org/project!99`},
		{"owner/repo#N in $(cat <path>)", `gh pr review 14 --comment -b "$(cat cat-body.md)"`, `outside-org/project#204`},
		{"chained cd and gh release edit --notes-file", `cd sub && gh release edit v1.2.0 --notes-file release-notes.md`, `outside-org/project#105`},
		{"gh release create inline --notes", `gh release create v1.3.0 --title "v1.3.0" --notes "Fixes outside-org/project#301"`, `outside-org/project#301`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertForgeTextDenied(t, runPreToolCommand(t, root, tc.command, nil), tc.wantSpan)
		})
	}
}

func TestForgeTextRefsAcceptedCases(t *testing.T) {
	root := repositoryWithManifest(t, "version: 1\nrepository:\n  owner: repo-owner\n  name: governed-repo\nforge:\n  own_organizations:\n    - fleet-org\n    - second-fleet\n")
	writeTestFixtureFile(t, filepath.Join(root, "good-body.md"),
		" fleet reference fleet-org/other#12, local #9, code `outside-org/project#42`, and fence:\n```\nhttps://github.com/outside-org/project/issues/99\n```\n")

	cases := []struct {
		name    string
		command string
	}{
		{
			name:    "fleet organisation reference from manifest",
			command: `gh issue create --title "Track fleet-org/other#12" --body "Also https://github.com/second-fleet/service/pull/34 and repo-owner/governed-repo#5"`,
		},
		{
			name:    "bare #N reference",
			command: `gh pr comment 10 --body "Follows up on #10 and #42"`,
		},
		{
			name:    "plain text without owner/repo#N",
			command: `gh issue edit 10 --title "Plain text reference to outside-org/project issue 42" --body "No active link syntax"`,
		},
		{
			name:    "reference inside inline code span",
			command: "gh issue create --title 'Code span' --body 'Reference `outside-org/project#42` and `https://github.com/outside-org/project/issues/7` stays inert'",
		},
		{
			name:    "body file with fleet refs and code spans",
			command: `gh pr create --title "Clean PR" --body-file good-body.md`,
		},
		{
			name:    "gh pr view read command",
			command: `gh pr view 42 --repo outside-org/project`,
		},
		{
			name:    "gh api GET read command",
			command: `gh api repos/outside-org/project/issues/42`,
		},
		{
			name:    "gh api explicit -X GET read command",
			command: `gh api -X GET repos/outside-org/project/pulls/42/comments -f per_page=10`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := runPreToolCommand(t, root, tc.command, nil)
			if resp.ExitCode != 0 {
				t.Fatalf("expected allow exit 0, got %d (stderr=%q)", resp.ExitCode, string(resp.Stderr))
			}
		})
	}
}

func TestForgeTextRefsBoundaryAndFailClosedCases(t *testing.T) {
	root := repositoryWithManifest(t, "version: 1\nrepository:\n  owner: repo-owner\n  name: governed-repo\nforge:\n  own_organizations:\n    - fleet-org\n")
	unreadableCases := []struct {
		name     string
		command  string
		wantPath string
	}{
		{"unreadable --body-file", `gh issue create --title "Title" --body-file missing-body.md`, "missing-body.md"},
		{"unreadable gh api --input", `gh api repos/fleet-org/governed-repo/issues/1/comments --input absent.json`, "absent.json"},
		{"unreadable $(cat <path>)", `gh pr comment 3 --body "$(cat no-such-file.md)"`, "no-such-file.md"},
		{"shell reader error on gh write command", `gh issue create --title "unclosed quote outside-org/project#42`, "unclosed quote"},
	}
	for _, tc := range unreadableCases {
		t.Run(tc.name, func(t *testing.T) {
			assertForgeTextDenied(t, runPreToolCommand(t, root, tc.command, nil), tc.wantPath)
		})
	}
}

func TestForgeTextRefsOrganizationResolutionCases(t *testing.T) {
	noOrgRoot := repositoryWithManifest(t, "version: 1\nrepository:\n  owner: solo-owner\n  name: solo-repo\n")
	if resp := runPreToolCommand(t, noOrgRoot, `gh issue comment 1 --body "See solo-owner/other-repo#8"`, nil); resp.ExitCode != 0 {
		t.Fatalf("expected repo owner reference to be allowed, got exit %d: %q", resp.ExitCode, string(resp.Stderr))
	}
	denyResp := runPreToolCommand(t, noOrgRoot, `gh issue comment 1 --body "See fleet-org/other-repo#8"`, nil)
	assertForgeTextDenied(t, denyResp, "fleet-org/other-repo#8")
	if !strings.Contains(string(denyResp.Stderr), forgeTextNoOrgNotice) {
		t.Errorf("expected refusal to name missing org list notice, got %q", string(denyResp.Stderr))
	}

	fleetFile := filepath.Join(t.TempDir(), "fleet.yaml")
	writeTestFixtureFile(t, fleetFile, "forge:\n  own_organizations:\n    - op-fleet-org\n")
	getenv := func(key string) string {
		if key == "PRAETOR_FLEET_CONFIG" {
			return fleetFile
		}
		return ""
	}
	if resp := runPreToolCommand(t, noOrgRoot, `gh issue comment 1 --body "See op-fleet-org/service#11"`, getenv); resp.ExitCode != 0 {
		t.Fatalf("expected operator fleet org to be allowed, got exit %d: %q", resp.ExitCode, string(resp.Stderr))
	}
	assertForgeTextDenied(t, runPreToolCommand(t, noOrgRoot, `gh issue comment 1 --body "See outside-org/service#11"`, getenv), "outside-org/service#11")
}

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

// repositoryWithManifest creates an isolated Git repository fixture with manifestYAML
// written to .standards.yaml and fails the test if setup cannot complete.
func repositoryWithManifest(t *testing.T, manifestYAML string) string {
	t.Helper()
	dir := repository(t, false)
	if err := os.WriteFile(filepath.Join(dir, manifestName), []byte(manifestYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeTestFixtureFile writes content to path, creating parent directories as needed,
// and fails the test if any filesystem write fails.
func writeTestFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeRefusedFormFixtures populates root with body, JSON, field, release-note and cat
// fixture files carrying foreign references that the pre-tool guard must refuse.
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

// runPreToolCommand executes a Claude pre-tool Bash hook invocation for command in workDir
// and returns the encoded hook response.
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

// assertForgeTextDenied verifies that resp refuses the command with exit code 2, names
// the forge-text rule and wantSpan, and prints the allowed reference forms.
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

// TestForgeTextRefsRefusedPerForm verifies that every scoped gh write subcommand and
// input source refuses active references to organisations outside the trusted set.
func TestForgeTextRefsRefusedPerForm(t *testing.T) {
	root := repositoryWithManifest(t, "version: 1\nrepository:\n  owner: repo-owner\n  name: governed-repo\nforge:\n  own_organizations:\n    - fleet-org\n    - second-fleet\n")
	writeRefusedFormFixtures(t, root)

	cases := []struct{ name, command, wantSpan string }{
		{"unrelated heredoc foreign", "cat <<EOF\noutside-org/project#47\nEOF\ngh pr comment 9 --body safe", "outside-org/project#47"},
		{"heredoc variable file foreign", "D=.\ncat > $D/body.md <<'EOF'\nreviewer's outside-org/project#43\nEOF\ngh pr comment 9 --body-file $D/body.md", "outside-org/project#43"},
		{"heredoc tabs foreign", "gh pr comment 9 --body-file - <<-'EOF'\n\treviewer's outside-org/project#44\n\tEOF\n", "outside-org/project#44"},
		{"heredoc double quoted foreign", "gh pr comment 9 --body-file - <<\"EOF\"\nreviewer's outside-org/project#45\nEOF\n", "outside-org/project#45"},
		{"gh write inside a sh heredoc", "sh <<EOF\ngh pr comment 9 --body \"See outside-org/project#48\"\nEOF\n", "outside-org/project#48"},
		{"gh write inside a cat heredoc piped to bash", "cat <<'EOF' | bash\ngh pr comment 9 --body \"See outside-org/project#47\"\nEOF\n", "outside-org/project#47"},
		{"gh write inside a heredoc script then run", "cat > run.sh <<'EOF'\ngh pr comment 9 --body \"See outside-org/project#48\"\nEOF\nbash run.sh\n", "outside-org/project#48"},
		{"gh write inside a bash heredoc", "bash <<'EOF'\ngh pr comment 9 --body \"See outside-org/project#46\"\nEOF\n", "outside-org/project#46"},
		{"heredoc written file foreign", "cat > body.md <<'EOF'\nreviewer's outside-org/project#42\nEOF\ngh pr comment 9 --body-file body.md", "outside-org/project#42"},
		{"inline owner/repo#N in --body", `gh issue create --title "Bug report" --body "Reproduces outside-org/project#42 on Linux"`, `outside-org/project#42`},
		{"github.com/<outside>/<repo>/issues/N in --title", `gh pr create --title "Follow up on https://github.com/outside-org/project/issues/19" --body "Clean fix"`, `https://github.com/outside-org/project/issues/19`},
		{"github.com/<outside>/<repo>/pull/N in --body-file", `gh pr edit 12 --body-file pr-body.md`, `https://github.com/outside-org/project/pull/77`},
		{"owner/repo#N in gh api -f body=", `gh api repos/fleet-org/governed-repo/issues/5/comments -f body="Upstream issue outside-org/project#63"`, `outside-org/project#63`},
		{"owner/repo#N in gh api --input JSON file", `gh api repos/fleet-org/governed-repo/pulls/5/comments --input comment.json`, `outside-org/project#88`},
		{"owner/repo#N in $(cat <<EOF here-document", "gh issue comment 9 --body \"$(cat <<'EOF'\nContext:\nSee outside-org/project#51 for background.\nEOF\n)\"", `outside-org/project#51`},
		{"owner/repo#N in unquoted here-document on standard input", "gh issue comment 9 --body-file - <<EOF\nContext:\nSee outside-org/project#52 for background.\nEOF\n", `outside-org/project#52`},
		{"owner/repo#N in unquoted here-document on /dev/stdin", "gh issue comment 9 --body-file /dev/stdin <<EOF\nContext:\nSee outside-org/project#53 for background.\nEOF\n", `outside-org/project#53`},
		{"owner/repo#N in unquoted here-document on /dev/fd/0", "gh pr comment 9 --body-file /dev/fd/0 <<EOF\nContext:\nSee outside-org/project#54 for background.\nEOF\n", `outside-org/project#54`},
		{"owner/repo!N in gh api -F body=@file", `gh api -X POST repos/fleet-org/governed-repo/issues/9/comments -F body=@field-body.md`, `outside-org/project!99`},
		{"owner/repo#N in $(cat <path>)", `gh pr review 14 --comment -b "$(cat cat-body.md)"`, `outside-org/project#204`},
		{"unquoted owner/repo#N in $(cat <path>)", `gh issue comment 9 --body $(cat cat-body.md)`, `outside-org/project#204`},
		{"chained cd and gh release edit --notes-file", `cd sub && gh release edit v1.2.0 --notes-file release-notes.md`, `outside-org/project#105`},
		{"gh release create inline --notes", `gh release create v1.3.0 --title "v1.3.0" --notes "Fixes outside-org/project#301"`, `outside-org/project#301`},
		{"gh issue close --comment", `gh issue close 5 --comment "Superseded by outside-org/project#310"`, `outside-org/project#310`},
		{"gh issue reopen --comment", `gh issue reopen 5 -c "Reopening after outside-org/project#311"`, `outside-org/project#311`},
		{"gh pr close --comment", `gh pr close 12 --comment "Closing for outside-org/project#312"`, `outside-org/project#312`},
		{"gh pr reopen --comment", `gh pr reopen 12 -c "Reopened for outside-org/project#313"`, `outside-org/project#313`},
		{"gh pr merge --subject and --body", `gh pr merge 12 --squash --subject "Fix outside-org/project#314" --body "Details in outside-org/project#315"`, `outside-org/project#314`},
		{"gh pr merge --body-file", `gh pr merge 12 --squash --body-file pr-body.md`, `https://github.com/outside-org/project/pull/77`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertForgeTextDenied(t, runPreToolCommand(t, root, tc.command, nil), tc.wantSpan)
		})
	}
}

// TestForgeTextRefsAcceptedCases verifies that own-organisation references, bare issue
// numbers, plain text, code spans, code fences, and read-only gh calls are allowed.
func TestForgeTextRefsAcceptedCases(t *testing.T) {
	root := repositoryWithManifest(t, "version: 1\nrepository:\n  owner: repo-owner\n  name: governed-repo\nforge:\n  own_organizations:\n    - fleet-org\n    - second-fleet\n")
	writeTestFixtureFile(t, filepath.Join(root, "good-body.md"),
		" fleet reference fleet-org/other#12, local #9, code `outside-org/project#42`, and fence:\n```\nhttps://github.com/outside-org/project/issues/99\n```\n")

	cases := []struct {
		name    string
		command string
	}{
		{name: "python heredoc apostrophe without gh", command: "python3 - <<'EOF'\nreviewer's\nEOF\n"},
		{name: "assignment heredoc written file apostrophe", command: "FOO=1 cat > body.md <<'EOF'\nreviewer's note\nEOF\ngh pr comment 9 --body-file body.md"},
		{name: "heredoc written file apostrophe", command: "cat > body.md <<'EOF'\nreviewer's note\nEOF\ncd . && gh pr comment 9 --body-file body.md"},
		{name: "heredoc variable file apostrophe", command: "D=.\ncat > $D/body.md <<'EOF'\nreviewer's note\nEOF\ngh pr comment 9 --body-file $D/body.md"},
		{name: "heredoc strip tabs", command: "gh pr comment 9 --body-file - <<-'EOF'\n\treviewer's note\n\tEOF\n"},
		{name: "heredoc double quoted", command: "gh pr comment 9 --body-file - <<\"EOF\"\nreviewer's note\nEOF\n"},
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

// TestForgeTextRefsBoundaryAndFailClosedCases verifies that unreadable files, shell reader
// errors on gh writes, and exceeded file, segment or word bounds fail closed.
func TestForgeTextRefsBoundaryAndFailClosedCases(t *testing.T) {
	root := repositoryWithManifest(t, "version: 1\nrepository:\n  owner: repo-owner\n  name: governed-repo\nforge:\n  own_organizations:\n    - fleet-org\n")
	unreadableCases := []struct {
		name     string
		command  string
		wantPath string
	}{
		{"unterminated heredoc", "gh pr comment 9 --body-file - <<'EOF'\ntext\n", "unterminated"},
		{"unresolved heredoc path", "cat > $D/body.md <<'EOF'\ntext\nEOF\ngh pr comment 9 --body-file $D/other.md", "$D/other.md"},
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
	assertForgeTextOverflowBoundsDenied(t, root)
}

// assertForgeTextOverflowBoundsDenied verifies that commands exceeding maxForgeTextFiles,
// maxForgeTextSegments or maxForgeTextWords are refused with the corresponding limit error.
func assertForgeTextOverflowBoundsDenied(t *testing.T, root string) {
	t.Helper()
	cleanPath := filepath.Join(root, "clean.md")
	foreignPath := filepath.Join(root, "foreign.md")
	writeTestFixtureFile(t, cleanPath, "Clean note referencing #10.\n")
	writeTestFixtureFile(t, foreignPath, "Foreign note referencing outside-org/project#42.\n")

	cases := []struct {
		name     string
		command  string
		wantSpan string
	}{
		{
			name: "flag file count over maxForgeTextFiles fails closed",
			command: "gh api -X POST repos/fleet-org/governed-repo/issues/1/comments" +
				strings.Repeat(" -F note=@clean.md", maxForgeTextFiles) + " -F extra=@foreign.md",
			wantSpan: "exceeds 32 named files",
		},
		{
			name: "cat substitution count over maxForgeTextFiles fails closed",
			command: `gh issue comment 1 --body "` +
				strings.Repeat("$(cat clean.md) ", maxForgeTextFiles) + `$(cat foreign.md)"`,
			wantSpan: "exceeds 32 named files",
		},
		{
			name: "command segment count over maxForgeTextSegments fails closed",
			command: strings.Repeat(`gh issue comment 1 --body "ok" ; `, maxForgeTextSegments) +
				`gh issue comment 1 --body "outside-org/project#42"`,
			wantSpan: "exceeds 256 command segments",
		},
		{
			name:     "shell word count over maxForgeTextWords fails closed",
			command:  strings.Repeat("true ", maxForgeTextWords) + `; g"h" issue comment 1 --body "outside-org/project#42"`,
			wantSpan: "4096 fields",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertForgeTextDenied(t, runPreToolCommand(t, root, tc.command, nil), tc.wantSpan)
		})
	}
}

// TestForgeTextRefsOrganizationResolutionCases verifies that trusted organisations resolve
// from the invocation environment, operator config and manifest while ignoring ambient
// process environment leaks and refusing unlisted foreign owners.
func TestForgeTextRefsOrganizationResolutionCases(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "process-leaked-org/process-repo")
	noOrgRoot := repositoryWithManifest(t, "version: 1\nrepository:\n  owner: solo-owner\n  name: solo-repo\n")
	if resp := runPreToolCommand(t, noOrgRoot, `gh issue comment 1 --body "See solo-owner/other-repo#8"`, nil); resp.ExitCode != 0 {
		t.Fatalf("expected repo owner reference to be allowed, got exit %d: %q", resp.ExitCode, string(resp.Stderr))
	}
	assertForgeTextDenied(t, runPreToolCommand(t, noOrgRoot, `gh issue comment 1 --body "See process-leaked-org/other-repo#8"`, nil), "process-leaked-org/other-repo#8")
	denyResp := runPreToolCommand(t, noOrgRoot, `gh issue comment 1 --body "See fleet-org/other-repo#8"`, nil)
	assertForgeTextDenied(t, denyResp, "fleet-org/other-repo#8")
	if !strings.Contains(string(denyResp.Stderr), forgeTextNoOrgNotice) {
		t.Errorf("expected refusal to name missing org list notice, got %q", string(denyResp.Stderr))
	}

	repoEnv := func(key string) string {
		if key == "GITHUB_REPOSITORY" {
			return "hook-env-owner/governed-repo"
		}
		return ""
	}
	if resp := runPreToolCommand(t, noOrgRoot, `gh issue comment 1 --body "See hook-env-owner/other-repo#8"`, repoEnv); resp.ExitCode != 0 {
		t.Fatalf("expected invocation GITHUB_REPOSITORY owner to be allowed, got exit %d: %q", resp.ExitCode, string(resp.Stderr))
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

// Boundary: the fallback pattern for unparseable commands names exactly the allowlisted gh write actions,
// so an action added to scopedGHActions is refused on a parse error too, never silently skipped.
func TestForgeTextWriteWordsCoverEveryScopedAction(t *testing.T) {
	for sub, actions := range scopedGHActions {
		for action := range actions {
			command := "gh " + sub + " " + action + ` --body "unterminated`
			if _, err := shellFieldsForGH(command); err == nil {
				t.Errorf("%q: parse error not refused", command)
			}
		}
	}
	if _, err := shellFieldsForGH(`gh repo view "unterminated`); err != nil {
		t.Errorf("read-only gh command with a parse error refused: %v", err)
	}
}

// TestForgeTextRefsSubshellDirectoryIsolation verifies that cd or pushd inside a subshell
// does not alter the parent shell working directory for subsequent gh commands.
func TestForgeTextRefsSubshellDirectoryIsolation(t *testing.T) {
	root := repositoryWithManifest(t, "version: 1\nrepository:\n  owner: repo-owner\n  name: governed-repo\nforge:\n  own_organizations:\n    - fleet-org\n")
	writeTestFixtureFile(t, filepath.Join(root, "body.md"), "Foreign reference outside-org/project#42\n")
	cleanDir := filepath.Join(root, "clean_sub")
	writeTestFixtureFile(t, filepath.Join(cleanDir, "body.md"), "Clean reference #42\n")
	writeTestFixtureFile(t, filepath.Join(cleanDir, "foreign.md"), "Foreign reference outside-org/project#99\n")
	cases := []struct {
		name     string
		command  string
		wantSpan string
	}{
		{
			name:     "subshell cd does not leak to parent command with semicolon",
			command:  `(cd clean_sub; true); gh issue comment 1 --body-file body.md`,
			wantSpan: "outside-org/project#42",
		},
		{
			name:     "spaced subshell cd does not leak to parent command",
			command:  `( cd clean_sub ; true ) ; gh issue comment 1 --body-file body.md`,
			wantSpan: "outside-org/project#42",
		},
		{
			name:     "single-segment subshell cd does not leak to parent command",
			command:  `( cd clean_sub ) ; gh issue comment 1 --body-file body.md`,
			wantSpan: "outside-org/project#42",
		},
		{
			name:     "subshell cd applies to commands inside the subshell",
			command:  `(cd clean_sub && gh issue comment 1 --body-file foreign.md)`,
			wantSpan: "outside-org/project#99",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertForgeTextDenied(t, runPreToolCommand(t, root, tc.command, nil), tc.wantSpan)
		})
	}

	allowedCases := []struct {
		name    string
		command string
	}{
		{
			name:    "clean command in subshell directory allowed",
			command: `(cd clean_sub && gh issue comment 1 --body-file body.md)`,
		},
		{
			name:    "clean command after subshell allowed",
			command: `(cd clean_sub; true); gh issue comment 1 --body "Clean fleet-org/project#1"`,
		},
	}
	for _, tc := range allowedCases {
		t.Run(tc.name, func(t *testing.T) {
			resp := runPreToolCommand(t, root, tc.command, nil)
			if resp.ExitCode != 0 {
				t.Fatalf("expected allow exit 0, got %d: %q", resp.ExitCode, string(resp.Stderr))
			}
		})
	}
}

// TestForgeTextRefsConcatenatedShellFragments verifies that references split across
// adjacent quoted and unquoted shell fragments are joined into the passed word and refused.
func TestForgeTextRefsConcatenatedShellFragments(t *testing.T) {
	root := repositoryWithManifest(t, "version: 1\nrepository:\n  owner: repo-owner\n  name: governed-repo\nforge:\n  own_organizations:\n    - fleet-org\n")

	cases := []struct {
		name     string
		command  string
		wantSpan string
	}{
		{
			name:     "single-quoted owner adjacent to unquoted repo and number",
			command:  `gh issue comment 1 --body 'outside-org'/project#5`,
			wantSpan: "outside-org/project#5",
		},
		{
			name:     "double-quoted owner and slash adjacent to unquoted repo and number",
			command:  `gh issue comment 1 --body "outside-org/"project#5`,
			wantSpan: "outside-org/project#5",
		},
		{
			name:     "multiple adjacent quoted fragments",
			command:  `gh issue comment 1 --body 'outside'-'org'/project#5`,
			wantSpan: "outside-org/project#5",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertForgeTextDenied(t, runPreToolCommand(t, root, tc.command, nil), tc.wantSpan)
		})
	}

	allowedCases := []struct {
		name    string
		command string
	}{
		{
			name:    "single-quoted own organisation adjacent to unquoted repo",
			command: `gh issue comment 1 --body 'fleet-org'/project#5`,
		},
		{
			name:    "double-quoted own organisation and slash adjacent to unquoted repo",
			command: `gh issue comment 1 --body "fleet-org/"project#5`,
		},
	}
	for _, tc := range allowedCases {
		t.Run(tc.name, func(t *testing.T) {
			resp := runPreToolCommand(t, root, tc.command, nil)
			if resp.ExitCode != 0 {
				t.Fatalf("expected allow exit 0, got %d: %q", resp.ExitCode, string(resp.Stderr))
			}
		})
	}
}

// TestForgeTextRefsUnquotedCatSubstRefused verifies that unquoted $(cat file) at the
// end of a segment is scanned and maintains subshell directory tracking for subsequent commands.
func TestForgeTextRefsUnquotedCatSubstRefused(t *testing.T) {
	root := repositoryWithManifest(t, "version: 1\nrepository:\n  owner: repo-owner\n  name: governed-repo\nforge:\n  own_organizations:\n    - fleet-org\n")
	writeTestFixtureFile(t, filepath.Join(root, "cat-body.md"), "Foreign ref outside-org/project#204\n")
	writeTestFixtureFile(t, filepath.Join(root, "sub", "cat-body.md"), "Foreign ref outside-org/project#204\n")
	writeTestFixtureFile(t, filepath.Join(root, "sub", "a.md"), "Clean body\n")
	writeTestFixtureFile(t, filepath.Join(root, "sub", "b.md"), "Foreign ref outside-org/project#204\n")
	writeTestFixtureFile(t, filepath.Join(root, "b.md"), "Clean body in root\n")

	cases := []struct {
		name     string
		command  string
		wantSpan string
	}{
		{
			name:     "unquoted cat at segment end refused",
			command:  `gh issue comment 9 --body $(cat cat-body.md)`,
			wantSpan: "outside-org/project#204",
		},
		{
			name:     "unquoted cat in subshell refused",
			command:  `(cd sub && gh issue comment 9 --body $(cat cat-body.md))`,
			wantSpan: "outside-org/project#204",
		},
		{
			name:     "unquoted cat keeps subshell directory for subsequent calls",
			command:  `(cd sub && gh issue comment 9 --body $(cat a.md) && gh issue comment 9 --body-file b.md)`,
			wantSpan: "outside-org/project#204",
		},
		{
			name:     "quoted cat behaves as before",
			command:  `gh issue comment 9 --body "$(cat cat-body.md)"`,
			wantSpan: "outside-org/project#204",
		},
		{
			name:     "apostrophe inside double quotes does not hide a later substitution",
			command:  `(cd sub && gh issue comment 9 --title "it's" --body $(cat a.md) && gh issue comment 9 --body-file b.md)`,
			wantSpan: "outside-org/project#204",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertForgeTextDenied(t, runPreToolCommand(t, root, tc.command, nil), tc.wantSpan)
		})
	}
}

// TestForgeTextRefsUnquotedCatSubstAccepted verifies that clean files pass when referenced
// through unquoted $(cat file) substitutions in root and subshell scopes.
func TestForgeTextRefsUnquotedCatSubstAccepted(t *testing.T) {
	root := repositoryWithManifest(t, "version: 1\nrepository:\n  owner: repo-owner\n  name: governed-repo\nforge:\n  own_organizations:\n    - fleet-org\n")
	writeTestFixtureFile(t, filepath.Join(root, "clean.md"), "Clean body with fleet-org/repo#10\n")
	writeTestFixtureFile(t, filepath.Join(root, "sub", "clean.md"), "Clean body in sub\n")
	writeTestFixtureFile(t, filepath.Join(root, "sub", "a.md"), "Clean body a\n")
	writeTestFixtureFile(t, filepath.Join(root, "sub", "b.md"), "Clean body b\n")

	cases := []struct {
		name    string
		command string
	}{
		{
			name:    "clean unquoted cat at root",
			command: `gh issue comment 9 --body $(cat clean.md)`,
		},
		{
			name:    "clean unquoted cat in subshell",
			command: `(cd sub && gh issue comment 9 --body $(cat clean.md))`,
		},
		{
			name:    "clean unquoted cat chained in subshell",
			command: `(cd sub && gh issue comment 9 --body $(cat a.md) && gh issue comment 9 --body-file b.md)`,
		},
		{
			name:    "clean quoted cat behaves as before",
			command: `gh issue comment 9 --body "$(cat clean.md)"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := runPreToolCommand(t, root, tc.command, nil)
			if resp.ExitCode != 0 {
				t.Fatalf("expected allow exit 0, got %d: %q", resp.ExitCode, string(resp.Stderr))
			}
		})
	}
}

// TestForgeTextRefsUnbalancedSubshellBoundary verifies that unbalanced trailing closing
// parentheses at root or exceeding subshell depth neither panic nor pop below the root.
func TestForgeTextRefsUnbalancedSubshellBoundary(t *testing.T) {
	root := repositoryWithManifest(t, "version: 1\nrepository:\n  owner: repo-owner\n  name: governed-repo\nforge:\n  own_organizations:\n    - fleet-org\n")
	writeTestFixtureFile(t, filepath.Join(root, "body.md"), "Foreign ref outside-org/project#42\n")

	assertForgeTextDenied(t, runPreToolCommand(t, root, `gh issue comment 9 --body "outside-org/project#42")`, nil), "outside-org/project#42")

	resp := runPreToolCommand(t, root, `gh issue comment 9 --body "Clean note")`, nil)
	if resp.ExitCode != 0 {
		t.Fatalf("expected stray paren at root to allow clean command, got %d: %q", resp.ExitCode, string(resp.Stderr))
	}

	extraResp := runPreToolCommand(t, root, `(cd sub ; true)) ; gh issue comment 9 --body-file body.md`, nil)
	assertForgeTextDenied(t, extraResp, "outside-org/project#42")
}

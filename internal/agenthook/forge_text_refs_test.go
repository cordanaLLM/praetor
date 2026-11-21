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
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a directory the test writes files into needs the owner execute bit; 0o700 is least privilege
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
		{"missing heredoc file case 03", "cat > missing-body.md <<'EOF'\nsafe\nEOF\ngh pr comment 9 --body-file missing-body.md", "missing-body.md"},
		{"missing variable heredoc file case 08", "D=a\ncat > $D/missing-body.md <<'EOF'\nsafe\nEOF\ngh pr comment 9 --body-file $D/missing-body.md", "$D/missing-body.md"},
		{"variable spelling bypass", "D=a\ncat > $D/body.md <<'EOF'\nsafe\nEOF\nprintf '%s' 'outside-org/project#47' > a/body.md\ngh pr comment 9 --body-file $D/body.md", "$D/body.md"},
		{"split gh in piped heredoc", "cat <<'EOF' | bash\ng\\\nh pr comment 9 --body \"See outside-org/project#51\"\nEOF\n", "outside-org/project#51"},
		{"split gh CRLF in piped heredoc", "cat <<'EOF' | bash\ng\\\r\nh pr comment 9 --body \"See outside-org/project#51\"\nEOF\n", "outside-org/project#51"},
		{"absolute cat missing file", "/usr/bin/cat > missing-body.md <<'EOF'\nsafe\nEOF\ngh pr comment 9 --body-file missing-body.md", "missing-body.md"},
		{"substitution in data", "cat <<EOF\nreviewer's note $(gh pr comment 9 --body \"outside-org/project#42\")\nEOF", "outside-org/project#42"},
		{"continued pipe write", "cat <<'EOF' | bash\ngh \\\npr comment 9 --body \"See outside-org/project#50\"\nEOF", "outside-org/project#50"},
		{"unrelated heredoc foreign", "cat <<EOF\noutside-org/project#47\nEOF\ngh pr comment 9 --body safe", "outside-org/project#47"},
		{"heredoc variable file foreign", "D=.\ncat > $D/body.md <<'EOF'\nreviewer's outside-org/project#43\nEOF\ngh pr comment 9 --body-file $D/body.md", "outside-org/project#43"},
		{"heredoc tabs foreign", "gh pr comment 9 --body-file - <<-'EOF'\n\treviewer's outside-org/project#44\n\tEOF\n", "outside-org/project#44"},
		{"heredoc double quoted foreign", "gh pr comment 9 --body-file - <<\"EOF\"\nreviewer's outside-org/project#45\nEOF\n", "outside-org/project#45"},
		{"gh write inside a sh heredoc", "sh <<EOF\ngh pr comment 9 --body \"See outside-org/project#48\"\nEOF\n", "outside-org/project#48"},
		{"gh write inside a cat heredoc piped to bash", "cat <<'EOF' | bash\ngh pr comment 9 --body \"See outside-org/project#47\"\nEOF\n", "outside-org/project#47"},
		{"gh write inside a heredoc script then run", "cat > run.sh <<'EOF'\ngh pr comment 9 --body \"See outside-org/project#48\"\nEOF\nbash run.sh\n", "outside-org/project#48"},
		{"gh write inside a bash heredoc", "bash <<'EOF'\ngh pr comment 9 --body \"See outside-org/project#46\"\nEOF\n", "outside-org/project#46"},
		{"quoted argument inside a bash heredoc", "bash <<'EOF'\ngh pr comment 9 --body \"See outside-org/project#49\"\nEOF\n", "outside-org/project#49"},
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
	writeTestFixtureFile(t, filepath.Join(root, "body.md"),
		" fleet reference fleet-org/other#12, local #9, code `outside-org/project#42`, and fence:\n```\nhttps://github.com/outside-org/project/issues/99\n```\n")

	writeTestFixtureFile(t, filepath.Join(root, "$D", "body.md"), "Clean text.\n")
	cases := []struct {
		name    string
		command string
	}{
		{name: "python heredoc apostrophe without gh", command: "python3 - <<'EOF'\nreviewer's\nEOF\n"},
		{name: "assignment heredoc written file apostrophe", command: "FOO=1 cat > body.md <<'EOF'\nreviewer's note\nEOF\ngh pr comment 9 --body-file body.md"},
		{name: "heredoc written file apostrophe", command: "cat > body.md <<'EOF'\nreviewer's note\nEOF\ncd . && gh pr comment 9 --body-file body.md"},
		{name: "heredoc written file quotes and substitution", command: "cat > body.md <<'EOF'\nit's \"quoted\" and $(not run)\nEOF\ngh pr comment 9 --body-file body.md"},
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
			command: `gh pr create --title "Clean PR" --body-file body.md`,
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

// TestForgeTextRefsDataProse permits write-shaped prose without foreign references.
func TestForgeTextRefsDataProse(t *testing.T) {
	root := repositoryWithManifest(t, "version: 1\nrepository:\n  owner: repo-owner\n  name: governed-repo\n")
	response := runPreToolCommand(t, root, "cat <<'EOF'\nmention gh pr comment in prose\nEOF\ngh pr comment 9 --body safe", nil)
	if response.ExitCode != 0 {
		t.Fatalf("expected allow: %s", response.Stderr)
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

// TestForgeTextRefsBodyFileOverwrite refuses equivalent-path overwrites without executing shell code.
func TestForgeTextRefsBodyFileOverwrite(t *testing.T) {
	for _, tc := range []struct {
		name, first, second, body string
		exists, allowed           bool
	}{
		{"dot then plain missing", "./missing-body.md", "missing-body.md", "outside-org/project#47", false, false},
		{"plain then dot missing", "missing-body.md", "./missing-body.md", "outside-org/project#47", false, false},
		{"dot then plain changed", "./missing-body.md", "missing-body.md", "outside-org/project#47", true, false},
		{"plain then dot changed", "missing-body.md", "./missing-body.md", "outside-org/project#47", true, false},
		{"fleet control", "./missing-body.md", "missing-body.md", "fleet-org/project#47", true, true},
		{"plain control", "missing-body.md", "./missing-body.md", "safe", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.exists {
				writeTestFixtureFile(t, filepath.Join(root, "missing-body.md"), tc.body)
			}
			command := "cat > " + tc.first + " <<'EOF'\nsafe\nEOF\necho '" + tc.body + "' > " + tc.second + "\ngh pr comment 9 --body-file " + tc.first
			calls, err := collectGHWriteCalls(command, root)
			if err != nil || len(calls) != 1 {
				t.Fatalf("collect writes: %#v, %v", calls, err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			verdict := judgeGHWriteCall(ctx, calls[0], []string{"fleet-org"}, false)
			if (verdict.Outcome == Allow) != tc.allowed {
				t.Fatalf("verdict = %#v; want allow %v", verdict, tc.allowed)
			}
			if !tc.allowed && !strings.Contains(verdict.Reason, "missing-body.md") {
				t.Fatalf("refusal must name body file: %s", verdict.Reason)
			}
		})
	}
}

// TestForgeTextRefsExecutingHereDocs judges executing bodies like direct write commands.
func TestForgeTextRefsExecutingHereDocs(t *testing.T) {
	root := repositoryWithManifest(t, "version: 1\nforge:\n  own_organizations: [own-org]\n")
	writeTestFixtureFile(t, filepath.Join(root, "sub", "foreign.md"), "outside-org/project#42")
	writeTestFixtureFile(t, filepath.Join(root, "sub", "clean.md"), "own-org/project#42")
	cases := []struct {
		name, body, span string
		deny             bool
	}{
		{"foreign file", "gh pr comment 9 --body-file foreign.md", "outside-org/project#42", true},
		{"missing file", "gh pr comment 9 --body-file missing.md", "missing.md", true},
		{"concatenated foreign", "gh pr comment 9 --body 'outside-org'/project#42", "outside-org/project#42", true},
		{"continued foreign", "gh pr comment 9 --body outside-org/proj\\\nect#42", "outside-org/project#42", true},
		{"clean file", "gh pr comment 9 --body-file clean.md", "", false},
		{"existing file", "gh pr comment 9 --body-file clean.md", "", false},
		{"concatenated own", "gh pr comment 9 --body 'own-org'/project#42", "", false},
		{"continued own", "gh pr comment 9 --body own-org/proj\\\nect#42", "", false},
	}
	for i := 0; i < len(cases) && i < 8; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) { checkExecutingForgeBody(t, root, tc.body, tc.span, tc.deny) })
	}
}

// checkExecutingForgeBody compares bounded code paths with the direct command verdict.
func checkExecutingForgeBody(t *testing.T, root, body, span string, deny bool) {
	t.Helper()
	for _, tc := range []struct{ name, script string }{
		{"direct", "cd sub\n" + body},
		{"shell stdin", "cd sub\nbash <<'EOF'\n" + body + "\nEOF\n"},
		{"cat pipe", "cd sub\ncat <<'EOF' | bash\n" + body + "\nEOF\n"},
		{"tee pipe", "cd sub\ntee copy <<'EOF' | bash\n" + body + "\nEOF\n"},
		{"script file", "cat > run.sh <<'EOF'\n" + body + "\nEOF\ncd sub\nbash ../run.sh\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := runPreToolCommand(t, root, tc.script, nil)
			if deny {
				assertForgeTextDenied(t, response, span)
			} else if response.ExitCode != 0 {
				t.Fatalf("allowed body refused: %s", response.Stderr)
			}
		})
	}
}

// checkAgentHereDocReader compares execution visibility across bounded body flows.
func checkAgentHereDocReader(t *testing.T, body string, read func(string) bool) {
	t.Helper()
	for _, tc := range []struct {
		name, script string
		want         bool
	}{
		{"cat pipe", "cat <<'EOF' | bash\n" + body + "\nEOF\n", true},
		{"tee pipe", "tee copy <<'EOF' | bash\n" + body + "\nEOF\n", true},
		{"shell stdin", "bash <<'EOF'\n" + body + "\nEOF\n", true},
		{"data", "cat > note <<'EOF'\n" + body + "\nEOF\n", false},
		{"script file", "cat > run.sh <<'EOF'\n" + body + "\nEOF\nbash run.sh\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := read(tc.script); got != tc.want {
				t.Fatalf("reader got %v; want %v", got, tc.want)
			}
		})
	}
}

// TestForgeTextRefsHereDocData retains raw prose checks without inspecting hypothetical files.
func TestForgeTextRefsHereDocData(t *testing.T) {
	root := repositoryWithManifest(t, "version: 1\nforge:\n  own_organizations: [own-org]\n")
	writeTestFixtureFile(t, filepath.Join(root, "foreign.md"), "outside-org/project#42")
	writeTestFixtureFile(t, filepath.Join(root, "clean.md"), "own-org/project#42")
	for _, tc := range []struct {
		body string
		deny bool
	}{
		{"gh pr comment 9 --body-file foreign.md", false},
		{"gh pr comment 9 --body-file missing.md", false},
		{"gh pr comment 9 --body 'outside-org'/project#42", false},
		{"gh pr comment 9 --body outside-org/proj\\\nect#42", false},
		{"gh pr comment 9 --body-file clean.md", false},
		{"gh pr comment 9 --body 'own-org'/project#42", false},
		{"gh pr comment 9 --body own-org/proj\\\nect#42", false},
		{"gh pr comment 9 --body outside-org/project#42", true},
	} {
		response := runPreToolCommand(t, root, "cat > note <<'EOF'\n"+tc.body+"\nEOF\n", nil)
		if tc.deny {
			assertForgeTextDenied(t, response, "outside-org/project#42")
		} else if response.ExitCode != 0 {
			t.Errorf("data refused: %s", response.Stderr)
		}
	}
	response := runPreToolCommand(t, root, "cat > $SCRIPT <<'EOF'\ngh pr comment 9 --body-file missing.md\nEOF\nbash $SCRIPT\n", nil)
	assertForgeTextDenied(t, response, "unproven redirect target")
}

// TestForgeTextRefsRound3Substitutions checks expanding data through the real hook.
func TestForgeTextRefsRound3Substitutions(t *testing.T) {
	root := repositoryWithManifest(t, "version: 1\nforge:\n  own_organizations: [own-org]\n")
	writeTestFixtureFile(t, filepath.Join(root, "foreign.md"), "outside-org/project#42")
	for _, tc := range []struct {
		name, delimiter, body string
		deny                  bool
	}{
		{"T1 unquoted substitution", "EOF", "$(gh pr comment 9 --body-file foreign.md)", true},
		{"T1 unquoted backtick", "EOF", "`gh pr comment 9 --body-file foreign.md`", true},
		{"T1 quoted twin", "'EOF'", "$(gh pr comment 9 --body-file foreign.md)", false},
		{"T1 innocent substitution", "EOF", "$(printf clean)", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := runPreToolCommand(t, root, "cat <<"+tc.delimiter+"\n"+tc.body+"\nEOF\n", nil)
			if tc.deny {
				assertForgeTextDenied(t, response, "outside-org/project#42")
			} else if response.ExitCode != 0 {
				t.Fatalf("allowed data refused: %s", response.Stderr)
			}
		})
	}
}

// TestForgeTextRefsRound3Unproven refuses a write-shaped body when ownership cannot be proved.
func TestForgeTextRefsRound3Unproven(t *testing.T) {
	root := repositoryWithManifest(t, "version: 1\nforge:\n  own_organizations: [own-org]\n")
	for _, program := range []string{"gh", "g'h'", "g\\\nh"} {
		t.Run(program, func(t *testing.T) {
			response := runPreToolCommand(t, root, "mystery <<'EOF'\n"+program+" pr comment 9 --body-file missing.md\nEOF\n", nil)
			assertForgeTextDenied(t, response, "unproven here-document owner: mystery")
		})
	}
}

// TestForgeTextRefsRound3ExpansionDirectories pins expansion-time directories and child scope.
func TestForgeTextRefsRound3ExpansionDirectories(t *testing.T) {
	root := repositoryWithManifest(t, "version: 1\nforge:\n  own_organizations: [own-org]\n")
	writeTestFixtureFile(t, filepath.Join(root, "foreign.md"), "Clean note")
	writeTestFixtureFile(t, filepath.Join(root, "sub", "foreign.md"), "outside-org/project#42")
	writeTestFixtureFile(t, filepath.Join(root, "unsafe.md"), "outside-org/project#42")
	writeTestFixtureFile(t, filepath.Join(root, "sub", "unsafe.md"), "Clean note")
	for _, tc := range []struct {
		name, script string
		deny         bool
	}{
		{"sibling substitution scope clean", "cat <<EOF\n$(cd sub; true) $(gh pr comment 9 --body-file foreign.md)\nEOF\n", false},
		{"sibling substitution scope foreign", "cat <<EOF\n$(cd sub; true) $(gh pr comment 9 --body-file unsafe.md)\nEOF\n", true},
		{"before later cd", "cat <<EOF\n$(gh pr comment 9 --body-file foreign.md)\nEOF\ncd sub\n", false},
		{"after earlier cd", "cd sub\ntee copy <<EOF\n$(gh pr comment 9 --body-file 'foreign.md')\nEOF\n", true},
		{"nested command", "cat <<EOF\n$(printf '%s' \"$(gh pr comment 9 --body-file sub/foreign.md)\")\nEOF\n", true},
		{"literal apostrophe", "cat <<EOF\nit's $(gh pr comment 9 --body-file sub/foreign.md)\nEOF\n", true},
		{"escaped expansion", "cat <<EOF\n\\$(gh pr comment 9 --body-file sub/foreign.md)\nEOF\n", false},
		{"child directory stays child", "cat <<EOF\n$(cd sub; gh pr comment 9 --body-file ../foreign.md)\nEOF\ngh pr comment 9 --body-file foreign.md\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := runPreToolCommand(t, root, tc.script, nil)
			if tc.deny {
				assertForgeTextDenied(t, response, "outside-org/project#42")
			} else if response.ExitCode != 0 {
				t.Fatalf("allowed expansion refused: %s", response.Stderr)
			}
		})
	}
}

// TestForgeTextRefsRound3NestedDirectoryUnproven refuses an unprovable nested expansion scope.
func TestForgeTextRefsRound3NestedDirectoryUnproven(t *testing.T) {
	root := repositoryWithManifest(t, "version: 1\nforge:\n  own_organizations: [own-org]\n")
	command := "cat <<EOF\n$(cd sub; printf '%s' \"$(gh pr comment 9 --body-file missing.md)\")\nEOF\n"
	assertForgeTextDenied(t, runPreToolCommand(t, root, command, nil), "unproven nested substitution directory state")
}

// TestForgeTextRefsHereDocEvidenceDefault prevents shared-reader uncertainty becoming execution proof.
func TestForgeTextRefsHereDocEvidenceDefault(t *testing.T) {
	for _, tc := range []struct {
		owner string
		want  bool
	}{
		{"bash", true}, {"cat", false}, {"mystery", false},
	} {
		t.Run(tc.owner, func(t *testing.T) {
			if got := commandMatches("git push", tc.owner+" <<'EOF'\ngit push\nEOF\n"); got != tc.want {
				t.Fatalf("evidence %v; want %v", got, tc.want)
			}
		})
	}
}

// TestForgeTextRefsRound3NonStdinUnproven refuses a descriptor body an explicit source may execute.
func TestForgeTextRefsRound3NonStdinUnproven(t *testing.T) {
	root := repositoryWithManifest(t, "version: 1\nforge:\n  own_organizations: [own-org]\n")
	command := "cat 2<<'A' <<'B' | bash\ngh pr comment 9 --body-file foreign.md\nA\nsource /dev/fd/2\nB\n"
	assertForgeTextDenied(t, runPreToolCommand(t, root, command, nil), "unproven non-stdin here-document descriptor")
}

// TestForgeTextRefsRound3CatOperandsUnproven refuses a script write outside the stdin-copy proof.
func TestForgeTextRefsRound3CatOperandsUnproven(t *testing.T) {
	root := repositoryWithManifest(t, "version: 1\nforge:\n  own_organizations: [own-org]\n")
	command := "cat -- > run.sh <<'EOF'\ngh pr comment 9 --body-file foreign.md\nEOF\nbash run.sh\n"
	assertForgeTextDenied(t, runPreToolCommand(t, root, command, nil), "unproven cat stdin operands")
}

// TestForgeTextRefsRound3PipedFileUnproven refuses unsupported downstream file definitions.
func TestForgeTextRefsRound3PipedFileUnproven(t *testing.T) {
	root := repositoryWithManifest(t, "version: 1\nforge:\n  own_organizations: [own-org]\n")
	for _, receiver := range []string{"tee run.sh", "cat > run.sh"} {
		t.Run(receiver, func(t *testing.T) {
			command := "cat <<'EOF' | " + receiver + "\ngh pr comment 9 --body-file foreign.md\nEOF\nbash run.sh\n"
			assertForgeTextDenied(t, runPreToolCommand(t, root, command, nil), "unproven piped file definition")
		})
	}
}

// TestForgeTextRefsRound3AppendedScope keeps one script's directory state across append fragments.
func TestForgeTextRefsRound3AppendedScope(t *testing.T) {
	root := repositoryWithManifest(t, "version: 1\nforge:\n  own_organizations: [own-org]\n")
	writeTestFixtureFile(t, filepath.Join(root, "foreign.md"), "Clean note")
	writeTestFixtureFile(t, filepath.Join(root, "sub", "foreign.md"), "outside-org/project#42")
	command := "cat > run.sh <<'A'\ncd sub\nA\ncat >> run.sh <<'B'\ngh pr comment 9 --body-file foreign.md\nB\nbash run.sh\n"
	assertForgeTextDenied(t, runPreToolCommand(t, root, command, nil), "outside-org/project#42")
}

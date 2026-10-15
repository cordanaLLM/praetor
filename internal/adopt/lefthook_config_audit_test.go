// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"path/filepath"
	"strings"
	"testing"
)

// lefthookConfigRoot returns a directory holding files, relative path to text.
func lefthookConfigRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, text := range files {
		mustWrite(t, filepath.Join(root, filepath.FromSlash(rel)), text)
	}
	return root
}

// governanceJobs runs both governance commands before a commit, the way the generated
// configuration does: through the engine launcher.
const governanceJobs = "pre-commit:\n  commands:\n    context:\n      run: sh .config/lefthook/engine.sh compile-context --verify\n" +
	"    hiss-audit:\n      run: sh .config/lefthook/engine.sh audit --offline\n"

// The hook configuration gate passed any lefthook.yml because the file existed, while a
// .pre-commit-config.yaml had to run both praetor commands (#1077). Positive: the generated
// configuration, praetorctl named directly, and the canonical policy pass. Negative: a
// configuration without the governance jobs, one running the audit only before a push, and an
// unreadable one fail and name what is missing. Boundary: jobs in a local file the configuration
// extends count; a file outside the repository or one that does not exist is not read.
func TestAuditGitHookConfig_3D_LefthookRunsTheGovernanceCommands(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"generated":        {lefthookFile: buildLefthookYAML()},
		"praetorctl named": {lefthookFile: strings.ReplaceAll(governanceJobs, "sh .config/lefthook/engine.sh", "praetorctl")},
		"extends a local file": {lefthookFile: "extends:\n  - .config/hooks/governance.yml\npre-commit:\n  commands:\n    fmt:\n      run: gofmt -l .\n",
			".config/hooks/governance.yml": governanceJobs},
		"extends the canonical policy": {lefthookFile: "extends:\n  - " + canonicalLefthookPolicy + "\n"},
	} {
		hooks, err := AuditGitHookConfig(t.Context(), nil, lefthookConfigRoot(t, files))
		if err != nil || hooks.File != lefthookFile || !strings.HasPrefix(hooks.Line, "[PASS] Git hook configuration lefthook.yml verified") {
			t.Errorf("%s: %+v, %v", name, hooks, err)
		}
	}
	for name, tc := range map[string]struct {
		files map[string]string
		want  string
	}{
		"no governance jobs": {map[string]string{lefthookFile: "pre-commit:\n  commands:\n    fmt:\n      run: gofmt -l .\n"},
			"does not run 'praetorctl compile-context --verify' or 'praetorctl audit'"},
		"audit before a push only": {map[string]string{lefthookFile: strings.Replace(governanceJobs, "    hiss-audit:", "pre-push:\n  commands:\n    hiss-audit:", 1)},
			"does not run 'praetorctl audit'"},
		"extends a missing file": {map[string]string{lefthookFile: "extends:\n  - .config/hooks/absent.yml\n"},
			"does not run 'praetorctl compile-context --verify' or 'praetorctl audit'"},
		"extends a file outside the repository": {map[string]string{lefthookFile: "extends:\n  - ../governance.yml\n"},
			"does not run 'praetorctl compile-context --verify' or 'praetorctl audit'"},
		"duplicate key": {map[string]string{lefthookFile: "pre-commit: {}\npre-commit: {}\n"}, "cannot be read"},
	} {
		_, err := AuditGitHookConfig(t.Context(), nil, lefthookConfigRoot(t, tc.files))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err %v; want %q", name, err, tc.want)
		}
	}
}

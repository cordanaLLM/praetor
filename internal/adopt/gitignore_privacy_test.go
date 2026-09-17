package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestAdoptionKeepsWorkingDirectoryPrivate(t *testing.T) {
	for name, existing := range map[string]string{
		"missing": "", "custom": "# retain exactly\nuser-output/",
		"old-opt-in":   ".workingdir/*\n!.workingdir/STATE.md\n",
		"pre-agy-rule": "bin/\n/.workingdir/\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := newTestRepo(t, name)
			path := filepath.Join(root, ".gitignore")
			if existing != "" {
				mustWrite(t, path, existing)
			}
			opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t), DryRun: true}
			before := snapshotTree(t, root)
			if _, err := Adopt(t.Context(), opts); err != nil {
				t.Fatal(err)
			}
			assertTreeUnchanged(t, before, snapshotTree(t, root))
			opts.DryRun = false
			if _, err := Adopt(t.Context(), opts); err != nil {
				t.Fatal(err)
			}
			got := mustRead(t, path)
			if !strings.HasPrefix(got, existing) {
				t.Fatalf("existing ignore content changed: %q", got)
			}
			for _, private := range []string{".workingdir/STATE.md", ".workingdir/cluster-guide.md", ".workingdir/nested/private.yaml"} {
				if _, err := util.RunGit(t.Context(), root, "check-ignore", "--no-index", "--", private); err != nil {
					t.Fatalf("private path %s remains publishable: %v", private, err)
				}
			}
			if _, err := util.RunGit(t.Context(), root, "check-ignore", "--no-index", "--", ".agents/mcp_config.json"); err != nil {
				t.Fatalf("per-host client configuration remains publishable: %v", err)
			}
			if _, err := util.RunGit(t.Context(), root, "check-ignore", "--no-index", "--", ".agents/plugins/praetor/plugin.json"); err == nil {
				t.Fatal("tracked plugin projection became ignored")
			}
			if _, err := Adopt(t.Context(), opts); err != nil {
				t.Fatal(err)
			}
			if repeated := mustRead(t, path); repeated != got {
				t.Fatalf("ignore reconciliation not idempotent: %q", repeated)
			}
		})
	}
}

func TestAdoptionRejectsLinkedGitignore(t *testing.T) {
	root := newTestRepo(t, "linked-ignore")
	target := filepath.Join(t.TempDir(), "private-ignore")
	mustWrite(t, target, "preserve")
	if err := os.Symlink(target, filepath.Join(root, ".gitignore")); err != nil {
		t.Fatal(err)
	}
	report, err := Adopt(t.Context(), AdoptOptions{Path: root, LockSourceRoot: newAdoptLockSource(t)})
	if err == nil && len(report.Errors) == 0 {
		t.Fatal("linked ignore file accepted")
	}
	if got := mustRead(t, target); got != "preserve" {
		t.Fatalf("private ignore target changed: %q", got)
	}
}

func TestMissingIgnoreRules(t *testing.T) {
	for name, tc := range map[string]struct {
		text string
		want string
	}{
		"empty":                  {"", "/.agents/mcp_config.json,/.workingdir/"},
		"complete":               {"/.agents/mcp_config.json\n/.workingdir/\n", ""},
		"complete reversed":      {"/.workingdir/\n/.agents/mcp_config.json\n", ""},
		"complete without EOL":   {"bin/\n/.workingdir/\n\n  /.agents/mcp_config.json  ", ""},
		"existing adopter":       {"bin/\n/.workingdir/\n", "/.agents/mcp_config.json"},
		"negation after private": {"/.agents/mcp_config.json\n/.workingdir/\n!.workingdir/STATE.md\n", "/.workingdir/"},
		"similar rule is not it": {".agents/mcp_config.json.bak\n/.workingdir/\n", "/.agents/mcp_config.json"},
		"CRLF":                   {"/.agents/mcp_config.json\r\n/.workingdir/\r\n", ""},
		"CRLF private missing":   {"bin/\r\n/.agents/mcp_config.json\r\n", "/.workingdir/"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := strings.Join(missingIgnoreRules(tc.text), ","); got != tc.want {
				t.Fatalf("missing rules = %q, want %q", got, tc.want)
			}
		})
	}
}

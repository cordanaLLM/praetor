package agenthook

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func TestRunGuardsGovernedCommandTargets(t *testing.T) {
	governed, plain := repository(t, true), repository(t, false)
	parent := filepath.Dir(governed)
	for _, tc := range []struct{ name, cwd, command string }{
		{"git-C", plain, "git -C " + governed + " push --no-verify origin HEAD"},
		{"cd", plain, "cd " + governed + " && git commit -n -m x"},
		{"assignment", plain, "GIT_DIR=" + governed + "/.git git push --no-verify"},
		{"git-dir", plain, "git --git-dir=" + governed + "/.git push --no-verify"},
		{"pushd", plain, "pushd " + governed + "; git push --no-verify"},
		{"env-C", plain, "env -C " + governed + " git push --no-verify"},
		{"relative-no-repository", parent, "git -C " + filepath.Base(governed) + " push --no-verify"},
		// The incident shapes: a leading cd, the flag after -q, output piped on; a signed commit
		// with the short flag after -S.
		{"incident-push", plain, "cd " + governed + " && git push -q --no-verify -u origin feature 2>&1 | tail -2"},
		{"incident-signed-commit", plain, "cd " + governed + " && git commit -S -n -m msg"},
		{"subshell", plain, "(cd " + governed + " && git push --no-verify)"},
		{"group", plain, "{ cd " + governed + "; git push --no-verify; }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := runTargetCommand(t, tc.cwd, tc.command)
			if response.ExitCode != 2 || !strings.Contains(string(response.Stderr), evasionMessage) {
				t.Fatalf("target evasion allowed: %+v", response)
			}
		})
	}
}

func runTargetCommand(t *testing.T, cwd, command string) Response {
	t.Helper()
	payload := commandPayload(t, map[string]any{
		"cwd": cwd, "tool_input": map[string]any{"command": command},
	})
	return serve(t, "claude", "pre-tool", cwd, payload)
}

func TestRunCommandTargetControls(t *testing.T) {
	governed, plain := repository(t, true), repository(t, false)
	for _, tc := range []struct{ name, command, stderr string }{
		{"plain", "git -C " + plain + " push --no-verify", "praetor hook: workspace not governed, skipped\n"},
		{"read-only", "git -C " + governed + " status", ""},
		{"variable", `git -C "$X" push --no-verify`, "praetor hook: workspace not governed, skipped\n"},
		{"missing", "git -C /does/not/exist push --no-verify", "praetor hook: workspace not governed, skipped\n"},
		{"argument-not-command", "echo cd " + governed + " --no-verify", "praetor hook: workspace not governed, skipped\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := runTargetCommand(t, plain, tc.command)
			if response.ExitCode != 0 || string(response.Stderr) != tc.stderr {
				t.Fatalf("control: %+v", response)
			}
		})
	}
}

func TestRunCommandTargetBoundaries(t *testing.T) {
	governed, plain := repository(t, true), repository(t, false)
	for _, tc := range []struct {
		count, exit int
		stderr      string
	}{
		{maxWorkspaces, 0, ""},
		{maxWorkspaces + 1, 2, "[BLOCKED BY HISS] command names more than 16 command targets\n"},
	} {
		t.Run(strconv.Itoa(tc.count), func(t *testing.T) {
			command := strings.Repeat("git -C "+plain+" status;", tc.count-1) + "git -C " + governed + " status"
			response := runTargetCommand(t, plain, command)
			if response.ExitCode != tc.exit || string(response.Stderr) != tc.stderr {
				t.Fatalf("%d targets: %+v", tc.count, response)
			}
		})
	}
}

func TestCommandTargetsResolveTheHomeDirectory(t *testing.T) {
	governed, plain := repository(t, true), repository(t, false)
	t.Setenv("HOME", filepath.Dir(governed))
	t.Setenv("USERPROFILE", filepath.Dir(governed))
	for _, command := range []string{
		"cd ~/" + filepath.Base(governed) + " && git push --no-verify",
		"git -C ~/" + filepath.Base(governed) + " push --no-verify",
	} {
		if root, err := commandTargetRoot(t.Context(), command, plain); err != nil || root != governed {
			t.Fatalf("%s: root %q, err %v; want %q", command, root, err, governed)
		}
	}
	t.Setenv("HOME", governed)
	t.Setenv("USERPROFILE", governed)
	if root, err := commandTargetRoot(t.Context(), "cd ~ && git status", plain); err != nil || root != governed {
		t.Fatalf("bare ~: root %q, err %v", root, err)
	}
	if root, err := commandTargetRoot(t.Context(), "cd ~nobody/x && git status", plain); err != nil || root != "" {
		t.Fatalf("~user must stay unresolved: root %q, err %v", root, err)
	}
}

func TestScopeAllJudgesACommandOutsideAnyRepository(t *testing.T) {
	outside := t.TempDir()
	all := Invocation{WorkDir: outside, Settings: config.HookSettings{Scope: config.HookScopeAll}}
	command := Canonical{Event: EventPreTool, Command: "git push --no-verify"}
	if root, _, ok := resolveGovernedRoot(t.Context(), Registration{Event: EventPreTool}, command, all); !ok || root != "" {
		t.Fatalf("scope all must judge a pre-tool command outside a repository: %q %v", root, ok)
	}
	for name, tc := range map[string]struct {
		canonical Canonical
		in        Invocation
	}{
		"governed scope":  {command, Invocation{WorkDir: outside}},
		"edit event":      {Canonical{Event: EventPreEdit, Command: "x"}, all},
		"no command text": {Canonical{Event: EventPreTool}, all},
	} {
		if _, verdict, ok := resolveGovernedRoot(t.Context(), Registration{Event: EventPreTool}, tc.canonical, tc.in); ok || verdict.Reason != "no repository" {
			t.Fatalf("%s: want the no repository skip, got %+v %v", name, verdict, ok)
		}
	}
}

func TestScopeAllInUngovernedRepository(t *testing.T) {
	plain := repository(t, false)
	all := Invocation{WorkDir: plain, Settings: config.HookSettings{Scope: config.HookScopeAll}}
	command := Canonical{Event: EventPreTool, Command: "git push --no-verify"}
	if root, _, ok := resolveGovernedRoot(t.Context(), Registration{Event: EventPreTool}, command, all); !ok || root != plain {
		t.Fatalf("scope all must judge a pre-tool command in an ungoverned repository: %q %v; want %q", root, ok, plain)
	}
	for name, tc := range map[string]struct {
		canonical Canonical
		in        Invocation
	}{
		"stop event":      {Canonical{Event: EventStop}, all},
		"edit event":      {Canonical{Event: EventPreEdit, Command: "x"}, all},
		"no command text": {Canonical{Event: EventPreTool}, all},
		"governed scope":  {command, Invocation{WorkDir: plain}},
	} {
		if _, verdict, ok := resolveGovernedRoot(t.Context(), Registration{Event: EventPreTool}, tc.canonical, tc.in); ok || verdict.Reason != "workspace not governed" {
			t.Fatalf("%s: want the workspace not governed skip, got %+v %v", name, verdict, ok)
		}
	}
}

func TestCommandTargetsEmptyCommand(t *testing.T) {
	plain := repository(t, false)
	root, verdict, ok := resolveGovernedRoot(t.Context(), Registration{Event: EventPreTool}, Canonical{}, Invocation{WorkDir: plain})
	if ok || root != "" || verdict.Outcome != Skip || verdict.Reason != "workspace not governed" {
		t.Fatalf("empty command changed: %q %+v %v", root, verdict, ok)
	}
}

func TestCommandTargetForms(t *testing.T) {
	governed, plain := repository(t, true), repository(t, false)
	nested := filepath.Join(governed, "nested space")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{
		"git --no-pager -c key=value -C " + filepath.Dir(governed) + " -C " + filepath.Base(governed) + " status",
		"git -C '" + nested + "' status", "git -C \"" + nested + "\" status",
		"git --git-dir " + governed + "/.git status",
		"git --work-tree=" + governed + " status", "git --work-tree " + governed + " status",
		"GIT_WORK_TREE='" + governed + "' git status",
		"env --chdir=" + governed + " git status", "env --chdir " + governed + " git status",
		"true&&cd " + governed, "false||pushd " + governed, "true|cd " + governed,
		"true\ncd " + governed, "env -u HOME -C " + governed + " git status",
		"git -C" + governed + " status", "git -C " + governed + " -C '' status",
		"git --work-tree=. -C " + governed + " status",
		"GIT_DIR='" + governed + "/.git' OTHER=x git status",
		"true;cd -- '" + nested + "'", "env -C " + governed + " -- git status",
		"git --attr-source HEAD --config-env key=VALUE --namespace ns -C " + governed + " status",
	} {
		t.Run(command, func(t *testing.T) {
			root, err := commandTargetRoot(t.Context(), command, plain)
			if err != nil || root != governed {
				t.Fatalf("root %q, err %v; want %q", root, err, governed)
			}
		})
	}
}

func TestCommandTargetsFailClosedBeforeSelectingARoot(t *testing.T) {
	governed, plain := repository(t, true), repository(t, false)
	for _, command := range []string{
		"git -C " + governed + " status;" + strings.Repeat("cd missing;", maxWorkspaces),
		"git -C " + governed + " " + strings.Repeat("x ", 5000),
		"git " + strings.Repeat("-C "+plain+" ", maxWorkspaces) + "-C " + governed + " status",
		"git -C " + governed + " status;" + strings.Repeat("\n", 100001),
	} {
		if root, err := commandTargetRoot(t.Context(), command, plain); err == nil || root != "" {
			t.Fatalf("overbound targets resolved: %q %v", root, err)
		}
	}
}

func TestCommandTargetsIgnoreOneProbeErrorAndContinue(t *testing.T) {
	governed, plain := repository(t, true), repository(t, false)
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, ".git"), []byte("malformed gitfile\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := "git -C " + broken + " status; git -C " + governed + " status"
	if root, err := commandTargetRoot(t.Context(), command, plain); err != nil || root != governed {
		t.Fatalf("probe error hid a governed target: %q %v", root, err)
	}
}

func TestCommandTargetsIgnoreAssignmentOnlyStatements(t *testing.T) {
	governed, plain := repository(t, true), repository(t, false)
	for _, command := range []string{
		"GIT_DIR=" + governed + "/.git", "GIT_WORK_TREE=" + governed,
		"cd '" + governed, "git -C \"'" + governed + "'\" status",
		"git -C " + filepath.Join(governed, manifestName) + " status",
	} {
		if root, err := commandTargetRoot(t.Context(), command, plain); err != nil || root != "" {
			t.Fatalf("unresolvable target: %q %v", root, err)
		}
	}
}

func TestCommandTargetsIgnoreUnresolvableDirectories(t *testing.T) {
	plain := repository(t, false)
	for _, command := range []string{
		`git -C "$X" status`, "git -C `pwd` status", "git -C ~/repo status",
		"git -C ~someone/repo status", "git -C repo* status", "git -C repo? status",
		"git -C [ab] status", "git -C /does/not/exist status", "git -C status", "cd",
		"git -C '$X' status", "echo git -C /outside status", "git status -C /outside",
	} {
		t.Run(command, func(t *testing.T) {
			root, err := commandTargetRoot(t.Context(), command, plain)
			if err != nil || root != "" {
				t.Fatalf("unresolvable root %q, err %v", root, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if root, err := commandTargetRoot(ctx, "git -C "+plain+" status", plain); root != "" || err != nil {
		t.Fatalf("derived git probe error must be ignored: %q %v", root, err)
	}
}

func TestCommandTargetsPreserveWorkspacePrecedence(t *testing.T) {
	governed, plain := repository(t, true), repository(t, false)
	canonical := Canonical{Event: EventPreTool, Workspaces: []string{governed}, Command: strings.Repeat("cd missing;", 17)}
	if root, _, ok := resolveGovernedRoot(t.Context(), Registration{Event: EventPreTool}, canonical, Invocation{WorkDir: plain}); !ok || root != governed {
		t.Fatalf("governed payload must win: %q %v", root, ok)
	}
	canonical.Workspaces = []string{plain}
	in := Invocation{WorkDir: governed, Settings: config.HookSettings{Scope: config.HookScopeAll}}
	if root, _, ok := resolveGovernedRoot(t.Context(), Registration{Event: EventPreTool}, canonical, in); !ok || root != plain {
		t.Fatalf("scope all must bypass target derivation: %q %v", root, ok)
	}
	canonical.Workspaces = []string{filepath.Join(plain, "missing")}
	canonical.Command = "git -C " + governed + " status"
	if _, verdict, ok := resolveGovernedRoot(t.Context(), Registration{Event: EventPreTool}, canonical, in); ok || verdict.Outcome != Deny {
		t.Fatalf("payload probe errors must deny: %+v %v", verdict, ok)
	}
}

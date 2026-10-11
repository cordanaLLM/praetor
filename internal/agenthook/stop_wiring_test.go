package agenthook

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// trackedStopFiles are the repository files whose stop registration must reach the engine.
var trackedStopFiles = map[string]struct {
	path string
	unit time.Duration
}{
	"claude": {".claude/settings.json", time.Second},
	"codex":  {".codex/hooks.json", time.Second},
	"gemini": {".gemini/settings.json", time.Millisecond},
}

// trackedStopCommands returns the commands the tracked file of client registers for the
// native stop event of its stop row.
func trackedStopCommands(t *testing.T, client string, row Registration) []string {
	t.Helper()
	var document struct {
		Hooks map[string][]nativeGroup `json:"hooks"`
	}
	readTrackedJSON(t, trackedStopFiles[client].path, &document)
	var commands []string
	for _, group := range document.Hooks[row.NativeEvent] {
		for _, hook := range group.Hooks {
			commands = append(commands, hook.Command)
		}
	}
	return commands
}

// TestTrackedStopRegistrationReachesTheEngine: the stop registration of each native client
// is the launcher call of its stop row, at the row's timeout, and nothing else. Before the
// wiring fix every one of them ran checkpoint.py directly, so the prose-question check in
// evaluateStop never ran for a real stop.
func TestTrackedStopRegistrationReachesTheEngine(t *testing.T) {
	for client, file := range trackedStopFiles {
		row := stopRow(t, client)
		var document struct {
			Hooks map[string][]nativeGroup `json:"hooks"`
		}
		readTrackedJSON(t, file.path, &document)
		if !groupsHold(document.Hooks[row.NativeEvent], row, file.unit) {
			t.Errorf("%s: no %s group with the engine stop command at %s", file.path, row.NativeEvent, row.Timeout)
		}
		commands := trackedStopCommands(t, client, row)
		if len(commands) != 1 || commands[0] != trackedCommand(row) {
			t.Errorf("%s: stop commands %q, want exactly %q", file.path, commands, trackedCommand(row))
		}
	}
}

// TestTrackedStopCommandIsNotTheCheckpointScript is the negative case of the check above: the
// command the files carried before the fix names no engine row.
func TestTrackedStopCommandIsNotTheCheckpointScript(t *testing.T) {
	for _, command := range []string{
		`python3 -B "${CLAUDE_PROJECT_DIR}/.config/agent/hooks/checkpoint.py"`,
		"python3 -B .config/agent/hooks/checkpoint.py",
	} {
		if client, event, engine := trackedPair(command); engine || client != "" || event != "" {
			t.Errorf("%q must not parse as an engine call: %q %q %t", command, client, event, engine)
		}
	}
}

// TestRegisteredStopCommandDeniesAClosingQuestion replays the registered command of each
// client through Run: the pair the command names resolves to the stop row, and a stop payload
// of that client with a closing question is denied, a clean one allowed.
func TestRegisteredStopCommandDeniesAClosingQuestion(t *testing.T) {
	root := syncedRepository(t)
	_, getenv := buildStub(t, "python3")
	stubStdout(t, cleanReport)
	for client := range trackedStopFiles {
		row := stopRow(t, client)
		named, event, engine := trackedPair(trackedStopCommands(t, client, row)[0])
		if !engine || named != client || event != string(EventStop) {
			t.Fatalf("%s: registered command names %q %q engine=%t", client, named, event, engine)
		}
		for message, want := range map[string]bool{"Fixed it.\n\nShould I push?": true, "Fixed it. Tests pass.": false} {
			in := stopInvocation(named, root, getenv, finalMessagePayload(t, client, message, false))
			in.Event = event
			in.Policy = policy(t)
			response := Run(context.Background(), in)
			denied := response.ExitCode == 2 && strings.Contains(string(response.Stderr), "asks the operator in prose")
			if denied != want {
				t.Errorf("%s %q: %+v, want denied=%t", client, message, response, want)
			}
		}
	}
}

// TestStopLauncherTimeouts pins the stop row's two bounds: the launcher outwaits the engine's
// stop budget, and both probes plus that wait end before the client's stop timeout.
func TestStopLauncherTimeouts(t *testing.T) {
	run := launcherSeconds(t, "STOP_RUN_TIMEOUT")
	probe := launcherSeconds(t, "PROBE_TIMEOUT")
	for client := range trackedStopFiles {
		timeout := stopRow(t, client).Timeout
		if problems := stopLauncherProblems(run, probe, timeout); len(problems) != 0 {
			t.Errorf("%s: %q", client, problems)
		}
	}
	budget := budgetFor(EventStop)
	for _, tc := range []struct {
		run, timeout time.Duration
		pass         bool
	}{
		{budget + launcherTimingMargin, stopTimeout, true},
		{budget + launcherTimingMargin - time.Second, stopTimeout, false},
		{stopTimeout - launcherCandidates*probe - launcherTimingMargin, stopTimeout, true},
		{stopTimeout - launcherCandidates*probe - launcherTimingMargin + time.Second, stopTimeout, false},
		{45 * time.Second, stopTimeout, false},
	} {
		if problems := stopLauncherProblems(tc.run, probe, tc.timeout); (len(problems) == 0) != tc.pass {
			t.Errorf("run %s, client timeout %s: %q, want pass=%t", tc.run, tc.timeout, problems, tc.pass)
		}
	}
}

// TestStopFallbackOutwaitsTheAdapterBackstop pins the no-engine fallback wait: it must exceed
// checkpoint.py's HOOK_LIMIT plus its stop allowance HOOK_STOP, so the adapter's own block
// answers a hang before the launcher kills it, and with both probes still end before the
// client timeout.
func TestStopFallbackOutwaitsTheAdapterBackstop(t *testing.T) {
	fallback := launcherSeconds(t, "STOP_FALLBACK_TIMEOUT")
	probe := launcherSeconds(t, "PROBE_TIMEOUT")
	const adapter = ".config/agent/hooks/checkpoint.py"
	limit := scriptSeconds(t, adapter, "HOOK_LIMIT")
	if problems := fallbackProblems(fallback, probe, limit, 2*scriptSeconds(t, adapter, "JOB_GRACE"), stopTimeout); len(problems) != 0 {
		t.Errorf("%q", problems)
	}
	for _, tc := range []struct {
		fallback time.Duration
		pass     bool
	}{
		{70 * time.Second, true},
		{limit + 12*time.Second + launcherTimingMargin - time.Second, false}, // below the bound
		{limit + 12*time.Second + launcherTimingMargin, true},                // boundary
		{55 * time.Second, false},                                            // the old value
		{80 * time.Second, false},                                            // probes push past 90 s
	} {
		if problems := fallbackProblems(tc.fallback, probe, limit, 12*time.Second, stopTimeout); (len(problems) == 0) != tc.pass {
			t.Errorf("fallback %s: %q, want pass=%t", tc.fallback, problems, tc.pass)
		}
	}
}

func fallbackProblems(fallback, probe, limit, stop, timeout time.Duration) []string {
	var problems []string
	if limit+stop+launcherTimingMargin > fallback {
		problems = append(problems, "fallback wait "+fallback.String()+" ends before the adapter backstop at "+(limit+stop).String())
	}
	if worst := launcherCandidates*probe + fallback + launcherTimingMargin; worst > timeout {
		problems = append(problems, "probes plus fallback take up to "+worst.String()+", past the client timeout "+timeout.String())
	}
	return problems
}

func stopLauncherProblems(run, probe, timeout time.Duration) []string {
	var problems []string
	if budget := budgetFor(EventStop); budget+launcherTimingMargin > run {
		problems = append(problems, "run timeout "+run.String()+" cuts the "+budget.String()+" stop budget short")
	}
	if worst := launcherCandidates*probe + run + launcherTimingMargin; worst > timeout {
		problems = append(problems, "probes plus run take up to "+worst.String()+", past the client timeout "+timeout.String())
	}
	return problems
}

// TestRegisteredStopCommandHaltsARepeatedDeny: a stop that is still denied after one
// continuation ends the session with continue:false and exit 0 instead of exit 2 again, for
// every client (positive); the first pass still exits 2 (negative); a clean repeated stop
// stays silent (boundary).
func TestRegisteredStopCommandHaltsARepeatedDeny(t *testing.T) {
	root := syncedRepository(t)
	_, getenv := buildStub(t, "python3")
	for client := range trackedStopFiles {
		stubStdout(t, dueReport("commit"))
		run := func(active bool) Response {
			in := stopInvocation(client, root, getenv, finalMessagePayload(t, client, "Done.", active))
			in.Event = string(EventStop)
			in.Policy = policy(t)
			return Run(context.Background(), in)
		}
		first := run(false)
		if first.ExitCode != 2 || len(first.Stdout) != 0 {
			t.Errorf("%s first pass: %+v, want exit 2 and no stdout", client, first)
		}
		second := run(true)
		var body struct {
			Continue *bool  `json:"continue"`
			Reason   string `json:"stopReason"`
		}
		if err := json.Unmarshal(second.Stdout, &body); err != nil || second.ExitCode != 0 ||
			body.Continue == nil || *body.Continue || !strings.Contains(body.Reason, "Checkpoint remains incomplete") {
			t.Errorf("%s repeated pass: %+v (%v), want exit 0 and continue:false", client, second, err)
		}
		stubStdout(t, cleanReport)
		if clean := run(true); clean.ExitCode != 0 || len(clean.Stdout) != 0 {
			t.Errorf("%s clean repeated pass: %+v, want silent allow", client, clean)
		}
	}
}

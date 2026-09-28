package agenthook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestRegistrationsPerClient(t *testing.T) {
	for client, events := range map[string][]Event{
		"claude": {EventPreTool, EventPreEdit, EventPostTool, EventStop, EventPreDispatch, EventDispatchReceipt,
			EventDispatchAbort, EventDispatchAbort, EventPreHandback, EventHandbackReceipt, EventHandbackAbort,
			EventHandbackAbort, EventPostReturn},
		"codex":    {EventPreTool, EventPostTool, EventStop, EventPreDispatch, EventPostReturn}, // no pre-edit row: measured fact, section 1
		"gemini":   {EventPreTool, EventPreEdit, EventPostTool, EventStop, EventPreDispatch},
		"lefthook": {EventPreTool, EventEnvironment}, // checkpoint rows land in H4
		"agy":      {EventPreTool, EventPreDispatch, EventStop},
	} {
		rows := Registrations(client)
		if len(rows) != len(events) {
			t.Fatalf("%s rows: %+v", client, rows)
		}
		for index, row := range rows {
			if row.Client != client || row.Event != events[index] || row.NativeEvent == "" {
				t.Errorf("%s row %d: %+v", client, index, row)
			}
		}
	}
	for _, client := range []string{"", "CLAUDE"} {
		if rows := Registrations(client); len(rows) != 0 {
			t.Errorf("%q has rows: %+v", client, rows)
		}
	}
	rows := Registrations("claude")
	rows[0].Matcher = "changed"
	if Registrations("claude")[0].Matcher == "changed" {
		t.Error("Registrations hands out the table itself")
	}
}

// TestNativePreToolRowSelectsTheCommandTool: the pre-tool row of every client with a repository
// hook file selects ^TOOL$ for the one tool its dialect routes to the command policy, the
// matcher every client reads as that tool alone, so an adopter entry of that anchored form
// is the registration adoption looks for (#502). AGY registers through its plugin and has no
// such file.
func TestNativePreToolRowSelectsTheCommandTool(t *testing.T) {
	checked := 0
	for _, row := range registrationTable {
		if _, ok := NativeHookFile(row.Client); !ok || row.Event != EventPreTool {
			continue
		}
		dialect, _ := DialectFor(row.Client)
		if len(dialect.commandTools) != 1 || row.Matcher != "^"+dialect.commandTools[0]+"$" {
			t.Errorf("%s pre-tool matcher %q, command tools %v", row.Client, row.Matcher, dialect.commandTools)
		}
		checked++
	}
	if checked != len(nativeHookFiles) {
		t.Fatalf("checked %d pre-tool rows for %d hook files", checked, len(nativeHookFiles))
	}
}

func TestRegistrationCommandIsOnePortableCall(t *testing.T) {
	shape := regexp.MustCompile(`^praetorctl hook [a-z-]+ [a-z-]+$`)
	for _, row := range registrationTable {
		command := row.Command()
		if !shape.MatchString(command) || strings.ContainsAny(command, "$`\"'()|&;<>%!^\\") {
			t.Errorf("registration needs a shell feature: %q", command)
		}
		if _, ok := DialectFor(row.Client); !ok {
			t.Errorf("%s is registered without a dialect", row.Client)
		}
	}
	for _, dialect := range dialectTable {
		if len(Registrations(dialect.Client)) == 0 || len(Registrations(dialect.payloadClient)) == 0 {
			t.Errorf("dialect %s has no registration rows", dialect.Client)
		}
	}
	if got := (Registration{}).Command(); got != "praetorctl hook  " {
		t.Errorf("zero row renders %q", got)
	}
}

func TestParseArguments(t *testing.T) {
	row, err := ParseArguments("gemini", "pre-tool")
	if err != nil || row.NativeEvent != "BeforeTool" || row.Timeout != 15*time.Second {
		t.Fatalf("gemini pre-tool: %+v %v", row, err)
	}
	if row, err = ParseArguments("lefthook", "environment"); err != nil || row.NativeEvent != "pre-rebase" {
		t.Fatalf("lefthook environment: %+v %v", row, err)
	}
	if row, err = ParseArguments("agy", "pre-tool"); err != nil || row.NativeEvent != "PreToolUse" || row.Matcher != "*" || row.Timeout != 30*time.Second {
		t.Fatalf("agy pre-tool: %+v %v", row, err)
	}
	if row, err = ParseArguments("agy", "stop"); err != nil || row.NativeEvent != "Stop" || row.Timeout != 30*time.Second {
		t.Fatalf("agy stop: %+v %v", row, err)
	}
	for _, pair := range [][2]string{
		{"", ""}, {"codex", "pre-edit"}, {"codex", "dispatch-receipt"}, {"agy", "post-tool"}, {"claude", "pre-tool\n"}, {"cl4ude", "pre-tool"},
		{"claude", "PRE-TOOL"}, {"../claude", "pre-tool"}, {"claude", "environment"}, {"gemini", "post-return"},
	} {
		if row, err := ParseArguments(pair[0], pair[1]); !errors.Is(err, ErrUnsupported) || row != (Registration{}) {
			t.Errorf("%q: %+v %v", pair, row, err)
		}
	}
}

// nativeGroup is the registration group shape the three native clients share today.
type nativeGroup struct {
	Matcher string `json:"matcher"`
	Hooks   []struct {
		Timeout int64  `json:"timeout"`
		Command string `json:"command"`
	} `json:"hooks"`
}

// TestRegistrationTableMatchesTheTrackedClientFiles replays each agent-text row against
// the tracked files clients read. Legacy command/checkpoint rows remain on their existing
// adapters; #415 owns only subagent brief, receipt and return registrations.
func TestRegistrationTableMatchesTheTrackedClientFiles(t *testing.T) {
	for client, file := range map[string]struct {
		path string
		unit time.Duration
	}{
		"claude": {".claude/settings.json", time.Second}, "codex": {".codex/hooks.json", time.Second},
		"gemini": {".gemini/settings.json", time.Millisecond},
	} {
		data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(file.path)))
		if err != nil {
			t.Fatal(err)
		}
		var document struct {
			Hooks map[string][]nativeGroup `json:"hooks"`
		}
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatalf("%s: %v", file.path, err)
		}
		for _, row := range Registrations(client) {
			if !agentTrafficEvent(row.Event) {
				continue
			}
			if !groupsHold(document.Hooks[row.NativeEvent], row, file.unit) {
				t.Errorf("%s: no %s group with matcher %q and %s", file.path, row.NativeEvent, row.Matcher, row.Timeout)
			}
		}
	}
}

// TestTrackedRegistrationsNameOnlyEngineRows is the reverse of the table-to-file check: every
// `praetorctl hook` command a tracked client file carries must parse to an engine row of
// that client, under that row's native event, matcher and timeout. Engine skew is a stated
// skip at runtime (unsupportedResponse), so a typo'd or extra tracked row must fail here.
func TestTrackedRegistrationsNameOnlyEngineRows(t *testing.T) {
	for client, file := range map[string]struct {
		path string
		unit time.Duration
	}{
		"claude": {".claude/settings.json", time.Second}, "codex": {".codex/hooks.json", time.Second},
		"gemini": {".gemini/settings.json", time.Millisecond},
	} {
		var document struct {
			Hooks map[string][]nativeGroup `json:"hooks"`
		}
		readTrackedJSON(t, file.path, &document)
		if problems := trackedCommandProblems(client, document.Hooks, file.unit); len(problems) != 0 {
			t.Errorf("%s names commands no engine row carries: %q", file.path, problems)
		}
	}
	var plugins map[string]map[string][]nativeGroup
	readTrackedJSON(t, ".agents/plugins/praetor/hooks.json", &plugins)
	for name, hooks := range plugins {
		if problems := trackedCommandProblems("agy", hooks, time.Second); len(problems) != 0 {
			t.Errorf("agy plugin %s names commands no engine row carries: %q", name, problems)
		}
	}
}

func TestTrackedCommandProblemsRejectsRowsTheEngineLacks(t *testing.T) {
	group := func(matcher, command string, timeout int64) nativeGroup {
		var built nativeGroup
		raw := `{"matcher":` + strconv.Quote(matcher) + `,"hooks":[{"command":` + strconv.Quote(command) +
			`,"timeout":` + strconv.FormatInt(timeout, 10) + `}]}`
		if err := json.Unmarshal([]byte(raw), &built); err != nil {
			t.Fatal(err)
		}
		return built
	}
	launch := func(root, pair string) string { return `python3 -B "` + root + "/" + launcherScript + `" ` + pair }
	claude := func(pair string) string { return launch("${CLAUDE_PROJECT_DIR}", pair) }
	valid := group("^Agent$", claude("claude pre-dispatch"), 15)
	for name, tc := range map[string]struct {
		native string
		group  nativeGroup
		want   int
	}{
		"engine row":                    {"PreToolUse", valid, 0},
		"second row of one event":       {"PermissionDenied", group("^Agent$", claude("claude dispatch-abort"), 15), 0},
		"legacy adapter is not a row":   {"PreToolUse", group("^Bash$", "python3 -B guard.py", 15), 0},
		"launcher row of a legacy pair": {"PreToolUse", group("^Bash$", claude("claude pre-tool"), 15), 0},
		"engine from PATH, no launcher": {"PreToolUse", group("^Agent$", "praetorctl hook claude pre-dispatch", 15), 1},
		"launcher from the cwd's tree":  {"PreToolUse", group("^Agent$", launch("$(git rev-parse --show-toplevel)", "claude pre-dispatch"), 15), 1},
		"event of another native":       {"Stop", group("^Agent$", claude("claude dispatch-abort"), 15), 1},
		"typo'd event":                  {"PreToolUse", group("^Agent$", claude("claude pre-dispach"), 15), 1},
		"other client's row":            {"PreToolUse", group("^Agent$", claude("codex pre-dispatch"), 15), 1},
		"wrong native event":            {"PostToolUse", valid, 1},
		"wrong matcher":                 {"PreToolUse", group("^Task$", claude("claude pre-dispatch"), 15), 1},
		"wrong timeout":                 {"PreToolUse", group("^Agent$", claude("claude pre-dispatch"), 16), 1},
		"extra argument":                {"PreToolUse", group("^Agent$", claude("claude pre-dispatch --x"), 15), 1},
		"missing event":                 {"PreToolUse", group("^Agent$", claude("claude"), 15), 1},
	} {
		got := trackedCommandProblems("claude", map[string][]nativeGroup{tc.native: {tc.group}}, time.Second)
		if len(got) != tc.want {
			t.Errorf("%s: problems %q, want %d", name, got, tc.want)
		}
	}
	checkout := `python3 -B "../../../` + launcherScript + `" agy pre-dispatch`
	for name, tc := range map[string]struct {
		command string
		want    int
	}{
		"plugin launcher":               {pluginLaunch + "agy pre-dispatch", 0},
		"engine from PATH, no launcher": {"praetorctl hook agy pre-dispatch", 1},
		"checkout launcher from plugin": {checkout, 1},
		"unquoted checkout launcher":    {strings.ReplaceAll(checkout, `"`, ""), 1},
		"gemini form from plugin":       {relativeLaunch + "agy pre-dispatch", 1},
		"launcher under another name":   {"python3 praetor_hook.py agy pre-dispatch", 1},
		"typo'd event":                  {pluginLaunch + "agy pre-dispach", 1},
		"other client's row":            {pluginLaunch + "claude pre-dispatch", 1},
		"extra argument":                {pluginLaunch + "agy pre-dispatch --x", 1},
	} {
		hooks := map[string][]nativeGroup{"PreToolUse": {group("invoke_subagent", tc.command, 30)}}
		if got := trackedCommandProblems("agy", hooks, time.Second); len(got) != tc.want {
			t.Errorf("agy %s: problems %q, want %d", name, got, tc.want)
		}
	}
}

// TestGeminiLauncherRowNeedsNoSubstitution: Gemini CLI runs hooks from its project directory,
// so its tracked row names the launcher by a relative path, like its legacy adapter rows; the
// Git-root substitution form of the Codex row is not its tracked form.
func TestGeminiLauncherRowNeedsNoSubstitution(t *testing.T) {
	substituted := `python3 -B "$(git rev-parse --show-toplevel)/` + launcherScript + `" gemini pre-dispatch`
	for command, want := range map[string]int{relativeLaunch + "gemini pre-dispatch": 0, substituted: 1} {
		var group nativeGroup
		raw := `{"matcher":"^invoke_agent$","hooks":[{"command":` + strconv.Quote(command) + `,"timeout":15000}]}`
		if err := json.Unmarshal([]byte(raw), &group); err != nil {
			t.Fatal(err)
		}
		hooks := map[string][]nativeGroup{"BeforeTool": {group}}
		if got := trackedCommandProblems("gemini", hooks, time.Millisecond); len(got) != want {
			t.Errorf("%s: problems %q, want %d", command, got, want)
		}
	}
}

// launcherScript hands a tracked row to an engine that serves it and skips with the reason
// when none does, so an engine older than the row never blocks the client.
const launcherScript = ".config/agent/hooks/praetor_hook.py"

// pluginLauncher is the AGY plugin's copy of launcherScript. AGY runs a plugin's hooks from
// the directory holding its hooks.json, and an installed plugin directory may sit outside any
// checkout, so the plugin carries the launcher and names it relative to that directory.
const (
	pluginLauncher = ".agents/plugins/praetor/praetor_hook.py"
	pluginLaunch   = "python3 -B praetor_hook.py "
)

// relativeLaunch is the Gemini CLI form of the launcher call. Gemini runs every hook from the
// project directory it started in, so the path is relative to it, as in its legacy adapter
// rows, and needs no quoting or substitution in bash or PowerShell.
const relativeLaunch = "python3 -B " + launcherScript + " "

// trackedRoot is how each repository-scoped client file names the checkout holding the
// launcher: Claude Code's project directory stays the tree its settings came from even after
// the session enters an older worktree; Codex runs hooks from the session directory, which
// may be a subdirectory. An empty root is relativeLaunch. AGY's row uses pluginLaunch instead.
var trackedRoot = map[string]string{
	"claude": "${CLAUDE_PROJECT_DIR}",
	"codex":  "$(git rev-parse --show-toplevel)",
	"gemini": "",
}

// trackedCommand is the exact string this repository's client files carry for row.
func trackedCommand(row Registration) string {
	pair := row.Client + " " + string(row.Event)
	if row.Client == "agy" {
		return pluginLaunch + pair
	}
	root, launched := trackedRoot[row.Client]
	switch {
	case !launched:
		return row.Command()
	case root == "":
		return relativeLaunch + pair
	}
	return `python3 -B "` + root + "/" + launcherScript + `" ` + pair
}

// trackedPair returns the client and event a tracked engine command names, direct or through
// either launcher form; engine is false for anything else (the legacy adapters). A command
// that names the launcher in another form, or has the wrong number of arguments, names no
// client.
func trackedPair(command string) (client, event string, engine bool) {
	rest, found := strings.CutPrefix(command, "praetorctl hook ")
	if !found {
		rest, found = strings.CutPrefix(command, pluginLaunch)
	}
	if !found {
		rest, found = strings.CutPrefix(command, relativeLaunch)
	}
	if !found {
		_, rest, found = strings.Cut(command, launcherScript+`" `)
	}
	if !found {
		return "", "", strings.HasPrefix(command, "praetorctl hook") || strings.Contains(command, "praetor_hook.py")
	}
	fields := strings.Split(rest, " ")
	if len(fields) != 2 {
		return "", "", true
	}
	return fields[0], fields[1], true
}

// trackedCommandProblems lists every engine command in hooks (native event to groups) that no
// engine row of client carries, in its tracked form, with that native event, matcher and timeout.
func trackedCommandProblems(client string, hooks map[string][]nativeGroup, unit time.Duration) []string {
	var problems []string
	for native, groups := range hooks {
		for _, group := range groups {
			for index := range group.Hooks {
				single := group
				single.Hooks = group.Hooks[index : index+1]
				if !trackedCommandHeld(client, native, single, unit) {
					problems = append(problems, native+": "+group.Hooks[index].Command)
				}
			}
		}
	}
	return problems
}

// trackedCommandHeld reports whether a one-hook group is either not an engine command or
// exactly an engine row of client under native, in the row's tracked form. One event may
// have several rows (Claude's dispatch-abort serves PostToolUseFailure and PermissionDenied),
// so every row is tried.
func trackedCommandHeld(client, native string, group nativeGroup, unit time.Duration) bool {
	named, event, engine := trackedPair(group.Hooks[0].Command)
	if !engine {
		return true
	}
	if named != client {
		return false
	}
	if _, err := ParseArguments(named, event); err != nil {
		return false
	}
	for _, row := range Registrations(client) {
		if string(row.Event) == event && row.NativeEvent == native && groupsHold([]nativeGroup{group}, row, unit) {
			return true
		}
	}
	return false
}

func readTrackedJSON(t *testing.T, rel string, target any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
}

// TestTrackedLauncherIsInTheTree guards the file every launcher row names: a missing script
// makes python3 exit 2, which the native clients read as a block.
func TestTrackedLauncherIsInTheTree(t *testing.T) {
	info, err := os.Stat(filepath.Join("..", "..", filepath.FromSlash(launcherScript)))
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("%s: %v", launcherScript, err)
	}
}

// TestPluginLauncherIsTheTrackedLauncher pins the AGY plugin's launcher to the canonical one
// byte for byte, so the two copies cannot drift into two behaviours (HISS-19).
func TestPluginLauncherIsTheTrackedLauncher(t *testing.T) {
	canonical, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(launcherScript)))
	if err != nil {
		t.Fatal(err)
	}
	plugin, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(pluginLauncher)))
	if err != nil {
		t.Fatal(err)
	}
	if len(canonical) == 0 || !bytes.Equal(plugin, canonical) {
		t.Fatalf("%s differs from %s: copy the canonical launcher over it", pluginLauncher, launcherScript)
	}
}

// launcherTimingMargin is the room the launcher timing checks leave for process start-up and
// for writing the answer.
const launcherTimingMargin = 2 * time.Second

// launcherCandidates is the most engines the launcher probes before it runs one (candidates in
// praetor_hook.py: the checkout's bin/praetorctl, then praetorctl from PATH).
const launcherCandidates = 2

// launcherSeconds reads one integer seconds constant of the tracked launcher.
func launcherSeconds(t *testing.T, name string) time.Duration {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(launcherScript)))
	if err != nil {
		t.Fatal(err)
	}
	seconds, err := launcherConstant(source, name)
	if err != nil {
		t.Fatalf("%s: %v", launcherScript, err)
	}
	return seconds
}

// launcherConstant reads the integer seconds constant name from launcher source. The launcher
// is text=auto in .gitattributes, so a Windows checkout with core.autocrlf holds it with CRLF
// line endings, which CPython runs as the same program (#558). The constant is read from the
// LF text; mixed endings and a lone carriage return stay an error
// (util.NormalizeLineEndingsStrict), never a match.
func launcherConstant(source []byte, name string) (time.Duration, error) {
	text, _, err := util.NormalizeLineEndingsStrict(string(source))
	if err != nil {
		return 0, err
	}
	match := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + ` = (\d+)$`).FindStringSubmatch(text)
	if match == nil {
		return 0, fmt.Errorf("no %s = <seconds> line", name)
	}
	seconds, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return time.Duration(seconds) * time.Second, nil
}

// TestLauncherConstantReadsEitherCheckoutLineEnding: the tracked launcher's constants read the
// same from an LF and a CRLF checkout (positive); a missing constant, mixed endings and a lone
// carriage return are errors (negative); a last line without a newline, a zero value and a
// longer name sharing the prefix are read exactly (boundary).
func TestLauncherConstantReadsEitherCheckoutLineEnding(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(launcherScript)))
	if err != nil {
		t.Fatal(err)
	}
	lf, _, err := util.NormalizeLineEndingsStrict(string(source))
	if err != nil {
		t.Fatalf("%s: %v", launcherScript, err)
	}
	crlf := util.RestoreLineEndings(lf, true)
	for _, name := range []string{"RUN_TIMEOUT", "PROBE_TIMEOUT"} {
		fromLF, lfErr := launcherConstant([]byte(lf), name)
		fromCRLF, crlfErr := launcherConstant([]byte(crlf), name)
		if lfErr != nil || crlfErr != nil || fromLF <= 0 || fromLF != fromCRLF {
			t.Errorf("%s: LF %s (%v), CRLF %s (%v)", name, fromLF, lfErr, fromCRLF, crlfErr)
		}
	}
	for _, tc := range []struct {
		source string
		want   time.Duration
		ok     bool
	}{
		{"A = 1\nRUN_TIMEOUT = 45\n", 45 * time.Second, true},
		{"A = 1\r\nRUN_TIMEOUT = 45\r\n", 45 * time.Second, true},
		{"A = 1\r\nRUN_TIMEOUT = 45", 45 * time.Second, true},
		{"RUN_TIMEOUT = 0\n", 0, true},
		{"RUN_TIMEOUT_MAX = 9\nRUN_TIMEOUT = 3\n", 3 * time.Second, true},
		{"PROBE_TIMEOUT = 5\n", 0, false},
		{"RUN_TIMEOUT_MAX = 9\n", 0, false},
		{"  RUN_TIMEOUT = 45\n", 0, false},
		{"A = 1\nRUN_TIMEOUT = 45\r\n", 0, false},
		{"A = 1\rRUN_TIMEOUT = 45\r", 0, false},
	} {
		got, err := launcherConstant([]byte(tc.source), "RUN_TIMEOUT")
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("%q: got %s (%v), want %s ok=%t", tc.source, got, err, tc.want, tc.ok)
		}
	}
}

// launcherTimeoutProblems lists how the launcher's run and probe timeouts break the timing of
// the rows it fronts. The run timeout must outwait each row's engine budget, or the launcher
// kills an engine that would still have answered and the verdict fails open (exit 1 for the
// native clients, allow for AGY). Both probes plus the run must also end before the longest
// launcher row gives up, so its stated fallback lands before the client's own timeout.
func launcherTimeoutProblems(run, probe time.Duration) []string {
	var problems []string
	for _, row := range launcherRows() {
		if budget := budgetFor(row.Event); budget+launcherTimingMargin > run {
			problems = append(problems, fmt.Sprintf("%s %s: run timeout %s cuts the %s engine budget short", row.Client, row.Event, run, budget))
		}
	}
	if worst, longest := launcherCandidates*probe+run+launcherTimingMargin, longestLauncherRow(); worst > longest {
		problems = append(problems, fmt.Sprintf("probes plus run take up to %s, past the longest launcher row's %s", worst, longest))
	}
	return problems
}

// launcherRows are the engine rows the tracked client files reach through praetor_hook.py.
func launcherRows() []Registration {
	var rows []Registration
	for _, row := range registrationTable {
		if agentTrafficEvent(row.Event) && strings.Contains(trackedCommand(row), "praetor_hook.py") {
			rows = append(rows, row)
		}
	}
	return rows
}

func longestLauncherRow() time.Duration {
	var longest time.Duration
	for _, row := range launcherRows() {
		longest = max(longest, row.Timeout)
	}
	return longest
}

// TestLauncherTimeoutsOutwaitEngineBudgets pins praetor_hook.py's RUN_TIMEOUT between the
// longest engine budget of a launcher row (post-return, 30 s) and the longest launcher row's
// timeout (60 s). A run timeout of 10 s killed post-return evaluation at 10 s and beat the
// 10 s dispatch budget, so a late deny failed open.
func TestLauncherTimeoutsOutwaitEngineBudgets(t *testing.T) {
	run, probe := launcherSeconds(t, "RUN_TIMEOUT"), launcherSeconds(t, "PROBE_TIMEOUT")
	if problems := launcherTimeoutProblems(run, probe); len(problems) != 0 {
		t.Fatalf("%s timing: %q", launcherScript, problems)
	}
	shortest := returnBudget + launcherTimingMargin
	longest := longestLauncherRow() - launcherCandidates*probe - launcherTimingMargin
	if len(launcherRows()) == 0 || longestLauncherRow() != 60*time.Second {
		t.Fatalf("launcher rows %+v: want the 60 s post-return rows among them", launcherRows())
	}
	for _, tc := range []struct {
		run  time.Duration
		pass bool
	}{
		{shortest, true}, {shortest - time.Second, false}, {longest, true}, {longest + time.Second, false},
		{10 * time.Second, false}, {60 * time.Second, false},
	} {
		if problems := launcherTimeoutProblems(tc.run, probe); (len(problems) == 0) != tc.pass {
			t.Errorf("run timeout %s: problems %q, want pass=%t", tc.run, problems, tc.pass)
		}
	}
}

func TestAgyDispatchRegistrationMatchesTrackedPlugin(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".agents", "plugins", "praetor", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]struct {
		PreToolUse []nativeGroup `json:"PreToolUse"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	row, err := ParseArguments("agy", string(EventPreDispatch))
	if err != nil || !groupsHold(document["praetor-subagent-register"].PreToolUse, row, time.Second) {
		t.Fatalf("agy tracked pre-dispatch registration: row=%+v err=%v", row, err)
	}
}

func TestHumanReplySurfacesHaveNoCavemanRegistration(t *testing.T) {
	for _, client := range []string{"claude", "codex", "gemini"} {
		for _, row := range Registrations(client) {
			if (row.NativeEvent == "Stop" || row.NativeEvent == "AfterAgent") && agentTrafficEvent(row.Event) {
				t.Fatalf("%s human completion surface carries agent text gate: %+v", client, row)
			}
		}
	}
}

func groupsHold(groups []nativeGroup, row Registration, unit time.Duration) bool {
	for _, group := range groups {
		if group.Matcher == row.Matcher && len(group.Hooks) == 1 && group.Hooks[0].Command == trackedCommand(row) &&
			time.Duration(group.Hooks[0].Timeout)*unit == row.Timeout {
			return true
		}
	}
	return false
}

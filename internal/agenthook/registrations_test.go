package agenthook

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
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
		if group.Matcher == row.Matcher && len(group.Hooks) == 1 && group.Hooks[0].Command == row.Command() &&
			time.Duration(group.Hooks[0].Timeout)*unit == row.Timeout {
			return true
		}
	}
	return false
}

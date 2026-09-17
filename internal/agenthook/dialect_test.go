package agenthook

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDialectFor(t *testing.T) {
	for _, client := range preToolClients {
		if dialect, ok := DialectFor(client); !ok || dialect.Client != client {
			t.Errorf("%s has no dialect", client)
		}
	}
	for _, client := range []string{"", "agy", "Claude", "claude "} {
		if dialect, ok := DialectFor(client); ok || !reflect.DeepEqual(dialect, Dialect{}) {
			t.Errorf("%q resolved to %+v", client, dialect)
		}
	}
}

func TestDialectDecode(t *testing.T) {
	claude, _ := DialectFor("claude")
	payload := []byte(`{"hook_event_name":"PreToolUse","tool_name":"Bash","cwd":"/w","tool_input":{"command":" git status "}}`)
	got, err := claude.Decode(EventPreTool, payload)
	want := Canonical{Event: EventPreTool, Tool: "Bash", Command: " git status ", Workspaces: []string{"/w"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("decode: %+v %v", got, err)
	}
	minimal, err := claude.Decode(EventPreTool, []byte(`{"cwd":null,"tool_input":{"command":"x"}}`))
	if err != nil || minimal.Workspaces != nil || minimal.Tool != "" || minimal.Command != "x" {
		t.Fatalf("minimal decode: %+v %v", minimal, err)
	}
	for name, invalid := range map[string]string{
		"empty": "", "array": "[]", "null": "null", "cwd type": `{"cwd":[],"tool_input":{"command":"x"}}`,
		"upper-case key is another key": `{"TOOL_INPUT":{"command":"x"}}`,
		"contradicting event":           `{"hook_event_name":"Stop","tool_input":{"command":"x"}}`,
	} {
		if decoded, err := claude.Decode(EventPreTool, []byte(invalid)); err == nil {
			t.Errorf("%s decoded to %+v", name, decoded)
		}
	}
}

func TestDialectDecodeKeepsTheLastDuplicateKey(t *testing.T) {
	claude, _ := DialectFor("claude")
	// Python's json.loads keeps the last duplicate as well, so both guards judge the same command.
	got, err := claude.Decode(EventPreTool, []byte(`{"tool_input":{"command":"first"},"tool_input":{"command":"last"}}`))
	if err != nil || got.Command != "last" {
		t.Fatalf("duplicate key: %+v %v", got, err)
	}
}

func TestDialectEncode(t *testing.T) {
	claude, _ := DialectFor("claude")
	lefthook, _ := DialectFor("lefthook")
	for _, tc := range []struct {
		name    string
		dialect Dialect
		event   Event
		verdict Verdict
		want    Response
	}{
		{"native allow", claude, EventPreTool, Verdict{Outcome: Allow}, Response{}},
		{"native deny", claude, EventPreTool, Verdict{Deny, "why"}, Response{Stderr: []byte("why\n"), ExitCode: 2}},
		{"native skip", claude, EventPreTool, Verdict{Skip, "no repository"}, Response{Stderr: []byte("praetor hook: no repository, skipped\n")}},
		{"lefthook allow", lefthook, EventPreTool, Verdict{Outcome: Allow}, Response{Stdout: []byte("PRAETOR_COMMAND_POLICY_OK\n")}},
		{"lefthook environment allow has no marker", lefthook, EventEnvironment, Verdict{Outcome: Allow}, Response{}},
		{"lefthook skip has no marker", lefthook, EventPreTool, Verdict{Skip, "workspace not governed"}, Response{Stderr: []byte("praetor hook: workspace not governed, skipped\n")}},
		{"lefthook deny", lefthook, EventPreTool, Verdict{Deny, "why"}, Response{Stderr: []byte("why\n"), ExitCode: 1}},
		{"unknown outcome denies", claude, EventPreTool, Verdict{Outcome: Outcome(9), Reason: "?"}, Response{Stderr: []byte("?\n"), ExitCode: 2}},
	} {
		if got := tc.dialect.Encode(tc.event, tc.verdict); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %+v", tc.name, got)
		}
	}
}

func TestDialectEncodeBoundsTheReason(t *testing.T) {
	claude, _ := DialectFor("claude")
	atBound := strings.Repeat("a", MaxReasonBytes)
	if got := claude.Encode(EventPreTool, Verdict{Deny, atBound}); len(got.Stderr) != MaxReasonBytes+1 {
		t.Errorf("reason at the bound changed: %d", len(got.Stderr))
	}
	// A three-byte rune straddles the bound: the cut must not leave half of it behind.
	straddling := strings.Repeat("a", MaxReasonBytes-1) + "€" + strings.Repeat("b", 100)
	got := claude.Encode(EventPreTool, Verdict{Deny, straddling})
	if len(got.Stderr) != MaxReasonBytes || !utf8.Valid(got.Stderr) || got.ExitCode != 2 {
		t.Errorf("reason over the bound: %d bytes, valid=%v", len(got.Stderr), utf8.Valid(got.Stderr))
	}
}

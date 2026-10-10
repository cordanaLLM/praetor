package schemacheck

import (
	"reflect"
	"testing"
)

func TestReadTOML(t *testing.T) {
	raw := `
[mcp_servers.praetor]
command = "/usr/local/bin/praetor-mcp"
args = ["--root", "/work/repo"]
enabled = true
startup_timeout_sec = 120
`
	val, err := ReadTOML([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	obj, ok := val.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", val)
	}
	servers, ok := obj["mcp_servers"].(map[string]any)
	if !ok {
		t.Fatalf("expected mcp_servers map, got %T", obj["mcp_servers"])
	}
	praetor, ok := servers["praetor"].(map[string]any)
	if !ok {
		t.Fatalf("expected praetor map, got %T", servers["praetor"])
	}
	if praetor["command"] != "/usr/local/bin/praetor-mcp" {
		t.Errorf("command = %v, want /usr/local/bin/praetor-mcp", praetor["command"])
	}
	if !reflect.DeepEqual(praetor["args"], []any{"--root", "/work/repo"}) {
		t.Errorf("args = %v", praetor["args"])
	}
	if praetor["enabled"] != true {
		t.Errorf("enabled = %v, want true", praetor["enabled"])
	}
}

func TestReadTOMLRefusals(t *testing.T) {
	cases := map[string]string{
		"syntax error":      "[mcp_servers\ncommand = ",
		"unclosed string":   "command = \"unclosed",
		"duplicate section": "[a]\nx = 1\n[a]\nx = 2\n",
	}
	for name, text := range cases {
		if _, err := ReadTOML([]byte(text)); err == nil {
			t.Errorf("%s: accepted invalid TOML", name)
		}
	}
}

func TestReadTOMLEmpty(t *testing.T) {
	val, err := ReadTOML([]byte(""))
	if err != nil {
		t.Fatal(err)
	}
	if obj, ok := val.(map[string]any); !ok || len(obj) != 0 {
		t.Fatalf("expected empty map, got %#v", val)
	}
}

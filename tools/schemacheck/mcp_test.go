package schemacheck

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

const mcpSchemaPath = "mcp/schema.json"

func mcpDefinition(t *testing.T, def string) *Schema {
	t.Helper()
	raw, err := loadManifest(t).Schema(mcpSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := CompileDefinition(mcpSchemaPath, raw, def, nil)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

// TestMCPSchemaDefinitionsRefuseMutatedMessages is the negative case for the MCP schema.
func TestMCPSchemaDefinitionsRefuseMutatedMessages(t *testing.T) {
	good := map[string]string{
		"JSONRPCResultResponse": `{"jsonrpc":"2.0","id":1,"result":{}}`,
		"JSONRPCErrorResponse":  `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"no"}}`,
		"CallToolResult":        `{"content":[{"type":"text","text":"ok"}]}`,
	}
	bad := map[string]struct{ doc, field string }{
		"JSONRPCResultResponse": {`{"jsonrpc":"1.0","id":1,"result":{}}`, "jsonrpc"},
		"JSONRPCErrorResponse":  {`{"jsonrpc":"2.0","id":1,"error":{"code":"x","message":"no"}}`, "/error/code"},
		"CallToolResult":        {`{"content":[{"type":"text"}]}`, "text"},
	}
	for def, doc := range good {
		schema := mcpDefinition(t, def)
		if err := schema.Validate([]byte(doc)); err != nil {
			t.Errorf("%s refuses a valid message: %v", def, err)
		}
		err := schema.Validate([]byte(bad[def].doc))
		var violations *Violations
		if !errors.As(err, &violations) || !violations.Names(bad[def].field) {
			t.Errorf("%s: mutated message gave %v, want a violation naming %s", def, err, bad[def].field)
		}
	}
}

type rpcLine struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

// buildServer compiles standards-mcp from this repository into a temporary directory.
func buildServer(ctx context.Context, t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "standards-mcp")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/standards-mcp")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build standards-mcp: %v\n%s", err, out)
	}
	return binary
}

// startServer starts the binary over stdio pipes and stops it when the test ends.
func startServer(ctx context.Context, t *testing.T, binary string) (io.WriteCloser, io.Reader) {
	t.Helper()
	server := exec.CommandContext(ctx, binary, "-root", t.TempDir())
	stdin, err := server.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := server.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Process.Kill(); err != nil {
			t.Logf("kill standards-mcp: %v", err)
		}
		if err := server.Wait(); err != nil {
			t.Logf("standards-mcp ended after the kill: %v", err) // a killed process reports a signal
		}
	})
	return stdin, stdout
}

// session drives standards-mcp over stdio and returns the raw response lines in request order.
func session(t *testing.T, requests []string) [][]byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	stdin, stdout := startServer(ctx, t, buildServer(ctx, t))
	reader := bufio.NewReaderSize(stdout, 1<<20)
	var responses [][]byte
	for _, request := range requests {
		if _, err := stdin.Write([]byte(request + "\n")); err != nil {
			t.Fatal(err)
		}
		var probe struct {
			ID *json.RawMessage `json:"id"`
		}
		if json.Unmarshal([]byte(request), &probe) != nil || probe.ID == nil {
			continue // a notification has no response
		}
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read response to %s: %v", request, err)
		}
		responses = append(responses, line)
	}
	return responses
}

// TestStandardsMCPResponsesConformToTheMCPSchema runs the real server and validates its
// initialize, tools/list, tools/call and error responses against the pinned MCP schema, so the
// hand-written envelopes cannot drift from the specification unnoticed.
func TestStandardsMCPResponsesConformToTheMCPSchema(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: building and starting standards-mcp is skipped")
	}
	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"schemacheck","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"no_such_tool","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"no/such/method"}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"standards_explain_rule","arguments":{"rule_id":"HISS-01"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"standards_explain_rule","arguments":{}}}`,
	}
	responses := session(t, requests)
	if len(responses) != 6 {
		t.Fatalf("got %d responses for 6 requests", len(responses))
	}
	envelope := map[bool]*Schema{true: mcpDefinition(t, "JSONRPCResultResponse"), false: mcpDefinition(t, "JSONRPCErrorResponse")}
	call := mcpDefinition(t, "CallToolResult")
	results := map[string]*Schema{"1": mcpDefinition(t, "InitializeResult"), "2": mcpDefinition(t, "ListToolsResult"), "5": call, "6": call}
	for _, line := range responses {
		var parsed rpcLine
		if err := json.Unmarshal(line, &parsed); err != nil {
			t.Fatalf("response is not JSON: %v\n%s", err, line)
		}
		failed := len(parsed.Error) > 0
		if err := envelope[!failed].Validate(line); err != nil {
			t.Errorf("response %s violates the JSON-RPC envelope: %v\n%s", parsed.ID, err, line)
		}
		if schema, ok := results[string(parsed.ID)]; ok && !failed {
			if err := schema.Validate(parsed.Result); err != nil {
				t.Errorf("result of request %s violates %s: %v", parsed.ID, schema.Name(), err)
			}
		}
	}
}

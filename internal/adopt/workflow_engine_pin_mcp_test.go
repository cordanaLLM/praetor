// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/mcp"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Exercise the shipped stdio server, including tools/call dispatch, argument parsing,
// adoption and result rendering. Foreign hook configuration prevents hook activation.
func TestWorkflowEnginePinThroughMCP(t *testing.T) {
	binary := buildPinMCPServer(t)
	source := newAdoptLockSource(t)
	ancestor, head, diverged := pinHistoryAt(t, source)
	for _, tc := range []struct {
		name, pin, want, detail string
		dryRun, isError         bool
	}{
		{"ancestor", ancestor, head, "~ .github/workflows/ci.yml", false, false},
		{"diverged", diverged, diverged, "not an ancestor", false, true},
		{"dry run", ancestor, ancestor, "~ .github/workflows/ci.yml", true, false},
		{"current", head, head, "Engine pin already at adopting commit " + head, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			initTestGit(t, root)
			rel := ".github/workflows/ci.yml"
			before := "env:\r\n  PRAETOR_COMMIT: '" + tc.pin + "' # keep\r\n"
			mustWrite(t, filepath.Join(root, rel), before)
			mustWrite(t, filepath.Join(root, lefthookFile), "pre-commit:\n  commands: {}\n")
			result := callPinMCP(t, binary, root, map[string]any{
				"source_root": source, "record_baseline": false, "dry_run": tc.dryRun,
			})
			if result.IsError != tc.isError || len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, tc.detail) {
				t.Errorf("unexpected MCP adoption result: %+v; want error=%v detail=%q", result, tc.isError, tc.detail)
			}
			if got := mustRead(t, filepath.Join(root, rel)); got != strings.ReplaceAll(before, tc.pin, tc.want) {
				t.Fatalf("MCP pin readback = %q; want %s with surrounding bytes preserved", got, tc.want)
			}
		})
	}
}

// buildPinMCPServer builds the standards-mcp executable in a temporary test directory.
func buildPinMCPServer(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), testsupport.ExecutableName("standards-mcp"))
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	out, err := util.RunCommandBytes(ctx, sourceCheckout, "go", util.MaxCommandOutputBytes,
		"build", "-p", "4", "-o", binary, "./cmd/standards-mcp")
	if err != nil {
		t.Fatalf("build real MCP server: %v: %s", err, out.Stderr)
	}
	return binary
}

// callPinMCP invokes the standards_adopt tool via stdio JSON-RPC against the test MCP server binary.
func callPinMCP(t *testing.T, binary, root string, args map[string]any) mcp.ToolResult {
	t.Helper()
	request, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "standards_adopt", "arguments": args},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := append([]byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{}}\n"), request...)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	ctx, err = util.WithCommandStdin(ctx, append(input, '\n'))
	if err != nil {
		t.Fatal(err)
	}
	out, err := util.RunCommandBytes(ctx, root, binary, util.MaxCommandOutputBytes,
		"--transport", "stdio", "--root", root, "--allow-outside-root")
	if err != nil {
		t.Fatalf("real MCP call: %v: %s", err, out.Stderr)
	}
	lines := bytes.Split(bytes.TrimSpace(out.Stdout), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("expected initialize and tools/call responses: %s", out.Stdout)
	}
	var response struct {
		ID     int            `json:"id"`
		Result mcp.ToolResult `json:"result"`
		Error  map[string]any `json:"error"`
	}
	if err := json.Unmarshal(lines[1], &response); err != nil {
		t.Fatal(err)
	}
	if response.ID != 2 || response.Error != nil {
		t.Fatalf("MCP protocol error: %s", lines[1])
	}
	return response.Result
}

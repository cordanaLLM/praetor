package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/standards/internal/mcp"
)

func TestServer_ToolsRegistrationAndAnnotations(t *testing.T) {
	srv, err := NewServer("../..", "v1.0.0")
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	expectedTools := []string{
		"standards_audit",
		"standards_plan",
		"standards_compile_context",
		"standards_explain_rule",
		"standards_inspect_symbols",
	}

	for _, name := range expectedTools {
		tool, exists := srv.tools[name]
		if !exists {
			t.Errorf("tool %q not registered", name)
			continue
		}

		if name == "standards_audit" || name == "standards_plan" || name == "standards_explain_rule" || name == "standards_inspect_symbols" {
			if !tool.Annotations.ReadOnlyHint {
				t.Errorf("tool %q expected readOnlyHint=true", name)
			}
		}
		if name == "standards_compile_context" {
			if tool.Annotations.ReadOnlyHint {
				t.Errorf("standards_compile_context expected readOnlyHint=false")
			}
		}
	}
}

func TestServer_JSONRPC_ProtocolMethods(t *testing.T) {
	srv, err := NewServer("../..", "v1.0.0")
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	ctx := context.Background()

	// 1. initialize
	initReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	}
	resp := srv.HandleRequest(ctx, initReq)
	if resp == nil || resp.Error != nil {
		t.Fatalf("initialize failed: %+v", resp)
	}
	resMap, ok := resp.Result.(map[string]any)
	if !ok || resMap["protocolVersion"] != "2024-11-05" {
		t.Errorf("unexpected initialize result: %+v", resp.Result)
	}

	// 2. notifications/initialized
	notifReq := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	notifResp := srv.HandleRequest(ctx, notifReq)
	if notifResp != nil {
		t.Errorf("notification should not produce response, got: %+v", notifResp)
	}

	// 3. ping
	pingReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "ping",
	}
	pingResp := srv.HandleRequest(ctx, pingReq)
	if pingResp == nil || pingResp.Error != nil {
		t.Errorf("ping failed: %+v", pingResp)
	}

	// 4. tools/list
	listReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/list",
	}
	listResp := srv.HandleRequest(ctx, listReq)
	if listResp == nil || listResp.Error != nil {
		t.Fatalf("tools/list failed: %+v", listResp)
	}
}

func TestServer_ToolCalls_ExplainRule(t *testing.T) {
	srv, err := NewServer("../..", "v1.0.0")
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	ctx := context.Background()

	// Positive explain rule
	explainParams, _ := json.Marshal(map[string]any{
		"name": "standards_explain_rule",
		"arguments": map[string]any{
			"rule_id": "HISS-01",
		},
	})
	explainResp := srv.HandleRequest(ctx, JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      10,
		Method:  "tools/call",
		Params:  explainParams,
	})
	if explainResp == nil || explainResp.Error != nil {
		t.Fatalf("explain_rule failed: %+v", explainResp)
	}
	tr, ok := explainResp.Result.(*mcp.ToolResult)
	if !ok || tr.IsError || !strings.Contains(tr.Content[0].Text, "HISS-01") {
		t.Errorf("unexpected explain_rule result: %+v", explainResp.Result)
	}

	// Negative explain rule: unknown rule
	unknownParams, _ := json.Marshal(map[string]any{
		"name": "standards_explain_rule",
		"arguments": map[string]any{
			"rule_id": "UNKNOWN-99",
		},
	})
	unknownResp := srv.HandleRequest(ctx, JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      11,
		Method:  "tools/call",
		Params:  unknownParams,
	})
	if unknownResp == nil {
		t.Fatalf("expected response for unknown rule")
	}
	trUnknown := unknownResp.Result.(*mcp.ToolResult)
	if !trUnknown.IsError || !strings.Contains(trUnknown.Content[0].Text, "Unknown rule") {
		t.Errorf("expected error result for unknown rule, got: %+v", trUnknown)
	}
}

func TestServer_ToolCalls_PlanAndInspect(t *testing.T) {
	srv, err := NewServer("../..", "v1.0.0")
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	ctx := context.Background()

	// standards_plan
	planParams, _ := json.Marshal(map[string]any{
		"name":      "standards_plan",
		"arguments": map[string]any{},
	})
	planResp := srv.HandleRequest(ctx, JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      12,
		Method:  "tools/call",
		Params:  planParams,
	})
	if planResp == nil || planResp.Error != nil {
		t.Fatalf("plan failed: %+v", planResp)
	}

	// standards_inspect_symbols
	inspectParams, _ := json.Marshal(map[string]any{
		"name": "standards_inspect_symbols",
		"arguments": map[string]any{
			"path": "internal/mcp",
		},
	})
	inspectResp := srv.HandleRequest(ctx, JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      13,
		Method:  "tools/call",
		Params:  inspectParams,
	})
	if inspectResp == nil || inspectResp.Error != nil {
		t.Fatalf("inspect_symbols failed: %+v", inspectResp)
	}
	trInspect := inspectResp.Result.(*mcp.ToolResult)
	if trInspect.IsError || !strings.Contains(trInspect.Content[0].Text, "Go AST Symbol") {
		t.Errorf("unexpected inspect_symbols output: %+v", trInspect)
	}
}

func TestServer_ToolCalls_NeedsReport(t *testing.T) {
	srv, err := NewServer("../..", "v1.0.0")
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	ctx := context.Background()

	params, err := json.Marshal(map[string]any{
		"name": "standards_needs_report",
		"arguments": map[string]any{
			"path": "../..",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	resp := srv.HandleRequest(ctx, JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      20,
		Method:  "tools/call",
		Params:  params,
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("needs_report tool call failed: %+v", resp)
	}
	tr, ok := resp.Result.(*mcp.ToolResult)
	if !ok || tr.IsError || !strings.Contains(tr.Content[0].Text, "Golusoris Migration Report") {
		t.Errorf("unexpected needs_report result: %+v", resp.Result)
	}
}

func TestServer_HTTP_Transport(t *testing.T) {
	srv, err := NewServer("../..", "v1.0.0")
	if err != nil {
		t.Fatalf("server setup error: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req JSONRPCRequest
		_ = json.Unmarshal(body, &req)
		resp := srv.HandleRequest(r.Context(), req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Health check
	hRes, err := http.Get(ts.URL + "/health")
	if err != nil || hRes.StatusCode != 200 {
		t.Errorf("health check failed: %v", err)
	}

	// JSON-RPC POST
	reqBody := `{"jsonrpc":"2.0","id":100,"method":"ping"}`
	pRes, err := http.Post(ts.URL+"/", "application/json", bytes.NewBufferString(reqBody))
	if err != nil || pRes.StatusCode != 200 {
		t.Fatalf("POST request failed: %v", err)
	}
	defer pRes.Body.Close()

	var rpcResp JSONRPCResponse
	if err := json.NewDecoder(pRes.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if rpcResp.ID == nil || rpcResp.Error != nil {
		t.Errorf("unexpected JSON-RPC response: %+v", rpcResp)
	}
}

func TestServer_SSE_Transport(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flush", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: endpoint\ndata: /messages?sessionId=test-123\n\n"))
		flusher.Flush()
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", ts.URL+"/sse", nil)
	if err != nil {
		t.Fatalf("failed to create SSE request: %v", err)
	}

	client := &http.Client{}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("SSE GET failed: %v", err)
	}
	defer res.Body.Close()

	buf := make([]byte, 128)
	n, err := res.Body.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("failed to read SSE body: %v", err)
	}
	msg := string(buf[:n])
	if !strings.Contains(msg, "event: endpoint") || !strings.Contains(msg, "test-123") {
		t.Errorf("unexpected SSE initial payload: %s", msg)
	}
}

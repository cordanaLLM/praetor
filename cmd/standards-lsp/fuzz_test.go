package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// FuzzLSPHandleMessage feeds arbitrary bytes (capped at 64 KiB) to a fresh server per input and
// requires HandleMessage to return a JSON-RPC parse error for invalid JSON, never an error of its
// own, and for valid JSON a well-formed frame whenever it answers: every request with an id gets a
// response, and a notification gets none required.
func FuzzLSPHandleMessage(f *testing.F) {
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{}}}`))
	f.Add([]byte(`{"jsonrpc":"2.0","id":2,"method":"textDocument/hover","params":{"position":{"line":0,"character":0}}}`))
	f.Add([]byte(`{"jsonrpc":"2.0","method":"initialized"}`))
	f.Add([]byte(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`))
	f.Add([]byte(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///f.go","languageId":"go","version":1,"text":"package p\nfunc f() { panic(1) }\n"}}}`))
	f.Add([]byte(`{"jsonrpc":"2.0", invalid-json`))
	f.Add([]byte(`{"method":"textDocument/didOpen"}`))
	f.Add([]byte(`{"jsonrpc":"2.0","id":9,"method":"textDocument/didChange","params":"nope"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 65536 {
			data = data[:65536]
		}
		// A fresh server per input: state such as shutdown must never leak between
		// inputs, or every later input short-circuits before reaching the analyzer.
		srv := NewServer(strings.NewReader(""), &bytes.Buffer{}, "fuzz")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		resp, notifs, err := srv.HandleMessage(ctx, data)
		if err != nil {
			t.Fatalf("HandleMessage must not fail on an input error, got %v", err)
		}
		if !json.Valid(data) {
			assertParseError(t, resp)
			return
		}
		assertResponseFrame(t, resp)
		assertRequestAnswered(t, data, resp)
		assertNotifications(t, notifs)
	})
}

// assertParseError checks the JSON-RPC answer to bytes that are not JSON.
func assertParseError(t *testing.T, resp *JSONRPCResponse) {
	t.Helper()
	if resp == nil || resp.Error == nil || resp.Error.Code != -32700 || resp.ID != nil {
		t.Fatalf("invalid JSON must yield a -32700 parse error with a null id, got %+v", resp)
	}
}

// assertResponseFrame checks that a response, when present, is a well-formed JSON-RPC frame:
// version 2.0, a top-level id member (null allowed) and exactly one top-level result or error.
func assertResponseFrame(t *testing.T, resp *JSONRPCResponse) {
	t.Helper()
	if resp == nil {
		return
	}
	if resp.JSONRPC != "2.0" {
		t.Fatalf("response without jsonrpc version: %+v", resp)
	}
	wire, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("response frame does not marshal: %v", err)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(wire, &members); err != nil {
		t.Fatalf("response frame is not a JSON object: %s err=%v", wire, err)
	}
	if _, ok := members["id"]; !ok {
		t.Fatalf("response frame must carry a top-level id: %s", wire)
	}
	_, hasError := members["error"]
	_, hasResult := members["result"]
	if hasError == hasResult || hasError != (resp.Error != nil) {
		t.Fatalf("frame must carry exactly one of result/error: %s", wire)
	}
}

// assertRequestAnswered requires a response for every request that carries a non-null id, whatever
// its method: JSON-RPC 2.0 exempts only notifications, which carry none.
func assertRequestAnswered(t *testing.T, data []byte, resp *JSONRPCResponse) {
	t.Helper()
	var req JSONRPCRequest
	if json.Unmarshal(data, &req) != nil || req.ID == nil {
		return
	}
	if resp == nil {
		t.Fatalf("request %q with id %v got no response", req.Method, req.ID)
	}
}

// assertNotifications checks that every notification names its protocol version and method.
func assertNotifications(t *testing.T, notifs []JSONRPCNotification) {
	t.Helper()
	for _, n := range notifs {
		if n.JSONRPC != "2.0" || n.Method == "" {
			t.Fatalf("malformed notification: %+v", n)
		}
	}
}

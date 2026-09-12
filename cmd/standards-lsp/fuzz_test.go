package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

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
			if resp == nil || resp.Error == nil || resp.Error.Code != -32700 || resp.ID != nil {
				t.Fatalf("invalid JSON must yield a -32700 parse error with a null id, got %+v", resp)
			}
			return
		}
		if resp != nil {
			if resp.JSONRPC != "2.0" {
				t.Fatalf("response without jsonrpc version: %+v", resp)
			}
			wire, mErr := json.Marshal(resp)
			if mErr != nil || !bytes.Contains(wire, []byte(`"id":`)) {
				t.Fatalf("response frame must carry an id: %s err=%v", wire, mErr)
			}
			if (resp.Error == nil) == bytes.Contains(wire, []byte(`"error":`)) || (resp.Error != nil) == bytes.Contains(wire, []byte(`"result":`)) {
				t.Fatalf("frame must carry exactly one of result/error: %s", wire)
			}
		}
		for _, n := range notifs {
			if n.JSONRPC != "2.0" || n.Method == "" {
				t.Fatalf("malformed notification: %+v", n)
			}
		}
	})
}

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestLSP_Negative_IDBearingNotificationMethodsAnswered plants, on one server, a request with an id
// for each method the server handles as a notification, with valid params so the handler itself
// succeeds, and requires a reply carrying that id (#1909): JSON-RPC 2.0 exempts only messages
// without an id, so a client waiting on one of these must get an answer.
func TestLSP_Negative_IDBearingNotificationMethodsAnswered(t *testing.T) {
	srv := NewServer(strings.NewReader(""), &bytes.Buffer{}, "test")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const doc = `{"textDocument":{"uri":"file:///answered.go"}}`
	for _, step := range []struct {
		id, method, params string
		wantID             any
	}{
		{"2", "textDocument/didOpen", `{"textDocument":{"uri":"file:///answered.go","languageId":"go","version":1,"text":"package p\n"}}`, float64(2)},
		{"3", "textDocument/didChange", `{"textDocument":{"uri":"file:///answered.go","version":2},"contentChanges":[{"text":"package p\n\nfunc f() {}\n"}]}`, float64(3)},
		{"4", "textDocument/didSave", doc, float64(4)},
		{"5", "textDocument/didClose", doc, float64(5)},
		{"1", "initialized", "", float64(1)},
		{`"s7"`, "initialized", "", "s7"},
		{"6", "exit", "", float64(6)},
	} {
		raw := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":%q}`, step.id, step.method)
		if step.params != "" {
			raw = fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":%q,"params":%s}`, step.id, step.method, step.params)
		}
		resp, _, err := srv.HandleMessage(ctx, []byte(raw))
		if err != nil {
			t.Fatalf("HandleMessage(%s): %v", step.method, err)
		}
		if resp == nil || resp.Error != nil || resp.ID != step.wantID {
			t.Fatalf("%s with id %s must be answered with that id and no error, got %+v", step.method, step.id, resp)
		}
	}
}

// TestLSP_Boundary_NotificationWithoutIDStaysUnanswered keeps the other side of the contract: the
// same methods without an id get no reply.
func TestLSP_Boundary_NotificationWithoutIDStaysUnanswered(t *testing.T) {
	srv := NewServer(strings.NewReader(""), &bytes.Buffer{}, "test")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, _, err := srv.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","method":"initialized"}`))
	if err != nil || resp != nil {
		t.Fatalf("a notification must get no reply, got resp=%+v err=%v", resp, err)
	}
}

// TestLSP_Negative_DidChangeMissingContentChanges rejects requests and drops notifications.
func TestLSP_Negative_DidChangeMissingContentChanges(t *testing.T) {
	srv := NewServer(strings.NewReader(""), &bytes.Buffer{}, "test")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	params := `{"textDocument":{"uri":"file:///answered.go","version":2}}`
	resp, notifs, err := srv.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":7,"method":"textDocument/didChange","params":`+params+`}`))
	if err != nil || resp == nil || resp.ID != float64(7) || resp.Error == nil || resp.Error.Code != -32602 || len(notifs) != 0 {
		t.Errorf("missing contentChanges request: resp=%+v notifs=%+v err=%v", resp, notifs, err)
	}
	resp, notifs, err = srv.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","method":"textDocument/didChange","params":`+params+`}`))
	if err != nil || resp != nil || len(notifs) != 0 {
		t.Errorf("missing contentChanges notification: resp=%+v notifs=%+v err=%v", resp, notifs, err)
	}
}

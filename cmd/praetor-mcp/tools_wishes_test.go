// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func applyWishFixture(t *testing.T, srv *Server, request string) map[string]any {
	t.Helper()
	result := callTool(t, srv, "standards_wishes_update", map[string]any{"request_json": request})
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("wish operation failed: %+v", result)
	}
	var ledger map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &ledger); err != nil {
		t.Fatal(err)
	}
	return ledger
}

func TestWishesMCPWriteReadback(t *testing.T) {
	srv, root := newFixtureServer(t)
	if result := callTool(t, srv, "standards_wishes_status", nil); !result.IsError {
		t.Fatal("missing ledger reported successful empty inventory")
	}
	applyWishFixture(t, srv, `{"action":"init"}`)
	first := callTool(t, srv, "standards_wishes_status", nil)
	if first.IsError {
		t.Fatal(first)
	}
	var ledger struct{ Revision uint64 }
	if err := json.Unmarshal([]byte(first.Content[0].Text), &ledger); err != nil {
		t.Fatal(err)
	}
	request, err := json.Marshal(map[string]any{"action": "add-wish", "expected_revision": ledger.Revision,
		"wish": map[string]string{"id": "fixture-wish", "kind": "template", "target": "fixture/repo", "title": "Fixture template", "description": "Private fixture content"}})
	if err != nil {
		t.Fatal(err)
	}
	applyWishFixture(t, srv, string(request))
	path := filepath.Join(root, ".workingdir", "wishes.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	result := callTool(t, srv, "standards_wishes_status", nil)
	expectText(t, "wishes status", result, "Private fixture content")
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("status changed ledger: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("ledger is not private: %v", err)
	}
}

func TestWishesMCPRejectsArgumentsAndEscapesBeforeWrites(t *testing.T) {
	srv, root := newFixtureServer(t)
	for _, args := range []map[string]any{
		{}, {"request_json": nil}, {"request_json": map[string]any{"action": "init"}},
		{"request_json": `{"action":"init","action":"init"}`},
		{"request_json": `{"action":"init"}`, "publish": true},
		{"request_json": `{"action":"init"}`, "store": nil},
		{"request_json": `{"action":"init"}`, "store": "../escaped-wishes.json"},
		{"request_json": `{"action":"init"}`, "store": filepath.Join(t.TempDir(), "outside.json")},
		{"request_json": strings.Repeat(" ", (1<<20)+1)},
	} {
		if result := callTool(t, srv, "standards_wishes_update", args); !result.IsError {
			t.Fatalf("invalid request accepted: %+v", args)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".workingdir", "wishes.json")); !os.IsNotExist(err) {
		t.Fatalf("invalid arguments initialized a ledger: %v", err)
	}
	for _, args := range []map[string]any{{"request_json": `{"action":"init"}`}, {"store": 42}, {"store": "../outside.json"}} {
		if result := callTool(t, srv, "standards_wishes_status", args); !result.IsError {
			t.Fatalf("invalid status args accepted: %+v", args)
		}
	}
}

func TestWishesMCPRejectsSymlinkStore(t *testing.T) {
	srv, root := newFixtureServer(t)
	outside := filepath.Join(t.TempDir(), "private.json")
	if err := os.WriteFile(outside, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.json")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"standards_wishes_status", "standards_wishes_update"} {
		args := map[string]any{"store": "linked.json"}
		if name == "standards_wishes_update" {
			args["request_json"] = `{"action":"init"}`
		}
		if result := callTool(t, srv, name, args); !result.IsError {
			t.Fatalf("symlink accepted by %s", name)
		}
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "untouched" {
		t.Fatalf("outside contents changed: %v", err)
	}
}

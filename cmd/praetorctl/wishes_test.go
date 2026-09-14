package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const wishesPolicyRequest = `{"action":"init","policy":{"max_wishes":4,"max_polls":4,"max_voters":4,"allow_vote_changes":true,"allow_withdrawal":true}}
`

func writeWishRequest(t *testing.T, dir, name, raw string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWishesCLIApplyAndStatusReadback(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "wishes.json")
	initPath := writeWishRequest(t, dir, "init.json", wishesPolicyRequest)
	out, err := captureStdout(t, func() error {
		return dispatchCommand("wishes", []string{"apply", "--store", store, "--request", initPath})
	})
	if err != nil {
		t.Fatal(err)
	}
	var ledger struct {
		Revision uint64 `json:"revision"`
	}
	if err := json.Unmarshal([]byte(out), &ledger); err != nil || ledger.Revision != 1 {
		t.Fatalf("unexpected init result: %s (%v)", out, err)
	}
	addPath := writeWishRequest(t, dir, "add.json", `{"action":"add-wish","expected_revision":1,"wish":{"id":"w1","kind":"template","target":"repo","title":"Add template","description":"A useful template"}}`)
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("wishes", []string{"apply", "--store=" + store, "--request=" + addPath})
	}); err != nil {
		t.Fatal(err)
	}
	out, err = captureStdout(t, func() error {
		return dispatchCommand("wishes", []string{"status", "--store=" + store})
	})
	var status struct {
		Revision uint64 `json:"revision"`
		Wishes   []struct {
			ID string `json:"id"`
		} `json:"wishes"`
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &status); err != nil || status.Revision != 2 || len(status.Wishes) != 1 || status.Wishes[0].ID != "w1" {
		t.Fatalf("status readback failed: %s (%v)", out, err)
	}
}

func TestWishesCLIRejectsInvalidArgumentsAndMissingStatus(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range [][]string{
		{"status", "--unknown"},
		{"status", "extra"},
		{"apply"},
		{"apply", "--request", filepath.Join(dir, "missing.json")},
	} {
		if _, err := captureStdout(t, func() error { return dispatchCommand("wishes", tc) }); err == nil {
			t.Fatalf("accepted invalid wishes arguments: %v", tc)
		}
	}
	missingStore := filepath.Join(dir, "missing.json")
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("wishes", []string{"status", "--store", missingStore})
	}); err == nil {
		t.Fatal("missing wish ledger reported as status")
	}
	if _, err := os.Stat(missingStore); !os.IsNotExist(err) {
		t.Fatalf("status implicitly created store: stat error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "missing.json")); !os.IsNotExist(err) {
		t.Fatalf("invalid apply implicitly created store: stat error=%v", err)
	}
}

func TestWishesCLIRejectsMalformedDuplicateAndSymlinkRequests(t *testing.T) {
	dir := t.TempDir()
	duplicate := writeWishRequest(t, dir, "duplicate.json", `{"action":"init","action":"init"}`)
	store := filepath.Join(dir, "duplicate-store.json")
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("wishes", []string{"apply", "--store", store, "--request", duplicate})
	}); err == nil {
		t.Fatal("duplicate JSON keys accepted")
	}
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Fatalf("malformed request created store: stat error=%v", err)
	}

	target := writeWishRequest(t, dir, "target.json", wishesPolicyRequest)
	link := filepath.Join(dir, "request-link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	linkStore := filepath.Join(dir, "link-store.json")
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("wishes", []string{"apply", "--store", linkStore, "--request", link})
	}); err == nil {
		t.Fatal("symlink request accepted")
	}
	if _, err := os.Stat(linkStore); !os.IsNotExist(err) {
		t.Fatalf("symlink request created store: stat error=%v", err)
	}
}

func TestWishesCLIRequestSizeBoundary(t *testing.T) {
	dir := t.TempDir()
	base := []byte(wishesPolicyRequest)
	exact := append(append([]byte(nil), base...), bytes.Repeat([]byte(" "), (1<<20)-len(base))...)
	exactPath := writeWishRequest(t, dir, "exact.json", string(exact))
	exactStore := filepath.Join(dir, "exact-store.json")
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("wishes", []string{"apply", "--store", exactStore, "--request", exactPath})
	}); err != nil {
		t.Fatalf("exact request boundary rejected: %v", err)
	}

	over := append(append([]byte(nil), exact...), ' ')
	overPath := writeWishRequest(t, dir, "over.json", string(over))
	overStore := filepath.Join(dir, "over-store.json")
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("wishes", []string{"apply", "--store", overStore, "--request", overPath})
	}); err == nil {
		t.Fatal("over-limit request accepted")
	}
	if _, err := os.Stat(overStore); !os.IsNotExist(err) {
		t.Fatalf("over-limit request created store: stat error=%v", err)
	}
}

package paperclip

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyRunRejectsInvalidHarness(t *testing.T) {
	disposition, err := CreateDisposition("fixture", "blocked", "waiting", "", "owner", "agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{"not-json", "null", "{}", `{"version":99,"platform":"owner/repo","operating_contract":["rule"]}`} {
		t.Run(data, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, ".paperclip"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, ".paperclip", "harness.json"), []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := VerifyRun(context.Background(), root, disposition, VerifyOptions{}); err == nil {
				t.Fatal("invalid harness certified as a valid run")
			}
		})
	}
}

func TestHarnessLoadValidatesRequiredFieldsAndBounds(t *testing.T) {
	base := Harness{Version: 1, Platform: "owner/repo", OperatingContract: []string{"custom contract"}, AGitPushFormat: "custom workflow", Invariants: []string{"custom invariant"}}
	cases := []struct {
		name string
		edit func(*Harness)
	}{
		{"version", func(h *Harness) { h.Version = 0 }},
		{"blank platform", func(h *Harness) { h.Platform = " " }},
		{"blank contract", func(h *Harness) { h.OperatingContract = []string{" "} }},
		{"missing push", func(h *Harness) { h.AGitPushFormat = "" }},
		{"missing invariants", func(h *Harness) { h.Invariants = nil }},
		{"oversized value", func(h *Harness) { h.Platform = strings.Repeat("x", 4097) }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			h := base
			test.edit(&h)
			path := filepath.Join(t.TempDir(), "harness.json")
			data, err := json.Marshal(h)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadHarness(path); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}

func TestHarnessContextAndReadBoundaries(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	h, err := SynthesizeHarness(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	h.OperatingContract = make([]string, 64)
	for i := range h.OperatingContract {
		h.OperatingContract[i] = strings.Repeat("x", 4096)
	}
	if err := WriteHarness(h, root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".paperclip", "harness.json")
	if _, err := LoadHarnessContext(ctx, path); err != nil {
		t.Fatalf("exact value/count limits rejected: %v", err)
	}
	var nilContext context.Context
	if _, err := LoadHarnessContext(nilContext, path); err == nil {
		t.Fatal("nil context accepted")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := LoadHarnessContext(cancelled, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read: %v", err)
	}
	link := filepath.Join(root, "linked.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHarnessContext(ctx, link); err == nil {
		t.Fatal("symlink followed")
	}
	h.OperatingContract = append(h.OperatingContract, "overflow")
	if err := WriteHarness(h, root); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHarnessContext(ctx, path); err == nil {
		t.Fatal("more than 64 contract values accepted")
	}
}

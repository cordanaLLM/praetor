// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// Negative / planted case: package.json pins x@2.0.1 and credits.yaml records x@2.0.0;
// the check fails naming x and both versions; matching versions pass.
func TestCheckCreditPinsPlantedMismatch(t *testing.T) {
	credits := Credits{
		Entries: []CreditEntry{
			{
				Name:     "x",
				URL:      "https://example.test/x",
				Detail:   "2.0.0",
				Section:  "shipped",
				Kind:     "dependency",
				Relation: "integrated",
				License:  "MIT",
				Use:      "example",
				Paths:    []string{"package.json"},
				Packages: []string{"npm:x"},
			},
		},
	}
	mismatchManifest := []byte(`{
		"name": "fixture",
		"dependencies": {
			"x": "2.0.1"
		}
	}`)
	err := CheckCreditPins(credits, map[string][]byte{"package.json": mismatchManifest})
	if err == nil {
		t.Fatal("expected failure on version mismatch, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "x") || !strings.Contains(msg, "2.0.1") || !strings.Contains(msg, "2.0.0") {
		t.Fatalf("error %q does not name package x and both versions (2.0.1 and 2.0.0)", msg)
	}

	matchingManifest := []byte(`{
		"name": "fixture",
		"dependencies": {
			"x": "2.0.0"
		}
	}`)
	if err := CheckCreditPins(credits, map[string][]byte{"package.json": matchingManifest}); err != nil {
		t.Fatalf("matching version failed: %v", err)
	}
}

// Boundary cases: prefix stripping, scoped subpackages, entries without versions, missing manifests.
func TestCheckCreditPinsBoundary(t *testing.T) {
	credits := Credits{
		Entries: []CreditEntry{
			{
				Name:     "tool",
				URL:      "https://example.test/tool",
				Detail:   "(`tool`)",
				Section:  "development",
				Kind:     "tool",
				Relation: "used by CI",
				License:  "MIT",
				Use:      "tooling",
				Paths:    []string{"package.json"},
				Packages: []string{"npm:tool"},
			},
			{
				Name:     "scoped",
				URL:      "https://example.test/scoped",
				Detail:   "core 1.2.3, extra 4.5.6",
				Section:  "shipped",
				Kind:     "dependency",
				Relation: "integrated",
				License:  "MIT",
				Use:      "scoped package",
				Paths:    []string{"package.json"},
				Packages: []string{"npm:@scope/core", "npm:@scope/extra"},
			},
		},
	}
	manifest := []byte(`{
		"name": "boundary-fixture",
		"dependencies": {
			"tool": "9.9.9",
			"@scope/core": "^1.2.3",
			"@scope/extra": "4.5.6"
		}
	}`)
	if err := CheckCreditPins(credits, map[string][]byte{"package.json": manifest}); err != nil {
		t.Fatalf("boundary case failed: %v", err)
	}

	mismatchExtra := []byte(`{
		"name": "boundary-fixture",
		"dependencies": {
			"@scope/core": "1.2.3",
			"@scope/extra": "4.5.7"
		}
	}`)
	err := CheckCreditPins(credits, map[string][]byte{"package.json": mismatchExtra})
	if err == nil || !strings.Contains(err.Error(), "4.5.7") || !strings.Contains(err.Error(), "4.5.6") {
		t.Fatalf("scoped mismatch not detected: %v", err)
	}
}

// Positive / checkout check: all package.json pins in the repository match docs/credits.yaml.
func TestCreditPinsMatchManifests(t *testing.T) {
	root := filepath.Join("..", "..")
	credits, err := ReadCredits(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	manifests, err := ReadCreditManifests(context.Background(), root, credits)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) == 0 {
		t.Fatal("read 0 manifests; want at least 1")
	}
	if err := CheckCreditPins(credits, manifests); err != nil {
		t.Fatal(err)
	}
}

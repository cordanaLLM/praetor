// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util_test

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestDecodeTOMLPositive(t *testing.T) {
	var document struct {
		Package struct {
			Edition string `toml:"edition"`
		} `toml:"package"`
		Workspace struct {
			Members []string `toml:"members"`
		} `toml:"workspace"`
	}
	data := []byte("package.edition = '2024'\n[workspace]\nmembers = [\n  'a', # first\n  'b',\n]\n")
	if err := util.DecodeTOML("Cargo.toml", data, &document); err != nil {
		t.Fatal(err)
	}
	if document.Package.Edition != "2024" || strings.Join(document.Workspace.Members, ",") != "a,b" {
		t.Fatalf("decoded document = %+v", document)
	}
}

func TestDecodeTOMLNegative(t *testing.T) {
	for _, data := range []string{"items = [\n", "value = 1\nvalue = 2\n"} {
		if err := util.DecodeTOML("broken.toml", []byte(data), &map[string]any{}); err == nil || !strings.Contains(err.Error(), "broken.toml") {
			t.Errorf("DecodeTOML(%q) error = %v, want file-context parse error", data, err)
		}
	}
}

func TestDecodeTOMLBoundary(t *testing.T) {
	var document map[string]any
	if err := util.DecodeTOML("empty.toml", nil, &document); err != nil || len(document) != 0 {
		t.Fatalf("empty TOML = %v, %v; want empty document", document, err)
	}
}

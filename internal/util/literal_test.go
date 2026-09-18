// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestLiteralStringAcceptsPlainValues(t *testing.T) {
	for _, value := range []string{"", "origin", "mcp(praetor/*)", "run_command(praetorctl hook)", "Grüße"} {
		if !util.LiteralString(value, 64) {
			t.Errorf("plain value %q refused", value)
		}
	}
}

func TestLiteralStringRejectsInterpolationControlAndInvalidUTF8(t *testing.T) {
	for _, value := range []string{"$HOME", "`id`", "{env:TOKEN}", "{file:/x}", "a\nb", "a\x7fb", "\xff"} {
		if util.LiteralString(value, 64) {
			t.Errorf("value %q accepted", value)
		}
	}
}

func TestLiteralStringByteBound(t *testing.T) {
	if !util.LiteralString(strings.Repeat("a", 8), 8) {
		t.Fatal("value at the bound refused")
	}
	if util.LiteralString(strings.Repeat("a", 9), 8) {
		t.Fatal("value over the bound accepted")
	}
}

func TestCleanAbsoluteLiteral(t *testing.T) {
	root := t.TempDir()
	if !util.CleanAbsoluteLiteral(root, 4096) {
		t.Fatalf("clean absolute path %q refused", root)
	}
	for _, value := range []string{"relative/path", root + string(filepath.Separator), filepath.Join(root, "$x"), string(filepath.Separator), ""} {
		if util.CleanAbsoluteLiteral(value, 4096) {
			t.Errorf("path %q accepted", value)
		}
	}
	if util.CleanAbsoluteLiteral(root, len(root)-1) {
		t.Fatal("path over the bound accepted")
	}
}

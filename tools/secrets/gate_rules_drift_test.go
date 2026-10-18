// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package secrets_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/tools/secrets"
)

func TestRulesGen_NoDrift(t *testing.T) {
	srcPath := filepath.Join("..", "..", secrets.RulesSourcePath)
	src, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read %s: %v", srcPath, err)
	}
	want, err := secrets.GenerateRulesGen(src)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	destPath := filepath.Join("..", "..", secrets.RulesGenDestPath)
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read %s: %v", destPath, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from generator output; run 'go run ./tools/secrets/cmd/generate' to update", destPath)
	}
}

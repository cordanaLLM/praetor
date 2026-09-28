// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util_test

import (
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestSourceLanguage(t *testing.T) {
	cases := []struct {
		name, path, want string
	}{
		{"positive: Go", "cmd/tool/main.go", "go"},
		{"positive: Rust", "crates/core/src/lib.rs", "rust"},
		{"positive: C header", "native/shim.h", "c"},
		{"positive: C++", "native/shim.cpp", "cpp"},
		{"positive: JavaScript is its own language", "web/index.js", "javascript"},
		{"positive: TypeScript", "web/app.tsx", "typescript"},
		{"positive: ES module JavaScript", "tools/build.mjs", "javascript"},
		{"positive: CommonJS JavaScript", "tools/config.cjs", "javascript"},
		{"positive: ES module TypeScript", "web/worker.mts", "typescript"},
		{"positive: Java", "java/src/Main.java", "java"},
		{"positive: Kotlin", "app/src/Main.kt", "kotlin"},
		{"positive: C#", "src/Program.cs", "csharp"},
		{"positive: CUDA", "kernels/reduce.cu", "cuda"},
		{"positive: C++ header", "native/shim.hh", "cpp"},
		{"positive: Lua", "lua/standards.lua", "lua"},
		{"positive: Ruby", "lib/tool.rb", "ruby"},
		{"positive: Swift", "Sources/App/main.swift", "swift"},
		{"negative: documentation", "docs/guide.md", ""},
		{"negative: configuration", ".config/settings.yaml", ""},
		{"negative: no extension", "Makefile", ""},
		{"negative: ambiguous extension is not guessed", "src/view.m", ""},
		{"negative: interface definition is not program source", "api/service.proto", ""},
		{"boundary: extension case is ignored", "SRC/MAIN.PY", "python"},
		{"boundary: an upper-case R script is R", "analysis/model.R", "r"},
		{"boundary: only the last extension counts", "archive.rs.bak", ""},
		{"boundary: a dot directory is not an extension", ".github/CODEOWNERS", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := util.SourceLanguage(tc.path); got != tc.want {
				t.Fatalf("SourceLanguage(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

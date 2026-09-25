package util

import (
	"slices"
	"strings"
	"testing"
)

func TestClangdArgumentsKeepHeaderPolicy(t *testing.T) {
	if !slices.Contains(ClangdArguments(), "--header-insertion=never") {
		t.Fatalf("header insertion policy dropped: %v", ClangdArguments())
	}
}

func TestClangdArgumentsNameNoCompilationDatabaseDirectory(t *testing.T) {
	for _, arg := range ClangdArguments() {
		if strings.HasPrefix(arg, "--compile-commands-dir") {
			t.Fatalf("clangd pinned to one repository layout: %q", arg)
		}
	}
}

func TestClangdArgumentsReturnsFreshSlice(t *testing.T) {
	first := ClangdArguments()
	first[0] = "--compile-commands-dir=core/build"
	if ClangdArguments()[0] == first[0] {
		t.Fatal("a caller's edit leaked into the shared clangd arguments")
	}
}

package util

// ClangdArguments returns the clangd flags every generated editor surface passes: the VS
// Code settings adoption writes (internal/editor) and the DevContainer settings
// (internal/devcontainer). One definition keeps the two from drifting (HISS-19).
//
// It carries no --compile-commands-dir. Without one, clangd searches each edited file's
// ancestor directories and their build/ subdirectories for compile_commands.json
// (https://clangd.llvm.org/installation, "Project setup"), which finds a root, build/ or
// core/build/ database alike. A fixed directory instead pointed every native adopter at one
// repository's core/build layout (BUG-879). Each call returns a fresh slice.
func ClangdArguments() []string {
	return []string{"--header-insertion=never"}
}

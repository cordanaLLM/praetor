package editor

import "github.com/cordanaLLM/praetor/internal/config"

// referenceLSPPath is where the engine's own build places the language server (`make build`).
const referenceLSPPath = "bin/standards-lsp"

// referencePaths maps a file the generator writes into a workspace to its place in the engine's
// editors/ tree. Only each editor's portable file ships there: the Neovim module is loaded from
// the runtimepath and the inspection profile is imported into any project.
var referencePaths = map[string]string{
	".idea/inspectionProfiles/standards.xml": "editors/jetbrains/inspectionProfiles/standards.xml",
	"lua/standards.lua":                      "editors/neovim/lua/standards.lua",
}

// referencePlan is the capability plan the reference integrations are rendered from: a Go
// workspace with the Praetor CLI on PATH, the language server at its build location and the
// HISS-04 ceiling. It is fixed rather than observed, so the rendered tree does not depend on
// whether the host has built bin/standards-lsp.
func referencePlan() Plan {
	return Plan{
		Editors:   []string{EditorJetBrains, EditorNeovim},
		Languages: []string{"go"},
		Commands: []Command{
			{Label: "Standards: Audit", Program: "praetorctl", Args: []string{"audit"}},
			{Label: "Standards: Compile Context", Program: "praetorctl", Args: []string{"compile-context"}},
			{Label: "Standards: Verify All", Program: "make", Args: []string{"verify-all"}, Group: "test"},
			// audit is the command that evaluates the baseline ratchet against the working tree
			// (cmd/standardsctl/audit_ratchet.go). This entry used to run `baseline --check`, a
			// flag the baseline command never had, so it failed on every invocation (BUG-588).
			{Label: "Standards: Ratchet Sweep", Program: "praetorctl", Args: []string{"audit"}},
		},
		Complexity: config.HISSComplexityCeiling(),
		LSPPath:    referenceLSPPath,
	}
}

// ReferenceSet renders the engine's editors/ reference integrations for the editors that have
// no extension of their own. They are the output of the same renderers `praetorctl editors
// generate` runs, re-rooted under editors/, so Write regenerates them and Verify reports a hand
// edit as drift. They used to be maintained by hand beside the generator and had drifted from
// it in commands, inspection classes and invocation style (BUG-600, BUG-625).
func ReferenceSet() *EditorConfigSet {
	plan := referencePlan()
	generated := append(generateJetBrains("framework", plan), generateNeovim("framework", plan)...)
	files := make([]GeneratedFile, 0, len(referencePaths))
	for _, file := range generated {
		if target, ok := referencePaths[file.Path]; ok {
			file.Path = target
			files = append(files, file)
		}
	}
	return &EditorConfigSet{Editors: plan.Editors, Files: files}
}

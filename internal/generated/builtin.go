// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package generated

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/readmegovernance"
)

// SelfCommand is the first word of a render command that runs the running praetorctl binary
// itself (os.Executable), so a built-in artefact renders through the generator's own command
// handler and with the same build that checks it.
const SelfCommand = "praetorctl"

// Names of the built-in artefacts, as generated.decline names them.
const (
	NameProjections   = "compiled context projections"
	NameRegisterBlock = "agent register block"
	NameBaseline      = "debt baseline"
	NameReadmeBlock   = "README governance block"
	NameNeeds         = "needs manifest"
	NameFigures       = "documentation figures"
	NameShippedTexts  = "shipped-text ledger"
	NameDevContainer  = "devcontainer bundle"
)

// praetorSourceMarker is the file only a Praetor source checkout holds; the artefacts rendered
// from Praetor's own sources apply there alone.
const praetorSourceMarker = "cmd/standardsctl/main.go"

// builtin is one of Praetor's own generated artefacts and when it applies to a tree.
type builtin struct {
	decl config.GeneratedArtefact
	// requires is a file the tree must hold for the artefact to apply; empty, the artefact
	// applies wherever its paths select a file.
	requires string
	// praetor limits the artefact to a Praetor source checkout (praetorSourceMarker).
	praetor bool
}

// scannedSources are the files the HISS scan reads, the sources of the debt baseline.
var scannedSources = []string{
	".standards.yaml", ".standards.lock", "**/*.go", "**/*.py", "**/*.rs", "**/*.js", "**/*.mjs",
	"**/*.ts", "**/*.sh", "**/*.c", "**/*.h", "**/*.cc", "**/*.cpp", "**/*.hpp", "**/*.service",
}

// builtinArtefacts returns Praetor's own artefacts in render order: a generator that reads
// another one's output runs after it (the README block reads the baseline). The projection paths
// follow the manifest's agent_clients exactly as compile-context selects them.
func builtinArtefacts(manifest *config.Manifest) ([]builtin, error) {
	projections, err := projectionPaths(manifest.AgentClients)
	if err != nil {
		return nil, err
	}
	compile := []string{SelfCommand, "compile-context"}
	return []builtin{
		{requires: "AGENTS.md", decl: config.GeneratedArtefact{Name: NameProjections, Paths: projections, Command: compile,
			Sources: []string{"AGENTS.md", compiler.CanonicalAgentsRel + "/*.md", compiler.CanonicalSkillsRel + "/*/" + compiler.SkillEntryName, config.ManifestFileName}}},
		{decl: config.GeneratedArtefact{Name: NameRegisterBlock, Paths: []string{"AGENTS.md"}, Command: compile,
			Block:   &config.GeneratedBlock{Start: config.RegisterBlockStart, End: config.RegisterBlockEnd},
			Sources: []string{config.ManifestFileName}}},
		{requires: ".standards-baseline.json", decl: config.GeneratedArtefact{Name: NameBaseline, Paths: []string{".standards-baseline.json"},
			Command: []string{SelfCommand, "baseline", "--record"}, Sources: scannedSources}},
		{decl: config.GeneratedArtefact{Name: NameReadmeBlock, Paths: []string{readmegovernance.File},
			Block:   &config.GeneratedBlock{Start: readmegovernance.Start, End: readmegovernance.End},
			Command: []string{SelfCommand, "docs", "readme"}, Sources: []string{".standards-baseline.json", config.ManifestFileName}}},
		{requires: ".needs.yaml", decl: config.GeneratedArtefact{Name: NameNeeds, Paths: []string{".needs.yaml"},
			Command: []string{SelfCommand, "needs", "scan", "--write", "--manifest="},
			Env:     map[string]string{"PRAETOR_FLEET_CONFIG": "", "PRAETOR_WORKSTATION_CONFIG": ""},
			Sources: []string{config.ManifestFileName, "**/go.mod", "**/go.sum", "**/*.go", "**/package.json", "**/Cargo.toml", "**/pyproject.toml", "**/build.zig.zon"}}},
		{requires: "tools/figures/build.mjs", decl: config.GeneratedArtefact{Name: NameFigures,
			Paths:   []string{"docs/assets/figures/*.json", "docs/assets/figures/*.svg"},
			Command: []string{"node", "tools/figures/build.mjs", "build"},
			Sources: []string{"docs/figures/*.ts", "tools/figures/*.mjs", "tools/figures/third_party/interfig/upstream/src/**"}}},
		{praetor: true, decl: config.GeneratedArtefact{Name: NameShippedTexts, Paths: []string{"internal/managedasset/testdata/shipped/*.sha256"},
			Command: []string{"go", "test", "-count=1", "-run", "^TestShippedTextLedger$", "./internal/managedasset"},
			Env:     map[string]string{"PRAETOR_UPDATE_SHIPPED_TEXTS": "1"},
			Sources: []string{"internal/managedasset/*.go", "tools/markdownlint/**", "tools/apicompat/**", "tools/figures/**", ".github/workflows/praetor-*.yml"}}},
		{praetor: true, requires: ".devcontainer/Dockerfile.praetor", decl: config.GeneratedArtefact{Name: NameDevContainer,
			Paths:   []string{".devcontainer/devcontainer.json", ".devcontainer/Dockerfile.praetor", ".devcontainer/praetor-source.*.b64"},
			Command: []string{SelfCommand, "devcontainer", "generate", "--source-root=.", "--force"},
			Sources: []string{"go.mod", "go.sum", "LICENSE", "cmd/**/*.go", "internal/**/*.go", "tools/markdownlint/**", "templates/**", config.ManifestFileName}}},
	}, nil
}

// projectionPaths are the files compile-context writes for an agent_clients selection: each
// selected vendor context file, every persona copy in the selected clients' persona directories,
// and the plugin copies of the personas and skills.
func projectionPaths(clients []string) ([]string, error) {
	targets, _, err := agentcontext.TargetPaths(clients)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", NameProjections, err)
	}
	personas, _, err := agentcontext.PersonaDirs(clients)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", NameProjections, err)
	}
	paths := append([]string(nil), targets...)
	for index := 0; index < len(personas); index++ {
		paths = append(paths, personas[index]+"/*.md")
	}
	return append(paths, compiler.PluginAgentsRel+"/*.md", compiler.PluginSkillsRel+"/*/"+compiler.SkillEntryName), nil
}

// builtinNames lists the built-in names in render order.
func builtinNames(builtins []builtin) []string {
	names := make([]string, 0, len(builtins))
	for index := 0; index < len(builtins); index++ {
		names = append(names, builtins[index].decl.Name)
	}
	return names
}

// resolve resolves the built-in artefact for tree and decides whether it applies there.
func (b builtin) resolve(ctx context.Context, tree Tree, files []string) (Artefact, error) {
	artefact := newArtefact(b.decl, OriginBuiltin)
	if err := artefact.selectFiles(ctx, tree, files); err != nil {
		return artefact, err
	}
	artefact.Active, artefact.Reason = b.applies(&artefact, files)
	return artefact, nil
}

// applies reports whether the artefact applies to the tree holding files, and why not.
func (b builtin) applies(artefact *Artefact, files []string) (bool, string) {
	index := make(map[string]bool, len(files))
	for position := 0; position < len(files) && position < MaxTreeFiles; position++ {
		index[files[position]] = true
	}
	switch {
	case b.praetor && !index[praetorSourceMarker]:
		return false, "not a Praetor source checkout (no " + praetorSourceMarker + ")"
	case b.requires != "" && !index[b.requires]:
		return false, b.requires + " is absent"
	case len(artefact.Files) == 0 && artefact.Block != nil:
		return false, "no file carries its block markers"
	case len(artefact.Files) == 0:
		return false, "its paths select no file"
	}
	return true, ""
}

package agentcontext

import (
	"fmt"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/clientid"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	MaxLineBudget = 300
	// CanonicalFile names the single canonical context file (HISS-16) every vendor target is compiled from.
	CanonicalFile = "AGENTS.md"
)

// maxCanonicalLines bounds the ownership scan (HISS-02). A canonical file larger than the
// shared body plus one full-budget section per target cannot compile within the budget.
const maxCanonicalLines = MaxLineBudget * (len(vendorTargets) + 1)

// TargetFile describes a generated vendor-specific agent instructions file.
type TargetFile struct {
	RelativePath string
	Content      string
	LineCount    int
}

// CompileResult contains the output of context transpilation.
type CompileResult struct {
	SourcePath string
	Files      []TargetFile
	// NotApplicable lists the projections the client selection leaves out, by relative path.
	// They are neither written nor verified, so a projection the repository deleted stays
	// deleted.
	NotApplicable []string
}

// Transpiler compiles canonical AGENTS.md into vendor-native agent configurations.
type Transpiler struct {
	MaxLines int
	// Clients selects the projections CompileContent emits, by agent client id. nil emits
	// every projection, the behaviour before selection existed; a non-nil list, empty
	// included, emits exactly the projections of the clients it names (#202).
	Clients []string
}

// NewTranspiler creates a Transpiler with standard budget constraints.
func NewTranspiler() *Transpiler {
	return &Transpiler{
		MaxLines: MaxLineBudget,
	}
}

// vendorTarget pairs a compiled file with the agent client that reads it and the AGENTS.md
// `## <Vendor>` heading that belongs to that file alone. Every line outside such a section is
// shared by all targets. Client ids reuse internal/clientid where the client is known there;
// Cursor, Copilot and Windsurf have a projection but no client setup adapter. personaDir is
// the directory the client reads agent personas from (compiler.CompileAgentSurfaces copies
// .agents/agents there); Cursor and Windsurf read none.
type vendorTarget struct {
	client     string
	path       string
	section    string
	prefix     string
	personaDir string
}

var vendorTargets = [6]vendorTarget{
	{client: string(clientid.Claude), path: "CLAUDE.md", section: "Claude Code", personaDir: ".claude/agents"},
	{client: "cursor", path: ".cursor/rules/hiss-invariants.mdc", section: "Cursor", prefix: cursorFrontmatter},
	{client: "copilot", path: ".github/copilot-instructions.md", section: "GitHub Copilot", personaDir: ".github/agents"},
	{client: "windsurf", path: ".windsurfrules", section: "Windsurf"},
	{client: string(clientid.Gemini), path: ".gemini/GEMINI.md", section: "Gemini", personaDir: ".gemini/agents"},
	{client: string(clientid.Codex), path: ".codex/rules.md", section: "Codex", personaDir: ".codex/agents"},
}

// PersonaDirs resolves a client selection to the persona directories it keeps and the ones it
// leaves out, both in registry order, under the same rules as the context files: nil keeps
// every directory, an empty list keeps none, and an unknown id fails. A client that reads no
// personas appears in neither list.
func PersonaDirs(clients []string) (selected, excluded []string, err error) {
	return partitionTargets(clients, func(target vendorTarget) string { return target.personaDir })
}

// SelectedClients resolves a client selection to the agent client ids it keeps and the ones it
// leaves out, both in registry order, under the same rules as the context files: nil keeps
// every client, an empty list none, and an unknown id fails. A step that acts per client
// (adoption's hook registration) reads the selection here instead of re-parsing it.
func SelectedClients(clients []string) (selected, excluded []string, err error) {
	return partitionTargets(clients, func(target vendorTarget) string { return target.client })
}

// partitionTargets resolves a selection through selectTargets and returns field of every
// chosen and every left-out projection, in registry order. A projection whose field is empty
// appears in neither list.
func partitionTargets(clients []string, field func(vendorTarget) string) (selected, excluded []string, err error) {
	targets, _, err := selectTargets(clients)
	if err != nil {
		return nil, nil, err
	}
	chosen := make(map[string]bool, len(targets))
	for _, target := range targets {
		chosen[target.client] = true
	}
	for _, target := range vendorTargets {
		value := field(target)
		switch {
		case value == "":
		case chosen[target.client]:
			selected = append(selected, value)
		default:
			excluded = append(excluded, value)
		}
	}
	return selected, excluded, nil
}

// maxSelectedClients bounds a declared client list (HISS-02). A list longer than this cannot
// name distinct projections and is rejected rather than scanned.
const maxSelectedClients = 64

// Clients returns the agent client id of every projection, in registry order.
func Clients() []string {
	ids := make([]string, 0, len(vendorTargets))
	for _, target := range vendorTargets {
		ids = append(ids, target.client)
	}
	return ids
}

// selectTargets resolves a client selection to the projections it emits, in registry order,
// and the relative paths it leaves out. nil selects every projection. An id that names no
// projection fails the whole selection: a silently ignored typo reads as a working
// declaration until the projection it meant to keep goes missing.
func selectTargets(clients []string) ([]vendorTarget, []string, error) {
	if clients == nil {
		return vendorTargets[:], nil, nil
	}
	if len(clients) > maxSelectedClients {
		return nil, nil, fmt.Errorf("agent_clients names at most %d clients, got %d", maxSelectedClients, len(clients))
	}
	chosen := make(map[string]bool, len(clients))
	var unknown []string
	for _, id := range clients {
		name := strings.ToLower(strings.TrimSpace(id))
		if !knownClient(name) {
			unknown = append(unknown, id)
			continue
		}
		chosen[name] = true
	}
	if len(unknown) > 0 {
		return nil, nil, fmt.Errorf("unknown agent client id(s): %s; supported: %s",
			strings.Join(unknown, ", "), strings.Join(Clients(), ", "))
	}
	targets := make([]vendorTarget, 0, len(vendorTargets))
	var excluded []string
	for _, target := range vendorTargets {
		if chosen[target.client] {
			targets = append(targets, target)
			continue
		}
		excluded = append(excluded, target.path)
	}
	return targets, excluded, nil
}

func knownClient(name string) bool {
	for _, target := range vendorTargets {
		if target.client == name {
			return true
		}
	}
	return false
}

// VendorTargetPaths returns the slash-separated repository paths compile-context writes, in
// compile order. Callers that must recognise a compiled file (the CI filter's agent
// classification) read this list instead of keeping their own copy of it.
func VendorTargetPaths() []string {
	return targetPaths(vendorTargets[:])
}

// ContextFiles returns the canonical context file followed by every vendor file compile-context
// can write, in compile order, whatever a repository's agent_clients selects. It is the one
// list of agent instruction files: workstation discovery (harvester) and the kind inference of
// `caveman check` both read it (HISS-19; the hand-kept list it replaced missed files, BUG-840).
func ContextFiles() []string {
	paths := make([]string, 0, len(vendorTargets)+1)
	paths = append(paths, CanonicalFile)
	paths = append(paths, VendorTargetPaths()...)
	return paths
}

// IsContextPath reports whether rel is one of ContextFiles at its place in the repository. rel
// must be clean, slash-separated and relative to the repository root, as filepath.Rel followed
// by filepath.ToSlash returns it on every platform (HISS-21). The comparison is exact and
// case-sensitive on every file system: nested/AGENTS.md or docs/claude.md is not the canonical
// file, and on a case-insensitive file system agents.md opens AGENTS.md but does not match it.
func IsContextPath(rel string) bool {
	return slices.Contains(ContextFiles(), rel)
}

// TargetPaths resolves a client selection to the vendor file paths it writes and the ones it
// leaves out, both in registry order, under the rules CompileContent applies: nil selects every
// file, an empty list none, and an unknown id fails. A caller that checks the targets before
// anything is compiled (adoption's preflight) reads them here.
func TargetPaths(clients []string) (selected, excluded []string, err error) {
	targets, excluded, err := selectTargets(clients)
	if err != nil {
		return nil, nil, err
	}
	return targetPaths(targets), excluded, nil
}

func targetPaths(targets []vendorTarget) []string {
	paths := make([]string, 0, len(targets))
	for _, target := range targets {
		paths = append(paths, target.path)
	}
	return paths
}

// VendorTarget is one compiled vendor context file: its slash-separated path relative to the
// repository root and the name of the AGENTS.md `## <Vendor>` section that belongs to it alone.
type VendorTarget struct {
	Path    string
	Section string
}

// VendorTargets returns the files CompileContent writes for a client selection, in compile
// order, under the rules Transpiler.Clients follows: nil selects every file, an empty list
// none, and an unknown id fails. Prose that names the protected files and scans that discover
// them read this list instead of restating it; the restated copies had drifted to four of the
// six targets (BUG-840), and a copy that ignored agent_clients named files compile-context
// never writes.
func VendorTargets(clients []string) ([]VendorTarget, error) {
	selected, _, err := selectTargets(clients)
	if err != nil {
		return nil, err
	}
	return exportTargets(selected), nil
}

// AllVendorTargets returns every file CompileContent can write, in compile order: the list a
// scan of other repositories recognizes, whatever each of them selects.
func AllVendorTargets() []VendorTarget {
	return exportTargets(vendorTargets[:])
}

func exportTargets(selected []vendorTarget) []VendorTarget {
	targets := make([]VendorTarget, 0, len(selected))
	for _, target := range selected {
		targets = append(targets, VendorTarget{Path: target.path, Section: target.section})
	}
	return targets
}

// CompileContent synthesizes vendor-specific files directly from in-memory markdown content.
// Each target keeps the shared body and its own `## <Vendor>` section in place; every other
// vendor's section is removed, so guidance written for one agent never reaches another.
// A source in one consistent line-ending style compiles as its LF form, so a CRLF checkout of
// AGENTS.md yields the projections of its LF blob rather than LF headers above CRLF lines; a
// source with mixed endings compiles as it is.
func (t *Transpiler) CompileContent(content string) (*CompileResult, error) {
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("canonical AGENTS.md content is empty")
	}
	if lf, _, err := util.NormalizeLineEndingsStrict(content); err == nil {
		content = lf
	}
	targets, excluded, err := selectTargets(t.Clients)
	if err != nil {
		return nil, err
	}
	lines, owners, err := ownVendorLines(content)
	if err != nil {
		return nil, err
	}
	files := make([]TargetFile, 0, len(targets))
	for _, target := range targets {
		body := target.prefix + generatedHeader + selectVendorLines(lines, owners, target.section)
		count := countLines(body)
		if count > t.MaxLines {
			return nil, fmt.Errorf("target file %s exceeds max line budget (%d > %d)", target.path, count, t.MaxLines)
		}
		files = append(files, TargetFile{RelativePath: target.path, Content: body, LineCount: count})
	}

	return &CompileResult{
		SourcePath:    CanonicalFile,
		Files:         files,
		NotApplicable: excluded,
	}, nil
}

// ownVendorLines splits content into lines and labels each one with the vendor section that
// owns it, or "" when it is shared by every target. A section opens at its `## <Vendor>`
// heading and closes at the next H1 or H2; a heading inside a fenced block opens nothing.
func ownVendorLines(content string) ([]string, []string, error) {
	lines := strings.Split(content, "\n")
	if len(lines) > maxCanonicalLines {
		return nil, nil, fmt.Errorf("canonical AGENTS.md has %d lines; the vendor line budget admits at most %d", len(lines), maxCanonicalLines)
	}
	owners := make([]string, len(lines))
	owner := ""
	// util.MarkdownFence is the repository's one fence tracker (HISS-19). A toggle here
	// could not tell a ``` line inside a ```` block from a real close, and reopened the
	// scan in the middle of an example.
	var fence util.MarkdownFence
	for i := range lines {
		trimmed := strings.TrimSpace(lines[i])
		if !fence.Inside(trimmed) && isTopHeading(trimmed) {
			owner = vendorFor(trimmed)
		}
		owners[i] = owner
	}
	return lines, owners, nil
}

// selectVendorLines rejoins the shared lines plus the lines owned by section.
func selectVendorLines(lines, owners []string, section string) string {
	kept := make([]string, 0, len(lines))
	for i := range lines {
		if owners[i] == "" || owners[i] == section {
			kept = append(kept, lines[i])
		}
	}
	return strings.Join(kept, "\n")
}

// isTopHeading reports whether a trimmed line is an ATX H1 or H2, the only headings that
// close a vendor section.
func isTopHeading(trimmed string) bool {
	return strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ")
}

// vendorFor returns the section a trimmed H2 heading opens, or "" for an H1 and for any
// heading whose title is not exactly a vendor name.
func vendorFor(trimmed string) string {
	title := strings.TrimPrefix(trimmed, "## ")
	if title == trimmed {
		return ""
	}
	title = strings.TrimSpace(title)
	for _, target := range vendorTargets {
		if strings.EqualFold(title, target.section) {
			return target.section
		}
	}
	return ""
}

func countLines(s string) int {
	return strings.Count(s, "\n") + 1
}

// Vendor wrappers carry metadata only. Policy must come entirely from AGENTS.md.
const generatedHeader = "<!-- markdownlint-disable MD013 -->\n" +
	"<!-- Compiled automatically by praetorctl compile-context from AGENTS.md. DO NOT EDIT DIRECTLY. -->\n\n"

const cursorFrontmatter = "---\ndescription: Canonical agent instructions\nglobs: \"*\"\nalwaysApply: true\n---\n\n"

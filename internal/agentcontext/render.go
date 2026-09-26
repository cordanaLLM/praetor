package agentcontext

import (
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/clientid"
	"github.com/cordanaLLM/praetor/internal/util"
)

const MaxLineBudget = 300

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
// Cursor, Copilot and Windsurf have a projection but no client setup adapter.
type vendorTarget struct {
	client  string
	path    string
	section string
	prefix  string
}

var vendorTargets = [6]vendorTarget{
	{client: string(clientid.Claude), path: "CLAUDE.md", section: "Claude Code"},
	{client: "cursor", path: ".cursor/rules/hiss-invariants.mdc", section: "Cursor", prefix: cursorFrontmatter},
	{client: "copilot", path: ".github/copilot-instructions.md", section: "GitHub Copilot"},
	{client: "windsurf", path: ".windsurfrules", section: "Windsurf"},
	{client: string(clientid.Gemini), path: ".gemini/GEMINI.md", section: "Gemini"},
	{client: string(clientid.Codex), path: ".codex/rules.md", section: "Codex"},
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

// CompileContent synthesizes vendor-specific files directly from in-memory markdown content.
// Each target keeps the shared body and its own `## <Vendor>` section in place; every other
// vendor's section is removed, so guidance written for one agent never reaches another.
func (t *Transpiler) CompileContent(content string) (*CompileResult, error) {
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("canonical AGENTS.md content is empty")
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
		SourcePath:    "AGENTS.md",
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

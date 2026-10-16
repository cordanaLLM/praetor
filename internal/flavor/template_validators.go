package flavor

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Content validators for required templates. Each one checks the shape its format makes
// checkable and states the limit of that claim; none is a full grammar. What they share is
// the property the audit needs: the one-line "# <file> configuration" placeholder the
// scaffolder used to write, an empty file and a file of prose all fail, while every body
// in templates/ passes (TestEveryShippedBodySatisfiesItsOwnValidator).

// maxValidatedLines bounds a line scan (HISS-02). Content reaches a validator through the
// audit's maxSettingBytes read, so no validated file has more lines than it has bytes.
const maxValidatedLines = maxSettingBytes + 1

// maxValidatedTokens bounds an XML token scan (HISS-02) on the same reasoning: every
// token consumes at least one byte of input.
const maxValidatedTokens = maxSettingBytes + 1

// validWorkflow reports whether content is a GitHub Actions workflow that declares at least
// one job. A workflow with no jobs mapping is rejected by GitHub before it runs anything.
func validWorkflow(content []byte) bool {
	var document struct {
		Jobs map[string]any `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(content, &document); err != nil {
		return false
	}
	return len(document.Jobs) > 0
}

// validClangTidyConfig reports whether content parses as a YAML mapping declaring a non-empty
// WarningsAsErrors setting, so clang-tidy warnings fail the build as errors (HISS-10).
func validClangTidyConfig(content []byte) bool {
	var document struct {
		WarningsAsErrors *string `yaml:"WarningsAsErrors"`
	}
	if err := yaml.Unmarshal(content, &document); err != nil {
		return false
	}
	return document.WarningsAsErrors != nil && strings.TrimSpace(*document.WarningsAsErrors) != ""
}

// validDockerfile reports whether content declares at least one build stage: a FROM
// instruction, matched case-insensitively as the first word of a line. Comment lines and
// parser directives start with '#' and never match. The rest of the grammar is not checked.
func validDockerfile(content []byte) bool {
	lines := strings.Split(string(content), "\n")
	for i := 0; i < len(lines) && i < maxValidatedLines; i++ {
		if _, ok := util.ParseDockerFrom(lines[i]); ok {
			return true
		}
	}
	return false
}

// assignsTOMLKey reports whether content is valid TOML with at least one assigned value.
func assignsTOMLKey(content []byte) bool {
	var document map[string]any
	return util.DecodeTOML("template.toml", content, &document) == nil && tomlHasValue(document)
}

// validGitleaksConfig reports whether a gitleaks configuration loads any rule.
//
// gitleaks reads a repository-local .gitleaks.toml in place of its built-in configuration,
// so a file that neither extends the defaults ([extend] useDefault = true, or an [extend]
// path naming another config) nor declares [[rules]] of its own scans with zero rules and
// exits 0 on every secret (#410). That file is worse than no file, and the audit says so.
func validGitleaksConfig(content []byte) bool {
	var document struct {
		Extend struct {
			UseDefault bool   `toml:"useDefault"`
			Path       string `toml:"path"`
		} `toml:"extend"`
		Rules []map[string]any `toml:"rules"`
	}
	return util.DecodeTOML(".gitleaks.toml", content, &document) == nil &&
		(document.Extend.UseDefault || document.Extend.Path != "" || len(document.Rules) > 0)
}

// tomlHasValue checks decoded TOML tables without recursion.
func tomlHasValue(document map[string]any) bool {
	queue := []map[string]any{document}
	for index := 0; index < len(queue) && index < maxValidatedTokens; index++ {
		keys := make([]string, 0, len(queue[index]))
		for key := range queue[index] {
			if len(keys) >= maxValidatedTokens {
				return false
			}
			keys = append(keys, key)
		}
		for keyIndex := 0; keyIndex < len(keys) && keyIndex < maxValidatedTokens; keyIndex++ {
			value := queue[index][keys[keyIndex]]
			if table, ok := value.(map[string]any); ok {
				queue = append(queue, table)
				continue
			}
			return true
		}
	}
	return false
}

// validXMLDocument reports whether content is well-formed XML carrying at least one element.
// Go's decoder never fetches a DOCTYPE's external DTD, so the check stays offline.
func validXMLDocument(content []byte) bool {
	decoder := xml.NewDecoder(bytes.NewReader(content))
	elements := 0
	for i := 0; i < maxValidatedTokens; i++ {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return elements > 0
		}
		if err != nil {
			return false
		}
		if _, ok := token.(xml.StartElement); ok {
			elements++
		}
	}
	return false
}

// validMarkdownDocument reports whether content carries text beyond headings and HTML
// comments. The placeholder "# AGENTS.md configuration for owner/repo" is a heading and
// nothing else; an agent harness or compiled instruction file has a body.
func validMarkdownDocument(content []byte) bool {
	lines := strings.Split(string(content), "\n")
	for i := 0; i < len(lines) && i < maxValidatedLines; i++ {
		line := strings.TrimSpace(lines[i])
		if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "<!--") {
			return true
		}
	}
	return false
}

// carriesCode reports whether content holds at least one line that is neither blank nor a
// comment in the '#', '//' or '/* */' styles. It serves formats Go cannot parse here --
// JavaScript and TypeScript modules, JSON with comments such as tsconfig.json -- and the
// alternatives beside them (.eslintrc.yml, .eslintrc.json), where rejecting a comment-only
// placeholder is the whole claim that can be made without a language toolchain.
func carriesCode(content []byte) bool {
	lines := strings.Split(string(content), "\n")
	inBlock := false
	for i := 0; i < len(lines) && i < maxValidatedLines; i++ {
		line := strings.TrimSpace(lines[i])
		if inBlock {
			_, rest, closed := strings.Cut(line, "*/")
			inBlock = !closed
			line = strings.TrimSpace(rest)
		}
		if strings.HasPrefix(line, "/*") {
			_, rest, closed := strings.Cut(line[2:], "*/")
			inBlock = !closed
			line = strings.TrimSpace(rest)
		}
		if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "//") {
			return true
		}
	}
	return false
}

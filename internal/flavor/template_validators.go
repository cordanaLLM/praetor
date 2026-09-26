package flavor

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
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

// validDockerfile reports whether content declares at least one build stage: a FROM
// instruction, matched case-insensitively as the first word of a line. Comment lines and
// parser directives start with '#' and never match. The rest of the grammar is not checked.
func validDockerfile(content []byte) bool {
	lines := strings.Split(string(content), "\n")
	for i := 0; i < len(lines) && i < maxValidatedLines; i++ {
		fields := strings.Fields(lines[i])
		if len(fields) >= 2 && strings.EqualFold(fields[0], "FROM") {
			return true
		}
	}
	return false
}

// tomlKeyAssignment matches a line that assigns a bare, quoted or dotted TOML key.
var tomlKeyAssignment = regexp.MustCompile(`^\s*(?:[A-Za-z0-9_-]+|"[^"]*"|'[^']*')(?:\s*\.\s*(?:[A-Za-z0-9_-]+|"[^"]*"|'[^']*'))*\s*=`)

// assignsTOMLKey reports whether content assigns at least one TOML key. It is a shape check,
// not a parser: the repository carries no TOML library, and what the audit must reject --
// a comment-only placeholder, an empty file, prose -- assigns nothing.
func assignsTOMLKey(content []byte) bool {
	lines := strings.Split(string(content), "\n")
	for i := 0; i < len(lines) && i < maxValidatedLines; i++ {
		if tomlKeyAssignment.MatchString(lines[i]) {
			return true
		}
	}
	return false
}

// validGitleaksConfig reports whether a gitleaks configuration loads any rule.
//
// gitleaks reads a repository-local .gitleaks.toml in place of its built-in configuration,
// so a file that neither extends the defaults ([extend] useDefault = true, or an [extend]
// path naming another config) nor declares [[rules]] of its own scans with zero rules and
// exits 0 on every secret (#410). That file is worse than no file, and the audit says so.
func validGitleaksConfig(content []byte) bool {
	table := ""
	lines := strings.Split(string(content), "\n")
	for i := 0; i < len(lines) && i < maxValidatedLines; i++ {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "[") {
			table = tomlTableName(line)
			if table == "[rules]" {
				return true
			}
			continue
		}
		if gitleaksExtendsRules(table, line) {
			return true
		}
	}
	return false
}

// tomlTableName normalizes a table header to its name with the spaces TOML permits
// removed: "[ extend ]" is "extend" and "[[ rules ]]" is "[rules]".
func tomlTableName(header string) string {
	name, _, _ := strings.Cut(header, "#")
	name = strings.ReplaceAll(strings.TrimSpace(name), " ", "")
	name = strings.TrimSuffix(strings.TrimPrefix(name, "["), "]")
	return strings.Trim(name, `"'`)
}

// gitleaksExtendsRules reports whether one key line, read inside table, makes gitleaks load
// rules from elsewhere: useDefault = true or a path under [extend], or the dotted
// extend.useDefault = true at the top level.
func gitleaksExtendsRules(table, line string) bool {
	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return false
	}
	key = strings.ReplaceAll(strings.TrimSpace(key), " ", "")
	words := strings.Fields(value)
	if len(words) == 0 {
		return false
	}
	switch {
	case table == "extend" && key == "useDefault", table == "" && key == "extend.useDefault":
		return words[0] == "true"
	case table == "extend" && key == "path":
		return strings.Trim(words[0], `"'`) != ""
	default:
		return false
	}
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

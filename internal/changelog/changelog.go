package changelog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/standards/internal/util"
	"gopkg.in/yaml.v3"
)

// FragmentType represents valid Keep-a-Changelog section types.
type FragmentType string

const (
	TypeAdded      FragmentType = "added"
	TypeChanged    FragmentType = "changed"
	TypeDeprecated FragmentType = "deprecated"
	TypeRemoved    FragmentType = "removed"
	TypeFixed      FragmentType = "fixed"
	TypeSecurity   FragmentType = "security"
)

// Fragment represents a single changelog entry stored in changelog.d/.
type Fragment struct {
	Type     FragmentType `yaml:"type"`
	Title    string       `yaml:"title"`
	Issue    string       `yaml:"issue,omitempty"`
	Breaking bool         `yaml:"breaking,omitempty"`
}

var sectionOrder = []FragmentType{
	TypeSecurity,
	TypeAdded,
	TypeChanged,
	TypeDeprecated,
	TypeRemoved,
	TypeFixed,
}

var sectionTitles = map[FragmentType]string{
	TypeAdded:      "Added",
	TypeChanged:    "Changed",
	TypeDeprecated: "Deprecated",
	TypeRemoved:    "Removed",
	TypeFixed:      "Fixed",
	TypeSecurity:   "Security",
}

// CreateFragment writes a new YAML fragment file into changelog.d/.
func CreateFragment(repoPath string, f Fragment) (string, error) {
	if f.Title == "" {
		return "", fmt.Errorf("changelog: title cannot be empty")
	}
	f.Type = FragmentType(strings.ToLower(string(f.Type)))
	if _, ok := sectionTitles[f.Type]; !ok {
		return "", fmt.Errorf("changelog: invalid fragment type %q", f.Type)
	}

	dir := filepath.Join(repoPath, "changelog.d")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create changelog.d: %w", err)
	}

	slug := slugify(f.Title)
	filename := fmt.Sprintf("%d-%s.yaml", time.Now().UnixNano()%1000000, slug)
	target := filepath.Join(dir, filename)

	data, err := yaml.Marshal(f)
	if err != nil {
		return "", fmt.Errorf("marshal fragment: %w", err)
	}

	if err := os.WriteFile(target, data, 0644); err != nil {
		return "", fmt.Errorf("write fragment: %w", err)
	}
	return target, nil
}

// LoadFragments reads all fragment files in changelog.d/.
func LoadFragments(repoPath string) ([]Fragment, []string, error) {
	dir := filepath.Join(repoPath, "changelog.d")
	if !util.DirExists(dir) {
		return nil, nil, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("read changelog.d: %w", err)
	}

	var fragments []Fragment
	var files []string

	for _, e := range entries {
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".yaml") && !strings.HasSuffix(e.Name(), ".yml")) {
			continue
		}
		filePath := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		var f Fragment
		if err := yaml.Unmarshal(data, &f); err == nil && f.Title != "" {
			f.Type = FragmentType(strings.ToLower(string(f.Type)))
			fragments = append(fragments, f)
			files = append(files, filePath)
		}
	}

	return fragments, files, nil
}

// RenderRelease renders fragments into CHANGELOG.md and removes rendered fragment files.
func RenderRelease(repoPath, version, date string) error {
	fragments, files, err := LoadFragments(repoPath)
	if err != nil {
		return err
	}
	if len(fragments) == 0 {
		return nil
	}

	renderedSection := buildReleaseSection(fragments, version, date)
	changelogPath := filepath.Join(repoPath, "CHANGELOG.md")

	if err := spliceChangelog(changelogPath, renderedSection); err != nil {
		return fmt.Errorf("splice changelog: %w", err)
	}

	// Remove rendered fragment files
	for _, f := range files {
		if rmErr := os.Remove(f); rmErr != nil {
			continue
		}
	}
	return nil
}

func buildReleaseSection(fragments []Fragment, version, date string) string {
	grouped := make(map[FragmentType][]Fragment)
	for _, f := range fragments {
		grouped[f.Type] = append(grouped[f.Type], f)
	}

	var sb strings.Builder
	if date == "" {
		date = time.Now().UTC().Format("2006-01-02")
	}
	sb.WriteString(fmt.Sprintf("## [%s] - %s\n\n", version, date))

	for _, sec := range sectionOrder {
		items := grouped[sec]
		if len(items) == 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("### %s\n\n", sectionTitles[sec]))
		for _, it := range items {
			line := fmt.Sprintf("- %s", it.Title)
			if it.Breaking {
				line = fmt.Sprintf("- **BREAKING**: %s", it.Title)
			}
			if it.Issue != "" {
				line = fmt.Sprintf("%s (#%s)", line, it.Issue)
			}
			sb.WriteString(line + "\n")
		}
		sb.WriteString("\n")
	}

	return strings.TrimRight(sb.String(), "\n") + "\n\n"
}

func spliceChangelog(path, releaseSection string) error {
	initialContent := `# Changelog

All notable changes to this project will be documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

`
	if !util.FileExists(path) {
		return os.WriteFile(path, []byte(initialContent+releaseSection), 0644)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	content := string(data)
	unreleasedHeader := "## [Unreleased]\n"
	idx := strings.Index(content, unreleasedHeader)
	if idx != -1 {
		insertPos := idx + len(unreleasedHeader)
		// Check for empty line after unreleased header
		if strings.HasPrefix(content[insertPos:], "\n") {
			insertPos++
		}
		newContent := content[:insertPos] + "\n" + releaseSection + content[insertPos:]
		return os.WriteFile(path, []byte(newContent), 0644)
	}

	// Fallback prepend after first header
	lines := strings.Split(content, "\n")
	var newLines []string
	inserted := false
	for _, line := range lines {
		newLines = append(newLines, line)
		if !inserted && strings.HasPrefix(line, "# ") {
			newLines = append(newLines, "", "## [Unreleased]", "", releaseSection)
			inserted = true
		}
	}
	return os.WriteFile(path, []byte(strings.Join(newLines, "\n")), 0644)
}

func slugify(s string) string {
	s = strings.ToLower(s)
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		} else if sb.Len() > 0 && sb.String()[sb.Len()-1] != '-' {
			sb.WriteRune('-')
		}
	}
	res := strings.Trim(sb.String(), "-")
	if len(res) > 30 {
		res = res[:30]
	}
	if res == "" {
		res = "change"
	}
	return res
}

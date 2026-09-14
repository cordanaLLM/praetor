package changelog

import (
	"context"
	"crypto/rand"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
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
	if strings.TrimSpace(f.Title) == "" {
		return "", fmt.Errorf("changelog: title cannot be empty")
	}
	// A title that cannot be represented is refused at creation rather than at release.
	// Accepting it writes a fragment that renders into the journaled section and fails the
	// release for whoever runs it next, with an error pointing at a JSON offset rather than
	// at the fragment that caused it.
	if !RenderTextRepresentable(f.Title) || !RenderTextRepresentable(f.Issue) {
		return "", fmt.Errorf("%w: fragment title and issue", ErrRenderTextUnrepresentable)
	}
	f.Type = FragmentType(strings.ToLower(string(f.Type)))
	if _, ok := sectionTitles[f.Type]; !ok {
		return "", fmt.Errorf("changelog: invalid fragment type %q", f.Type)
	}

	dir := filepath.Join(repoPath, "changelog.d")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := contextopt.EnsureDirectory(ctx, dir, 0755); err != nil {
		return "", fmt.Errorf("create changelog.d: %w", err)
	}

	slug := slugify(f.Title)
	filename := fmt.Sprintf("%s-%s.yaml", rand.Text(), slug)
	target := filepath.Join(dir, filename)

	data, err := yaml.Marshal(f)
	if err != nil {
		return "", fmt.Errorf("marshal fragment: %w", err)
	}
	// Refuse a fragment this repository's own reader could not load back. Enumerating the
	// characters that break the encoding is a losing game -- a tab in a title emits a block
	// scalar the parser rejects, and that is only one shape -- so the encoding is asked
	// directly instead. Without this the write succeeds and the next release fails for
	// whoever runs it, naming a YAML line rather than the fragment that caused it.
	if err := verifyFragmentRoundTrip(data, f); err != nil {
		return "", err
	}

	if err := contextopt.ReplaceSnapshot(ctx, target, data, contextopt.ReplaceOptions{Mode: 0644}); err != nil {
		return "", fmt.Errorf("write fragment: %w", err)
	}
	return target, nil
}

// LoadFragments reads all fragment files in changelog.d/.
func LoadFragments(repoPath string) ([]Fragment, []string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return loadFragmentsContext(ctx, repoPath)
}

// RenderRelease renders a release and resumes any interrupted matching cleanup.
func RenderRelease(repoPath, version, date string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return RenderReleaseContext(ctx, repoPath, version, date)
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
	fmt.Fprintf(&sb, "## [%s] - %s\n\n", version, date)

	for _, sec := range sectionOrder {
		items := grouped[sec]
		if len(items) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "### %s\n\n", sectionTitles[sec])
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

func spliceChangelog(data []byte, exists bool, releaseSection string) []byte {
	initialContent := `# Changelog

All notable changes to this project will be documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

`
	if !exists {
		return []byte(initialContent + releaseSection)
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
		return []byte(newContent)
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
	if !inserted {
		return []byte(initialContent + releaseSection + content)
	}
	return []byte(strings.Join(newLines, "\n"))
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

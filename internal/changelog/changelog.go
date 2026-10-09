package changelog

import (
	"context"
	"crypto/rand"
	"errors"
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

// FragmentDir is the repository-relative directory that holds changelog fragments.
const FragmentDir = "changelog.d"

// FragmentPlaceholder is the empty file a release render leaves in FragmentDir once it has
// removed the rendered fragments. Git keeps no empty directory, so without a tracked file the
// directory would exist in the rendering checkout but not in a fresh clone (FragmentDirPresent).
const FragmentPlaceholder = ".gitkeep"

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
	normIssue, err := NormaliseIssue(f.Issue)
	if err != nil {
		return "", fmt.Errorf("changelog: %w", err)
	}
	f.Issue = normIssue
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

	dir := filepath.Join(repoPath, FragmentDir)
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
			sb.WriteString(renderFragmentLine(it) + "\n")
		}
		sb.WriteString("\n")
	}

	return strings.TrimRight(sb.String(), "\n") + "\n\n"
}

func renderFragmentLine(it Fragment) string {
	line := fmt.Sprintf("- %s", it.Title)
	if it.Breaking {
		line = fmt.Sprintf("- **BREAKING**: %s", it.Title)
	}
	if it.Issue == "" {
		return line
	}
	issue, err := NormaliseIssue(it.Issue)
	if err == nil {
		it.Issue = issue
	}
	if strings.Contains(it.Issue, "/") {
		return fmt.Sprintf("%s (%s)", line, it.Issue)
	}
	return fmt.Sprintf("%s (#%s)", line, it.Issue)
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

// NormaliseIssue normalises an issue reference: strips one leading '#',
// validates digits only, an owner/repo#n cross reference, or a comma-separated list
// of issue references. An empty-after-strip value or invalid format returns an error.
func NormaliseIssue(issue string) (string, error) {
	if issue == "" {
		return "", nil
	}
	raw := strings.TrimSpace(issue)
	if raw == "" {
		return "", errors.New("issue cannot be whitespace only")
	}
	stripped := strings.TrimPrefix(raw, "#")
	if strings.TrimSpace(stripped) == "" {
		return "", fmt.Errorf("issue %q is empty after stripping leading #", issue)
	}

	items := strings.Split(stripped, ",")
	parts := make([]string, len(items))
	for i, item := range items {
		norm, err := validateIssueComponent(i, item, issue)
		if err != nil {
			return "", err
		}
		parts[i] = norm
	}
	return strings.Join(parts, ", "), nil
}

func validateIssueComponent(index int, item, original string) (string, error) {
	trimmed := strings.TrimSpace(item)
	if trimmed == "" {
		return "", fmt.Errorf("invalid issue %q: empty component", original)
	}
	if index == 0 {
		if strings.HasPrefix(trimmed, "#") {
			return "", fmt.Errorf("invalid issue %q: multiple leading # symbols", original)
		}
		if !isValidIssueRef(trimmed) {
			return "", fmt.Errorf("invalid issue %q: must be digits or owner/repo#n", original)
		}
		return trimmed, nil
	}
	if strings.HasPrefix(trimmed, "##") {
		return "", fmt.Errorf("invalid issue %q: multiple leading # symbols", original)
	}
	ref := strings.TrimPrefix(trimmed, "#")
	if !isValidIssueRef(ref) {
		return "", fmt.Errorf("invalid issue %q: must be digits or owner/repo#n", original)
	}
	if !strings.Contains(ref, "/") {
		return "#" + ref, nil
	}
	return ref, nil
}

func isValidIssueRef(s string) bool {
	return isDigitsOnly(s) || isOwnerRepoIssue(s)
}

func isDigitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isOwnerRepoIssue(s string) bool {
	slash := strings.IndexByte(s, '/')
	if slash <= 0 || slash == len(s)-1 {
		return false
	}
	owner := s[:slash]
	rest := s[slash+1:]
	hash := strings.IndexByte(rest, '#')
	if hash <= 0 || hash == len(rest)-1 {
		return false
	}
	repo := rest[:hash]
	num := rest[hash+1:]
	return isIssueIdentifier(owner) && isIssueIdentifier(repo) && isDigitsOnly(num)
}

func isIssueIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !validIssueByte(s[i]) {
			return false
		}
	}
	return true
}

func validIssueByte(c byte) bool {
	if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
		return true
	}
	if c >= '0' && c <= '9' {
		return true
	}
	return c == '_' || c == '-' || c == '.'
}

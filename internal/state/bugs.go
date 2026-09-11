package state

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// BugEntry captures a defect discovered during active development.
type BugEntry struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Severity   string    `json:"severity"` // "p0", "p1", "p2", "p3"
	Location   string    `json:"location,omitempty"`
	Context    string    `json:"context,omitempty"`
	Status     string    `json:"status"` // "open", "investigating", "deferred", "resolved"
	Resolution string    `json:"resolution,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	ResolvedAt time.Time `json:"resolved_at,omitempty"`
}

// AddBug records a new bug into .workingdir/BUGS.md.
func AddBug(rootPath string, bug BugEntry) (*BugEntry, error) {
	if err := InitWorkingDir(rootPath); err != nil {
		return nil, err
	}
	existing, err := ListBugs(rootPath, "all")
	if err != nil {
		return nil, err
	}

	bug.ID = fmt.Sprintf("BUG-%03d", len(existing)+1)
	if bug.Severity == "" {
		bug.Severity = "p2"
	}
	if bug.Status == "" {
		bug.Status = "open"
	}
	if bug.CreatedAt.IsZero() {
		bug.CreatedAt = time.Now().UTC()
	}

	allBugs := append(existing, bug)
	if err := saveBugs(rootPath, allBugs); err != nil {
		return nil, err
	}
	return &bug, nil
}

// ListBugs parses and returns bugs from .workingdir/BUGS.md.
func ListBugs(rootPath string, filterStatus string) ([]BugEntry, error) {
	bugsFile := filepath.Join(rootPath, WorkingDirName, "BUGS.md")
	if !util.FileExists(bugsFile) {
		return []BugEntry{}, nil
	}
	content, err := os.ReadFile(bugsFile)
	if err != nil {
		return nil, err
	}

	all := ParseBugsMarkdown(string(content))
	if filterStatus == "" || filterStatus == "all" {
		return all, nil
	}

	filtered := make([]BugEntry, 0, len(all))
	for _, b := range all {
		if strings.EqualFold(b.Status, filterStatus) {
			filtered = append(filtered, b)
		}
	}
	return filtered, nil
}

// ResolveBug marks a bug as resolved with the provided resolution summary.
func ResolveBug(rootPath string, id string, resolution string) error {
	existing, err := ListBugs(rootPath, "all")
	if err != nil {
		return err
	}
	found := false
	for i := range existing {
		if strings.EqualFold(existing[i].ID, id) {
			existing[i].Status = "resolved"
			existing[i].Resolution = resolution
			existing[i].ResolvedAt = time.Now().UTC()
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("bug with ID %q not found", id)
	}
	return saveBugs(rootPath, existing)
}

func saveBugs(rootPath string, bugs []BugEntry) error {
	bugsFile := filepath.Join(rootPath, WorkingDirName, "BUGS.md")
	rendered := RenderBugsMarkdown(bugs)
	return os.WriteFile(bugsFile, []byte(rendered), 0644)
}

var bugRowRegex = regexp.MustCompile(`^\|\s*` + "`" + `(BUG-\d+)` + "`" + `\s*\|\s*([^|]+)\|\s*([a-zA-Z0-9]+)\s*\|\s*([a-zA-Z]+)\s*\|\s*([^|]*)\|\s*([^|]*)\|`)

// ParseBugsMarkdown extracts structured bug entries from markdown table rows.
func ParseBugsMarkdown(md string) []BugEntry {
	lines := strings.Split(md, "\n")
	res := make([]BugEntry, 0)
	for _, line := range lines {
		m := bugRowRegex.FindStringSubmatch(strings.TrimSpace(line))
		if len(m) >= 7 {
			res = append(res, BugEntry{
				ID:         m[1],
				Title:      strings.TrimSpace(m[2]),
				Severity:   strings.ToLower(strings.TrimSpace(m[3])),
				Status:     strings.ToLower(strings.TrimSpace(m[4])),
				Location:   strings.TrimSpace(m[5]),
				Resolution: strings.TrimSpace(m[6]),
			})
		}
	}
	return res
}

// RenderBugsMarkdown formats a slice of bugs into standard markdown table.
func RenderBugsMarkdown(bugs []BugEntry) string {
	var sb strings.Builder
	sb.WriteString("# Bug Ledger\n\n")
	sb.WriteString("> Discoveries made while coding that should not distract from the active task.\n\n")
	sb.WriteString("| ID | Title | Severity | Status | Location | Resolution |\n")
	sb.WriteString("| :--- | :--- | :--- | :--- | :--- | :--- |\n")
	for _, b := range bugs {
		sb.WriteString(fmt.Sprintf("| `%s` | %s | %s | %s | %s | %s |\n",
			b.ID, b.Title, b.Severity, b.Status, b.Location, b.Resolution))
	}
	return sb.String()
}

func defaultBugsMD() string {
	return RenderBugsMarkdown([]BugEntry{})
}

package state

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// archiveMonthLayout fixes portable month names; dates without a valid day use the run month.
const archiveMonthLayout = "2006-01"

var archiveDatePattern = regexp.MustCompile(`\b[0-9]{4}-[0-9]{2}-[0-9]{2}\b`)

// FileArchiveReport is the per-file preview emitted before optional writes.
type FileArchiveReport struct {
	Name        string
	Entries     int
	BytesBefore int
	BytesAfter  int
}

// taskArchivePlan binds verbatim live bytes to monthly archive records.
type taskArchivePlan struct {
	file   liveStateFile
	after  []byte
	groups map[string][]string
	report FileArchiveReport
}

// planTaskArchive separates real completed rows, retaining every other byte and fence.
func planTaskArchive(file liveStateFile, now time.Time) (taskArchivePlan, error) {
	plan := taskArchivePlan{file: file, after: file.content, groups: map[string][]string{},
		report: FileArchiveReport{Name: file.usage.Name, BytesBefore: len(file.content), BytesAfter: len(file.content)}}
	if file.usage.Name == bugLedgerName {
		return planBugArchive(plan, now)
	}
	lines := strings.SplitAfter(string(file.content), "\n")
	removed := make(map[int]bool, file.usage.Completed)
	for i := 0; i < len(file.tasks) && i < maxScannedLines; i++ {
		task := file.tasks[i]
		if !task.completed {
			continue
		}
		month := taskCompletionMonth(task.description, now)
		plan.groups[month] = append(plan.groups[month], lines[task.line])
		removed[task.line] = true
		plan.report.Entries++
	}
	var kept strings.Builder
	for i := 0; i < len(lines) && i <= maxScannedLines; i++ {
		if !removed[i] {
			kept.WriteString(lines[i])
		}
	}
	plan.after = []byte(kept.String())
	plan.report.BytesAfter = len(plan.after)
	return plan, nil
}

// planBugArchive keeps the live table header and renders sidecar rows self-contained.
func planBugArchive(plan taskArchivePlan, now time.Time) (taskArchivePlan, error) {
	if plan.file.bugs == nil {
		return plan, nil
	}
	doc := plan.file.bugs
	last := 0
	var kept strings.Builder
	for i := 0; i < len(doc.rows) && i < maxLedgerEntries; i++ {
		row := doc.rows[i]
		if row.bug.Status != "resolved" {
			continue
		}
		record, err := bugArchiveRecord(doc, row)
		if err != nil {
			return plan, fmt.Errorf("archive %s: %w", row.bug.ID, err)
		}
		month := bugResolutionMonth(row.bug, now)
		plan.groups[month] = append(plan.groups[month], record)
		kept.WriteString(doc.text[last:row.start])
		last = bugArchiveRowEnd(doc, row)
		plan.report.Entries++
	}
	kept.WriteString(doc.text[last:])
	plan.after = []byte(kept.String())
	plan.report.BytesAfter = len(plan.after)
	return plan, nil
}

// bugArchiveRowEnd includes the row's existing line terminator in the removed range.
func bugArchiveRowEnd(doc *bugDocument, row bugRow) int {
	end := row.end
	if strings.HasPrefix(doc.text[end:], doc.newline) {
		end += len(doc.newline)
	}
	return end
}

// bugArchiveRecord preserves original rows except v2 references, whose metadata travels inline.
func bugArchiveRecord(doc *bugDocument, row bugRow) (string, error) {
	if row.form != bugRowSidecar {
		return doc.text[row.start:bugArchiveRowEnd(doc, row)], nil
	}
	encoded, err := encodeInlineBugRow(row.bug)
	return encoded + doc.newline, err
}

// taskCompletionMonth reads only a completion marker, never a task's unrelated dates.
func taskCompletionMonth(description string, now time.Time) string {
	_, suffix, found := strings.Cut(description, "(completed:")
	if found {
		date, _, _ := strings.Cut(suffix, ")")
		return archiveDateMonth(strings.TrimSpace(date), now)
	}
	return now.UTC().Format(archiveMonthLayout)
}

// bugResolutionMonth prefers structured metadata, then a dated legacy resolution cell.
func bugResolutionMonth(bug BugEntry, now time.Time) string {
	if !bug.ResolvedAt.IsZero() {
		return bug.ResolvedAt.UTC().Format(archiveMonthLayout)
	}
	return archiveDateMonth(archiveDatePattern.FindString(bug.Resolution), now)
}

// archiveDateMonth validates a full completion date before allowing it into a filename.
func archiveDateMonth(date string, now time.Time) string {
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		return now.UTC().Format(archiveMonthLayout)
	}
	return parsed.Format(archiveMonthLayout)
}

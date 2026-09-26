package caveman

import (
	"errors"
	"regexp"
	"strings"
)

var taskFieldValueRe = regexp.MustCompile(`(?i)^(?:[-*+]\s+)?task:\s*(.*?)\s*$`)

// ExtractBriefTask returns the task label carried by a structured Caveman brief. It
// deliberately reuses Check's scanner and field grammar, so task-aware runtime gates do
// not grow a second interpretation of fenced code, caveman:off regions, or list fields.
// Check remains responsible for the complete brief contract; this helper only extracts
// the one routing value a caller needs after validation.
func ExtractBriefTask(text string) (string, error) {
	lines, _ := scan(text)
	task := ""
	seen := 0
	for _, line := range lines {
		if !shapeContent(line) {
			continue
		}
		prose := maskQuoted(proseOf(line.text))
		if schemaFieldAtStart(prose) != "task" {
			continue
		}
		seen++
		match := taskFieldValueRe.FindStringSubmatch(strings.TrimSpace(prose))
		if len(match) == 2 {
			task = strings.TrimSpace(match[1])
		}
	}
	if seen == 0 {
		return "", errors.New("brief task field is missing")
	}
	if seen > 1 {
		return "", errors.New("brief task field is duplicated")
	}
	if task == "" {
		return "", errors.New("brief task field must be nonempty")
	}
	return task, nil
}

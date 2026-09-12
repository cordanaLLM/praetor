package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// QuestionEntry buffers non-blocking user decisions and questions collected during autonomous work.
type QuestionEntry struct {
	ID             string    `json:"id"`
	Question       string    `json:"question"`
	Context        string    `json:"context,omitempty"`
	Options        []string  `json:"options,omitempty"`
	SelectedAnswer string    `json:"selected_answer,omitempty"`
	Status         string    `json:"status"` // "pending", "decided", "dismissed"
	CreatedAt      time.Time `json:"created_at"`
	DecidedAt      time.Time `json:"decided_at,omitempty"`
}

// AddQuestion records a new decision requirement into .workingdir/QUESTIONS.md.
func AddQuestion(rootPath string, q QuestionEntry) (*QuestionEntry, error) {
	if err := InitWorkingDir(rootPath); err != nil {
		return nil, err
	}
	existing, err := ListQuestions(rootPath, "all")
	if err != nil {
		return nil, err
	}

	q.ID = fmt.Sprintf("Q-%03d", len(existing)+1)
	if q.Status == "" {
		q.Status = "pending"
	}
	if q.CreatedAt.IsZero() {
		q.CreatedAt = time.Now().UTC()
	}

	all := append(existing, q)
	if err := saveQuestions(rootPath, all); err != nil {
		return nil, err
	}
	return &q, nil
}

// ListQuestions retrieves questions from .workingdir/QUESTIONS.md.
func ListQuestions(rootPath string, filterStatus string) ([]QuestionEntry, error) {
	return ListQuestionsContext(context.Background(), rootPath, filterStatus)
}

// ListQuestionsContext reads a bounded question snapshot under the caller's deadline.
func ListQuestionsContext(ctx context.Context, rootPath string, filterStatus string) ([]QuestionEntry, error) {
	qFile := filepath.Join(rootPath, WorkingDirName, "QUESTIONS.md")
	content, err := contextopt.ReadSnapshot(ctx, qFile)
	if errors.Is(err, os.ErrNotExist) {
		return []QuestionEntry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read QUESTIONS.md: %w", err)
	}

	all := ParseQuestionsMarkdown(string(content))
	if filterStatus == "" || filterStatus == "all" {
		return all, nil
	}

	filtered := make([]QuestionEntry, 0, len(all))
	for _, q := range all {
		if strings.EqualFold(q.Status, filterStatus) {
			filtered = append(filtered, q)
		}
	}
	return filtered, nil
}

// DecideQuestion records the user's decision for a buffered question.
func DecideQuestion(rootPath string, id string, answer string) error {
	existing, err := ListQuestions(rootPath, "all")
	if err != nil {
		return err
	}
	found := false
	for i := range existing {
		if strings.EqualFold(existing[i].ID, id) {
			existing[i].Status = "decided"
			existing[i].SelectedAnswer = answer
			existing[i].DecidedAt = time.Now().UTC()
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("question with ID %q not found", id)
	}
	return saveQuestions(rootPath, existing)
}

func saveQuestions(rootPath string, qs []QuestionEntry) error {
	qFile := filepath.Join(rootPath, WorkingDirName, "QUESTIONS.md")
	rendered := RenderQuestionsMarkdown(qs)
	return os.WriteFile(qFile, []byte(rendered), 0644)
}

var qRowRegex = regexp.MustCompile(`^\|\s*` + "`" + `(Q-\d+)` + "`" + `\s*\|\s*([^|]+)\|\s*([^|]*)\|\s*([a-zA-Z]+)\s*\|\s*([^|]*)\|`)

// ParseQuestionsMarkdown parses question rows from markdown table.
func ParseQuestionsMarkdown(md string) []QuestionEntry {
	lines := strings.Split(md, "\n")
	res := make([]QuestionEntry, 0)
	for _, line := range lines {
		m := qRowRegex.FindStringSubmatch(strings.TrimSpace(line))
		if len(m) >= 6 {
			rawOpts := strings.Split(strings.TrimSpace(m[3]), ",")
			opts := make([]string, 0, len(rawOpts))
			for _, o := range rawOpts {
				clean := strings.TrimSpace(o)
				if clean != "" {
					opts = append(opts, clean)
				}
			}
			res = append(res, QuestionEntry{
				ID:             m[1],
				Question:       strings.TrimSpace(m[2]),
				Options:        opts,
				Status:         strings.ToLower(strings.TrimSpace(m[4])),
				SelectedAnswer: strings.TrimSpace(m[5]),
			})
		}
	}
	return res
}

// RenderQuestionsMarkdown renders questions into markdown table format.
func RenderQuestionsMarkdown(qs []QuestionEntry) string {
	var sb strings.Builder
	sb.WriteString("# User Decisions & Questions Buffer\n\n")
	sb.WriteString("> Questions buffered by autonomous agents for operator review.\n\n")
	sb.WriteString("| ID | Question | Options | Status | Decision |\n")
	sb.WriteString("| :--- | :--- | :--- | :--- | :--- |\n")
	for _, q := range qs {
		optsStr := strings.Join(q.Options, ", ")
		sb.WriteString(fmt.Sprintf("| `%s` | %s | %s | %s | %s |\n",
			q.ID, q.Question, optsStr, q.Status, q.SelectedAnswer))
	}
	return sb.String()
}

func defaultQuestionsMD() string {
	return RenderQuestionsMarkdown([]QuestionEntry{})
}

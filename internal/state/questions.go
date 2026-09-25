package state

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

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

// QUESTIONS.md shares the ledger codec with BUGS.md (ledger_codec.go, ledger_meta.go).
// A row written by this version ends in questionMetadataRef: its cells are entity-encoded,
// so '|', line breaks and commas inside a field survive, and its Context, CreatedAt and
// DecidedAt live in questions.meta.json under its ID (DecidedAt as the record's
// resolved_at). A row without the marker is the legacy form and is read literally.
const (
	questionLedgerName  = "QUESTIONS.md"
	questionMetaName    = "questions.meta.json"
	questionIDPrefix    = "Q-"
	questionMetadataRef = "<!-- praetor-question:v2 -->"
	maxQuestionOptions  = 64
)

var (
	questionSidecar  = sidecarSpec{name: questionMetaName, member: "questions", checkID: checkQuestionID}
	questionStatuses = []string{"pending", "decided", "dismissed"}
	// qRowRegex reads the legacy row form only; its cells cannot contain '|'.
	qRowRegex = regexp.MustCompile(`^\|\s*` + "`" + `(Q-\d+)` + "`" + `\s*\|\s*([^|]+)\|\s*([^|]*)\|\s*([a-zA-Z]+)\s*\|\s*([^|]*)\|$`)
)

// AddQuestion records a new decision requirement into .workingdir/QUESTIONS.md. The ID is
// one past the largest ID in the ledger, never a count of its rows.
func AddQuestion(rootPath string, q QuestionEntry) (*QuestionEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := InitWorkingDirContext(ctx, rootPath); err != nil {
		return nil, err
	}
	files, existing, err := loadQuestions(ctx, rootPath)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(existing))
	for _, e := range existing {
		ids = append(ids, e.ID)
	}
	if q.ID, err = nextLedgerID(questionIDPrefix, ids); err != nil {
		return nil, err
	}
	if q.Status == "" {
		q.Status = "pending"
	}
	if q.CreatedAt.IsZero() {
		q.CreatedAt = time.Now().UTC()
	}
	if err := validateQuestion(q); err != nil {
		return nil, fmt.Errorf("refuse question: %w", err)
	}
	if err := saveQuestions(ctx, rootPath, files, append(existing, q)); err != nil {
		return nil, err
	}
	return &q, nil
}

// ListQuestions retrieves questions from .workingdir/QUESTIONS.md.
func ListQuestions(rootPath string, filterStatus string) ([]QuestionEntry, error) {
	return ListQuestionsContext(context.Background(), rootPath, filterStatus)
}

// ListQuestionsContext reads a bounded question snapshot under the caller's deadline. A
// missing ledger is empty; a malformed one is an error, never a partial list.
func ListQuestionsContext(ctx context.Context, rootPath string, filterStatus string) ([]QuestionEntry, error) {
	files, all, err := loadQuestions(ctx, rootPath)
	if err != nil {
		return nil, err
	}
	if !files.ledgerExists || filterStatus == "" || filterStatus == "all" {
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	files, existing, err := loadQuestions(ctx, rootPath)
	if err != nil {
		return err
	}
	if !files.ledgerExists {
		return fmt.Errorf("question ledger %s is missing", questionLedgerName)
	}
	// IDs are unique (parseQuestionDocument rejects duplicates), so the match is the record.
	index := slices.IndexFunc(existing, func(q QuestionEntry) bool { return strings.EqualFold(q.ID, id) })
	if index < 0 {
		return fmt.Errorf("question with ID %q not found", id)
	}
	existing[index].Status, existing[index].SelectedAnswer, existing[index].DecidedAt = "decided", answer, time.Now().UTC()
	if err := validateQuestion(existing[index]); err != nil {
		return fmt.Errorf("refuse decision: %w", err)
	}
	return saveQuestions(ctx, rootPath, files, existing)
}

// loadQuestions reads QUESTIONS.md and its sidecar and validates them together. The ledger
// is read first: writers publish the sidecar first, so a ledger row is never newer than the
// sidecar read after it.
func loadQuestions(ctx context.Context, rootPath string) (ledgerFiles, []QuestionEntry, error) {
	var files ledgerFiles
	var err error
	dir := filepath.Join(rootPath, WorkingDirName)
	files.ledger, files.ledgerExists, err = contextopt.ObserveSnapshot(ctx, filepath.Join(dir, questionLedgerName))
	if err != nil {
		return files, nil, fmt.Errorf("read %s: %w", questionLedgerName, err)
	}
	if !files.ledgerExists {
		return files, []QuestionEntry{}, nil
	}
	files.meta, files.metaExists, err = contextopt.ObserveSnapshot(ctx, filepath.Join(dir, questionMetaName))
	if err != nil {
		return files, nil, fmt.Errorf("read %s: %w", questionMetaName, err)
	}
	index, err := sidecarIndex(questionSidecar, files)
	if err != nil {
		return files, nil, err
	}
	all, err := parseQuestionDocument(string(files.ledger), index)
	return files, all, err
}

// saveQuestions renders every question in the current form and publishes the sidecar
// before the ledger, each bound to the bytes it was derived from, so a crash in between
// never leaves a row without its metadata and a concurrent writer fails instead of being
// overwritten.
func saveQuestions(ctx context.Context, rootPath string, files ledgerFiles, qs []QuestionEntry) error {
	rendered, index, err := renderQuestionDocument(qs)
	if err != nil {
		return err
	}
	meta, err := encodeSidecar(questionSidecar, index)
	if err != nil {
		return err
	}
	dir := filepath.Join(rootPath, WorkingDirName)
	if !files.metaExists || !bytes.Equal(meta, files.meta) {
		options := contextopt.ReplaceOptions{Expected: files.meta, Exists: files.metaExists, Mode: 0o600}
		if err := contextopt.ReplaceSnapshot(ctx, filepath.Join(dir, questionMetaName), meta, options); err != nil {
			return fmt.Errorf("write %s: %w", questionMetaName, err)
		}
	}
	options := contextopt.ReplaceOptions{Expected: files.ledger, Exists: true, Mode: 0o600}
	if err := contextopt.ReplaceSnapshot(ctx, filepath.Join(dir, questionLedgerName), []byte(rendered), options); err != nil {
		return fmt.Errorf("write %s: %w", questionLedgerName, err)
	}
	return nil
}

func checkQuestionID(id string) error {
	_, err := ledgerIDNumber(questionIDPrefix, id)
	return err
}

// validateQuestion mirrors validateBug: a canonical ID, a question, a known status, and
// bounded UTF-8 text without NUL in every field.
func validateQuestion(q QuestionEntry) error {
	if err := checkQuestionID(q.ID); err != nil {
		return err
	}
	if strings.TrimSpace(q.Question) == "" {
		return fmt.Errorf("question text is required")
	}
	if !slices.Contains(questionStatuses, q.Status) {
		return fmt.Errorf("invalid question status %q", q.Status)
	}
	for _, field := range []string{q.Question, q.Context, q.SelectedAnswer} {
		if err := validateLedgerText(field); err != nil {
			return err
		}
	}
	return validateQuestionOptions(q.Options)
}

func validateQuestionOptions(options []string) error {
	if len(options) > maxQuestionOptions {
		return fmt.Errorf("question has %d options, at most %d", len(options), maxQuestionOptions)
	}
	for _, option := range options {
		if strings.TrimSpace(option) == "" {
			return fmt.Errorf("question options must not be blank")
		}
		if err := validateLedgerText(option); err != nil {
			return err
		}
	}
	return nil
}

// ParseQuestionsMarkdown parses question rows from a markdown table.
// Deprecated: use ParseQuestionsMarkdownStrict to distinguish invalid from empty.
// Invalid documents yield nil; no partial record set is returned.
func ParseQuestionsMarkdown(md string) []QuestionEntry {
	qs, err := ParseQuestionsMarkdownStrict(md)
	if err != nil {
		return nil
	}
	return qs
}

// ParseQuestionsMarkdownStrict validates the whole table before returning records. It reads
// the table alone: Context and timestamps live in questions.meta.json, which
// ListQuestionsContext reads together with the table.
func ParseQuestionsMarkdownStrict(md string) ([]QuestionEntry, error) {
	return parseQuestionDocument(md, nil)
}

// parseQuestionDocument validates a whole QUESTIONS.md. index is the sidecar, or nil when
// the caller has none; with an index, a current-form row without its record is an error.
func parseQuestionDocument(text string, index ledgerMetaIndex) ([]QuestionEntry, error) {
	if len(text) > maxLedgerBytes || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return nil, fmt.Errorf("question ledger must be UTF-8 without NUL, at most %d bytes", maxLedgerBytes)
	}
	lines := strings.Split(text, "\n")
	if len(lines) > maxScannedLines {
		return nil, fmt.Errorf("question ledger line bound exceeded")
	}
	seen := make(map[string]bool)
	qs := make([]QuestionEntry, 0)
	for i, line := range lines {
		q, ok, err := decodeQuestionLine(strings.TrimSpace(line), index)
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", questionLedgerName, i+1, err)
		}
		if !ok {
			continue
		}
		if seen[q.ID] {
			return nil, fmt.Errorf("%s line %d: duplicate question ID %s", questionLedgerName, i+1, q.ID)
		}
		if len(qs) >= maxLedgerEntries {
			return nil, fmt.Errorf("question count exceeds %d", maxLedgerEntries)
		}
		seen[q.ID] = true
		qs = append(qs, q)
	}
	return qs, nil
}

// decodeQuestionLine reads one line. A line that is not a question row reports ok false; a
// line claiming to be one must decode, so no row is ever skipped silently.
func decodeQuestionLine(line string, index ledgerMetaIndex) (QuestionEntry, bool, error) {
	if !strings.HasPrefix(line, "|") || !claimsLedgerRow(line, questionIDPrefix) {
		return QuestionEntry{}, false, nil
	}
	if strings.HasSuffix(line, questionMetadataRef) {
		q, err := decodeQuestionRow(line, index)
		return q, err == nil, err
	}
	m := qRowRegex.FindStringSubmatch(line)
	if m == nil {
		return QuestionEntry{}, false, fmt.Errorf("malformed question row")
	}
	q := QuestionEntry{
		ID:             m[1],
		Question:       strings.TrimSpace(m[2]),
		Options:        legacyQuestionOptions(m[3]),
		Status:         strings.ToLower(strings.TrimSpace(m[4])),
		SelectedAnswer: strings.TrimSpace(m[5]),
	}
	return q, true, validateQuestion(q)
}

// legacyQuestionOptions reads the legacy options cell, where a comma always separated two
// options: that ambiguity is why the current form escapes commas inside an option.
func legacyQuestionOptions(cell string) []string {
	raw := strings.Split(strings.TrimSpace(cell), ",")
	options := make([]string, 0, len(raw))
	for _, option := range raw {
		if clean := strings.TrimSpace(option); clean != "" {
			options = append(options, clean)
		}
	}
	return options
}

// decodeQuestionRow reads the current row form and attaches its sidecar record.
func decodeQuestionRow(line string, index ledgerMetaIndex) (QuestionEntry, error) {
	parts := strings.Split(line, "|")
	if len(parts) != 7 || strings.TrimSpace(parts[0]) != "" || strings.TrimSpace(parts[6]) != questionMetadataRef {
		return QuestionEntry{}, fmt.Errorf("expected exactly five question columns")
	}
	id, err := decodeLedgerIDCell(parts[1])
	if err != nil {
		return QuestionEntry{}, err
	}
	q := QuestionEntry{
		ID:             id,
		Question:       decodeLedgerCell(parts[2]),
		Options:        decodeQuestionOptions(parts[3]),
		Status:         strings.ToLower(strings.TrimSpace(parts[4])),
		SelectedAnswer: decodeLedgerCell(parts[5]),
	}
	if index != nil {
		metadata, ok := index[id]
		if !ok {
			return QuestionEntry{}, fmt.Errorf("%s metadata missing from %s", id, questionMetaName)
		}
		q.Context, q.CreatedAt, q.DecidedAt = metadata.Context, metadata.CreatedAt, metadata.ResolvedAt
	}
	return q, validateQuestion(q)
}

// encodeQuestionOptions joins options with ", " after escaping each option's own commas.
func encodeQuestionOptions(options []string) string {
	cells := make([]string, 0, len(options))
	for _, option := range options {
		cells = append(cells, strings.ReplaceAll(encodeLedgerCell(option), ",", "&#44;"))
	}
	return strings.Join(cells, ", ")
}

func decodeQuestionOptions(cell string) []string {
	if strings.Trim(cell, " ") == "" {
		return nil
	}
	raw := strings.Split(cell, ",")
	options := make([]string, 0, len(raw))
	for _, option := range raw {
		options = append(options, decodeLedgerCell(option))
	}
	return options
}

// RenderQuestionsMarkdown renders questions into the markdown table. The table holds the
// visible fields only; Context and timestamps are written to questions.meta.json by the
// ledger writers. Invalid records produce an explicitly invalid document rather than an
// apparently valid ledger.
func RenderQuestionsMarkdown(qs []QuestionEntry) string {
	md, _, err := renderQuestionDocument(qs)
	if err != nil {
		return "# User Decisions & Questions Buffer\n\nINVALID LEDGER: record validation failed\n"
	}
	return md
}

// renderQuestionDocument validates and renders every question, and returns the sidecar
// index the rendered rows refer to.
func renderQuestionDocument(qs []QuestionEntry) (string, ledgerMetaIndex, error) {
	if len(qs) > maxLedgerEntries {
		return "", nil, fmt.Errorf("question count exceeds %d", maxLedgerEntries)
	}
	var sb strings.Builder
	sb.WriteString(defaultQuestionsMD())
	index := make(ledgerMetaIndex, len(qs))
	for _, q := range qs {
		if err := validateQuestion(q); err != nil {
			return "", nil, err
		}
		if _, dup := index[q.ID]; dup {
			return "", nil, fmt.Errorf("duplicate question ID %s", q.ID)
		}
		index[q.ID] = ledgerMetadata{Context: q.Context, CreatedAt: q.CreatedAt, ResolvedAt: q.DecidedAt}
		sb.WriteString("| `" + q.ID + "` | " + encodeLedgerCell(q.Question) + " | " + encodeQuestionOptions(q.Options) +
			" | " + q.Status + " | " + encodeLedgerCell(q.SelectedAnswer) + " | " + questionMetadataRef + "\n")
	}
	return sb.String(), index, nil
}

func defaultQuestionsMD() string {
	return "# User Decisions & Questions Buffer\n\n" +
		"> Questions buffered by autonomous agents for operator review.\n\n" +
		"| ID | Question | Options | Status | Decision |\n" +
		"| :--- | :--- | :--- | :--- | :--- |\n"
}

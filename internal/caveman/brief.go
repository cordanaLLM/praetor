package caveman

import (
	"errors"
	"fmt"
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
		prose := maskQuoted(proseOf(line))
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

var readonlyFieldValueRe = regexp.MustCompile(`(?i)^(?:[-*+]\s+)?(?:readonly|read-only):\s*(.*?)\s*$`)

// ExtractBriefReadOnly returns whether a structured Caveman brief marks the execution
// read-only via a readonly (or read-only) field. An absent field returns false, nil (default
// read-write). Multiple fields or invalid values return an error.
func ExtractBriefReadOnly(text string) (bool, error) {
	lines, _ := scan(text)
	raw := ""
	seen := 0
	for _, line := range lines {
		if !shapeContent(line) {
			continue
		}
		prose := maskQuoted(proseOf(line))
		if schemaFieldAtStart(prose) != "readonly" {
			continue
		}
		seen++
		match := readonlyFieldValueRe.FindStringSubmatch(strings.TrimSpace(prose))
		if len(match) == 2 {
			raw = strings.TrimSpace(match[1])
		}
	}
	if seen == 0 {
		return false, nil
	}
	if seen > 1 {
		return false, errors.New("brief readonly field is duplicated")
	}
	if raw == "" {
		return false, errors.New("brief readonly field must be nonempty")
	}
	return parseBoolStrict(raw)
}

func parseBoolStrict(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "1", "on":
		return true, nil
	case "false", "no", "0", "off":
		return false, nil
	default:
		return false, fmt.Errorf("brief readonly field has invalid boolean value %q", s)
	}
}

// IsBriefReadOnly reports whether text carries a valid brief readonly field evaluating to true.
// Any error or absent field returns false.
func IsBriefReadOnly(text string) bool {
	val, err := ExtractBriefReadOnly(text)
	return err == nil && val
}

// MaxBriefClaimIssues bounds the issues one brief may name.
const MaxBriefClaimIssues = 16

var claimFieldRe = regexp.MustCompile(`^(issue|session):[ \t]*(.*?)\s*$`)

// BriefClaim is what a brief says about the forge issues it works on: the raw issue tokens
// of its `issue:` lines and the raw session of its `session:` line.
type BriefClaim struct {
	Issues  []string
	Session string
}

// ExtractBriefClaim reads the `issue:` and `session:` fields of a brief with the scanner and
// field grammar ExtractBriefTask uses, so fenced code and caveman:off regions are skipped. Caveman
// only extracts the raw field tokens; syntax and validation belong to the forge claim parser.
// A brief without either field returns the zero BriefClaim.
func ExtractBriefClaim(text string) (BriefClaim, error) {
	lines, _ := scan(text)
	var claim BriefClaim
	sessions := 0
	for _, line := range lines {
		if !shapeContent(line) {
			continue
		}
		match := claimFieldRe.FindStringSubmatch(maskQuoted(proseOf(line)))
		if len(match) != 3 {
			continue
		}
		if match[1] == "session" {
			sessions++
			claim.Session = strings.TrimSpace(match[2])
			continue
		}
		if err := claim.addIssueTokens(match[2]); err != nil {
			return BriefClaim{}, err
		}
	}
	if len(claim.Issues) == 0 {
		return BriefClaim{}, nil
	}
	if sessions > 1 {
		return BriefClaim{}, errors.New("brief session field is duplicated")
	}
	return claim, nil
}

func (c *BriefClaim) addIssueTokens(value string) error {
	tokens := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	if len(tokens) == 0 {
		return errors.New("brief issue field must name an issue as <owner>/<repo>#<number>")
	}
	for _, token := range tokens {
		if len(c.Issues) >= MaxBriefClaimIssues {
			return errors.New("brief names more issues than the limit")
		}
		c.Issues = append(c.Issues, token)
	}
	return nil
}

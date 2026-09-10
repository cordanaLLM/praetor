package forge

import (
	"errors"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Invariant bounds adhering to HISS-02.
const (
	MaxPRLinesLimit   = 3000
	MaxPathsLimit     = 1000
	StandardReviewBot = "cordana-standards[bot]"
)

// PRChecklistResult encapsulates validation of the mandatory PR checklist.
type PRChecklistResult struct {
	Valid           bool     `json:"valid"`
	HasHISS16Check  bool     `json:"has_hiss16_check"`
	Has3DTestsCheck bool     `json:"has_3d_tests_check"`
	HasReceipt      bool     `json:"has_receipt"`
	ReceiptProof    string   `json:"receipt_proof,omitempty"`
	Errors          []string `json:"errors,omitempty"`
}

// ReviewerAssignment contains mapped human owners and review automation bots.
type ReviewerAssignment struct {
	HumanReviewers []string `json:"human_reviewers"`
	BotReviewers   []string `json:"bot_reviewers"`
}

// CommitAnalysis details HISS-14 breaking change invariants on commit messages.
type CommitAnalysis struct {
	IsBreaking           bool     `json:"is_breaking"`
	HasBreakingIndicator bool     `json:"has_breaking_indicator"`
	HasMigrationFooter   bool     `json:"has_migration_footer"`
	MigrationText        string   `json:"migration_text,omitempty"`
	Valid                bool     `json:"valid"`
	Errors               []string `json:"errors,omitempty"`
}

var (
	checkedBoxRegex     = regexp.MustCompile(`(?i)\[[xX]\]`)
	breakingHeaderRegex = regexp.MustCompile(`(?i)^[a-z]+(\([^\)]+\))?!:\s*.+`)
	migrationRegex      = regexp.MustCompile(`(?i)(?:\r?\n|^)Migration:\s*([\s\S]+)`)
)

// ValidatePRChecklist verifies that a PR body fulfills HISS-16, 3D tests, and receipt requirements.
func ValidatePRChecklist(prBody string) (*PRChecklistResult, error) {
	if strings.TrimSpace(prBody) == "" {
		return &PRChecklistResult{
			Valid:  false,
			Errors: []string{"PR body is empty; checklist required"},
		}, errors.New("empty PR body")
	}

	lines := strings.Split(prBody, "\n")
	res := &PRChecklistResult{Valid: true}
	var inReceiptBlock bool
	var receiptBuilder strings.Builder

	for i := 0; i < len(lines) && i < MaxPRLinesLimit; i++ {
		line := strings.TrimSpace(lines[i])
		checkLineForRequirements(line, res)

		if strings.HasPrefix(line, "```") {
			inReceiptBlock = !inReceiptBlock
			continue
		}
		if inReceiptBlock && strings.TrimSpace(line) != "" {
			receiptBuilder.WriteString(line + "\n")
		}
	}

	res.ReceiptProof = strings.TrimSpace(receiptBuilder.String())
	if res.ReceiptProof != "" || strings.Contains(prBody, "Receipt Signature") || strings.Contains(prBody, "Exit-0 Receipt") {
		res.HasReceipt = true
	}

	finalizeChecklistValidation(res)
	return res, nil
}

func checkLineForRequirements(line string, res *PRChecklistResult) {
	if checkedBoxRegex.MatchString(line) {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "hiss-16") || strings.Contains(lower, "compile-context") {
			res.HasHISS16Check = true
		}
		if strings.Contains(lower, "3d test") || strings.Contains(lower, "hiss-15") ||
			(strings.Contains(lower, "positive") && strings.Contains(lower, "negative")) {
			res.Has3DTestsCheck = true
		}
	}
}

func finalizeChecklistValidation(res *PRChecklistResult) {
	if !res.HasHISS16Check {
		res.Errors = append(res.Errors, "missing mandatory HISS-16 Context Integrity verification check")
	}
	if !res.Has3DTestsCheck {
		res.Errors = append(res.Errors, "missing mandatory HISS-15 3D Testing verification check")
	}
	if !res.HasReceipt {
		res.Errors = append(res.Errors, "missing mandatory Ed25519 Exit-0 Verification Receipt")
	}
	res.Valid = len(res.Errors) == 0
}

// AssignReviewers maps touched file paths to owners via CODEOWNERS rules and attaches bots.
func AssignReviewers(touchedPaths []string, codeownersContent string) (*ReviewerAssignment, error) {
	rules := parseCodeowners(codeownersContent)
	assigned := make(map[string]struct{})

	for i := 0; i < len(touchedPaths) && i < MaxPathsLimit; i++ {
		path := touchedPaths[i]
		for _, rule := range rules {
			if matchPattern(rule.pattern, path) {
				for _, o := range rule.owners {
					assigned[o] = struct{}{}
				}
			}
		}
	}

	humanList := make([]string, 0, len(assigned))
	for owner := range assigned {
		humanList = append(humanList, owner)
	}
	sort.Strings(humanList)

	return &ReviewerAssignment{
		HumanReviewers: humanList,
		BotReviewers:   []string{StandardReviewBot},
	}, nil
}

type codeownerRule struct {
	pattern string
	owners  []string
}

func parseCodeowners(content string) []codeownerRule {
	lines := strings.Split(content, "\n")
	var rules []codeownerRule

	for i := 0; i < len(lines) && i < MaxPRLinesLimit; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			rules = append(rules, codeownerRule{
				pattern: parts[0],
				owners:  parts[1:],
			})
		}
	}
	return rules
}

func matchPattern(pattern, path string) bool {
	cleanPat := strings.TrimPrefix(pattern, "/")
	cleanPath := strings.TrimPrefix(path, "/")

	if cleanPat == "*" || cleanPat == cleanPath {
		return true
	}
	if strings.HasSuffix(cleanPat, "/*") {
		prefix := strings.TrimSuffix(cleanPat, "/*")
		return strings.HasPrefix(cleanPath, prefix)
	}
	matched, err := filepath.Match(cleanPat, cleanPath)
	return err == nil && matched
}

// AnalyzeCommit checks conventional commits for breaking indicators and mandatory HISS-14 footers.
func AnalyzeCommit(message string) (*CommitAnalysis, error) {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return nil, errors.New("empty commit message")
	}

	lines := strings.Split(trimmed, "\n")
	header := lines[0]

	res := &CommitAnalysis{Valid: true}
	if breakingHeaderRegex.MatchString(header) {
		res.IsBreaking = true
		res.HasBreakingIndicator = true
	} else if strings.Contains(trimmed, "BREAKING CHANGE:") {
		res.IsBreaking = true
	}

	if res.IsBreaking {
		if m := migrationRegex.FindStringSubmatch(trimmed); len(m) > 1 {
			res.HasMigrationFooter = true
			res.MigrationText = strings.TrimSpace(m[1])
		} else {
			res.Errors = append(res.Errors, "HISS-14 violation: breaking change commit requires mandatory 'Migration:' footer")
		}
	}

	res.Valid = len(res.Errors) == 0
	return res, nil
}

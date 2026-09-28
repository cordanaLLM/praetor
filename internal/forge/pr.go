package forge

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/lockdown"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Invariant bounds adhering to HISS-02.
const (
	MaxPRLinesLimit = 3000
	MaxPathsLimit   = 1000
	MaxPathSegments = 64
	// MaxBotReviewers bounds the bot accounts one assignment requests (HISS-02).
	MaxBotReviewers = 16
	// ReceiptFenceToken is the info-string token that marks the fenced block carrying the
	// Ed25519 Exit-0 receipt, e.g. "```receipt", "```json receipt" or "~~~receipt".
	ReceiptFenceToken = "receipt"
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

// ReceiptPolicy binds the Ed25519 Exit-0 receipt carried in a PR body to a trust anchor.
// PinnedKey is the repository's pinned receipt.public_key; when it is empty the receipt is
// only checked for internal consistency, which is strictly weaker and must not be used by
// a merge gate. HeadSHA, when set, is the commit the receipt has to certify.
type ReceiptPolicy struct {
	PinnedKey ed25519.PublicKey
	HeadSHA   string
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
	// checkedBoxRegex matches a ticked GitHub task-list item: a bullet or ordered list
	// marker, then "[x]" followed by whitespace or the end of the line. A "[x]" quoted
	// mid-sentence is prose, not a ticked box.
	checkedBoxRegex     = regexp.MustCompile(`^(?:[-*+]|\d{1,9}[.)])[ \t]+\[[xX]\](?:[ \t]|$)`)
	breakingHeaderRegex = regexp.MustCompile(`(?i)^[a-z]+(\([^\)]+\))?!:\s*.+`)
	// breakingFooterRegex matches both Conventional Commits 1.0.0 spellings of the
	// breaking-change footer token, which the specification declares synonymous.
	breakingFooterRegex = regexp.MustCompile(`(?m)^BREAKING[ -]CHANGE:`)
	migrationRegex      = regexp.MustCompile(`(?i)(?:\r?\n|^)Migration:\s*([\s\S]+)`)
)

// ValidatePRChecklist verifies that a PR body fulfills HISS-16, 3D tests, and receipt
// requirements. The receipt is verified for internal Ed25519 consistency only; a merge
// gate must use ValidatePRChecklistWithPolicy with the repository's pinned public key.
func ValidatePRChecklist(prBody string) (*PRChecklistResult, error) {
	return ValidatePRChecklistWithPolicy(prBody, ReceiptPolicy{})
}

// ValidatePRChecklistWithPolicy verifies the checklist and cryptographically verifies the
// Ed25519 Exit-0 receipt block against the supplied trust anchor.
func ValidatePRChecklistWithPolicy(prBody string, policy ReceiptPolicy) (*PRChecklistResult, error) {
	if strings.TrimSpace(prBody) == "" {
		return &PRChecklistResult{
			Valid:  false,
			Errors: []string{"PR body is empty; checklist required"},
		}, errors.New("empty PR body")
	}

	lines := strings.Split(prBody, "\n")
	if len(lines) > MaxPRLinesLimit {
		lines = lines[:MaxPRLinesLimit]
	}

	res := &PRChecklistResult{}
	scanChecklistLines(lines, res)
	receipt, err := extractReceiptBlock(lines)
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
	} else {
		applyReceiptVerification(res, receipt, policy)
	}
	finalizeChecklistValidation(res)
	return res, nil
}

// scanChecklistLines reads the ticked boxes outside fenced code. A box quoted inside a
// ``` or ~~~ example renders as code, not as a checkbox, so it never satisfies a
// requirement. A body that ends inside a fence is reported: everything after the opening
// delimiter renders as code, so a box the contributor ticked there is silently unread.
func scanChecklistLines(lines []string, res *PRChecklistResult) {
	fence := util.MarkdownFence{}
	opened := 0
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		wasOpen := fence.Open()
		if fence.Inside(trimmed) {
			if !wasOpen {
				opened = i + 1
			}
			continue
		}
		checkLineForRequirements(trimmed, res)
	}
	if fence.Open() {
		res.Errors = append(res.Errors, fmt.Sprintf(
			"PR body ends inside the %q code fence opened at line %d: close it, or every checklist box after it is read as code",
			fence.Marker(), opened))
	}
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

// isReceiptFence reports whether a fence line opens the receipt block, i.e. carries the
// "receipt" token in its info string. An unlabelled code block is never a receipt.
func isReceiptFence(line string) bool {
	info := strings.ToLower(strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "`~")))
	return slices.Contains(strings.Fields(info), ReceiptFenceToken)
}

// extractReceiptBlock returns the verbatim content of the first fenced block, backtick or
// tilde, labelled as a receipt. Unlabelled blocks are ignored: a code snippet in a
// description is not a receipt, and a "```receipt" line quoted inside another fence is that
// fence's content. A receipt fence that is never closed is an error rather than a receipt
// running to the end of the body.
func extractReceiptBlock(lines []string) (string, error) {
	var sb strings.Builder
	fence := util.MarkdownFence{}
	inReceipt := false
	for i := 0; i < len(lines); i++ {
		wasOpen := fence.Open()
		if !fence.Inside(strings.TrimSpace(lines[i])) {
			continue
		}
		switch {
		case !wasOpen:
			inReceipt = isReceiptFence(lines[i])
		case !fence.Open() && inReceipt:
			return strings.TrimSpace(sb.String()), nil
		case inReceipt:
			sb.WriteString(lines[i])
			sb.WriteString("\n")
		}
	}
	if inReceipt {
		return "", fmt.Errorf("the Ed25519 Exit-0 receipt fence %q is never closed: end the receipt block with a matching fence", fence.Marker())
	}
	return "", nil
}

// verifyReceiptEnvelope verifies the signature, the certified gate output, its format version
// and that it records a clean scanned tree (lockdown.VerifyPinnedReceiptFile), and the commit
// binding of a receipt carried in a PR body.
func verifyReceiptEnvelope(rf *lockdown.ReceiptFile, policy ReceiptPolicy) error {
	var err error
	if len(policy.PinnedKey) > 0 {
		err = lockdown.VerifyPinnedReceiptFile(rf, policy.PinnedKey)
	} else {
		err = lockdown.VerifyUnpinnedReceiptFile(rf)
	}
	if err != nil {
		return err
	}
	if policy.HeadSHA != "" && !strings.EqualFold(strings.TrimSpace(rf.CommitSHA), strings.TrimSpace(policy.HeadSHA)) {
		return fmt.Errorf("receipt certifies commit %q but the pull request head is %q", rf.CommitSHA, policy.HeadSHA)
	}
	return nil
}

// applyReceiptVerification parses and verifies the receipt block, recording a precise
// reason whenever the receipt requirement is not met.
func applyReceiptVerification(res *PRChecklistResult, raw string, policy ReceiptPolicy) {
	if raw == "" {
		res.Errors = append(res.Errors,
			"missing mandatory Ed25519 Exit-0 receipt: expected a fenced ```receipt block containing the signed receipt JSON")
		return
	}
	var rf lockdown.ReceiptFile
	if err := json.Unmarshal([]byte(raw), &rf); err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("Ed25519 Exit-0 receipt block is not valid receipt JSON: %v", err))
		return
	}
	if err := verifyReceiptEnvelope(&rf, policy); err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("Ed25519 Exit-0 receipt verification failed: %v", err))
		return
	}
	res.HasReceipt = true
	res.ReceiptProof = fmt.Sprintf("command=%q commit=%s output_sha256=%s", rf.Command, rf.CommitSHA, rf.OutputHash)
}

func finalizeChecklistValidation(res *PRChecklistResult) {
	if !res.HasHISS16Check {
		res.Errors = append(res.Errors, "missing mandatory HISS-16 Context Integrity verification check")
	}
	if !res.Has3DTestsCheck {
		res.Errors = append(res.Errors, "missing mandatory HISS-15 3D Testing verification check")
	}
	res.Valid = len(res.Errors) == 0
}

// AssignReviewers maps touched file paths to owners via CODEOWNERS rules and attaches bots,
// the operator's bot reviewers (forge.review_bot; nil when unset requests none). As on
// GitHub, the last matching rule for a path wins; earlier matches are discarded. More than
// MaxBotReviewers bots, or an empty bot name, is an error: no bot is requested that the
// caller did not name.
func AssignReviewers(touchedPaths []string, codeownersContent string, bots []string) (*ReviewerAssignment, error) {
	botList, err := botReviewers(bots)
	if err != nil {
		return nil, err
	}
	rules := parseCodeowners(codeownersContent)
	assigned := make(map[string]struct{})

	for i := 0; i < len(touchedPaths) && i < MaxPathsLimit; i++ {
		owners := ownersForPath(rules, touchedPaths[i])
		for _, o := range owners {
			assigned[o] = struct{}{}
		}
	}

	humanList := make([]string, 0, len(assigned))
	for owner := range assigned {
		humanList = append(humanList, owner)
	}
	sort.Strings(humanList)

	return &ReviewerAssignment{
		HumanReviewers: humanList,
		BotReviewers:   botList,
	}, nil
}

// botReviewers copies the requested bot accounts, refusing an empty name and more than
// MaxBotReviewers entries. No bots is an empty list, never a built-in account.
func botReviewers(bots []string) ([]string, error) {
	if len(bots) > MaxBotReviewers {
		return nil, fmt.Errorf("%d bot reviewers requested; at most %d are allowed", len(bots), MaxBotReviewers)
	}
	list := make([]string, 0, len(bots))
	for i := 0; i < len(bots) && i < MaxBotReviewers; i++ {
		name := strings.TrimSpace(bots[i])
		if name == "" {
			return nil, fmt.Errorf("bot reviewer %d is empty", i+1)
		}
		list = append(list, name)
	}
	return list, nil
}

// ownersForPath returns the owners of the last CODEOWNERS rule matching the path.
func ownersForPath(rules []codeownerRule, filePath string) []string {
	var owners []string
	for i := 0; i < len(rules); i++ {
		if matchPattern(rules[i].pattern, filePath) {
			owners = rules[i].owners
		}
	}
	return owners
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

// matchPattern reports whether a CODEOWNERS pattern matches a repository-relative path.
// It follows gitignore semantics as GitHub does: a pattern containing an interior or
// leading '/' is anchored at the repository root, a pattern without one matches at any
// depth, a trailing '/' owns a directory subtree, '**' spans directories, and '*' never
// crosses a path separator.
func matchPattern(pattern, filePath string) bool {
	pat := strings.TrimSpace(pattern)
	cleanPath := strings.TrimPrefix(strings.TrimSpace(filePath), "/")
	if pat == "" || cleanPath == "" {
		return false
	}
	if pat == "*" || pat == "**" {
		return true
	}

	dirRule := strings.HasSuffix(pat, "/")
	anchored := strings.HasPrefix(pat, "/") || strings.Contains(strings.TrimSuffix(pat, "/"), "/")
	patSegs := strings.Split(strings.Trim(pat, "/"), "/")
	pathSegs := strings.Split(cleanPath, "/")
	if len(pathSegs) > MaxPathSegments || len(patSegs) > MaxPathSegments {
		return false
	}

	if anchored {
		return matchFrom(patSegs, pathSegs, dirRule)
	}
	for start := 0; start < len(pathSegs); start++ {
		if matchFrom(patSegs, pathSegs[start:], dirRule) {
			return true
		}
	}
	return false
}

// matchFrom matches the pattern segments against the path segments: a directory rule has
// to match a proper prefix of the path (everything below the directory is owned), a file
// rule has to match the path in full.
func matchFrom(patSegs, pathSegs []string, dirRule bool) bool {
	if !dirRule {
		return util.MatchGlobSegments(patSegs, pathSegs)
	}
	for k := 1; k < len(pathSegs); k++ {
		if util.MatchGlobSegments(patSegs, pathSegs[:k]) {
			return true
		}
	}
	return false
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
	} else if breakingFooterRegex.MatchString(trimmed) {
		res.IsBreaking = true
		res.HasBreakingIndicator = true
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

package clientsetup

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net"
	"path"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/httpendpoint"
	"github.com/cordanaLLM/praetor/internal/util"
)

// AGYPermissionsPlan describes one in-memory merge of declared grants into
// Antigravity CLI settings. Content may contain unrelated private settings and
// is therefore excluded from serialized plan metadata.
type AGYPermissionsPlan struct {
	Managed      bool     `json:"managed"`
	BeforeCount  int      `json:"before_count"`
	AfterCount   int      `json:"after_count"`
	Added        []string `json:"added"`
	Changed      bool     `json:"changed"`
	SourceSHA256 string   `json:"source_sha256"`
	Content      []byte   `json:"-"`
}

// PlanAGYPermissions appends exact missing allow rules while retaining all
// unrelated settings. It performs no filesystem I/O.
func PlanAGYPermissions(ctx context.Context, existing []byte, permissions config.ClientPermissions) (*AGYPermissionsPlan, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if len(existing) > MaxConfigBytes {
		return nil, errors.New("existing AGY settings exceed 1 MiB")
	}
	plan := &AGYPermissionsPlan{
		Managed:      permissions.Manage,
		Added:        []string{},
		SourceSHA256: digest(existing),
		Content:      bytes.Clone(existing),
	}
	if !permissions.Manage {
		return plan, nil
	}
	if err := validateAGYDeclaredRules(permissions.Allow); err != nil {
		return nil, err
	}
	root, permissionFields, current, err := decodeAGYPermissionSettings(ctx, existing)
	if err != nil {
		return nil, err
	}
	if err := validateAGYPermissionPrecedence(permissions.Allow, current); err != nil {
		return nil, err
	}
	plan.BeforeCount = len(current.allow)
	current.allow, plan.Added = appendMissingAGYRules(current.allow, permissions.Allow)
	plan.AfterCount = len(current.allow)
	if len(plan.Added) == 0 {
		return plan, nil
	}
	plan.Content, err = encodeAGYPermissionSettings(root, permissionFields, current.allow)
	if err != nil {
		return nil, err
	}
	if len(plan.Content) > MaxOutputBytes {
		return nil, errors.New("AGY settings candidate exceeds 2 MiB")
	}
	plan.Changed = true
	return plan, checkContext(ctx)
}

type agyPermissionLists struct {
	allow []string
	ask   []string
	deny  []string
}

func decodeAGYPermissionSettings(ctx context.Context, existing []byte) (map[string]jsontext.Value, map[string]jsontext.Value, agyPermissionLists, error) {
	root := make(map[string]jsontext.Value)
	if len(existing) != 0 {
		if err := validateJSON(ctx, existing); err != nil {
			return nil, nil, agyPermissionLists{}, err
		}
		var err error
		if root, err = jsonObject(existing); err != nil {
			return nil, nil, agyPermissionLists{}, err
		}
	}
	fields := make(map[string]jsontext.Value)
	if raw, ok := root["permissions"]; ok {
		var err error
		if fields, err = jsonObject(raw); err != nil {
			return nil, nil, agyPermissionLists{}, err
		}
	}
	current := agyPermissionLists{}
	var err error
	current.allow, err = agyPermissionRules(fields["allow"], "allow")
	if err != nil {
		return nil, nil, agyPermissionLists{}, err
	}
	if current.ask, err = agyPermissionRules(fields["ask"], "ask"); err != nil {
		return nil, nil, agyPermissionLists{}, err
	}
	if current.deny, err = agyPermissionRules(fields["deny"], "deny"); err != nil {
		return nil, nil, agyPermissionLists{}, err
	}
	return root, fields, current, nil
}

func validateAGYPermissionPrecedence(declared []string, current agyPermissionLists) error {
	for i := 0; i < len(declared) && i < config.MaxPermissionGrants; i++ {
		if blocking, overlap := firstAGYOverlap(current.deny, declared[i], true); overlap != agyNoOverlap {
			return agyOverlapError("deny", blocking, declared[i], overlap)
		}
		if blocking, overlap := firstAGYOverlap(current.ask, declared[i], false); overlap != agyNoOverlap {
			return agyOverlapError("ask", blocking, declared[i], overlap)
		}
	}
	return nil
}

type agyOverlap uint8

const (
	agyNoOverlap agyOverlap = iota
	agyDefiniteOverlap
	agyUnprovableRegexOverlap
	agyUnprovableWorkspacePathOverlap
	agyUnprovableURLOverlap
)

func firstAGYOverlap(rules []string, declared string, deny bool) (string, agyOverlap) {
	for i := 0; i < len(rules) && i < MaxConfigBytes; i++ {
		if overlap := agyRulesOverlap(rules[i], declared, deny); overlap != agyNoOverlap {
			return rules[i], overlap
		}
	}
	return "", agyNoOverlap
}

func agyRulesOverlap(blocking, declared string, deny bool) agyOverlap {
	if blocking == declared {
		return agyDefiniteOverlap
	}
	blockingAction, blockingTarget, blockingOK := splitAGYPermissionRule(blocking)
	declaredAction, declaredTarget, declaredOK := splitAGYPermissionRule(declared)
	if !blockingOK || !declaredOK {
		return agyNoOverlap
	}
	relation := agyActionOverlap(blockingAction, declaredAction, deny)
	if relation == "" {
		return agyNoOverlap
	}
	return agyRuleTargetsOverlap(relation, blockingAction, blockingTarget, declaredTarget)
}

func agyRuleTargetsOverlap(relation, action, blocking, declared string) agyOverlap {
	if blocking == "*" || declared == "*" {
		return agyDefiniteOverlap
	}
	if overlap := agyUnprovableOverlap(relation, action, blocking, declared); overlap != agyNoOverlap {
		return overlap
	}
	if agyTargetsOverlap(action, blocking, declared) {
		return agyDefiniteOverlap
	}
	return agyNoOverlap
}

// agyUnprovableOverlap names the pairs whose overlap depends on runtime behavior this
// adapter cannot model, so the caller fails closed instead of assuming disjointness.
func agyUnprovableOverlap(relation, action, blocking, declared string) agyOverlap {
	switch {
	case relation == "same" && (agyRegexTarget(blocking) || agyRegexTarget(declared)):
		return agyUnprovableRegexOverlap
	case agyFileAction(action) && agyPathRootingDiffers(blocking, declared):
		return agyUnprovableWorkspacePathOverlap
	case agyURLAction(action) && !httpendpoint.CanonicalHost(normalizeAGYDomain(blocking)):
		// Declared URL targets are validated canonical hosts; an operator rule whose
		// host cannot be reduced to one (empty, wildcard, backslash, non-canonical IP)
		// could still match at runtime.
		return agyUnprovableURLOverlap
	default:
		return agyNoOverlap
	}
}

func agyURLAction(action string) bool {
	return action == "read_url" || action == "execute_url"
}

func agyFileAction(action string) bool {
	return action == "read_file" || action == "write_file"
}

func agyPathRootingDiffers(left, right string) bool {
	left, right = normalizeAGYPathTarget(left), normalizeAGYPathTarget(right)
	return left != "" && right != "" && path.IsAbs(left) != path.IsAbs(right)
}

func agyActionOverlap(blocking, declared string, deny bool) string {
	if blocking == declared {
		return "same"
	}
	if deny && blocking == "read_file" && declared == "write_file" {
		return "implicit-read-deny"
	}
	return ""
}

func splitAGYPermissionRule(rule string) (string, string, bool) {
	open := strings.IndexByte(rule, '(')
	if open < 1 || rule[len(rule)-1] != ')' || open == len(rule)-2 {
		return "", "", false
	}
	return rule[:open], rule[open+1 : len(rule)-1], true
}

func agyRegexTarget(target string) bool {
	return strings.HasPrefix(target, "regex:")
}

func agyTargetsOverlap(action, blocking, declared string) bool {
	switch action {
	case "command":
		return commandLiteralPrefix(blocking, declared) || commandLiteralPrefix(declared, blocking)
	case "read_file", "write_file":
		return agyPathTargetsOverlap(blocking, declared)
	case "read_url", "execute_url":
		return agyDomainTargetsOverlap(blocking, declared)
	case "mcp":
		return agyMCPTargetsOverlap(blocking, declared)
	default:
		return false
	}
}

func commandLiteralPrefix(prefix, candidate string) bool {
	prefixWords, candidateWords := strings.Fields(prefix), strings.Fields(candidate)
	if len(prefixWords) == 0 || len(prefixWords) > len(candidateWords) {
		return false
	}
	for i := 0; i < len(prefixWords) && i < maxAGYPermissionRuleBytes; i++ {
		if prefixWords[i] != candidateWords[i] {
			return false
		}
	}
	return true
}

func agyPathTargetsOverlap(left, right string) bool {
	left, right = normalizeAGYPathTarget(left), normalizeAGYPathTarget(right)
	if left == "" || right == "" || path.IsAbs(left) != path.IsAbs(right) {
		return false
	}
	return agyPathContains(left, right) || agyPathContains(right, left)
}

func normalizeAGYPathTarget(target string) string {
	target = strings.ReplaceAll(target, `\`, "/")
	if len(target) >= 2 && target[1] == ':' {
		target = target[2:]
	}
	return path.Clean(target)
}

func agyPathContains(parent, candidate string) bool {
	if parent == candidate || parent == "/" && path.IsAbs(candidate) {
		return true
	}
	if parent == "." && !path.IsAbs(candidate) {
		return true
	}
	return strings.HasPrefix(candidate, strings.TrimSuffix(parent, "/")+"/")
}

func agyDomainTargetsOverlap(left, right string) bool {
	left, right = normalizeAGYDomain(left), normalizeAGYDomain(right)
	if left == "" || right == "" {
		return false
	}
	return left == right || strings.HasSuffix(left, "."+right) || strings.HasSuffix(right, "."+left)
}

// normalizeAGYDomain reduces an existing operator URL rule to its host. Declared rules
// are already bare canonical hosts; operator-owned deny/ask rules may carry a scheme,
// userinfo, port, brackets or path, and each must still be compared by host so that
// an overlapping higher-precedence rule cannot hide behind a different spelling. A
// backslash yields "": WHATWG URL parsing treats it as a path separator for special
// schemes while RFC 3986 rejects it, so the host AGY would match is ambiguous.
func normalizeAGYDomain(target string) string {
	target = strings.ToLower(strings.TrimSpace(target))
	if strings.ContainsRune(target, '\\') {
		return ""
	}
	if _, rest, ok := strings.Cut(target, "://"); ok {
		target = rest
	}
	if end := strings.IndexAny(target, "/?#"); end >= 0 {
		target = target[:end]
	}
	if at := strings.LastIndexByte(target, '@'); at >= 0 {
		target = target[at+1:]
	}
	if host, _, err := net.SplitHostPort(target); err == nil {
		target = host
	} else if len(target) > 1 && target[0] == '[' && target[len(target)-1] == ']' {
		target = target[1 : len(target)-1]
	}
	return strings.TrimSuffix(target, ".")
}

func agyMCPTargetsOverlap(left, right string) bool {
	leftServer, leftTool, leftOK := strings.Cut(left, "/")
	rightServer, rightTool, rightOK := strings.Cut(right, "/")
	if !leftOK || !rightOK || leftServer != rightServer {
		return false
	}
	return leftTool == rightTool || leftTool == "*" || rightTool == "*"
}

func agyOverlapError(list, blocking, declared string, overlap agyOverlap) error {
	if overlap == agyUnprovableRegexOverlap {
		return fmt.Errorf("cannot prove non-overlap between declared AGY allow rule %q and permissions.%s regex rule %q; regex equivalence is not implemented",
			declared, list, blocking)
	}
	if overlap == agyUnprovableWorkspacePathOverlap {
		return fmt.Errorf("cannot prove non-overlap between declared AGY allow rule %q and permissions.%s file rule %q; absolute and workspace-relative paths require runtime workspace resolution",
			declared, list, blocking)
	}
	if overlap == agyUnprovableURLOverlap {
		return fmt.Errorf("cannot prove non-overlap between declared AGY allow rule %q and permissions.%s URL rule %q; its target does not reduce to a canonical host",
			declared, list, blocking)
	}
	return agyShadowError(list, blocking, declared)
}

func agyShadowError(list, blocking, declared string) error {
	return fmt.Errorf("declared AGY allow rule %q overlaps permissions.%s rule %q (AGY precedence: deny > ask > allow)",
		declared, list, blocking)
}

func appendMissingAGYRules(existing, declared []string) ([]string, []string) {
	seen := make(map[string]bool, len(existing)+len(declared))
	for _, rule := range existing {
		seen[rule] = true
	}
	added := []string{}
	for _, rule := range declared {
		if !seen[rule] {
			existing = append(existing, rule)
			added = append(added, rule)
			seen[rule] = true
		}
	}
	return existing, added
}

func encodeAGYPermissionSettings(root, fields map[string]jsontext.Value, allow []string) ([]byte, error) {
	encoded, err := marshalJSON(allow)
	if err != nil {
		return nil, err
	}
	fields["allow"] = encoded
	if root["permissions"], err = marshalJSON(fields); err != nil {
		return nil, err
	}
	return marshalJSON(root)
}

const maxAGYPermissionRuleBytes = 512

func validateAGYDeclaredRules(rules []string) error {
	if len(rules) > config.MaxPermissionGrants {
		return fmt.Errorf("AGY declared permissions exceed %d grants", config.MaxPermissionGrants)
	}
	for i, rule := range rules {
		if !agyPermissionRule(rule) {
			return fmt.Errorf("AGY declared permission %d must use a supported action(target) rule", i)
		}
	}
	return nil
}

func agyPermissionRule(rule string) bool {
	if len(rule) == 0 || !util.LiteralString(rule, maxAGYPermissionRuleBytes) {
		return false
	}
	action, target, ok := splitAGYPermissionRule(rule)
	if !ok {
		return false
	}
	switch action {
	case "read_file", "write_file":
		return target == "*" || !strings.ContainsRune(target, '*')
	case "read_url", "execute_url":
		// AGY matches URL rules by hostname and subdomain. A scheme, port, path or
		// userinfo is not documented grammar and would escape the overlap check.
		return target == "*" || httpendpoint.CanonicalHost(target)
	case "command":
		return agyCommandPermissionTarget(target)
	case "mcp":
		return agyMCPPermissionTarget(target)
	default:
		return false
	}
}

func agyCommandPermissionTarget(target string) bool {
	if target == "*" || !strings.HasPrefix(target, "regex:") {
		return true
	}
	tokens := strings.Fields(strings.TrimPrefix(target, "regex:"))
	if len(tokens) == 0 {
		return false
	}
	for _, token := range tokens {
		// Validate the operator-supplied token before adding AGY's documented anchors.
		// Otherwise an unmatched closing group at the front and an unmatched opening
		// group at the end can borrow this wrapper's parentheses and become valid.
		if _, err := regexp.Compile(token); err != nil {
			return false
		}
		if _, err := regexp.Compile("^(?:" + token + ")$"); err != nil {
			return false
		}
	}
	return true
}

func agyMCPPermissionTarget(target string) bool {
	if target == "*" {
		return true
	}
	server, tool, ok := strings.Cut(target, "/")
	if !ok || server == "" || tool == "" || strings.ContainsRune(server, '*') || strings.ContainsRune(server, '/') || strings.ContainsRune(tool, '/') {
		return false
	}
	return tool == "*" || !strings.ContainsRune(tool, '*')
}

func agyPermissionRules(raw []byte, key string) ([]string, error) {
	if len(raw) == 0 {
		return []string{}, nil
	}
	var rules []string
	if err := json.Unmarshal(raw, &rules); err != nil || rules == nil {
		return nil, fmt.Errorf("AGY permissions.%s must be an array of strings", key)
	}
	return rules, nil
}

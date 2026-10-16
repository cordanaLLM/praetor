package router

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode"
)

// MaxIdentityTools bounds the tool set one run identity describes. A record keeps the tool-set
// digest and count, so the bound is on the input list, not on the record size.
const MaxIdentityTools = 4096

const toolSetDigestPrefix = "sha256:"

// RunIdentity records the provenance, execution harness, and configuration digests of a run.
// The tool set is identified by ToolSetDigest and ToolCount; ToolSet, the full list, is optional
// and must match both when present. CostEstimate is nil when no estimate was made before
// dispatch, which is never the same as an estimate of zero.
type RunIdentity struct {
	PhysicalModel  string   `json:"physical_model"`
	Harness        string   `json:"harness"`
	HarnessVersion string   `json:"harness_version"`
	PromptDigest   string   `json:"prompt_digest"`
	ContextDigest  string   `json:"context_digest"`
	ContextBytes   int64    `json:"context_bytes"`
	ToolSetDigest  string   `json:"tool_set_digest,omitempty"`
	ToolCount      int      `json:"tool_count,omitempty"`
	ToolSet        []string `json:"tool_set,omitempty"`
	PriorRounds    int      `json:"prior_rounds,omitempty"`
	Retries        int      `json:"retries,omitempty"`
	CostEstimate   *float64 `json:"cost_estimate,omitempty"`
}

// Key computes a deterministic cryptographic hash identifying this exact run configuration.
// Two runs differing only in their prompt template (or brief) digest yield distinct keys.
// Per-attempt values (retries, prior rounds, cost estimate, context bytes) are excluded.
func (id RunIdentity) Key() string {
	h := sha256.New()
	h.Write([]byte("v2\x00"))
	for _, field := range []string{id.PhysicalModel, id.Harness, id.HarnessVersion, id.PromptDigest, id.ContextDigest, id.toolSetDigest()} {
		h.Write([]byte(field))
		h.Write([]byte{0})
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil))
}

// toolSetDigest returns the recorded tool-set digest, or the digest of the list when only the
// list is set, so a key never ignores the tools.
func (id RunIdentity) toolSetDigest() string {
	if id.ToolSetDigest != "" || len(id.ToolSet) == 0 {
		return id.ToolSetDigest
	}
	digest, _, err := DigestToolSet(id.ToolSet)
	if err != nil {
		return ""
	}
	return digest
}

// DigestToolSet validates a tool set and returns its digest and count: SHA-256 over the sorted
// names, one per line, so the order a harness lists its tools in does not change the identity.
// An empty set has no digest and a count of zero.
func DigestToolSet(tools []string) (string, int, error) {
	if err := validateIdentityTools(tools); err != nil {
		return "", 0, err
	}
	if len(tools) == 0 {
		return "", 0, nil
	}
	sorted := slices.Clone(tools)
	slices.Sort(sorted)
	h := sha256.New()
	for i := 0; i < len(sorted); i++ {
		h.Write([]byte(sorted[i]))
		h.Write([]byte{'\n'})
	}
	return fmt.Sprintf("%s%x", toolSetDigestPrefix, h.Sum(nil)), len(sorted), nil
}

// ValidateRunIdentity verifies that all mandatory identity fields are present and valid.
func ValidateRunIdentity(id RunIdentity) error {
	if err := validateIdentityNames(id); err != nil {
		return err
	}
	if err := validateIdentityDigests(id); err != nil {
		return err
	}
	if err := validateIdentityToolSet(id); err != nil {
		return err
	}
	return validateIdentityMetrics(id)
}

func validateIdentityNames(id RunIdentity) error {
	if id.PhysicalModel == "" || !routingName(id.PhysicalModel) {
		return errors.New("identity requires a valid physical model name")
	}
	if id.Harness == "" || !routingName(id.Harness) {
		return errors.New("identity requires a valid harness name")
	}
	if id.HarnessVersion == "" || len(id.HarnessVersion) > maxOutcomeNoteBytes {
		return errors.New("identity requires a nonempty harness version")
	}
	return nil
}

// validateIdentityTools checks the names of a tool list: bounded, nonempty, no whitespace or
// control characters (the digest separates names by newline), no duplicates.
func validateIdentityTools(tools []string) error {
	if len(tools) > MaxIdentityTools {
		return fmt.Errorf("identity tool set holds %d tools, exceeding bound of %d", len(tools), MaxIdentityTools)
	}
	seen := make(map[string]bool, len(tools))
	for i := 0; i < len(tools); i++ {
		tool := tools[i]
		if tool == "" || len(tool) > maxRoutingNameBytes || strings.IndexFunc(tool, unicode.IsSpace) >= 0 || strings.IndexFunc(tool, unicode.IsControl) >= 0 {
			return errors.New("identity tool set must contain valid tool names")
		}
		if seen[tool] {
			return fmt.Errorf("identity tool set contains duplicate tool %q", tool)
		}
		seen[tool] = true
	}
	return nil
}

// validateIdentityToolSet checks that digest and count describe the tool set together, and that
// a stored list matches both.
func validateIdentityToolSet(id RunIdentity) error {
	if (id.ToolSetDigest == "") != (id.ToolCount == 0) {
		return errors.New("identity tool-set digest and tool count must be set together")
	}
	if id.ToolCount < 0 || id.ToolCount > MaxIdentityTools {
		return fmt.Errorf("identity tool count must be between 0 and %d", MaxIdentityTools)
	}
	if id.ToolSetDigest != "" && !validToolSetDigest(id.ToolSetDigest) {
		return errors.New("identity tool-set digest must be sha256:<64 hex digits>")
	}
	if len(id.ToolSet) == 0 {
		return nil
	}
	digest, count, err := DigestToolSet(id.ToolSet)
	if err != nil {
		return err
	}
	if digest != id.ToolSetDigest || count != id.ToolCount {
		return errors.New("identity tool list does not match its tool-set digest and count")
	}
	return nil
}

func validToolSetDigest(digest string) bool {
	sum, ok := strings.CutPrefix(digest, toolSetDigestPrefix)
	if !ok || len(sum) != 2*sha256.Size {
		return false
	}
	_, err := hex.DecodeString(sum)
	return err == nil
}

func validateIdentityDigests(id RunIdentity) error {
	if id.PromptDigest == "" || len(id.PromptDigest) > maxOutcomeNoteBytes {
		return errors.New("identity requires a prompt template digest")
	}
	if id.ContextDigest == "" || len(id.ContextDigest) > maxOutcomeNoteBytes {
		return errors.New("identity requires a compiled context digest")
	}
	return nil
}

func validateIdentityMetrics(id RunIdentity) error {
	if id.ContextBytes < 0 {
		return errors.New("identity context bytes must be nonnegative")
	}
	if id.PriorRounds < 0 {
		return errors.New("identity prior rounds must be nonnegative")
	}
	if id.Retries < 0 {
		return errors.New("identity retries must be nonnegative")
	}
	if !validCost(id.CostEstimate) {
		return errors.New("identity cost estimate must be a finite nonnegative amount")
	}
	return nil
}

// validCost reports whether an optional amount is absent or finite and nonnegative.
func validCost(amount *float64) bool {
	return amount == nil || (*amount >= 0 && !math.IsInf(*amount, 1))
}

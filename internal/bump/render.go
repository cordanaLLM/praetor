package bump

import (
	"fmt"
	"strings"
)

// actionUpToDateLabel is the ActionDriftStatus of a current, non-deprecated pin; every
// other label marks a row the audit counts as not up to date (actionsBehind).
const actionUpToDateLabel = "[UP-TO-DATE]"

// ActionDriftStatus labels one workflow action reference. A SHA pin its upstream refutes
// (PinCommitMissing, PinReleaseMismatch) is a bad pin, whatever its version says; then come a
// deprecated runtime, a SHA pin nobody confirmed (PinUnverified) or that names no release
// (PinUnversioned), neither of which is ever up to date, and last drift behind the latest
// release or up to date.
func ActionDriftStatus(a ActionCandidate) string {
	switch {
	case a.Pin == PinCommitMissing || a.Pin == PinReleaseMismatch:
		return "[BAD-PIN]"
	case a.Deprecated:
		return "[DEPRECATED]"
	case a.Pin == PinUnverified:
		return "[UNVERIFIED]"
	case a.Pin == PinUnversioned:
		return "[UNVERSIONED]"
	case !a.UpToDate:
		return "[DRIFT]"
	default:
		return actionUpToDateLabel
	}
}

// FormatActionsInventory renders the GitHub Actions section of a version audit. The CLI
// `bump audit` and the MCP standards_version_audit tool both print it, so the two surfaces
// report the same workflow drift (BUG-872). Each row names its workflow file, and its line
// when the scan recorded one. Each distinct reason a SHA pin went unverified follows the
// rows once, with the number of pins it left unverified. No actions renders nothing.
func FormatActionsInventory(actions []ActionCandidate) string {
	if len(actions) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("GitHub Actions Inventory:\n")
	var reasons []string
	unverified := make(map[string]int)
	for _, a := range actions {
		fmt.Fprintf(&sb, "  %-13s %-32s %s -> %s (%s)\n",
			ActionDriftStatus(a), a.Action, a.CurrentVersion, a.LatestVersion, workflowLocation(a))
		if a.Pin != PinUnverified {
			continue
		}
		if unverified[a.PinDetail] == 0 {
			reasons = append(reasons, a.PinDetail)
		}
		unverified[a.PinDetail]++
	}
	for _, reason := range reasons {
		fmt.Fprintf(&sb, "  %d SHA pin(s) unverified: %s\n", unverified[reason], reason)
	}
	sb.WriteString("\n")
	return sb.String()
}

// workflowLocation names where a reference is declared: its workflow file, and ":<line>"
// when the scan recorded the line.
func workflowLocation(a ActionCandidate) string {
	if a.Line > 0 {
		return fmt.Sprintf("%s:%d", a.WorkflowFile, a.Line)
	}
	return a.WorkflowFile
}

package bump

import (
	"fmt"
	"strings"
)

// ActionDriftStatus labels one workflow action reference: deprecated, behind its latest
// release, or up to date.
func ActionDriftStatus(a ActionCandidate) string {
	switch {
	case a.Deprecated:
		return "[DEPRECATED]"
	case a.CurrentVersion != a.LatestVersion:
		return "[DRIFT]"
	default:
		return "[UP-TO-DATE]"
	}
}

// FormatActionsInventory renders the GitHub Actions section of a version audit. The CLI
// `bump audit` and the MCP standards_version_audit tool both print it, so the two surfaces
// report the same workflow drift (BUG-872). No actions renders nothing.
func FormatActionsInventory(actions []ActionCandidate) string {
	if len(actions) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("GitHub Actions Inventory:\n")
	for _, a := range actions {
		fmt.Fprintf(&sb, "  %-12s %-32s %s -> %s (%s)\n",
			ActionDriftStatus(a), a.Action, a.CurrentVersion, a.LatestVersion, a.WorkflowFile)
	}
	sb.WriteString("\n")
	return sb.String()
}

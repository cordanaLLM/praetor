// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// RenderJSON serializes the efficiency report to pretty-printed JSON.
func RenderJSON(report *Report, out io.Writer) error {
	if report == nil {
		return fmt.Errorf("cannot render nil report")
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal efficiency JSON: %w", err)
	}
	_, err = out.Write(append(data, '\n'))
	return err
}

// RenderTable prints the efficiency report as a formatted table followed by milestone summary.
func RenderTable(report *Report, out io.Writer) error {
	if report == nil {
		return fmt.Errorf("cannot render nil report")
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	header := "PR\tBRANCH\tISSUE-TO-MERGE\tTOUCHES\tFRONTIER TOKENS\tSPEND\tCACHE HIT\tLOCAL-1ST\n"
	if _, err := fmt.Fprint(w, header); err != nil {
		return err
	}

	for _, u := range report.Units {
		line := fmt.Sprintf("#%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			u.PullRequestNumber,
			u.HeadBranch,
			u.IssueToMerge,
			u.OperatorTouches,
			u.FrontierTokens,
			u.Spend,
			u.PromptCacheHitRate,
			u.LocalFirstRatio,
		)
		if _, err := fmt.Fprint(w, line); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}

	if err := renderSummary(report, out); err != nil {
		return err
	}
	return nil
}

func renderSummary(report *Report, out io.Writer) error {
	ms := report.MilestoneSummary
	title := ms.Milestone
	if title == "" {
		title = "All Landed Units"
	}

	lines := []string{
		fmt.Sprintf("\n--- Milestone Summary: %s ---", title),
		fmt.Sprintf("Units:                   %d", ms.UnitsCount),
		fmt.Sprintf("Avg Issue-to-Merge:      %s", ms.AvgIssueToMerge),
		fmt.Sprintf("Operator Touches:        %s", ms.OperatorTouches),
		fmt.Sprintf("Frontier Tokens:         %s", ms.FrontierTokens),
		fmt.Sprintf("Attributed Spend:        %s", ms.AttributedSpend),
		fmt.Sprintf("Other Units Spend:       %s", ms.OtherUnitsSpend),
		fmt.Sprintf("Unattributed Spend:      %s", ms.UnattributedSpend),
		fmt.Sprintf("Total Spend:             %s", ms.TotalSpend),
		fmt.Sprintf("Prompt-Cache Hit Rate:   %s", ms.PromptCacheHitRate),
		fmt.Sprintf("Local-First Ratio:       %s", ms.LocalFirstRatio),
		fmt.Sprintf("Fact-Hit Ratio:          %s", ms.FactHitRatio),
		fmt.Sprintf("Checks-Before-Reviews:   %s", ms.ChecksBeforeReviews),
	}
	if len(report.Notes) > 0 {
		lines = append(lines, "\n--- Notes ---")
		for _, note := range report.Notes {
			lines = append(lines, "- "+note)
		}
	}
	_, err := fmt.Fprintln(out, strings.Join(lines, "\n"))
	return err
}

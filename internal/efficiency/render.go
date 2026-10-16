// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
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
	header := "PR\tBRANCH\tSTATUS\tISSUE-TO-MERGE\tTOUCHES\tFRONTIER TOKENS\tSPEND\tCACHE HIT\tLOCAL-1ST\tSOURCES\n"
	if _, err := fmt.Fprint(w, header); err != nil {
		return err
	}

	for _, u := range report.Units {
		line := fmt.Sprintf("#%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			u.PullRequestNumber,
			u.HeadBranch,
			u.Disposition,
			u.IssueToMerge,
			u.OperatorTouches,
			u.FrontierTokens,
			u.Spend,
			u.PromptCacheHitRate,
			u.LocalFirstRatio,
			u.Sources,
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

// summaryLabelWidth fits the longest summary label, "Tokens by Provider per Qualified Unit:".
const summaryLabelWidth = 38

func summaryLine(label, value string) string {
	return fmt.Sprintf("%-*s %s", summaryLabelWidth, label+":", value)
}

// renderSummary prints the milestone summary. Each label names its rule: "per Qualified Unit"
// is the total over every unit divided by the qualified units, "Qualified Mean" averages
// qualified units only, "All Usage" pools every unit's usage.
func renderSummary(report *Report, out io.Writer) error {
	ms := report.MilestoneSummary
	title := ms.Milestone
	if title == "" {
		title = "All Landed Units"
	}
	lines := []string{
		fmt.Sprintf("\n--- Milestone Summary: %s ---", title),
		summaryLine("Units", fmt.Sprintf("%d", ms.UnitsCount)),
		summaryLine("Qualified Units", fmt.Sprintf("%d", ms.QualifiedUnits)),
		summaryLine("Lane Counts", ms.LaneCounts.String()),
	}
	laneKeys := make([]string, 0, len(ms.PerLane))
	for k := range ms.PerLane {
		laneKeys = append(laneKeys, k)
	}
	sort.Strings(laneKeys)
	for _, k := range laneKeys {
		lines = append(lines, summaryLine(fmt.Sprintf("Lane Counts (%s)", k), ms.PerLane[k].String()))
	}
	lines = append(lines,
		summaryLine("Issue-to-Merge (Qualified Mean)", ms.AvgIssueToMerge),
		summaryLine("Wall Seconds per Qualified Unit", ms.WallSeconds.Display),
		summaryLine("Review Rounds per Qualified Unit", ms.ReviewRounds.Display),
		summaryLine("Retries per Qualified Unit", ms.Retries.Display),
		summaryLine("Operator Minutes per Qualified Unit", ms.OperatorMinutes.Display),
		summaryLine("Escaped Defects per Qualified Unit", ms.EscapedDefects.Display),
		summaryLine("Tokens by Provider per Qualified Unit", ms.TokensByProvider.Display),
		summaryLine("Operator Touches per Qualified Unit", ms.OperatorTouches),
		summaryLine("Frontier Tokens per Qualified Unit", ms.FrontierTokens),
		summaryLine("Spend per Qualified Unit", ms.SpendPerQualifiedUnit),
		summaryLine("Attributed Spend", ms.AttributedSpend),
		summaryLine("Other Units Spend", ms.OtherUnitsSpend),
		summaryLine("Unattributed Spend", ms.UnattributedSpend),
		summaryLine("Total Spend", ms.TotalSpend),
		summaryLine("Prompt-Cache Hit Rate (All Usage)", ms.PromptCacheHitRate),
		summaryLine("Local-First Ratio (All Usage)", ms.LocalFirstRatio),
		summaryLine("Fact-Hit Ratio", ms.FactHitRatio),
		summaryLine("Checks-Before-Reviews", ms.ChecksBeforeReviews),
	)
	if ms.EstimateError != "" && ms.EstimateError != NotMeasured {
		lines = append(lines, summaryLine("Estimate Error", ms.EstimateError))
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

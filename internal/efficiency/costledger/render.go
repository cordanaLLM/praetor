// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package costledger

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
)

// RenderJSON serializes the cost ledger report to indented JSON.
func RenderJSON(report *Report, out io.Writer) error {
	if report == nil {
		return fmt.Errorf("cannot render nil report")
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cost ledger JSON: %w", err)
	}
	_, err = out.Write(append(data, '\n'))
	return err
}

// RenderTable prints the cost ledger report as formatted human-readable tables.
func RenderTable(report *Report, out io.Writer) error {
	if report == nil {
		return fmt.Errorf("cannot render nil report")
	}

	if err := renderSessionsTable(report, out); err != nil {
		return err
	}
	if err := renderCategoryTotalsTable(report, out); err != nil {
		return err
	}
	if len(report.Mechanisms) > 0 {
		if err := renderMechanismsTable(report.Mechanisms, out); err != nil {
			return err
		}
	}
	return nil
}

func renderSessionsTable(report *Report, out io.Writer) error {
	if _, err := fmt.Fprintln(out, "=== Cost Ledger: Session Costs ==="); err != nil {
		return err
	}
	for _, s := range report.Sessions {
		if err := renderSingleSession(s, out); err != nil {
			return err
		}
	}
	return nil
}

func renderSingleSession(s SessionReport, out io.Writer) error {
	if _, err := fmt.Fprintf(out, "\nSession: %s (Own: %d tokens / $%s [%s])\n",
		s.Session, s.OwnTokens, formatSpendMicros(s.OwnSpendMicros), s.OwnProvenance); err != nil {
		return err
	}
	if err := renderSessionCategories(s.Categories, out); err != nil {
		return err
	}
	if s.AllocatedShare != nil {
		if err := renderSessionAllocation(s.AllocatedShare, out); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(
		out,
		"  Total Session Cost: %d tokens / $%s\n",
		s.TotalTokens,
		formatSpendMicros(s.TotalSpendMicros),
	)
	return err
}

func renderSessionCategories(cats map[string]CategoryCost, out io.Writer) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "  CATEGORY\tTOKENS\tSPEND\tPROVENANCE"); err != nil {
		return err
	}
	for _, cat := range ValidCategories {
		c := cats[cat]
		if c.Tokens > 0 || c.SpendMicros > 0 || c.Provenance != "not measured" {
			if _, err := fmt.Fprintf(
				w,
				"  %s\t%d\t$%s\t%s\n",
				cat,
				c.Tokens,
				formatSpendMicros(c.SpendMicros),
				c.Provenance,
			); err != nil {
				return err
			}
		}
	}
	return w.Flush()
}

func renderSessionAllocation(sh *AllocatedShare, out io.Writer) error {
	if _, err := fmt.Fprintf(
		out,
		"  Shared Overhead (%s): %d tokens / $%s\n",
		sh.Rule,
		sh.Tokens,
		formatSpendMicros(sh.SpendMicros),
	); err != nil {
		return err
	}
	_, err := fmt.Fprintf(
		out,
		"  Sensitivity Share (%s): %d tokens / $%s (Difference: %+d tokens / $%s)\n",
		sh.SensitivityRule,
		sh.SensitivityTokens,
		formatSpendMicros(sh.SensitivitySpendMicros),
		sh.DifferenceTokens,
		formatSignedSpendMicros(sh.DifferenceSpendMicros),
	)
	return err
}

func formatSignedSpendMicros(micros SignedSpendMicros) string {
	if micros >= 0 {
		return "+" + formatSpendMicros(SpendMicros(micros))
	}
	return formatSpendMicros(SpendMicros(micros))
}

func renderCategoryTotalsTable(report *Report, out io.Writer) error {
	if _, err := fmt.Fprintln(out, "\n=== Category Totals ==="); err != nil {
		return err
	}
	if report.SharedOverhead != nil {
		sh := report.SharedOverhead
		if _, err := fmt.Fprintf(
			out,
			"Shared Pool: %d tokens / $%s [%s] (Rule: %s, Sensitivity: %s)\n\n",
			sh.Tokens,
			formatSpendMicros(sh.SpendMicros),
			sh.Provenance,
			report.Rule,
			report.SensitivityRule,
		); err != nil {
			return err
		}
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "CATEGORY\tTOKENS\tSPEND\tPROVENANCE"); err != nil {
		return err
	}
	for _, cat := range ValidCategories {
		c := report.CategoryTotals[cat]
		if _, err := fmt.Fprintf(
			w,
			"%s\t%d\t$%s\t%s\n",
			cat,
			c.Tokens,
			formatSpendMicros(c.SpendMicros),
			c.Provenance,
		); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(
		w,
		"TOTAL\t%d\t$%s\t%s\n",
		report.GrandTotal.Tokens,
		formatSpendMicros(report.GrandTotal.SpendMicros),
		report.GrandTotal.Provenance,
	); err != nil {
		return err
	}
	return w.Flush()
}

func renderMechanismsTable(mechanisms []MechanismReport, out io.Writer) error {
	if _, err := fmt.Fprintln(out, "\n=== Efficiency Mechanisms ==="); err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "MECHANISM\tKIND\tFIXED\tSAVING/USE\tUSES\tBREAK-EVEN N*\tSTATUS\tCALLS Δ\tCOST Δ\tLATENCY Δ\tCOSMETIC"); err != nil {
		return err
	}
	for _, m := range mechanisms {
		nStarStr := "no break-even"
		if m.BreakEvenNStar != nil {
			nStarStr = fmt.Sprintf("%d", *m.BreakEvenNStar)
		}
		cosmeticStr := "no"
		if m.Cosmetic {
			cosmeticStr = "yes (calls down, cost and latency not)"
		}
		callsStr := fmt.Sprintf("%+d", m.CallsDelta)
		costStr := fmt.Sprintf("%+.2f", m.CostDelta)
		latStr := fmt.Sprintf("%+.2fs", m.LatencyDeltaS)

		line := fmt.Sprintf("%s\t%s\t%d\t%d\t%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			m.ID, m.Kind, m.FixedCostTokens, m.SavingPerUseTokens, m.Uses,
			nStarStr, m.BreakEvenStatus, callsStr, costStr, latStr, cosmeticStr)
		if _, err := fmt.Fprint(w, line); err != nil {
			return err
		}
	}
	return w.Flush()
}

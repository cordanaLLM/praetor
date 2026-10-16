package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/ruleregistry"
)

const (
	rulesAuditTimeout = time.Minute
	maxReportRules    = 256
	maxReportProblems = 256
)

func runRules(args []string) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		printRulesUsage()
		return nil
	}
	if args[0] != "audit" {
		return fmt.Errorf("unknown rules subcommand: %s", args[0])
	}
	return runRulesAudit(args[1:])
}

func printRulesUsage() {
	fmt.Println("Usage: standardsctl rules audit [--json] [--root <dir>]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  audit  Resolve declared enforcement evidence for HISS and Operational Rules")
}

func runRulesAudit(args []string) error {
	fs := flag.NewFlagSet("rules audit", flag.ContinueOnError)
	root := fs.String("root", ".", "Repository root to inspect")
	jsonOutput := fs.Bool("json", false, "Write the report as JSON")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), rulesAuditTimeout)
	defer cancel()
	report, err := ruleregistry.Audit(ctx, *root)
	if report != nil {
		if printErr := printRulesReport(report, *jsonOutput); printErr != nil {
			return printErr
		}
	}
	if err != nil {
		return err
	}
	return nil
}

func printRulesReport(report *ruleregistry.Report, jsonOutput bool) error {
	if jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	fmt.Println("=== Rule enforcement audit ===")
	for i := 0; i < len(report.Rows) && i < maxReportRules; i++ {
		row := report.Rows[i]
		line := fmt.Sprintf("%s %s %s", row.ID, row.Class, row.Evidence)
		if row.Candidate {
			line += "; told not to, nothing checks it"
		}
		fmt.Println(line)
	}
	printRulesSummary(report)
	return nil
}

func printRulesSummary(report *ruleregistry.Report) {
	counts := report.Counts()
	classes := []ruleregistry.Class{
		ruleregistry.ClassCIGate, ruleregistry.ClassHook, ruleregistry.ClassReviewGate,
		ruleregistry.ClassGuidance, ruleregistry.ClassStaged,
	}
	parts := make([]string, 0, len(classes))
	for i := 0; i < len(classes); i++ {
		parts = append(parts, fmt.Sprintf("%s=%d", classes[i], counts[classes[i]]))
	}
	fmt.Printf("classes: %s\n", strings.Join(parts, ", "))
	fmt.Printf("unenforced: %d\n", len(report.ProseOnlyIDs()))
	if len(report.Problems) == 0 {
		return
	}
	for i := 0; i < len(report.Problems) && i < maxReportProblems; i++ {
		fmt.Printf("invalid: %s\n", report.Problems[i])
	}
}

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/cordanaLLM/praetor/internal/seo"
)

const seoUsage = "usage: praetorctl seo audit [--json] [--require-robots] [--allow-placeholders] [site-dir]"

// maxSEOFindingsPrinted bounds the findings the text report prints (HISS-02); --json
// carries every recorded finding.
const maxSEOFindingsPrinted = 256

// runSEO is `praetorctl seo audit`: it audits a built static site -- the directory
// `mkdocs build` or `astro build` writes, site/ by default -- through internal/seo, so the
// seo-audit skill checks the pages crawlers will actually read instead of re-running the
// package's unit fixtures (BUG-526, BUG-691).
func runSEO(args []string) error {
	if len(args) > 0 && isHelpToken(args[0]) {
		fmt.Println("Usage: praetorctl seo audit [--json] [--require-robots] [--allow-placeholders] [site-dir]")
		fmt.Println("  Audits a built site (default: site): JSON-LD in every HTML page head, every")
		fmt.Println("  sitemap*.xml at the root, and robots.txt. Exits non-zero on any finding;")
		fmt.Println("  an unedited preset placeholder (example-org/example-repo, PlaceholderLang)")
		fmt.Println("  in a page head is a finding unless --allow-placeholders is set.")
		return nil
	}
	parsed, err := parseSEOAuditArgs(args)
	if err != nil {
		return err
	}
	report, err := seo.AuditSite(context.Background(), parsed.root, parsed.options)
	if err != nil {
		return err
	}
	if parsed.asJSON {
		if err := printJSON(report); err != nil {
			return err
		}
	} else {
		printSEOReport(report)
	}
	if !report.Valid {
		return fmt.Errorf("[FAIL] %d SEO finding(s) in %s", len(report.Findings), parsed.root)
	}
	return nil
}

// seoAuditArgs is a parsed `seo audit` command line.
type seoAuditArgs struct {
	root    string
	asJSON  bool
	options seo.SiteAuditOptions
}

// parseSEOAuditArgs parses `audit [--json] [--require-robots] [--allow-placeholders]
// [site-dir]`; site-dir defaults to site, the MkDocs output directory.
func parseSEOAuditArgs(args []string) (seoAuditArgs, error) {
	if len(args) == 0 || args[0] != "audit" {
		return seoAuditArgs{}, errors.New(seoUsage)
	}
	fs := flag.NewFlagSet("seo audit", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "Print the report as one line of JSON")
	requireRobots := fs.Bool("require-robots", false,
		"Fail when the site root has no robots.txt (crawlers read it only at a host root, so a project site under a path cannot serve one)")
	allowPlaceholders := fs.Bool("allow-placeholders", false,
		"Permit the preset placeholders example-org/example-repo and PlaceholderLang in page heads")
	if err := fs.Parse(args[1:]); err != nil {
		return seoAuditArgs{}, err
	}
	if fs.NArg() > 1 {
		return seoAuditArgs{}, errors.New(seoUsage)
	}
	parsed := seoAuditArgs{root: "site", asJSON: *asJSON, options: seo.SiteAuditOptions{
		RequireRobots:     *requireRobots,
		AllowPlaceholders: *allowPlaceholders,
	}}
	if fs.NArg() == 1 {
		parsed.root = fs.Arg(0)
	}
	return parsed, nil
}

// printSEOReport prints the counts on a clean run too, so a pass cannot be mistaken for an
// audit that read nothing; findings go to stderr.
func printSEOReport(report *seo.SiteReport) {
	fmt.Printf("=== SEO Site Audit: %s ===\n", report.Root)
	fmt.Printf("  HTML pages:      %d\n", report.Pages)
	fmt.Printf("  JSON-LD blocks:  %d\n", report.JSONLDBlocks)
	for _, sitemap := range report.Sitemaps {
		kind := "URLs"
		if sitemap.IsIndex {
			kind = "sitemaps"
		}
		fmt.Printf("  Sitemap:         %s (%d %s)\n", sitemap.File, sitemap.Entries, kind)
	}
	robots := "absent"
	if report.Robots {
		robots = "present"
	}
	fmt.Printf("  robots.txt:      %s\n", robots)
	if report.Valid {
		fmt.Println("[PASS] every page head carries valid JSON-LD; sitemaps and robots.txt validate.")
		return
	}
	for i := 0; i < len(report.Findings) && i < maxSEOFindingsPrinted; i++ {
		fmt.Fprintf(os.Stderr, "  %s: %s\n", report.Findings[i].File, report.Findings[i].Message)
	}
	if hidden := len(report.Findings) - maxSEOFindingsPrinted; hidden > 0 {
		fmt.Fprintf(os.Stderr, "  ... and %d more (use --json for all)\n", hidden)
	}
	if report.Truncated {
		fmt.Fprintf(os.Stderr, "  findings stopped at %d; the site holds more\n", seo.MaxSiteFindings)
	}
}

package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/seo"
)

const (
	seoFixtureArticle = `<script type="application/ld+json">{"@context":"https://schema.org","@type":"TechArticle",` +
		`"headline":"H","description":"D","author":{"name":"cordanaLLM"},"dateModified":"2026-09-25"}</script>`
	seoFixtureSitemap = `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>https://example.com/</loc></url></urlset>`
)

// seoSite writes a one-page built site whose page head carries head.
func seoSite(t *testing.T, head string) string {
	t.Helper()
	dir := t.TempDir()
	writeFixtureFile(t, dir, "index.html", "<html><head>"+head+"</head><body></body></html>")
	writeFixtureFile(t, dir, "sitemap.xml", seoFixtureSitemap)
	return dir
}

func TestRunSEO_Positive_AuditsABuiltSite(t *testing.T) {
	dir := seoSite(t, seoFixtureArticle)
	out, err := captureStdout(t, func() error { return dispatchCommand("seo", []string{"audit", dir}) })
	if err != nil {
		t.Fatalf("seo audit: %v\n%s", err, out)
	}
	for _, want := range []string{"HTML pages:      1", "JSON-LD blocks:  1", "sitemap.xml (1 URLs)", "robots.txt:      absent", "[PASS]"} {
		mustContain(t, out, want)
	}

	out, err = captureStdout(t, func() error { return runSEO([]string{"audit", "--json", dir}) })
	if err != nil {
		t.Fatalf("seo audit --json: %v", err)
	}
	var report seo.SiteReport
	if err := json.Unmarshal([]byte(out), &report); err != nil || !report.Valid || report.Pages != 1 {
		t.Fatalf("json report = %+v (err %v)\n%s", report, err, out)
	}
}

func TestRunSEO_Negative_FindingsFailTheCommand(t *testing.T) {
	// BUG-974: an empty dateModified fails the audit.
	dir := seoSite(t, strings.Replace(seoFixtureArticle, `"2026-09-25"`, `""`, 1))
	stderr, err := captureStderr(t, func() error {
		_, runErr := captureStdout(t, func() error { return runSEO([]string{"audit", dir}) })
		return runErr
	})
	mustErrContain(t, err, "[FAIL] 1 SEO finding(s)")
	mustContain(t, stderr, "index.html: JSON-LD block 1: dateModified is required")

	// --require-robots turns an absent robots.txt into a finding.
	dir = seoSite(t, seoFixtureArticle)
	_, err = captureStdout(t, func() error { return runSEO([]string{"audit", "--require-robots", dir}) })
	mustErrContain(t, err, "[FAIL] 1 SEO finding(s)")

	for _, args := range [][]string{{}, {"bogus"}, {"audit", "a", "b"}} {
		mustErrContain(t, runSEO(args), seoUsage)
	}
	if err := runSEO([]string{"audit", filepath.Join(dir, "absent")}); err == nil {
		t.Fatal("expected an error for a missing site directory")
	}
}

func TestRunSEO_Boundary_HelpAndDefaultRoot(t *testing.T) {
	for _, tok := range []string{"-h", "--help", "help"} {
		out, err := captureStdout(t, func() error { return dispatchCommand("seo", []string{tok}) })
		if err != nil {
			t.Fatalf("seo %s must succeed, got %v", tok, err)
		}
		mustContain(t, out, "Usage: praetorctl seo audit")
	}
	// With no site-dir the audit reads ./site, the MkDocs default output directory.
	t.Chdir(seoSite(t, "")) // a head without JSON-LD, but no ./site below it
	if err := runSEO([]string{"audit"}); err == nil || !strings.Contains(err.Error(), "site") {
		t.Fatalf("expected the default ./site root to be missing, got %v", err)
	}
	writeFixtureFile(t, ".", "site/index.html", "<html><head>"+seoFixtureArticle+"</head></html>")
	writeFixtureFile(t, ".", "site/sitemap.xml", seoFixtureSitemap)
	if _, err := captureStdout(t, func() error { return runSEO([]string{"audit"}) }); err != nil {
		t.Fatalf("default ./site root: %v", err)
	}
}

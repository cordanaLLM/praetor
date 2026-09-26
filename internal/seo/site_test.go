package seo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	siteArticle = `{"@context":"https://schema.org","@type":"TechArticle","headline":"H","description":"D",` +
		`"author":{"@type":"Organization","name":"cordanaLLM"},"dateModified":"2026-09-25"}`
	siteCode = `{"@context":"https://schema.org","@type":"SoftwareSourceCode","name":"praetor",` +
		`"programmingLanguage":"Go","codeRepository":"https://github.com/cordanaLLM/praetor"}`
	siteSitemap = `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` +
		`<url><loc>https://example.com/</loc><lastmod>2026-09-25</lastmod></url></urlset>`
	siteRobots = "User-agent: *\nDisallow: /private/\nSitemap: https://example.com/sitemap.xml\n"
)

// writeSite writes files (slash-separated path -> content) below a fresh directory.
func writeSite(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

// page renders an HTML page whose head carries the given markup.
func page(head string) string {
	return "<!doctype html><html lang=en><head><meta charset=utf-8><title>T</title>" + head +
		"</head><body><header>h</header><p>body</p></body></html>"
}

func jsonLD(body string) string {
	return `<script type="application/ld+json">` + body + `</script>`
}

func auditSite(t *testing.T, root string, opts SiteAuditOptions) *SiteReport {
	t.Helper()
	report, err := AuditSite(context.Background(), root, opts)
	if err != nil {
		t.Fatalf("AuditSite: %v", err)
	}
	if report.Valid != (len(report.Findings) == 0) {
		t.Fatalf("Valid=%v disagrees with findings %v", report.Valid, report.Findings)
	}
	return report
}

// requireFinding fails unless one finding names file and contains want.
func requireFinding(t *testing.T, report *SiteReport, file, want string) {
	t.Helper()
	for _, f := range report.Findings {
		if f.File == file && strings.Contains(f.Message, want) {
			return
		}
	}
	t.Fatalf("no finding for %s containing %q in %+v", file, want, report.Findings)
}

func TestAuditSite_Positive_BuiltMkDocsShapedSite(t *testing.T) {
	root := writeSite(t, map[string]string{
		"index.html": page(jsonLD(siteArticle) + jsonLD(siteCode) + `<script src="app.js"></script>`),
		// Minified output drops attribute quotes and may upper-case nothing, or everything.
		"guide/index.html": `<HTML><HEAD><SCRIPT TYPE=application/ld+json>` + siteArticle + `</SCRIPT></HEAD><BODY></BODY></HTML>`,
		"404.html":         page(`<script type='application/ld+json'>` + siteCode + `</script>`),
		"sitemap.xml":      siteSitemap,
		"robots.txt":       siteRobots,
		"assets/app.js":    "console.log('not audited')",
		"sitemap.xml.gz":   "not audited",
	})
	report := auditSite(t, root, SiteAuditOptions{RequireRobots: true})
	if !report.Valid {
		t.Fatalf("expected a valid site, got %+v", report.Findings)
	}
	if report.Pages != 3 || report.JSONLDBlocks != 4 || !report.Robots {
		t.Fatalf("pages=%d blocks=%d robots=%v, want 3, 4, true", report.Pages, report.JSONLDBlocks, report.Robots)
	}
	if len(report.Sitemaps) != 1 || report.Sitemaps[0].File != "sitemap.xml" || report.Sitemaps[0].Entries != 1 {
		t.Fatalf("sitemaps = %+v", report.Sitemaps)
	}
}

func TestAuditSite_Positive_StarlightShapedSitemapIndexWithoutRobots(t *testing.T) {
	root := writeSite(t, map[string]string{
		"index.html": page(jsonLD(siteCode) + jsonLD(siteArticle)),
		"sitemap-index.xml": `<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` +
			`<sitemap><loc>https://example.com/sitemap-0.xml</loc></sitemap></sitemapindex>`,
		"sitemap-0.xml": siteSitemap,
	})
	report := auditSite(t, root, SiteAuditOptions{})
	if !report.Valid || report.Robots || len(report.Sitemaps) != 2 {
		t.Fatalf("valid=%v robots=%v sitemaps=%+v findings=%+v", report.Valid, report.Robots, report.Sitemaps, report.Findings)
	}
	if !report.Sitemaps[1].IsIndex || report.Sitemaps[1].File != "sitemap-index.xml" {
		t.Fatalf("sitemap index not recognised: %+v", report.Sitemaps)
	}
}

func TestAuditSite_Negative_RequiredTechArticleFieldsFail(t *testing.T) {
	root := writeSite(t, map[string]string{
		"a/index.html": page(jsonLD(strings.Replace(siteArticle, `"2026-09-25"`, `""`, 1))),
		"b/index.html": page(jsonLD(`{"@context":"https://schema.org","@type":"TechArticle","headline":"H","description":"D","dateModified":"2026-09-25"}`)),
		"sitemap.xml":  siteSitemap,
	})
	report := auditSite(t, root, SiteAuditOptions{})
	if report.Valid || len(report.Findings) != 2 {
		t.Fatalf("expected exactly two findings, got %+v", report.Findings)
	}
	requireFinding(t, report, "a/index.html", "JSON-LD block 1: dateModified is required")
	requireFinding(t, report, "b/index.html", "JSON-LD block 1: author is required")
}

func TestAuditSite_Negative_BrokenPagesSitemapAndRobots(t *testing.T) {
	root := writeSite(t, map[string]string{
		"none.html":    page(`<script src="x.js"></script>`),
		"body.html":    "<html><head></head><body>" + jsonLD(siteCode) + "</body></html>",
		"badjson.html": page(jsonLD(`{"@type":`)),
		"person.html":  page(jsonLD(`{"@context":"https://schema.org","@type":"Person"}`)),
		"sitemap.xml":  `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>/relative</loc></url></urlset>`,
		"robots.txt":   "Disallow: /\n",
	})
	report := auditSite(t, root, SiteAuditOptions{})
	requireFinding(t, report, "none.html", "no application/ld+json script in <head>")
	requireFinding(t, report, "body.html", "no application/ld+json script in <head>")
	requireFinding(t, report, "badjson.html", "JSON-LD block 1: unexpected end of JSON input")
	requireFinding(t, report, "person.html", "unsupported or missing @type: 'Person'")
	requireFinding(t, report, "sitemap.xml", "invalid absolute loc")
	requireFinding(t, report, "robots.txt", "missing required User-agent directive")
}

func TestAuditSite_Negative_MissingSitemapAndRequiredRobots(t *testing.T) {
	// A sitemap below the root is not the site's sitemap, and neither is a nested robots.txt.
	root := writeSite(t, map[string]string{
		"index.html":      page(jsonLD(siteCode)),
		"sub/sitemap.xml": siteSitemap,
		"sub/robots.txt":  siteRobots,
	})
	report := auditSite(t, root, SiteAuditOptions{RequireRobots: true})
	requireFinding(t, report, ".", "no sitemap.xml or sitemap*.xml at the site root")
	requireFinding(t, report, "robots.txt", "required but absent")
	if len(report.Findings) != 2 {
		t.Fatalf("expected two findings, got %+v", report.Findings)
	}
}

func TestAuditSite_Negative_UnusableRoot(t *testing.T) {
	file := filepath.Join(writeSite(t, map[string]string{"f.html": "x"}), "f.html")
	for name, root := range map[string]string{"missing": filepath.Join(t.TempDir(), "absent"), "file": file} {
		if _, err := AuditSite(context.Background(), root, SiteAuditOptions{}); err == nil {
			t.Errorf("%s root: expected an error", name)
		}
	}
	var nilContext context.Context
	if _, err := AuditSite(nilContext, t.TempDir(), SiteAuditOptions{}); err == nil {
		t.Error("nil context: expected an error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := AuditSite(ctx, t.TempDir(), SiteAuditOptions{}); err == nil {
		t.Error("cancelled context: expected an error")
	}
}

func TestAuditSite_Boundary_HeadDetection(t *testing.T) {
	root := writeSite(t, map[string]string{
		// No head at all, and a <header> that must not be mistaken for one.
		"nohead.html": "<html><body><header>" + jsonLD(siteCode) + "</header></body></html>",
		// The optional </head> is omitted: the head ends at <body>.
		"implicit.html": "<html><head>" + jsonLD(siteCode) + "<body>" + jsonLD(`{"@type":`) + "</body></html>",
		// A "</head>" inside a script body is raw text, not the end of the head.
		"rawtext.html": page(`<script>var s = "</head>";</script>` + jsonLD(siteCode)),
		// A '>' inside a quoted attribute does not close the start tag.
		"quoted.html":        page(`<script data-x="a>b" type="application/ld+json">` + siteCode + `</script>`),
		"unterm.html":        "<html><head>" + `<script type="application/ld+json">` + siteCode,
		"emptyld.html":       page(jsonLD("   ")),
		"commenthead.html":   "<html><!-- <head> --> <head>" + jsonLD(siteCode) + "</head></html>",
		"commentscript.html": page("<!-- " + jsonLD(`{"@type":`) + " -->" + jsonLD(siteCode)),
		"sitemap.xml":        siteSitemap,
	})
	report := auditSite(t, root, SiteAuditOptions{})
	requireFinding(t, report, "nohead.html", "no <head> element")
	requireFinding(t, report, "unterm.html", "unterminated <script> element in <head>")
	requireFinding(t, report, "emptyld.html", "empty payload")
	for _, clean := range []string{"implicit.html", "rawtext.html", "quoted.html", "commenthead.html", "commentscript.html"} {
		for _, f := range report.Findings {
			if f.File == clean {
				t.Errorf("%s: unexpected finding %q", clean, f.Message)
			}
		}
	}
	if len(report.Findings) != 3 {
		t.Fatalf("expected three findings, got %+v", report.Findings)
	}
}

// TestAuditSite_Boundary_CommentState pins where "<!--" opens a comment: only in the HTML
// data state, never inside raw text, RCDATA, a JSON-LD string or after a '<' that opens no
// tag, and the empty comments "<!-->" and "<!--->" close at once.
func TestAuditSite_Boundary_CommentState(t *testing.T) {
	jsonComment := strings.Replace(siteCode, `"name":"praetor"`, `"name":"x <!-- y"`, 1)
	if jsonComment == siteCode {
		t.Fatal("jsoncomment fixture: siteCode no longer carries the replaced name")
	}
	tail := "</head><body><!-- x --></body></html>"
	root := writeSite(t, map[string]string{
		"scriptcomment.html": page(`<script>var x = "<!--";</script>` + jsonLD(siteCode)),
		"jsoncomment.html":   page(jsonLD(jsonComment)),
		"attrcomment.html":   page(`<meta content="<!--">` + jsonLD(siteCode)),
		"titlecomment.html":  "<html><head><title>a <!-- b</title>" + jsonLD(siteCode) + tail,
		"emptycomment.html":  "<html><head><!-->" + jsonLD(siteCode) + "<!--->" + tail,
		"textlt.html":        "<html>1 < 2 <!-- <head></head> --><head>" + jsonLD(siteCode) + tail,
		"unclosed.html":      "<html><head><!-- " + jsonLD(siteCode) + "</head><body></body></html>",
		"sitemap.xml":        siteSitemap,
	})
	report := auditSite(t, root, SiteAuditOptions{})
	requireFinding(t, report, "unclosed.html", "no application/ld+json script in <head>")
	if len(report.Findings) != 1 {
		t.Fatalf("expected only the unclosed-comment finding, got %+v", report.Findings)
	}
}

// TestAuditSite_Placeholders pins where a preset placeholder is a finding: anywhere in the
// page head (title, meta, JSON-LD), but not in body text, which may name the placeholders
// on purpose, and not inside a head comment.
func TestAuditSite_Placeholders(t *testing.T) {
	placeholderCode := strings.Replace(siteCode, `"programmingLanguage":"Go"`, `"programmingLanguage":"PlaceholderLang"`, 1)
	if placeholderCode == siteCode {
		t.Fatal("jsonld fixture: siteCode no longer carries the replaced programmingLanguage")
	}
	root := writeSite(t, map[string]string{
		"head.html":    "<html><head><title>example-org/example-repo</title>" + jsonLD(siteCode) + "</head><body></body></html>",
		"jsonld.html":  page(jsonLD(placeholderCode)),
		"body.html":    page(jsonLD(siteCode)) + "<p>Replace example-org/example-repo and PlaceholderLang.</p>",
		"comment.html": page("<!-- example-org/example-repo PlaceholderLang -->" + jsonLD(siteCode)),
		"clean.html":   page(jsonLD(siteCode)),
		"sitemap.xml":  siteSitemap,
	})

	// With AllowPlaceholders false (the default), a placeholder in a head is a finding.
	strict := auditSite(t, root, SiteAuditOptions{AllowPlaceholders: false})
	requireFinding(t, strict, "head.html", "carries unedited example-org/example-repo placeholder")
	requireFinding(t, strict, "jsonld.html", "carries unedited PlaceholderLang placeholder")
	if len(strict.Findings) != 2 {
		t.Fatalf("expected only the head.html and jsonld.html findings, got %+v", strict.Findings)
	}

	// With AllowPlaceholders true, the placeholders are ignored.
	lax := auditSite(t, root, SiteAuditOptions{AllowPlaceholders: true})
	for _, f := range lax.Findings {
		t.Errorf("unexpected finding in lax audit: %s: %s", f.File, f.Message)
	}
}

func TestAuditSite_Boundary_SitemapURLCap(t *testing.T) {
	for _, tc := range []struct {
		n     int
		valid bool
	}{{MaxURLLimit, true}, {MaxURLLimit + 1, false}} {
		root := writeSite(t, map[string]string{
			"index.html":  page(jsonLD(siteCode)),
			"sitemap.xml": string(buildSitemap("urlset", "url", tc.n)),
		})
		report := auditSite(t, root, SiteAuditOptions{})
		if report.Valid != tc.valid || report.Sitemaps[0].Entries != tc.n {
			t.Fatalf("%d URLs: valid=%v entries=%d findings=%+v", tc.n, report.Valid, report.Sitemaps[0].Entries, report.Findings)
		}
		if !tc.valid {
			requireFinding(t, report, "sitemap.xml", "exceeds maximum limit of 50000")
		}
	}
}

func TestAuditSite_Boundary_JSONLDBlockAndRobotsSizeCaps(t *testing.T) {
	root := writeSite(t, map[string]string{
		"max.html":    page(strings.Repeat(jsonLD(siteCode), MaxJSONLDBlocks)),
		"over.html":   page(strings.Repeat(jsonLD(siteCode), MaxJSONLDBlocks+1)),
		"sitemap.xml": siteSitemap,
		"robots.txt":  "User-agent: *\n" + strings.Repeat("#", MaxRobotsBytes),
	})
	report := auditSite(t, root, SiteAuditOptions{})
	if report.JSONLDBlocks != MaxJSONLDBlocks {
		t.Fatalf("blocks = %d, want %d from max.html alone", report.JSONLDBlocks, MaxJSONLDBlocks)
	}
	requireFinding(t, report, "over.html", "more than 64 JSON-LD blocks")
	requireFinding(t, report, "robots.txt", "carries more than")
	if len(report.Findings) != 2 {
		t.Fatalf("expected two findings, got %+v", report.Findings)
	}
}

func TestAuditSite_Boundary_DepthCapAndSymlinkedRoot(t *testing.T) {
	deep := strings.Repeat("d/", MaxSiteDepth-1) + "index.html"
	root := writeSite(t, map[string]string{deep: page(jsonLD(siteCode)), "sitemap.xml": siteSitemap})
	if report := auditSite(t, root, SiteAuditOptions{}); !report.Valid || report.Pages != 1 {
		t.Fatalf("page %d directories deep: valid=%v pages=%d %+v", MaxSiteDepth-1, report.Valid, report.Pages, report.Findings)
	}
	tooDeep := writeSite(t, map[string]string{strings.Repeat("d/", MaxSiteDepth) + "index.html": page(jsonLD(siteCode))})
	if _, err := AuditSite(context.Background(), tooDeep, SiteAuditOptions{}); err == nil || !strings.Contains(err.Error(), "nested deeper") {
		t.Fatalf("expected a depth error, got %v", err)
	}

	link := filepath.Join(t.TempDir(), "site")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symbolic links unavailable on this host: %v", err)
	}
	if report := auditSite(t, link, SiteAuditOptions{}); !report.Valid || report.Pages != 1 || report.Root != link {
		t.Fatalf("symlinked root: valid=%v pages=%d root=%q", report.Valid, report.Pages, report.Root)
	}
}

func TestSiteAudit_Boundary_FindingsCap(t *testing.T) {
	audit := &siteAudit{report: &SiteReport{}}
	for i := 0; i < MaxSiteFindings; i++ {
		audit.add("f", "m")
	}
	if audit.report.Truncated || len(audit.report.Findings) != MaxSiteFindings {
		t.Fatalf("at the cap: truncated=%v findings=%d", audit.report.Truncated, len(audit.report.Findings))
	}
	audit.add("f", "m")
	if !audit.report.Truncated || len(audit.report.Findings) != MaxSiteFindings {
		t.Fatalf("past the cap: truncated=%v findings=%d", audit.report.Truncated, len(audit.report.Findings))
	}
}

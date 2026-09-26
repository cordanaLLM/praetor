package seo

import (
	"fmt"
	"strings"
	"testing"
)

// ============================================================================
// 1. POSITIVE TESTS (3D Dimension 1)
// ============================================================================

func TestTechArticle_Positive(t *testing.T) {
	raw := []byte(`{
		"@context": "https://schema.org",
		"@type": "TechArticle",
		"headline": "Deterministic Fleet Governance with HISS-16",
		"description": "Comprehensive formal specification for software integrity.",
		"author": {
			"@type": "Organization",
			"name": "cordanaLLM"
		},
		"datePublished": "2026-09-10T12:00:00Z",
		"dateModified": "2026-09-10T14:30:00Z",
		"inLanguage": "en",
		"proficiencyLevel": "Expert"
	}`)

	res, err := ValidateTechArticle(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Valid {
		t.Fatalf("expected valid TechArticle, got errors: %v", res.Errors)
	}
	if res.SchemaType != "TechArticle" {
		t.Fatalf("expected schema type TechArticle, got %s", res.SchemaType)
	}
}

func TestSoftwareSourceCode_Positive(t *testing.T) {
	raw := []byte(`{
		"@context": "https://schema.org",
		"@type": "SoftwareSourceCode",
		"name": "cordanaLLM/praetor",
		"programmingLanguage": "Go",
		"codeRepository": "https://github.com/cordanaLLM/praetor",
		"runtimePlatform": "Linux / POSIX",
		"license": "https://spdx.org/licenses/Apache-2.0.html",
		"description": "Enterprise Fleet Governance & Universal AI Agent Engineering Engine"
	}`)

	res, err := ValidateSoftwareSourceCode(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Valid {
		t.Fatalf("expected valid SoftwareSourceCode, got errors: %v", res.Errors)
	}
	if res.SchemaType != "SoftwareSourceCode" {
		t.Fatalf("expected schema type SoftwareSourceCode, got %s", res.SchemaType)
	}
}

func TestValidateJSONLD_AutoDispatch_Positive(t *testing.T) {
	rawArticle := []byte(`{
		"@context": "https://schema.org",
		"@type": "TechArticle",
		"headline": "Title",
		"description": "Desc"
	}`)
	res, err := ValidateJSONLD(rawArticle)
	if err != nil || !res.Valid {
		t.Fatalf("expected valid dispatch for TechArticle: %v, err: %v", res.Errors, err)
	}

	rawCode := []byte(`{
		"@context": "https://schema.org",
		"@type": "SoftwareSourceCode",
		"name": "my-tool",
		"programmingLanguage": "Rust",
		"codeRepository": "https://github.com/org/repo"
	}`)
	resCode, errCode := ValidateJSONLD(rawCode)
	if errCode != nil || !resCode.Valid {
		t.Fatalf("expected valid dispatch for SoftwareSourceCode: %v, err: %v", resCode.Errors, errCode)
	}
}

func TestSitemapURLSet_Positive(t *testing.T) {
	raw := []byte(`<?xml version="1.0" encoding="UTF-8"?>
	<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
		<url>
			<loc>https://standards.cordana.ai/</loc>
			<lastmod>2026-09-10T10:00:00Z</lastmod>
			<changefreq>daily</changefreq>
			<priority>1.0</priority>
		</url>
		<url>
			<loc>https://standards.cordana.ai/standards/hiss-16-spec/</loc>
			<lastmod>2026-09-09</lastmod>
			<changefreq>weekly</changefreq>
			<priority>0.8</priority>
		</url>
	</urlset>`)

	res, err := ValidateSitemap(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Valid {
		t.Fatalf("expected valid sitemap, got errors: %v", res.Errors)
	}
	if res.IsIndex {
		t.Fatalf("expected urlset, got is_index true")
	}
	if res.URLCount != 2 {
		t.Fatalf("expected URLCount 2, got %d", res.URLCount)
	}
}

func TestSitemapIndex_Positive(t *testing.T) {
	raw := []byte(`<?xml version="1.0" encoding="UTF-8"?>
	<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
		<sitemap>
			<loc>https://standards.cordana.ai/sitemap-docs.xml</loc>
			<lastmod>2026-09-10</lastmod>
		</sitemap>
		<sitemap>
			<loc>https://standards.cordana.ai/sitemap-blog.xml</loc>
		</sitemap>
	</sitemapindex>`)

	res, err := ValidateSitemap(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Valid {
		t.Fatalf("expected valid sitemap index, got errors: %v", res.Errors)
	}
	if !res.IsIndex {
		t.Fatalf("expected IsIndex true")
	}
	if res.URLCount != 2 {
		t.Fatalf("expected 2 sitemaps, got %d", res.URLCount)
	}
}

func TestRobotsTxt_Positive(t *testing.T) {
	content := `
# Standards Robots Policy
User-agent: *
Allow: /
Disallow: /internal/
Disallow: /ephemeral/
Crawl-delay: 1.5

User-agent: GPTBot
Disallow: /private/

Sitemap: https://standards.cordana.ai/sitemap.xml
Sitemap: https://standards.cordana.ai/sitemapindex.xml
`
	res, err := ValidateRobotsTxt(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Valid {
		t.Fatalf("expected valid robots.txt, got errors: %v", res.Errors)
	}
	if len(res.UserAgents) != 2 {
		t.Fatalf("expected 2 user agents, got %d", len(res.UserAgents))
	}
	if len(res.Sitemaps) != 2 {
		t.Fatalf("expected 2 sitemaps, got %d", len(res.Sitemaps))
	}
	if len(res.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(res.Rules))
	}
	if res.Rules[0].CrawlDelay != 1.5 {
		t.Fatalf("expected CrawlDelay 1.5, got %f", res.Rules[0].CrawlDelay)
	}
}

// ============================================================================
// 2. NEGATIVE TESTS (3D Dimension 2)
// ============================================================================

func TestTechArticle_Negative_MissingFieldsAndBadContext(t *testing.T) {
	// Missing headline, bad context, bad date
	raw := []byte(`{
		"@context": "https://invalid.org",
		"@type": "TechArticle",
		"headline": "   ",
		"description": "Valid desc",
		"datePublished": "not-a-date"
	}`)

	res, err := ValidateTechArticle(raw)
	if err != nil {
		t.Fatalf("expected validation result with errors, not parse error: %v", err)
	}
	if res.Valid {
		t.Fatalf("expected invalid result, but got valid")
	}
	if len(res.Errors) < 3 {
		t.Fatalf("expected at least 3 errors (context, headline, date), got %v", res.Errors)
	}
}

func TestSoftwareSourceCode_Negative_BadRepoURL(t *testing.T) {
	raw := []byte(`{
		"@context": "https://schema.org",
		"@type": "SoftwareSourceCode",
		"name": "standards",
		"programmingLanguage": "Go",
		"codeRepository": "not_an_absolute_url"
	}`)

	res, err := ValidateSoftwareSourceCode(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Valid {
		t.Fatalf("expected invalid codeRepository URL error")
	}
}

func TestValidateJSONLD_Negative_UnknownType(t *testing.T) {
	raw := []byte(`{
		"@context": "https://schema.org",
		"@type": "UnsupportedRandomType"
	}`)

	res, err := ValidateJSONLD(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Valid {
		t.Fatalf("expected invalid for unsupported type")
	}
	if !strings.Contains(res.Errors[0], "unsupported or missing @type") {
		t.Fatalf("expected error message to mention unsupported type, got %s", res.Errors[0])
	}
}

func TestSitemapURLSet_Negative_InvalidURLsAndPriority(t *testing.T) {
	raw := []byte(`<?xml version="1.0" encoding="UTF-8"?>
	<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
		<url>
			<loc>relative/path/not/valid</loc>
			<changefreq>invalid_frequency</changefreq>
			<priority>1.5</priority>
		</url>
	</urlset>`)

	res, err := ValidateSitemap(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Valid {
		t.Fatalf("expected invalid sitemap result")
	}
	if len(res.Errors) < 3 {
		t.Fatalf("expected 3 errors (loc, changefreq, priority), got: %v", res.Errors)
	}
}

func TestSitemap_Negative_MalformedXML(t *testing.T) {
	raw := []byte(`<unclosed_tag>`)
	_, err := ValidateSitemap(raw)
	if err == nil {
		t.Fatalf("expected error for malformed xml, got nil")
	}
}

func TestRobotsTxt_Negative_MissingUserAgentAndInvalidSitemap(t *testing.T) {
	content := `
Allow: /docs/
Sitemap: relative/sitemap.xml
UnknownDirective: value
`
	res, err := ValidateRobotsTxt(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Valid {
		t.Fatalf("expected invalid robots.txt")
	}

	foundUAMissing := false
	foundSitemapInvalid := false
	for _, e := range res.Errors {
		if strings.Contains(e, "missing required User-agent") {
			foundUAMissing = true
		}
		if strings.Contains(e, "invalid sitemap url") {
			foundSitemapInvalid = true
		}
	}
	if !foundUAMissing || !foundSitemapInvalid {
		t.Fatalf("expected UA missing and sitemap invalid errors, got: %v", res.Errors)
	}
}

// ============================================================================
// 3. BOUNDARY TESTS (3D Dimension 3)
// ============================================================================

func TestTechArticle_Boundary_EmptyPayload(t *testing.T) {
	_, err := ValidateTechArticle([]byte{})
	if err == nil {
		t.Fatalf("expected error on empty payload")
	}
}

func TestSoftwareSourceCode_Boundary_MinimalValid(t *testing.T) {
	raw := []byte(`{
		"@context": "http://schema.org",
		"@type": "SoftwareSourceCode",
		"name": "x",
		"programmingLanguage": "C",
		"codeRepository": "http://example.com/repo"
	}`)

	res, err := ValidateSoftwareSourceCode(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Valid {
		t.Fatalf("expected minimal SoftwareSourceCode to be valid, got %v", res.Errors)
	}
}

func TestSitemap_Boundary_ZeroURLs(t *testing.T) {
	raw := []byte(`<?xml version="1.0" encoding="UTF-8"?>
	<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
	</urlset>`)

	res, err := ValidateSitemap(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Valid {
		t.Fatalf("expected empty urlset to be technically valid, got: %v", res.Errors)
	}
	if res.URLCount != 0 {
		t.Fatalf("expected URLCount 0, got %d", res.URLCount)
	}
}

func TestSitemap_Boundary_PriorityLimits(t *testing.T) {
	raw := []byte(`<?xml version="1.0" encoding="UTF-8"?>
	<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
		<url>
			<loc>https://example.com/min</loc>
			<priority>0.0</priority>
		</url>
		<url>
			<loc>https://example.com/max</loc>
			<priority>1.0</priority>
		</url>
	</urlset>`)

	res, err := ValidateSitemap(raw)
	if err != nil || !res.Valid {
		t.Fatalf("expected 0.0 and 1.0 priorities to be valid, got: %v", res.Errors)
	}
}

func TestRobotsTxt_Boundary_EmptyAndCommentOnly(t *testing.T) {
	_, err := ValidateRobotsTxt("")
	if err == nil {
		t.Fatalf("expected error on completely empty robots.txt")
	}

	commentOnly := "# Just a comment\n# Another comment\n"
	res, err := ValidateRobotsTxt(commentOnly)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Valid {
		t.Fatalf("expected invalid result because User-agent is required")
	}
}

// buildSitemap renders a root element in the sitemaps.org namespace holding n entries.
func buildSitemap(root, entry string, n int) []byte {
	var builder strings.Builder
	fmt.Fprintf(&builder, `<%s xmlns="%s">`, root, StandardSitemapNS)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&builder, "<%s><loc>https://example.com/page/%d</loc></%s>", entry, i, entry)
	}
	fmt.Fprintf(&builder, "</%s>", root)
	return []byte(builder.String())
}

func TestSitemap_Boundary_HighVolumeCapping(t *testing.T) {
	// Boundary: exactly MaxURLLimit entries is the largest valid sitemap.
	res, err := ValidateSitemap(buildSitemap("urlset", "url", MaxURLLimit))
	if err != nil || !res.Valid {
		t.Fatalf("expected valid %d-entry sitemap, got: %v %v", MaxURLLimit, err, res.Errors)
	}
	if res.URLCount != MaxURLLimit {
		t.Fatalf("expected %d URLs, got %d", MaxURLLimit, res.URLCount)
	}

	// Boundary: one entry over the cap is rejected, for a urlset and for an index.
	want := fmt.Sprintf("exceeds maximum limit of %d", MaxURLLimit)
	for _, tc := range []struct{ root, entry string }{{"urlset", "url"}, {"sitemapindex", "sitemap"}} {
		res, err := ValidateSitemap(buildSitemap(tc.root, tc.entry, MaxURLLimit+1))
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.root, err)
		}
		if res.Valid || res.URLCount != MaxURLLimit+1 || len(res.Errors) != 1 || !strings.Contains(res.Errors[0], want) {
			t.Fatalf("%s of %d entries: valid=%v count=%d errors=%v", tc.root, MaxURLLimit+1, res.Valid, res.URLCount, res.Errors)
		}
	}
}

func TestSitemap_NamespaceIsRequired(t *testing.T) {
	// Positive: a prefixed declaration of the sitemaps.org namespace resolves to it.
	prefixed := `<sm:urlset xmlns:sm="` + StandardSitemapNS + `"><sm:url><sm:loc>https://example.com/</sm:loc></sm:url></sm:urlset>`
	if res, err := ValidateSitemap([]byte(prefixed)); err != nil || !res.Valid {
		t.Fatalf("prefixed namespace rejected: %v %v", err, res.Errors)
	}
	// Negative: a wrong or missing namespace is reported for both root elements.
	cases := map[string]string{
		"wrong urlset":   `<urlset xmlns="http://www.google.com/schemas/sitemap/0.84"><url><loc>https://example.com/</loc></url></urlset>`,
		"missing urlset": `<urlset><url><loc>https://example.com/</loc></url></urlset>`,
		"wrong index":    `<sitemapindex xmlns="http://example.com/ns"><sitemap><loc>https://example.com/s.xml</loc></sitemap></sitemapindex>`,
	}
	for name, raw := range cases {
		res, err := ValidateSitemap([]byte(raw))
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if res.Valid || len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "namespace must be '"+StandardSitemapNS+"'") {
			t.Errorf("%s: valid=%v errors=%v", name, res.Valid, res.Errors)
		}
	}
}

func TestSitemap_Boundary_FieldLength(t *testing.T) {
	prefix := "https://example.com/"
	exact := prefix + strings.Repeat("a", MaxFieldLength-len(prefix))
	urlset := func(loc string) []byte {
		return []byte(`<urlset xmlns="` + StandardSitemapNS + `"><url><loc>` + loc + `</loc></url></urlset>`)
	}
	// Boundary: a loc of exactly MaxFieldLength bytes is accepted.
	if res, err := ValidateSitemap(urlset(exact)); err != nil || !res.Valid {
		t.Fatalf("loc of %d bytes rejected: %v %v", len(exact), err, res.Errors)
	}
	// Negative: one byte more is reported by length, without echoing the value.
	res, err := ValidateSitemap(urlset(exact + "a"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := fmt.Sprintf("url[0]: loc is %d bytes, maximum %d", MaxFieldLength+1, MaxFieldLength)
	if res.Valid || len(res.Errors) != 1 || res.Errors[0] != want {
		t.Fatalf("over-long loc: valid=%v errors=%q", res.Valid, res.Errors)
	}
	long := strings.Repeat("d", MaxFieldLength+1)
	index := `<sitemapindex xmlns="` + StandardSitemapNS + `"><sitemap><loc>https://example.com/s.xml</loc><lastmod>` + long + `</lastmod></sitemap></sitemapindex>`
	if res, err := ValidateSitemap([]byte(index)); err != nil || res.Valid || !strings.Contains(res.Errors[0], "sitemap[0]: lastmod is") {
		t.Fatalf("over-long index lastmod: %v %v", err, res.Errors)
	}
}

func TestRobotsTxt_Positive_TrailingCommentsStripped(t *testing.T) {
	content := "User-agent: * # every crawler\n" +
		"Disallow: /private # staff only\n" +
		"Allow: /private/press#anchor\n" +
		"Crawl-delay: 5 # slow down\n" +
		"Sitemap: https://example.com/sitemap.xml # map\n"
	res, err := ValidateRobotsTxt(content)
	if err != nil || !res.Valid {
		t.Fatalf("trailing comments rejected: %v %v", err, res.Errors)
	}
	rule := res.Rules[0]
	if rule.UserAgent != "*" || rule.CrawlDelay != 5 {
		t.Errorf("group parsed wrong: %+v", rule)
	}
	if len(rule.Disallows) != 1 || rule.Disallows[0] != "/private" || len(rule.Allows) != 1 || rule.Allows[0] != "/private/press" {
		t.Errorf("comment text kept in rule paths: %+v", rule)
	}
	if len(res.Sitemaps) != 1 || res.Sitemaps[0] != "https://example.com/sitemap.xml" {
		t.Errorf("sitemap parsed wrong: %v", res.Sitemaps)
	}
}

func TestRobotsTxt_Negative_OrphanDirectivesReported(t *testing.T) {
	content := "Disallow: /a\nAllow: /b\nCrawl-delay: 2\nUser-agent: *\nDisallow: /c\n"
	res, err := ValidateRobotsTxt(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		"line 1: disallow directive appears before any User-agent line and applies to no group",
		"line 2: allow directive appears before any User-agent line and applies to no group",
		"line 3: crawl-delay directive appears before any User-agent line and applies to no group",
	}
	if res.Valid || strings.Join(res.Errors, "\n") != strings.Join(want, "\n") {
		t.Fatalf("orphan directives: valid=%v errors=%q", res.Valid, res.Errors)
	}
	if len(res.Rules) != 1 || len(res.Rules[0].Disallows) != 1 || res.Rules[0].Disallows[0] != "/c" {
		t.Errorf("grouped rule parsed wrong: %+v", res.Rules)
	}
}

func TestRobotsTxt_Boundary_LineAndLengthBounds(t *testing.T) {
	build := func(lines int) string {
		return "User-agent: *\n" + strings.Repeat("Disallow: /x\n", lines-1)
	}
	// Boundary: exactly MaxRobotsLines lines (with the final newline) is fully validated.
	if res, err := ValidateRobotsTxt(build(MaxRobotsLines)); err != nil || !res.Valid {
		t.Fatalf("%d-line robots.txt rejected: %v %v", MaxRobotsLines, err, res.Errors)
	}
	// Boundary: one line more is reported as truncated instead of passing silently.
	res, err := ValidateRobotsTxt(build(MaxRobotsLines + 1))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := fmt.Sprintf("robots.txt has %d lines; only the first %d were validated", MaxRobotsLines+1, MaxRobotsLines)
	if res.Valid || len(res.Errors) != 1 || res.Errors[0] != want {
		t.Fatalf("truncation: valid=%v errors=%q", res.Valid, res.Errors)
	}
	// Negative: an over-long directive is reported by length.
	long := "User-agent: *\nDisallow: /" + strings.Repeat("p", MaxFieldLength) + "\n"
	res, err = ValidateRobotsTxt(long)
	if err != nil || res.Valid || len(res.Errors) != 1 || !strings.HasPrefix(res.Errors[0], "line 2: directive is ") {
		t.Fatalf("over-long directive: %v %q", err, res.Errors)
	}
}

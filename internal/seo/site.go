package seo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Site-audit bounds (HISS-02).
const (
	// MaxSiteEntries bounds the files and directories one audit walks.
	MaxSiteEntries = 100000
	// MaxSiteDepth bounds directory nesting below the site root.
	MaxSiteDepth = 32
	// MaxPageBytes bounds one HTML page read.
	MaxPageBytes = 8 << 20
	// MaxSitemapBytes is the sitemaps.org cap on one uncompressed sitemap file.
	MaxSitemapBytes = 50 << 20
	// MaxRobotsBytes is the size RFC 9309 section 2.5 obliges a crawler to parse; content
	// past it may be ignored, so a larger robots.txt is reported rather than read.
	MaxRobotsBytes = 500 << 10
	// MaxHeadScripts bounds the <script> elements scanned in one page head.
	MaxHeadScripts = 1024
	// MaxJSONLDBlocks bounds the JSON-LD blocks validated on one page.
	MaxJSONLDBlocks = 64
	// MaxSiteFindings bounds the findings one report carries.
	MaxSiteFindings = 10000
	// SiteAuditTimeout is the deadline one audit runs under.
	SiteAuditTimeout = 5 * time.Minute
)

// SiteAuditOptions tunes AuditSite.
type SiteAuditOptions struct {
	// RequireRobots fails the audit when the site root carries no robots.txt. Crawlers read
	// robots.txt only at a host root (RFC 9309 section 2.3), so a project site served under
	// a path cannot publish one and the default treats it as optional.
	RequireRobots bool
	// AllowPlaceholders permits the presence of example-org/example-repo and PlaceholderLang
	// in the site's pages, which the preset test build emits natively.
	AllowPlaceholders bool
}

// SiteFinding is one reason a built site fails the audit.
type SiteFinding struct {
	File    string `json:"file"`
	Message string `json:"message"`
}

// SitemapFile summarises one sitemap file found at the site root.
type SitemapFile struct {
	File    string `json:"file"`
	IsIndex bool   `json:"is_index"`
	Entries int    `json:"entries"`
}

// SiteReport is the result of auditing one built site directory.
type SiteReport struct {
	Root         string        `json:"root"`
	Pages        int           `json:"pages"`
	JSONLDBlocks int           `json:"jsonld_blocks"`
	Sitemaps     []SitemapFile `json:"sitemaps"`
	Robots       bool          `json:"robots_txt"`
	Findings     []SiteFinding `json:"findings,omitempty"`
	Truncated    bool          `json:"findings_truncated,omitempty"`
	Valid        bool          `json:"valid"`
}

// AuditSite audits a built static site: the JSON-LD in every HTML page head through
// ValidateJSONLD, every sitemap*.xml at the root through ValidateSitemap, and robots.txt
// through ValidateRobotsTxt. Every page must carry at least one JSON-LD block, and the root
// must carry at least one sitemap. The returned error covers an audit that could not run;
// a site that fails is a report with Valid false.
func AuditSite(ctx context.Context, root string, opts SiteAuditOptions) (*SiteReport, error) {
	if ctx == nil {
		return nil, errors.New("seo: site audit requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, SiteAuditTimeout)
	defer cancel()
	// WalkDir does not descend into a root that is itself a symbolic link, so the walk
	// starts from the resolved directory; the report keeps the path the caller named.
	resolved, err := util.ResolveExistingPath(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("seo: resolve site root: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("seo: site root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("seo: site root %q is not a directory", root)
	}
	audit := &siteAudit{ctx: ctx, root: resolved, options: opts, report: &SiteReport{Root: root, Sitemaps: []SitemapFile{}}}
	if err := filepath.WalkDir(resolved, audit.visit); err != nil {
		return nil, fmt.Errorf("seo: audit %q: %w", root, err)
	}
	if len(audit.report.Sitemaps) == 0 {
		audit.add(".", "no sitemap.xml or sitemap*.xml at the site root")
	}
	if opts.RequireRobots && !audit.report.Robots {
		audit.add("robots.txt", "robots.txt is required but absent from the site root")
	}
	audit.report.Valid = len(audit.report.Findings) == 0
	return audit.report, nil
}

// siteAudit carries one AuditSite walk.
type siteAudit struct {
	ctx     context.Context
	root    string
	entries int
	options SiteAuditOptions
	report  *SiteReport
}

func (a *siteAudit) visit(path string, entry fs.DirEntry, walkErr error) error {
	if err := a.ctx.Err(); err != nil {
		return err
	}
	if walkErr != nil {
		return walkErr
	}
	a.entries++
	if a.entries > MaxSiteEntries {
		return fmt.Errorf("site holds more than %d entries", MaxSiteEntries)
	}
	rel, err := filepath.Rel(a.root, path)
	if err != nil {
		return fmt.Errorf("relate %q to the site root: %w", path, err)
	}
	rel = filepath.ToSlash(rel)
	if strings.Count(rel, "/") >= MaxSiteDepth {
		return fmt.Errorf("%s is nested deeper than %d directories", rel, MaxSiteDepth)
	}
	if entry.IsDir() {
		return nil
	}
	a.auditFile(rel, strings.ToLower(entry.Name()))
	return nil
}

// auditFile dispatches one file to the check its name calls for.
func (a *siteAudit) auditFile(rel, lowerName string) {
	atRoot := !strings.Contains(rel, "/")
	switch {
	case strings.HasSuffix(lowerName, ".html") || strings.HasSuffix(lowerName, ".htm"):
		a.auditPage(rel)
	case atRoot && strings.HasPrefix(lowerName, "sitemap") && strings.HasSuffix(lowerName, ".xml"):
		a.auditSitemap(rel)
	case atRoot && lowerName == "robots.txt":
		a.auditRobots(rel)
	}
}

func (a *siteAudit) auditPage(rel string) {
	a.report.Pages++
	data, err := util.ReadConfinedLimited(a.root, filepath.FromSlash(rel), MaxPageBytes)
	if err != nil {
		a.add(rel, err.Error())
		return
	}
	if !a.options.AllowPlaceholders {
		if bytes.Contains(data, []byte("example-org/example-repo")) {
			a.add(rel, "carries unedited example-org/example-repo placeholder")
		}
		if bytes.Contains(data, []byte("PlaceholderLang")) {
			a.add(rel, "carries unedited PlaceholderLang placeholder")
		}
	}
	blocks, err := headJSONLD(data)
	if err != nil {
		a.add(rel, err.Error())
		return
	}
	if len(blocks) == 0 {
		a.add(rel, "no application/ld+json script in <head>")
		return
	}
	for i := 0; i < len(blocks) && i < MaxJSONLDBlocks; i++ {
		a.report.JSONLDBlocks++
		res, verr := ValidateJSONLD(blocks[i])
		a.addResult(rel, fmt.Sprintf("JSON-LD block %d: ", i+1), res.Errors, verr)
	}
}

func (a *siteAudit) auditSitemap(rel string) {
	file := SitemapFile{File: rel}
	data, err := util.ReadConfinedLimited(a.root, filepath.FromSlash(rel), MaxSitemapBytes)
	if err != nil {
		a.report.Sitemaps = append(a.report.Sitemaps, file)
		a.add(rel, err.Error())
		return
	}
	res, verr := ValidateSitemap(data)
	file.IsIndex, file.Entries = res.IsIndex, res.URLCount
	a.report.Sitemaps = append(a.report.Sitemaps, file)
	a.addResult(rel, "", res.Errors, verr)
}

func (a *siteAudit) auditRobots(rel string) {
	a.report.Robots = true
	data, err := util.ReadConfinedLimited(a.root, filepath.FromSlash(rel), MaxRobotsBytes)
	if err != nil {
		a.add(rel, err.Error())
		return
	}
	res, verr := ValidateRobotsTxt(string(data))
	a.addResult(rel, "", res.Errors, verr)
}

// addResult records a validator's errors. A validator that returns an error lists it in
// errs too; the error is recorded on its own only when errs is empty.
func (a *siteAudit) addResult(file, prefix string, errs []string, err error) {
	if len(errs) == 0 && err != nil {
		a.add(file, prefix+err.Error())
		return
	}
	for i := 0; i < len(errs); i++ {
		a.add(file, prefix+errs[i])
	}
}

// add records one finding, up to MaxSiteFindings.
func (a *siteAudit) add(file, message string) {
	if len(a.report.Findings) >= MaxSiteFindings {
		a.report.Truncated = true
		return
	}
	a.report.Findings = append(a.report.Findings, SiteFinding{File: file, Message: message})
}

// headJSONLD returns the trimmed body of every application/ld+json script in the page's
// <head>. The head ends at </head>, or at <body> when the optional end tag is omitted.
// Script bodies are skipped as raw text, so a "</head>" inside one does not end the head.
func headJSONLD(page []byte) ([][]byte, error) {
	lower := lowerASCII(page)
	start := findTag(lower, 0, "head")
	if start < 0 {
		return nil, errors.New("no <head> element")
	}
	pos := tagEnd(lower, start)
	if pos < 0 {
		return nil, errors.New("unterminated <head> tag")
	}
	var blocks [][]byte
	for n := 0; n < MaxHeadScripts; n++ {
		script := findTag(lower, pos, "script")
		if script < 0 || script > headEnd(lower, pos) {
			return blocks, nil
		}
		body, next, err := scriptBody(page, lower, script)
		if err != nil {
			return nil, err
		}
		if body != nil {
			if len(blocks) == MaxJSONLDBlocks {
				return nil, fmt.Errorf("<head> carries more than %d JSON-LD blocks", MaxJSONLDBlocks)
			}
			blocks = append(blocks, body)
		}
		pos = next
	}
	return nil, fmt.Errorf("<head> carries more than %d scripts", MaxHeadScripts)
}

// headEnd returns where the head closes at or after pos: the first </head> or <body>, or
// the end of the page when neither follows.
func headEnd(lower []byte, pos int) int {
	end := len(lower)
	if i := bytes.Index(lower[pos:], []byte("</head")); i >= 0 {
		end = pos + i
	}
	if i := findTag(lower, pos, "body"); i >= 0 && i < end {
		end = i
	}
	return end
}

// scriptBody reads the <script> element starting at start. It returns the trimmed body when
// the element is JSON-LD (nil otherwise) and the index just past its end tag.
func scriptBody(page, lower []byte, start int) ([]byte, int, error) {
	open := tagEnd(lower, start)
	if open < 0 {
		return nil, 0, errors.New("unterminated <script> tag in <head>")
	}
	closing := bytes.Index(lower[open:], []byte("</script"))
	if closing < 0 {
		return nil, 0, errors.New("unterminated <script> element in <head>")
	}
	closing += open
	next := closing + len("</script")
	if !strings.EqualFold(scriptType(page[start:open]), "application/ld+json") {
		return nil, next, nil
	}
	body := bytes.TrimSpace(page[open:closing])
	if body == nil {
		// TrimSpace returns nil for a blank body; an empty JSON-LD block is still a block,
		// and ValidateJSONLD reports it as an empty payload.
		body = []byte{}
	}
	return body, next, nil
}

// findTag returns the index of the first <name start tag at or after from, or -1. The name
// must end at whitespace, '/' or '>', so "<head" never matches "<header".
func findTag(lower []byte, from int, name string) int {
	needle := []byte("<" + name)
	for n := 0; n < len(lower) && from < len(lower); n++ {
		i := bytes.Index(lower[from:], needle)
		if i < 0 {
			return -1
		}
		at := from + i
		after := at + len(needle)
		if after >= len(lower) || isTagNameEnd(lower[after]) {
			return at
		}
		from = after
	}
	return -1
}

// tagEnd returns the index just past the '>' that closes the start tag at start, skipping
// a '>' inside a quoted attribute value, or -1 when the tag never closes.
func tagEnd(lower []byte, start int) int {
	var quote byte
	for i := start; i < len(lower); i++ {
		c := lower[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			return i + 1
		}
	}
	return -1
}

// scriptType returns the trimmed value of the type attribute of a <script ...> start tag,
// or "" when the tag carries none. Quoted and unquoted values are both read, because a
// minifier drops the quotes (type=application/ld+json).
func scriptType(tag []byte) string {
	i := len("<script")
	for n := 0; n < len(tag) && i < len(tag); n++ {
		var name, value string
		name, value, i = nextAttribute(tag, i)
		if strings.EqualFold(name, "type") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// nextAttribute reads the attribute at or after i and returns its name, its value and the
// index after it. It always advances, so a caller loop terminates.
func nextAttribute(tag []byte, i int) (name, value string, next int) {
	i = skipAttributeSpace(tag, i)
	start := i
	for i < len(tag) && !isAttributeNameEnd(tag[i]) {
		i++
	}
	name = string(tag[start:i])
	i = skipAttributeSpace(tag, i)
	if i >= len(tag) || tag[i] != '=' {
		if i == start {
			i++
		}
		return name, "", i
	}
	value, next = attributeValue(tag, skipAttributeSpace(tag, i+1))
	return name, value, next
}

// attributeValue reads a quoted or unquoted attribute value starting at i.
func attributeValue(tag []byte, i int) (string, int) {
	if i < len(tag) && (tag[i] == '"' || tag[i] == '\'') {
		end := bytes.IndexByte(tag[i+1:], tag[i])
		if end < 0 {
			return string(tag[i+1:]), len(tag)
		}
		return string(tag[i+1 : i+1+end]), i + 2 + end
	}
	start := i
	for i < len(tag) && !isHTMLSpace(tag[i]) && tag[i] != '>' {
		i++
	}
	return string(tag[start:i]), i
}

// skipAttributeSpace skips whitespace and the '/' of a self-closing tag.
func skipAttributeSpace(tag []byte, i int) int {
	for i < len(tag) && (isHTMLSpace(tag[i]) || tag[i] == '/') {
		i++
	}
	return i
}

func isAttributeNameEnd(c byte) bool {
	return isHTMLSpace(c) || c == '=' || c == '>' || c == '/'
}

func isTagNameEnd(c byte) bool {
	return isHTMLSpace(c) || c == '/' || c == '>'
}

// isHTMLSpace reports the ASCII whitespace the HTML tokenizer separates attributes on.
func isHTMLSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

// lowerASCII lowercases ASCII letters only, so every index into the result is an index
// into b; bytes.ToLower would re-encode some non-ASCII runes at a different length.
func lowerASCII(b []byte) []byte {
	out := make([]byte, len(b))
	inComment := false
	for i := 0; i < len(b); i++ {
		if !inComment && bytes.HasPrefix(b[i:], []byte("<!--")) {
			inComment = true
		}
		c := b[i]
		if inComment {
			out[i] = ' '
			if bytes.HasSuffix(b[:i+1], []byte("-->")) {
				inComment = false
			}
		} else {
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			out[i] = c
		}
	}
	return out
}

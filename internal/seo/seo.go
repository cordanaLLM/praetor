package seo

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Invariant bounds adhering to HISS-02.
const (
	MaxURLLimit       = 50000
	MaxRobotsLines    = 5000
	MaxFieldLength    = 4096
	StandardSitemapNS = "http://www.sitemaps.org/schemas/sitemap/0.9"
)

// Allowed change frequencies for sitemaps.
var validChangeFreqs = map[string]struct{}{
	"always":  {},
	"hourly":  {},
	"daily":   {},
	"weekly":  {},
	"monthly": {},
	"yearly":  {},
	"never":   {},
}

// TechArticle represents a Schema.org TechArticle JSON-LD entity.
type TechArticle struct {
	Context          string      `json:"@context"`
	Type             string      `json:"@type"`
	Headline         string      `json:"headline"`
	Description      string      `json:"description"`
	Author           interface{} `json:"author,omitempty"`
	DatePublished    string      `json:"datePublished,omitempty"`
	DateModified     string      `json:"dateModified,omitempty"`
	InLanguage       string      `json:"inLanguage,omitempty"`
	ProficiencyLevel string      `json:"proficiencyLevel,omitempty"`
}

// SoftwareSourceCode represents a Schema.org SoftwareSourceCode JSON-LD entity.
type SoftwareSourceCode struct {
	Context             string `json:"@context"`
	Type                string `json:"@type"`
	Name                string `json:"name"`
	ProgrammingLanguage string `json:"programmingLanguage"`
	CodeRepository      string `json:"codeRepository"`
	RuntimePlatform     string `json:"runtimePlatform,omitempty"`
	License             string `json:"license,omitempty"`
	Description         string `json:"description,omitempty"`
}

// JSONLDValidationResult encapsulates the result of JSON-LD schema validation.
type JSONLDValidationResult struct {
	Valid      bool     `json:"valid"`
	SchemaType string   `json:"schema_type"`
	Errors     []string `json:"errors,omitempty"`
}

// SitemapURL represents an entry in a sitemap urlset.
type SitemapURL struct {
	Loc        string   `xml:"loc"`
	LastMod    string   `xml:"lastmod,omitempty"`
	ChangeFreq string   `xml:"changefreq,omitempty"`
	Priority   *float64 `xml:"priority,omitempty"`
}

// URLSet is the root element of a sitemap.xml file.
type URLSet struct {
	XMLName   xml.Name     `xml:"urlset"`
	Namespace string       `xml:"xmlns,attr"`
	URLs      []SitemapURL `xml:"url"`
}

// SitemapEntry represents a sub-sitemap link within a sitemapindex.
type SitemapEntry struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

// SitemapIndex represents the root element of an index sitemap.
type SitemapIndex struct {
	XMLName   xml.Name       `xml:"sitemapindex"`
	Namespace string         `xml:"xmlns,attr"`
	Sitemaps  []SitemapEntry `xml:"sitemap"`
}

// SitemapValidationResult holds the result of sitemap.xml validation.
type SitemapValidationResult struct {
	Valid    bool     `json:"valid"`
	IsIndex  bool     `json:"is_index"`
	URLCount int      `json:"url_count"`
	Errors   []string `json:"errors,omitempty"`
}

// RobotsRule groups allow/disallow paths for a user agent.
type RobotsRule struct {
	UserAgent  string   `json:"user_agent"`
	Allows     []string `json:"allows"`
	Disallows  []string `json:"disallows"`
	CrawlDelay float64  `json:"crawl_delay,omitempty"`
}

// RobotsValidationResult holds the result of robots.txt validation.
type RobotsValidationResult struct {
	Valid      bool         `json:"valid"`
	UserAgents []string     `json:"user_agents"`
	Sitemaps   []string     `json:"sitemaps"`
	Rules      []RobotsRule `json:"rules"`
	Errors     []string     `json:"errors,omitempty"`
}

// ValidateTechArticle validates a TechArticle struct.
func ValidateTechArticle(raw []byte) (*JSONLDValidationResult, error) {
	if len(raw) == 0 {
		return &JSONLDValidationResult{Valid: false, Errors: []string{"empty payload"}}, errors.New("empty payload")
	}

	var article TechArticle
	if err := json.Unmarshal(raw, &article); err != nil {
		return &JSONLDValidationResult{Valid: false, Errors: []string{err.Error()}}, fmt.Errorf("invalid json: %w", err)
	}

	res := &JSONLDValidationResult{SchemaType: "TechArticle", Valid: true}
	validateContext(article.Context, res)

	if article.Type != "TechArticle" {
		res.Errors = append(res.Errors, fmt.Sprintf("expected @type 'TechArticle', got '%s'", article.Type))
	}
	if strings.TrimSpace(article.Headline) == "" {
		res.Errors = append(res.Errors, "headline is required and cannot be empty")
	}
	if strings.TrimSpace(article.Description) == "" {
		res.Errors = append(res.Errors, "description is required and cannot be empty")
	}
	validateDateField(article.DatePublished, "datePublished", res)
	validateDateField(article.DateModified, "dateModified", res)

	res.Valid = len(res.Errors) == 0
	return res, nil
}

// ValidateSoftwareSourceCode validates a SoftwareSourceCode struct.
func ValidateSoftwareSourceCode(raw []byte) (*JSONLDValidationResult, error) {
	if len(raw) == 0 {
		return &JSONLDValidationResult{Valid: false, Errors: []string{"empty payload"}}, errors.New("empty payload")
	}

	var code SoftwareSourceCode
	if err := json.Unmarshal(raw, &code); err != nil {
		return &JSONLDValidationResult{Valid: false, Errors: []string{err.Error()}}, fmt.Errorf("invalid json: %w", err)
	}

	res := &JSONLDValidationResult{SchemaType: "SoftwareSourceCode", Valid: true}
	validateContext(code.Context, res)

	if code.Type != "SoftwareSourceCode" {
		res.Errors = append(res.Errors, fmt.Sprintf("expected @type 'SoftwareSourceCode', got '%s'", code.Type))
	}
	if strings.TrimSpace(code.Name) == "" {
		res.Errors = append(res.Errors, "name is required and cannot be empty")
	}
	if strings.TrimSpace(code.ProgrammingLanguage) == "" {
		res.Errors = append(res.Errors, "programmingLanguage is required and cannot be empty")
	}
	validateAbsoluteURL(code.CodeRepository, "codeRepository", res)

	res.Valid = len(res.Errors) == 0
	return res, nil
}

// ValidateJSONLD automatically detects and validates known Schema.org entities.
func ValidateJSONLD(raw []byte) (*JSONLDValidationResult, error) {
	if len(raw) == 0 {
		return &JSONLDValidationResult{Valid: false, Errors: []string{"empty payload"}}, errors.New("empty payload")
	}

	var typeDetector struct {
		Type string `json:"@type"`
	}
	if err := json.Unmarshal(raw, &typeDetector); err != nil {
		return &JSONLDValidationResult{Valid: false, Errors: []string{err.Error()}}, fmt.Errorf("invalid json: %w", err)
	}

	switch typeDetector.Type {
	case "TechArticle":
		return ValidateTechArticle(raw)
	case "SoftwareSourceCode":
		return ValidateSoftwareSourceCode(raw)
	default:
		return &JSONLDValidationResult{
			Valid:      false,
			SchemaType: typeDetector.Type,
			Errors:     []string{fmt.Sprintf("unsupported or missing @type: '%s'", typeDetector.Type)},
		}, nil
	}
}

// ValidateSitemap parses and validates a sitemap.xml or sitemapindex.
func ValidateSitemap(raw []byte) (*SitemapValidationResult, error) {
	if len(raw) == 0 {
		return &SitemapValidationResult{Valid: false, Errors: []string{"empty sitemap"}}, errors.New("empty sitemap")
	}

	var urlSet URLSet
	if err := xml.Unmarshal(raw, &urlSet); err == nil && urlSet.XMLName.Local == "urlset" {
		return validateURLSet(&urlSet)
	}

	var sitemapIndex SitemapIndex
	if err := xml.Unmarshal(raw, &sitemapIndex); err == nil && sitemapIndex.XMLName.Local == "sitemapindex" {
		return validateSitemapIndex(&sitemapIndex)
	}

	return &SitemapValidationResult{
		Valid:  false,
		Errors: []string{"document root must be <urlset> or <sitemapindex>"},
	}, fmt.Errorf("unrecognized sitemap xml structure")
}

func validateURLSet(u *URLSet) (*SitemapValidationResult, error) {
	res := &SitemapValidationResult{Valid: true, IsIndex: false, URLCount: len(u.URLs)}
	validateSitemapNamespace("urlset", u.XMLName.Space, res)
	if len(u.URLs) > MaxURLLimit {
		res.Errors = append(res.Errors, fmt.Sprintf("url count %d exceeds maximum limit of %d", len(u.URLs), MaxURLLimit))
	}

	for i := 0; i < len(u.URLs) && i < MaxURLLimit; i++ {
		validateSitemapURL(i, u.URLs[i], res)
	}

	res.Valid = len(res.Errors) == 0
	return res, nil
}

// validateSitemapURL checks one <url> entry of a urlset.
func validateSitemapURL(i int, entry SitemapURL, res *SitemapValidationResult) {
	label := fmt.Sprintf("url[%d]", i)
	if withinFieldLimit(label, "loc", entry.Loc, res) && !isValidAbsoluteURL(entry.Loc) {
		res.Errors = append(res.Errors, fmt.Sprintf("%s: invalid absolute loc: '%s'", label, entry.Loc))
	}
	withinFieldLimit(label, "lastmod", entry.LastMod, res)
	if entry.ChangeFreq != "" && withinFieldLimit(label, "changefreq", entry.ChangeFreq, res) {
		if _, ok := validChangeFreqs[strings.ToLower(entry.ChangeFreq)]; !ok {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: invalid changefreq '%s'", label, entry.ChangeFreq))
		}
	}
	if entry.Priority != nil && (*entry.Priority < 0.0 || *entry.Priority > 1.0) {
		res.Errors = append(res.Errors, fmt.Sprintf("%s: priority %0.2f out of bounds [0.0, 1.0]", label, *entry.Priority))
	}
}

func validateSitemapIndex(idx *SitemapIndex) (*SitemapValidationResult, error) {
	res := &SitemapValidationResult{Valid: true, IsIndex: true, URLCount: len(idx.Sitemaps)}
	validateSitemapNamespace("sitemapindex", idx.XMLName.Space, res)
	if len(idx.Sitemaps) > MaxURLLimit {
		res.Errors = append(res.Errors, fmt.Sprintf("sitemap count %d exceeds maximum limit of %d", len(idx.Sitemaps), MaxURLLimit))
	}

	for i := 0; i < len(idx.Sitemaps) && i < MaxURLLimit; i++ {
		entry := idx.Sitemaps[i]
		label := fmt.Sprintf("sitemap[%d]", i)
		if withinFieldLimit(label, "loc", entry.Loc, res) && !isValidAbsoluteURL(entry.Loc) {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: invalid absolute loc: '%s'", label, entry.Loc))
		}
		withinFieldLimit(label, "lastmod", entry.LastMod, res)
	}

	res.Valid = len(res.Errors) == 0
	return res, nil
}

// validateSitemapNamespace requires the sitemaps.org namespace on the root element. space
// is the resolved namespace (xml.Name.Space), so a prefixed declaration is accepted too;
// crawlers ignore a sitemap declared in any other namespace, or in none.
func validateSitemapNamespace(root, space string, res *SitemapValidationResult) {
	if withinFieldLimit("<"+root+">", "namespace", space, res) && space != StandardSitemapNS {
		res.Errors = append(res.Errors, fmt.Sprintf("<%s> namespace must be '%s', got '%s'", root, StandardSitemapNS, space))
	}
}

// withinFieldLimit reports a sitemap field longer than MaxFieldLength bytes, without
// echoing it, and returns false so the caller skips the checks that would quote it.
func withinFieldLimit(label, field, value string, res *SitemapValidationResult) bool {
	if len(value) <= MaxFieldLength {
		return true
	}
	res.Errors = append(res.Errors, fmt.Sprintf("%s: %s is %d bytes, maximum %d", label, field, len(value), MaxFieldLength))
	return false
}

// ValidateRobotsTxt parses and validates standard robots.txt format.
func ValidateRobotsTxt(content string) (*RobotsValidationResult, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return &RobotsValidationResult{Valid: false, Errors: []string{"empty robots.txt"}}, errors.New("empty robots.txt")
	}

	lines := robotsLines(content)
	res := &RobotsValidationResult{Valid: true}
	var currentRule *RobotsRule

	for i := 0; i < len(lines) && i < MaxRobotsLines; i++ {
		currentRule = processRobotsLine(lines[i], i+1, currentRule, res)
	}

	if currentRule != nil {
		res.Rules = append(res.Rules, *currentRule)
	}

	// Lines past the bound were never inspected, so the file cannot be reported valid.
	if len(lines) > MaxRobotsLines {
		res.Errors = append(res.Errors, fmt.Sprintf("robots.txt has %d lines; only the first %d were validated", len(lines), MaxRobotsLines))
	}
	if len(res.UserAgents) == 0 {
		res.Errors = append(res.Errors, "missing required User-agent directive")
	}

	res.Valid = len(res.Errors) == 0
	return res, nil
}

// robotsLines splits content into lines; the newline that ends the last line does not
// start another one.
func robotsLines(content string) []string {
	lines := strings.Split(content, "\n")
	if n := len(lines); n > 1 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// processRobotsLine parses one line. RFC 9309 section 2.2 ends every line with an optional
// '#' comment (EOL = *WS [comment] NL) and path patterns cannot contain '#', so everything
// from the first '#' is dropped before the directive is split.
func processRobotsLine(raw string, lineNum int, current *RobotsRule, res *RobotsValidationResult) *RobotsRule {
	line, _, _ := strings.Cut(raw, "#")
	line = strings.TrimSpace(line)
	if line == "" {
		return current
	}
	if len(line) > MaxFieldLength {
		res.Errors = append(res.Errors, fmt.Sprintf("line %d: directive is %d bytes, maximum %d", lineNum, len(line), MaxFieldLength))
		return current
	}
	key, val, found := strings.Cut(line, ":")
	if !found {
		res.Errors = append(res.Errors, fmt.Sprintf("line %d: malformed directive '%s'", lineNum, line))
		return current
	}
	return processRobotsDirective(strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(val), lineNum, current, res)
}

func processRobotsDirective(key, val string, lineNum int, current *RobotsRule, res *RobotsValidationResult) *RobotsRule {
	switch key {
	case "user-agent":
		if current != nil {
			res.Rules = append(res.Rules, *current)
		}
		res.UserAgents = append(res.UserAgents, val)
		return &RobotsRule{UserAgent: val}
	case "allow", "disallow":
		addRulePath(key, val, lineNum, current, res)
	case "sitemap":
		if !isValidAbsoluteURL(val) {
			res.Errors = append(res.Errors, fmt.Sprintf("line %d: invalid sitemap url '%s'", lineNum, val))
		} else {
			res.Sitemaps = append(res.Sitemaps, val)
		}
	case "crawl-delay":
		processCrawlDelay(val, lineNum, current, res)
	default:
		// Unknown directive
		res.Errors = append(res.Errors, fmt.Sprintf("line %d: unknown directive '%s'", lineNum, key))
	}
	return current
}

// addRulePath records an Allow or Disallow path on the current group.
func addRulePath(key, val string, lineNum int, current *RobotsRule, res *RobotsValidationResult) {
	if current == nil {
		res.Errors = append(res.Errors, orphanDirective(key, lineNum))
		return
	}
	if key == "allow" {
		current.Allows = append(current.Allows, val)
		return
	}
	current.Disallows = append(current.Disallows, val)
}

// orphanDirective reports a group directive that precedes every User-agent line. RFC 9309
// section 2.2 tells crawlers to ignore such a rule, so the author's intent is silently lost.
func orphanDirective(key string, lineNum int) string {
	return fmt.Sprintf("line %d: %s directive appears before any User-agent line and applies to no group", lineNum, key)
}

func processCrawlDelay(val string, lineNum int, current *RobotsRule, res *RobotsValidationResult) {
	if current == nil {
		res.Errors = append(res.Errors, orphanDirective("crawl-delay", lineNum))
		return
	}
	delay, err := strconv.ParseFloat(val, 64)
	if err != nil || !(delay >= 0) {
		res.Errors = append(res.Errors, fmt.Sprintf("line %d: invalid crawl-delay '%s'", lineNum, val))
		return
	}
	current.CrawlDelay = delay
}

func validateContext(ctx string, res *JSONLDValidationResult) {
	if ctx != "https://schema.org" && ctx != "http://schema.org" {
		res.Errors = append(res.Errors, fmt.Sprintf("expected @context 'https://schema.org', got '%s'", ctx))
	}
}

func validateDateField(d string, fieldName string, res *JSONLDValidationResult) {
	if strings.TrimSpace(d) == "" {
		return
	}
	// Accept RFC3339 or ISO 8601 YYYY-MM-DD
	if _, err := time.Parse(time.RFC3339, d); err == nil {
		return
	}
	if _, err := time.Parse("2006-01-02", d); err == nil {
		return
	}
	res.Errors = append(res.Errors, fmt.Sprintf("%s must be valid ISO-8601 (RFC3339 or YYYY-MM-DD), got '%s'", fieldName, d))
}

func validateAbsoluteURL(rawURL string, fieldName string, res *JSONLDValidationResult) {
	if !isValidAbsoluteURL(rawURL) {
		res.Errors = append(res.Errors, fmt.Sprintf("%s must be a valid absolute URL (http/https), got '%s'", fieldName, rawURL))
	}
}

func isValidAbsoluteURL(raw string) bool {
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

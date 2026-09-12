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
	if len(u.URLs) > MaxURLLimit {
		res.Errors = append(res.Errors, fmt.Sprintf("url count %d exceeds maximum limit of %d", len(u.URLs), MaxURLLimit))
	}

	for i := 0; i < len(u.URLs) && i < MaxURLLimit; i++ {
		entry := u.URLs[i]
		if !isValidAbsoluteURL(entry.Loc) {
			res.Errors = append(res.Errors, fmt.Sprintf("url[%d]: invalid absolute loc: '%s'", i, entry.Loc))
		}
		if entry.ChangeFreq != "" {
			if _, ok := validChangeFreqs[strings.ToLower(entry.ChangeFreq)]; !ok {
				res.Errors = append(res.Errors, fmt.Sprintf("url[%d]: invalid changefreq '%s'", i, entry.ChangeFreq))
			}
		}
		if entry.Priority != nil {
			if *entry.Priority < 0.0 || *entry.Priority > 1.0 {
				res.Errors = append(res.Errors, fmt.Sprintf("url[%d]: priority %0.2f out of bounds [0.0, 1.0]", i, *entry.Priority))
			}
		}
	}

	res.Valid = len(res.Errors) == 0
	return res, nil
}

func validateSitemapIndex(idx *SitemapIndex) (*SitemapValidationResult, error) {
	res := &SitemapValidationResult{Valid: true, IsIndex: true, URLCount: len(idx.Sitemaps)}
	if len(idx.Sitemaps) > MaxURLLimit {
		res.Errors = append(res.Errors, fmt.Sprintf("sitemap count %d exceeds maximum limit of %d", len(idx.Sitemaps), MaxURLLimit))
	}

	for i := 0; i < len(idx.Sitemaps) && i < MaxURLLimit; i++ {
		entry := idx.Sitemaps[i]
		if !isValidAbsoluteURL(entry.Loc) {
			res.Errors = append(res.Errors, fmt.Sprintf("sitemap[%d]: invalid absolute loc: '%s'", i, entry.Loc))
		}
	}

	res.Valid = len(res.Errors) == 0
	return res, nil
}

// ValidateRobotsTxt parses and validates standard robots.txt format.
func ValidateRobotsTxt(content string) (*RobotsValidationResult, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return &RobotsValidationResult{Valid: false, Errors: []string{"empty robots.txt"}}, errors.New("empty robots.txt")
	}

	lines := strings.Split(content, "\n")
	res := &RobotsValidationResult{Valid: true}
	var currentRule *RobotsRule

	for i := 0; i < len(lines) && i < MaxRobotsLines; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			res.Errors = append(res.Errors, fmt.Sprintf("line %d: malformed directive '%s'", i+1, line))
			continue
		}

		key := strings.ToLower(strings.TrimSpace(parts[0]))
		val := strings.TrimSpace(parts[1])

		currentRule = processRobotsDirective(key, val, i+1, currentRule, res)
	}

	if currentRule != nil {
		res.Rules = append(res.Rules, *currentRule)
	}

	if len(res.UserAgents) == 0 {
		res.Errors = append(res.Errors, "missing required User-agent directive")
	}

	res.Valid = len(res.Errors) == 0
	return res, nil
}

func processRobotsDirective(key, val string, lineNum int, current *RobotsRule, res *RobotsValidationResult) *RobotsRule {
	switch key {
	case "user-agent":
		if current != nil {
			res.Rules = append(res.Rules, *current)
		}
		res.UserAgents = append(res.UserAgents, val)
		return &RobotsRule{UserAgent: val}
	case "allow":
		if current != nil {
			current.Allows = append(current.Allows, val)
		}
	case "disallow":
		if current != nil {
			current.Disallows = append(current.Disallows, val)
		}
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

func processCrawlDelay(val string, lineNum int, current *RobotsRule, res *RobotsValidationResult) {
	if current == nil {
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

package seo

import (
	"strings"
	"testing"
)

// fuzzInputLimit bounds one fuzz input (HISS-02); it stays above the validators' own
// bounds so the truncation paths are reachable.
const fuzzInputLimit = 65536

// checkValidity asserts the contract every validator result keeps: a result always
// comes back, Valid is exactly "no errors", and a returned error is never Valid.
func checkValidity(t *testing.T, name string, valid bool, errs []string, err error) {
	t.Helper()
	if valid != (len(errs) == 0) {
		t.Fatalf("%s: Valid=%v disagrees with %d errors %q", name, valid, len(errs), errs)
	}
	if err != nil && valid {
		t.Fatalf("%s: returned error %v with a valid result", name, err)
	}
}

func FuzzValidateJSONLD(f *testing.F) {
	f.Add([]byte(`{"@context":"https://schema.org","@type":"TechArticle","headline":"Hello","description":"World"}`))
	f.Add([]byte(`{"@context":"https://schema.org","@type":"SoftwareSourceCode","name":"Praetor","codeRepository":"https://github.com/cordanaLLM/praetor"}`))
	f.Add([]byte(`{"@type":"TechArticle","datePublished":"not-a-date"}`))
	f.Add([]byte(`{"@type":"Person"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > fuzzInputLimit {
			data = data[:fuzzInputLimit]
		}
		res, err := ValidateJSONLD(data)
		if res == nil {
			t.Fatalf("ValidateJSONLD returned a nil result (err %v)", err)
		}
		checkValidity(t, "ValidateJSONLD", res.Valid, res.Errors, err)
		if res.Valid && res.SchemaType != "TechArticle" && res.SchemaType != "SoftwareSourceCode" {
			t.Fatalf("unsupported schema type %q reported valid", res.SchemaType)
		}
	})
}

func FuzzValidateRobotsTxt(f *testing.F) {
	f.Add("User-agent: *\nDisallow: /admin/\nSitemap: https://example.com/sitemap.xml\n")
	f.Add("User-agent: Googlebot\nAllow: /\nCrawl-delay: 1.5\n")
	f.Add("Disallow: /orphan # before any group\nUser-agent: *\nAllow: /a#b\n")
	f.Add(strings.Repeat("\n", MaxRobotsLines) + "User-agent: *\n")

	f.Fuzz(func(t *testing.T, content string) {
		if len(content) > fuzzInputLimit {
			content = content[:fuzzInputLimit]
		}
		res, err := ValidateRobotsTxt(content)
		if res == nil {
			t.Fatalf("ValidateRobotsTxt returned a nil result (err %v)", err)
		}
		checkValidity(t, "ValidateRobotsTxt", res.Valid, res.Errors, err)
		// Every User-agent line opens exactly one group, so the two lists stay paired.
		if len(res.Rules) != len(res.UserAgents) {
			t.Fatalf("%d rules for %d user agents", len(res.Rules), len(res.UserAgents))
		}
		if res.Valid && len(res.UserAgents) == 0 {
			t.Fatal("robots.txt without a User-agent reported valid")
		}
		if res.Valid && len(robotsLines(content)) > MaxRobotsLines {
			t.Fatal("robots.txt longer than MaxRobotsLines reported valid")
		}
		for i := 0; i < len(res.Rules); i++ {
			paths := append(append([]string{}, res.Rules[i].Allows...), res.Rules[i].Disallows...)
			for _, p := range paths {
				if strings.Contains(p, "#") {
					t.Fatalf("rule path %q kept comment text", p)
				}
			}
		}
	})
}

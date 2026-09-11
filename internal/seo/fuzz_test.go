package seo

import (
	"testing"
)

func FuzzValidateJSONLD(f *testing.F) {
	f.Add([]byte(`{"@context":"https://schema.org","@type":"TechArticle","headline":"Hello","description":"World"}`))
	f.Add([]byte(`{"@context":"https://schema.org","@type":"SoftwareSourceCode","name":"Praetor","codeRepository":"https://github.com/cordanaLLM/praetor"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 65536 {
			data = data[:65536]
		}
		res, err := ValidateJSONLD(data)
		if err != nil && res == nil {
			t.Fatal("ValidateJSONLD returned nil result on error")
		}
	})
}

func FuzzValidateRobotsTxt(f *testing.F) {
	f.Add("User-agent: *\nDisallow: /admin/\nSitemap: https://example.com/sitemap.xml\n")
	f.Add("User-agent: Googlebot\nAllow: /\nCrawl-delay: 1.5\n")

	f.Fuzz(func(t *testing.T, content string) {
		if len(content) > 65536 {
			content = content[:65536]
		}
		res, err := ValidateRobotsTxt(content)
		if err != nil && res == nil {
			t.Fatal("ValidateRobotsTxt returned nil result on error")
		}
	})
}

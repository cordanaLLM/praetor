package adopt

import (
	"bytes"
	"fmt"
	"text/template"
)

// TemplateContext holds variables for synthesizing repository assets across archetypes.
type TemplateContext struct {
	RepoName          string `json:"repo_name"`
	Owner             string `json:"owner"`
	Archetype         string `json:"archetype"`
	Runtime           string `json:"runtime"`
	VerifyCmd         string `json:"verify_cmd"`
	TestCmd           string `json:"test_cmd"`
	RunnerTag         string `json:"runner_tag"`
	HasGPU            bool   `json:"has_gpu"`
	SLSALevel         int    `json:"slsa_level"`
	CopyrightHolder   string `json:"copyright_holder"`
	LicenseIdentifier string `json:"license_identifier"`
}

// RenderTemplate parses and executes a Go template string with the provided context.
func RenderTemplate(name, tmplStr string, ctx TemplateContext) (string, error) {
	if tmplStr == "" {
		return "", fmt.Errorf("template content cannot be empty")
	}

	tmpl, err := template.New(name).Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parse template %s: %w", name, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("execute template %s: %w", name, err)
	}

	return buf.String(), nil
}

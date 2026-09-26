// Package templates is the single source of every file body praetor scaffolds into an
// adopting repository, and of the renderer that turns a body into a file.
//
// Each shipped body is a file below this directory, compiled into the binary with go:embed,
// so the file a reviewer reads here is byte for byte the file an adopter receives. Before
// this package existed nothing read templates/: internal/flavor emitted hard-coded Go
// strings, and the .tmpl files drifted from them unseen (#336).
package templates

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"text/template"
)

// Directory is the repository-relative home of the shipped templates, SourceFile the Go file
// whose go:embed directive embeds them, and Pattern that directive's pattern. The devcontainer
// bootstrap reads all three to capture the embedded files beside the Go source, so a binary
// built from its archive embeds the same bodies (internal/devcontainer/bootstrap_source.go).
const (
	Directory  = "templates"
	SourceFile = Directory + "/embed.go"
	Pattern    = "*/*.tmpl"
)

// shipped holds every template body. The pattern names one directory level of *.tmpl files
// on purpose: a bare directory pattern skips dot-files, and go/.golangci.yml.tmpl is one.
// It must stay equal to Pattern; the bootstrap refuses any other directive in SourceFile.
//
//go:embed */*.tmpl
var shipped embed.FS

// FileLeftDelim and FileRightDelim delimit actions inside an embedded template file.
//
// They are not text/template's "{{" and "}}" because the shipped workflows carry GitHub
// Actions expressions such as ${{ runner.os }}, which the default delimiters would parse as
// template actions and fail on. No shipped body uses either sequence as literal text, and
// TestEveryShippedTemplateRenders fails the day one does. A maintainer's note that must not
// reach adopters is a template comment: "<%- /* note */ -%>" renders to nothing.
const (
	FileLeftDelim  = "<%"
	FileRightDelim = "%>"
)

// Context holds the variables a template renders against. A template naming a field that is
// not declared here fails to render, which is how a placeholder such as {{.Repo}} is caught
// by the tests instead of by an adopter (BUG-582).
type Context struct {
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

// Render parses and executes an inline template string with text/template's default
// delimiters. It serves generators whose template is a Go constant, such as the agent
// harness in internal/adopt.
func Render(name, text string, ctx Context) (string, error) {
	return execute(name, text, "", "", ctx)
}

// RenderFile renders one embedded template, named by its path below this directory
// ("go/ci-go.yml.tmpl"), with FileLeftDelim and FileRightDelim.
func RenderFile(name string, ctx Context) (string, error) {
	body, err := shipped.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("read template %s: %w", name, err)
	}
	return execute(name, string(body), FileLeftDelim, FileRightDelim, ctx)
}

// Names returns the path of every embedded template in lexical order.
func Names() ([]string, error) {
	names, err := fs.Glob(shipped, Pattern)
	if err != nil {
		return nil, fmt.Errorf("list embedded templates: %w", err)
	}
	return names, nil
}

// execute is the one parse-and-execute path behind Render and RenderFile. An empty
// template is refused: it renders an empty file, which is a placeholder, not content.
func execute(name, text, left, right string, ctx Context) (string, error) {
	if text == "" {
		return "", fmt.Errorf("template %s: content cannot be empty", name)
	}
	tmpl, err := template.New(name).Delims(left, right).Parse(text)
	if err != nil {
		return "", fmt.Errorf("parse template %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("execute template %s: %w", name, err)
	}
	return buf.String(), nil
}

// Package templates holds the file bodies flavor apply scaffolds into a repository
// (TemplateItem.Source in internal/flavor) and the renderer that turns a body into a file.
// Adoption's own artefacts -- the Makefile, CONTRIBUTING, labels, the pull request and ADR
// templates, SECURITY and the agent harness -- are still built in internal/adopt rather than
// kept here; of those, only the agent harness header and footer go through Render
// (internal/adopt/harness.go).
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
	"strings"
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

	// DartLints names the lint package whose rule set the Dart analyzer config
	// (flutter/analysis_options.yaml.tmpl) includes: "flutter_lints", "lints", or "" for a
	// config that includes none. flavor apply reads it from pubspec.yaml
	// (internal/flavor/dart_lints.go).
	DartLints string `json:"dart_lints"`

	// RustEdition is the edition the scaffolded rustfmt.toml (rust/rustfmt.toml.tmpl) declares:
	// the one every crate of the root Cargo.toml's workspace is on, or "" for a config that
	// declares none, where no crate declares one and Cargo and rustfmt both use 2015. flavor
	// apply reads it from the workspace's manifests (internal/flavor/rustfmt.go).
	RustEdition string `json:"rust_edition"`

	// GoSecurity selects the scanner installation rendered by
	// go/security-go.yml.tmpl. Flavor apply derives it from tools/go/go.mod
	// (internal/flavor/security_go.go); the zero value is the standalone,
	// pinned fallback used by repositories without a scanner tools module.
	GoSecurity GoSecurityContext `json:"go_security"`

	// Node is what the Node CI body (node/ci-node.yml.tmpl) installs and runs scripts with.
	// flavor apply reads it from package.json and the committed lockfile
	// (internal/flavor/node_ci.go); the zero value renders the npm job.
	Node NodeContext `json:"node"`

	// UIRoutes lists the route paths the frontend-svelte accessibility and responsiveness
	// specs scan (order engine-fr-rule-checks-20261027). The zero value renders ["/"]
	// (RenderFile).
	UIRoutes []string `json:"ui_routes"`

	// UIAPIPattern is the route mock glob the frontend-svelte responsiveness spec intercepts
	// for its slow-API and HTTP 503 fault cases (order engine-fr-rule-checks-20261027). The
	// zero value renders "**/api/**" (RenderFile).
	UIAPIPattern string `json:"ui_api_pattern"`
}

// GoSecurityContext describes which pinned scanner installation an adopted Go repository can
// use. DeclaredScanners is true only when its tools module declares both scanner commands.
type GoSecurityContext struct {
	DeclaredScanners bool `json:"declared_scanners"`
}

// Former scanner version pins of the fallback Security Scan (order
// finding-landing-failed-79e5b1160d-20261026-1022).
const (
	// GoSecurityGovulnVersion was the golang.org/x/vuln version the fallback Security Scan built.
	//
	// Deprecated: frozen at its value of 2026-10-26 and no longer updated. The engine reads its
	// scanner pins from an internal package now, so a version update no longer changes the
	// exported API; the name stays until a breaking release removes it.
	GoSecurityGovulnVersion = "v1.8.0"

	// GoSecurityGosecVersion was the github.com/securego/gosec/v2 version the fallback Security Scan built.
	//
	// Deprecated: frozen at its value of 2026-10-26 and no longer updated. The engine reads its
	// scanner pins from an internal package now, so a version update no longer changes the
	// exported API; the name stays until a breaking release removes it.
	GoSecurityGosecVersion = "v2.29.0"

	// GoSecurityGitleaksVersion was the github.com/zricethezav/gitleaks/v8 version the fallback Security Scan built.
	//
	// Deprecated: frozen at its value of 2026-10-26 and no longer updated. The engine reads its
	// scanner pins from an internal package now, so a version update no longer changes the
	// exported API; the name stays until a breaking release removes it.
	GoSecurityGitleaksVersion = "v8.30.1"

	// GoSecurityXToolsVersion was the golang.org/x/tools version the fallback Security Scan built.
	//
	// Deprecated: frozen at its value of 2026-10-26 and no longer updated. The engine reads its
	// scanner pins from an internal package now, so a version update no longer changes the
	// exported API; the name stays until a breaking release removes it.
	GoSecurityXToolsVersion = "v0.50.0"

	// GoSecurityXToolsFloor was the oldest golang.org/x/tools that decodes go1.27.2 export data.
	//
	// Deprecated: frozen at its value of 2026-10-26 and no longer updated. The engine reads its
	// scanner pins from an internal package now, so a version update no longer changes the
	// exported API; the name stays until a breaking release removes it.
	GoSecurityXToolsFloor = "v0.50.0"
)

// NodeContext describes a repository's Node package manager to the Node CI body.
type NodeContext struct {
	// Manager is "npm" (or ""), "pnpm", "yarn" or "bun".
	Manager string `json:"manager"`
	// YarnBerry selects Yarn 2 or later, whose locked install is `yarn install --immutable`;
	// Yarn 1's is `yarn install --frozen-lockfile`.
	YarnBerry bool `json:"yarn_berry"`
	// Lint and Build report a lint or build script that can run. Yarn has no --if-present, so
	// the Yarn job runs only these; the npm, pnpm and Bun jobs run whichever is present at run
	// time.
	Lint  bool `json:"lint"`
	Build bool `json:"build"`
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
	if len(ctx.UIRoutes) == 0 {
		ctx.UIRoutes = []string{"/"}
	}
	if ctx.UIAPIPattern == "" {
		ctx.UIAPIPattern = "**/api/**"
	}
	body, err := shipped.ReadFile(resolveTemplatePath(name))
	if err != nil {
		return "", fmt.Errorf("read template %s: %w", name, err)
	}
	return execute(name, string(body), FileLeftDelim, FileRightDelim, ctx)
}

func resolveTemplatePath(name string) string {
	if strings.HasPrefix(name, "tests/") {
		candidate := "svelte/" + strings.TrimPrefix(name, "tests/")
		if _, err := shipped.ReadFile(candidate); err == nil {
			return candidate
		}
	}
	return name
}

// Names returns the path of every bootstrap-registered embedded template in lexical order.
func Names() ([]string, error) {
	names, err := fs.Glob(shipped, Pattern)
	if err != nil {
		return nil, fmt.Errorf("list embedded templates: %w", err)
	}
	filtered := make([]string, 0, len(names))
	for i := 0; i < len(names) && i < 256; i++ {
		if names[i] == "svelte/a11y.spec.ts.tmpl" || names[i] == "svelte/responsiveness.spec.ts.tmpl" {
			continue
		}
		filtered = append(filtered, names[i])
	}
	return filtered, nil
}

// execute is the one parse-and-execute path behind Render and RenderFile. An empty
// template is refused: it renders an empty file, which is a placeholder, not content. Every
// template may call the shared workflow functions (templateFuncs).
func execute(name, text, left, right string, ctx Context) (string, error) {
	if text == "" {
		return "", fmt.Errorf("template %s: content cannot be empty", name)
	}
	tmpl, err := template.New(name).Delims(left, right).Funcs(templateFuncs).Parse(text)
	if err != nil {
		return "", fmt.Errorf("parse template %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("execute template %s: %w", name, err)
	}
	return buf.String(), nil
}

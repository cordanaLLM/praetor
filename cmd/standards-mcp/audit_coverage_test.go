package main

import "testing"

func TestServerAuditReportsScopeForUnsupportedSource(t *testing.T) {
	srv, root := newFixtureServer(t)
	writeFixtureFile(t, root, "Plugin.cs", "class Plugin {}")
	result := callTool(t, srv, "standards_audit", nil)
	expectText(t, "source coverage", result, "HISS file scope:")
	expectText(t, "unsupported sources", result, "files outside supported extensions")
	expectText(t, "application scope", result, "Native application tests are separate")
}

// Positive, negative and boundary: standards_audit measures complexity under the effective
// policy and appends the same report lines the CLI audit prints, and a measurement never turns
// the audit into an error. The fixture's Classify has cyclomatic 13: the resolved limit of 15
// admits it, a deployment limit of 13 still admits it, and a limit of 12 measures it.
func TestServerAuditReportsComplexityWithoutEnforcingIt(t *testing.T) {
	srv, root := newFixtureServer(t)
	for _, tc := range []struct {
		limit, want string
	}{
		{"", "0 measurements over limit"},
		{"13", "0 measurements over limit"},
		{"12", "[REPORT] HISS-04 complex.go:4 Function 'Classify' cyclomatic complexity 13 exceeds 12"},
	} {
		args := map[string]any{}
		if tc.limit != "" {
			writeFixtureFile(t, root, "deployment.yaml", "complexity:\n  max_cyclomatic: "+tc.limit+"\n")
			args["deployment_config_path"] = "deployment.yaml"
		}
		result := callTool(t, srv, "standards_audit", args)
		expectText(t, "limit "+tc.limit, result, "[REPORT] HISS-04 complexity measured, not enforced")
		expectText(t, "limit "+tc.limit, result, tc.want)
	}
}

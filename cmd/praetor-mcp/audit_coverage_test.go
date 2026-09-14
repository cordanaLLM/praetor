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

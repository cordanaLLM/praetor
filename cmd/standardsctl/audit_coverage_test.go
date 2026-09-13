package main

import "testing"

func TestAuditReportsScopeForUnsupportedSource(t *testing.T) {
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, "Plugin.cs", "class Plugin {}")
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("governance audit failed: %v\n%s", err, out)
	}
	mustContain(t, out, "HISS file scope:", "files outside supported extensions", "Native application tests are separate", "source analysis is limited")
}

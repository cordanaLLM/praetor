package main

import "testing"

func TestReportClientCapabilitiesRejectsArguments(t *testing.T) {
	if err := reportClientCapabilities([]string{"unexpected"}); err == nil {
		t.Fatal("positional argument accepted")
	}
}

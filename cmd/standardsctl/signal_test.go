package main

import "testing"

func TestOwnsTerminationSignals_3D(t *testing.T) {
	// Positive: serve drains on SIGINT and SIGTERM, so main must not end it first.
	if !ownsTerminationSignals("serve") {
		t.Fatal("serve's graceful drain would be cut short")
	}
	// Negative and boundary: every other command, an alias, and an empty or unknown name
	// get the command-group termination.
	for _, command := range []string{"ci", "gate", "bootstrap", "", "serve ", "unknown"} {
		if ownsTerminationSignals(command) {
			t.Fatalf("%q would leave its commands running on an interrupt", command)
		}
	}
}

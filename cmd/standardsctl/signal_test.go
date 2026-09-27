package main

import "testing"

func TestOwnsTerminationSignals_3D(t *testing.T) {
	// Positive: serve, gate run, and gatekeeper agent manage their own termination lifecycle.
	for _, tc := range []struct {
		cmd  string
		args []string
	}{
		{"serve", nil},
		{"gate", []string{"run"}},
		{"gate", []string{"run", "--path=."}},
		{"agent", []string{"run", "praetor-gatekeeper"}},
		{"agent", []string{"run", "praetor_gatekeeper"}},
	} {
		if !ownsTerminationSignals(tc.cmd, tc.args) {
			t.Errorf("ownsTerminationSignals(%q, %v) = false, want true", tc.cmd, tc.args)
		}
	}

	// Negative and boundary: other commands, gate non-run subcommands, other agents, empty/unknown names
	// get command-group termination.
	for _, tc := range []struct {
		cmd  string
		args []string
	}{
		{"ci", nil},
		{"gate", nil},
		{"gate", []string{"verify"}},
		{"gate", []string{"deadline"}},
		{"gate", []string{"keygen"}},
		{"agent", nil},
		{"agent", []string{"list"}},
		{"agent", []string{"run", "praetor-auditor"}},
		{"bootstrap", nil},
		{"", nil},
		{"serve ", nil},
		{"unknown", nil},
		{"gate", []string{""}},
		{"agent", []string{"run"}},
		{"agent", []string{"run", ""}},
	} {
		if ownsTerminationSignals(tc.cmd, tc.args) {
			t.Errorf("ownsTerminationSignals(%q, %v) = true, want false", tc.cmd, tc.args)
		}
	}
}

func TestNeedsSignalRootContext_3D(t *testing.T) {
	// Positive: gate run and gatekeeper agent derive work from signal-notified root context.
	for _, tc := range []struct {
		cmd  string
		args []string
	}{
		{"gate", []string{"run"}},
		{"gate", []string{"run", "--path=."}},
		{"agent", []string{"run", "praetor-gatekeeper"}},
		{"agent", []string{"run", "praetor_gatekeeper"}},
	} {
		if !needsSignalRootContext(tc.cmd, tc.args) {
			t.Errorf("needsSignalRootContext(%q, %v) = false, want true", tc.cmd, tc.args)
		}
	}

	// Negative and boundary: pure-Go commands and serve must not install signal.NotifyContext.
	// On Windows, unhandled commands must let Ctrl-C terminate the process rather than being swallowed.
	for _, tc := range []struct {
		cmd  string
		args []string
	}{
		{"serve", nil},
		{"audit", nil},
		{"hiss", nil},
		{"compile-context", nil},
		{"dedupe", nil},
		{"gate", nil},
		{"gate", []string{"verify"}},
		{"gate", []string{"deadline"}},
		{"gate", []string{"keygen"}},
		{"agent", nil},
		{"agent", []string{"list"}},
		{"agent", []string{"run", "praetor-auditor"}},
		{"", nil},
		{"unknown", nil},
		{"gate", []string{""}},
		{"agent", []string{"run"}},
		{"agent", []string{"run", ""}},
	} {
		if needsSignalRootContext(tc.cmd, tc.args) {
			t.Errorf("needsSignalRootContext(%q, %v) = true, want false", tc.cmd, tc.args)
		}
	}
}

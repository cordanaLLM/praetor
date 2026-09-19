// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import "testing"

// BUG-811: editors and notebook read their first token as a raw subcommand name, never
// through flag.Parse, so "-h"/"--help"/"help" fell into the unknown-subcommand branch
// instead of the exit-0 help path every flag-parsed command gets.

func TestRunEditors_Positive_HelpTokensPrintUsageAndSucceed(t *testing.T) {
	for _, tok := range []string{"-h", "--help", "help"} {
		out, err := captureStdout(t, func() error { return dispatchCommand("editors", []string{tok}) })
		if err != nil {
			t.Fatalf("editors %s must exit success, got %v", tok, err)
		}
		mustContain(t, out, "Usage: praetorctl editors")
	}
}

func TestRunEditors_Boundary_NoArgsPrintsUsageAndSucceeds(t *testing.T) {
	out, err := captureStdout(t, func() error { return dispatchCommand("editors", []string{}) })
	if err != nil {
		t.Fatalf("editors with no args must exit success, got %v", err)
	}
	mustContain(t, out, "Usage: praetorctl editors")
}

func TestRunEditors_Negative_UnknownSubcommandIsRejected(t *testing.T) {
	err := dispatchCommand("editors", []string{"bogus"})
	mustErrContain(t, err, "unknown editors command: bogus")
}

func TestNotebookCommand_Positive_HelpTokensPrintUsageAndSucceed(t *testing.T) {
	for _, tok := range []string{"-h", "--help", "help"} {
		out, err := captureStdout(t, func() error { return dispatchCommand("notebook", []string{tok}) })
		if err != nil {
			t.Fatalf("notebook %s must exit success, got %v", tok, err)
		}
		mustContain(t, out, "Usage: notebook prepare|validate")
	}
}

func TestNotebookCommand_Negative_NoArgsStillErrors(t *testing.T) {
	// Unchanged by BUG-811: only the explicit help tokens were broken, not the
	// no-argument usage error.
	err := dispatchCommand("notebook", []string{})
	mustErrContain(t, err, "usage: notebook prepare|validate")
}

func TestNotebookCommand_Boundary_UnknownActionStillErrors(t *testing.T) {
	err := dispatchCommand("notebook", []string{"bogus", "--bundle=x"})
	if err == nil {
		t.Fatal("expected an error for an unsupported notebook action")
	}
}

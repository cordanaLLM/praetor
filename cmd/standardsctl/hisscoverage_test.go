// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import "testing"

// #571: the top-level help says to run `<command> -h`, but `hiss` read every first token as
// a subcommand name, so -h, --help and help failed with "unknown hiss subcommand".

func TestRunHiss_Positive_HelpTokensPrintUsageAndSucceed(t *testing.T) {
	for _, tok := range []string{"-h", "--help", "help"} {
		out, err := captureStdout(t, func() error { return dispatchCommand("hiss", []string{tok}) })
		if err != nil {
			t.Fatalf("hiss %s must exit success, got %v", tok, err)
		}
		mustContain(t, out, "Usage: praetorctl hiss", "coverage [--path=.] [--verify]")
	}
}

func TestRunHiss_Negative_UnknownSubcommandIsRejected(t *testing.T) {
	err := dispatchCommand("hiss", []string{"bogus"})
	mustErrContain(t, err, "unknown hiss subcommand: bogus")
}

func TestRunHiss_Boundary_NoArgsPrintsUsageAndSucceeds(t *testing.T) {
	out, err := captureStdout(t, func() error { return dispatchCommand("hiss", nil) })
	if err != nil {
		t.Fatalf("hiss with no args must exit success, got %v", err)
	}
	mustContain(t, out, "Usage: praetorctl hiss")
}

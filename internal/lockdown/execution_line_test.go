// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package lockdown

import (
	"errors"
	"strings"
	"testing"
)

// Positive: the execution line the gate writes is the one CertifiedExecution reads back, and an
// envelope carrying it verifies exactly as one without it does.
func TestExecutionLine_Positive_RoundTripsAndVerifies(t *testing.T) {
	line := ExecutionLine("devcontainer", "runtime=docker", "image=sha256:abc", "stages=Race-Detector Tests (go)")
	if line != "execution\tdevcontainer\truntime=docker\timage=sha256:abc\tstages=Race-Detector Tests (go)" {
		t.Fatalf("ExecutionLine = %q; the signed format must not drift", line)
	}
	output := gateOutputWith(WorktreeCleanLine(true), line)
	got, ok := CertifiedExecution(output)
	if !ok || got != "devcontainer\truntime=docker\timage=sha256:abc\tstages=Race-Detector Tests (go)" {
		t.Fatalf("CertifiedExecution = %q, %v", got, ok)
	}
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyPinnedReceiptFile(signedEnvelope(t, priv, output), pub); err != nil {
		t.Fatalf("an envelope recording its execution must verify: %v", err)
	}
}

// Negative: output that records no execution, as every receipt minted before the line existed,
// or records two, yields none; the older receipt still verifies.
func TestExecutionLine_Negative_AbsentOrAmbiguous(t *testing.T) {
	older := gateOutputWith(WorktreeCleanLine(true))
	if got, ok := CertifiedExecution(older); ok || got != "" {
		t.Errorf("output without the line must yield none, got %q", got)
	}
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyPinnedReceiptFile(signedEnvelope(t, priv, older), pub); err != nil {
		t.Errorf("a receipt minted before the line existed must still verify: %v", err)
	}
	twice := gateOutputWith(WorktreeCleanLine(true), ExecutionLine("host", "reason=a"), ExecutionLine("devcontainer"))
	if _, ok := CertifiedExecution(twice); ok {
		t.Error("two execution lines must yield none")
	}
	afterStage := GateOutputVersion + "\nstage\tHISS\tpassed\t\n" + ExecutionLine("host") + "\n"
	if _, ok := CertifiedExecution(afterStage); ok {
		t.Error("a line after the first stage line is not header and must not be read")
	}
}

// Boundary: a tab or line break inside a field cannot split the line or forge another header line.
func TestExecutionLine_Boundary_FieldsCannotSplitTheLine(t *testing.T) {
	line := ExecutionLine("host", "reason=a\tb\nworktree_clean\ttrue\rz")
	if strings.ContainsAny(line[len("execution\thost\t"):], "\t\r\n") {
		t.Fatalf("a field kept its separator: %q", line)
	}
	// Unsanitized, the reason would add a second worktree_clean line and the refusal would read
	// ErrWorktreeUnrecorded instead of the recorded false.
	output := gateOutputWith(WorktreeCleanLine(false), line)
	if err := RequireCleanWorktree(output); !errors.Is(err, ErrWorktreeNotClean) {
		t.Errorf("a reason carrying a line break must not add a worktree_clean header line, got %v", err)
	}
}

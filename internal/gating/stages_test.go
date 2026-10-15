package gating

import "testing"

func TestStageNames_PipelineOrder(t *testing.T) {
	want := []string{
		stagePrefetch, stageHISS, stageSecurity, stageFlavor, stageTests, stageDeclaredVerify, stageReceipt,
	}
	got := StageNames()
	if len(got) != len(want) {
		t.Fatalf("StageNames() length = %d, want %d", len(got), len(want))
	}
	for i := 0; i < len(want); i++ {
		if got[i] != want[i] {
			t.Errorf("StageNames()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	got[0] = "changed"
	if StageNames()[0] != stagePrefetch {
		t.Fatal("StageNames() exposed mutable stage declarations")
	}
}

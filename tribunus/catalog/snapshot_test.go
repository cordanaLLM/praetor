package catalog

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSnapshotMarshalParse_Positive(t *testing.T) {
	snap := Snapshot{
		GeneratedAt: time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC),
		Records:     []Record{validRecord()},
		SourceRuns: []SourceRun{
			{Source: "litellm-gateway", Status: StatusOK, Count: 1},
		},
	}
	data, err := snap.Marshal()
	if err != nil {
		t.Fatalf("Marshal() = %v, want nil", err)
	}
	got, err := ParseSnapshot(data)
	if err != nil {
		t.Fatalf("ParseSnapshot() = %v, want nil", err)
	}
	if len(got.Records) != 1 || got.Records[0].ModelID != "example/auto" {
		t.Fatalf("ParseSnapshot() round-trip = %+v", got)
	}
}

func TestSnapshotMarshal_Negative(t *testing.T) {
	snap := Snapshot{
		GeneratedAt: time.Now(),
		Records:     []Record{{}}, // empty record fails Validate
	}
	if _, err := snap.Marshal(); err == nil {
		t.Fatal("Marshal() = nil error, want validation failure to propagate")
	}
}

func TestParseSnapshot_Negative(t *testing.T) {
	if _, err := ParseSnapshot([]byte("not json")); err == nil {
		t.Fatal("ParseSnapshot() = nil error, want a decode failure")
	}
}

// TestSnapshotValidate_Boundary confirms MaxSnapshotRecords is enforced at
// exactly one past the bound, not off by one in either direction.
func TestSnapshotValidate_Boundary(t *testing.T) {
	records := make([]Record, MaxSnapshotRecords+1)
	for i := range records {
		r := validRecord()
		r.ModelID = "m" + strconv.Itoa(i)
		records[i] = r
	}
	snap := Snapshot{GeneratedAt: time.Now(), Records: records}
	if err := snap.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error one past MaxSnapshotRecords")
	}
}

func TestUsageWindowBurnAndAgentsRoundTrip(t *testing.T) {
	used, burn, agents := 42.5, 12.25, 7
	resets := time.Date(2026, 10, 13, 14, 0, 0, 0, time.UTC)
	withWindow := validRecord()
	withWindow.AccessPath = AccessSubscriptionCLI
	withWindow.UsageWindow = &UsageWindow{UsedPercent: &used, ResetsAt: &resets, BurnPercentPerHour: &burn, ActiveAgents: &agents}
	bare := validRecord()
	bare.AccessPath = AccessSubscriptionCLI
	bare.UsageWindow = &UsageWindow{UsedPercent: &used}
	data, err := Snapshot{GeneratedAt: resets, Records: []Record{withWindow, bare}}.Marshal()
	if err != nil {
		t.Fatalf("Marshal() = %v, want nil", err)
	}
	got, err := ParseSnapshot(data)
	if err != nil {
		t.Fatalf("ParseSnapshot() = %v, want nil", err)
	}
	window := got.Records[0].UsageWindow
	if window == nil || window.BurnPercentPerHour == nil || *window.BurnPercentPerHour != burn || window.ActiveAgents == nil || *window.ActiveAgents != agents {
		t.Fatalf("burn and agents lost in round-trip: %+v", window)
	}
	if empty := got.Records[1].UsageWindow; empty.BurnPercentPerHour != nil || empty.ActiveAgents != nil {
		t.Fatalf("absent burn and agents must stay absent, not zero: %+v", empty)
	}
	if count := strings.Count(string(data), "burn_percent_per_hour"); count != 1 {
		t.Fatalf("an absent burn must be omitted from the JSON, found %d keys", count)
	}
}

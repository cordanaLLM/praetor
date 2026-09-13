package router

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

const snapshotPrefix = `{"version":1,"captured_at":"2026-09-12T12:00:00Z","models":`

func TestUsageSnapshotExplicitObservations(t *testing.T) {
	body := snapshotPrefix + `{"known":{"current_rpm":0,"current_tpm":20}}}`
	snapshot, err := LoadUsageSnapshot(context.Background(), routingInput(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Models["known"].CurrentTPM != 20 || snapshot.Models["known"].CurrentRPM != 0 {
		t.Fatal("explicit counters changed")
	}
	if _, err := TrackerFromSnapshot(taskConfig(taskModel("known", 1, 1)), snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := TrackerFromSnapshot(taskConfig(taskModel("other", 1, 1)), snapshot); err == nil {
		t.Fatal("unknown model observation accepted")
	}
}

func TestUsageSnapshotRejectsAmbiguousObservations(t *testing.T) {
	cases := []string{
		`{"known":null}`, `{"known":{}}`, `{"known":{"current_rpm":0}}`,
		`{"known":{"current_rpm":0,"current_tpm":null}}`,
		`{"known":{"current_rpm":9,"current_rpm":0,"current_tpm":0}}`,
		`{"known":{"current_rpm":9,"current_tpm":0},"known":{"current_rpm":0,"current_tpm":0}}`,
		`{"known":{"Current_RPM":0,"current_tpm":0}}`,
		`{"known":{"current_rpm":9,"CurrentRPM":0,"current_tpm":0}}`,
		`{"known":{"current_rpm":-1,"current_tpm":0}}`,
		`{"known":{"current_rpm":0.5,"current_tpm":0}}`,
		`{"known":{"current_rpm":"0","current_tpm":0}}`,
		`{"known":{"current_rpm":0,"current_tpm":0,"last_429_time":null}}`,
	}
	for i, models := range cases {
		if _, err := LoadUsageSnapshot(context.Background(), routingInput(t, snapshotPrefix+models+"}")); err == nil {
			t.Fatalf("ambiguous observation %d accepted", i)
		}
	}
	valid := snapshotPrefix + `{"known":{"current_rpm":0,"current_tpm":0}}}`
	for _, body := range []string{strings.Replace(valid, `"version":1`, `"version":0,"version":1`, 1), strings.Replace(valid, `"version":1`, `"Version":1`, 1), valid + " {}", "null", "{}"} {
		if _, err := LoadUsageSnapshot(context.Background(), routingInput(t, body)); err == nil {
			t.Fatalf("ambiguous snapshot accepted: %s", body)
		}
	}
}

func TestUsageSnapshotRowBound(t *testing.T) {
	snapshot := UsageSnapshot{Version: 1, CapturedAt: time.Now().UTC(), Models: make(map[string]ModelUsage)}
	for i := 0; i < MaxRoutingModels; i++ {
		snapshot.Models[fmt.Sprintf("model-%d", i)] = ModelUsage{}
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := LoadUsageSnapshot(context.Background(), routingInput(t, string(data))); err != nil || len(got.Models) != MaxRoutingModels {
		t.Fatalf("exact row bound failed: %v", err)
	}
	snapshot.Models["overflow"] = ModelUsage{}
	data, err = json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUsageSnapshot(context.Background(), routingInput(t, string(data))); err == nil {
		t.Fatal("row cap+1 accepted")
	}
}

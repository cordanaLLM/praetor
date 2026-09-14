package main

import (
	"context"
	"testing"
)

func TestDogfoodScheduleRejectsInvalidCLI(t *testing.T) {
	for _, args := range [][]string{nil, {"reset"}, {"run"}, {"status", "--config", "/missing", "extra"}, {"status", "--unknown"}, {"run", "--config", "/missing"}} {
		if err := runDogfoodSchedule(context.Background(), args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if err := runDogfood([]string{"schedule", "invalid"}); err == nil {
		t.Fatal("dispatch did not route schedule")
	}
}

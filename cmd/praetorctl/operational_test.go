package main

import (
	"context"
	"testing"
)

func TestOperationalRejectsUnsupportedAndIncompleteCommands(t *testing.T) {
	for _, args := range [][]string{nil, {"sync"}, {"publish", "plan"}, {"sync", "publish"}, {"sync", "plan"}, {"sync", "plan", "unexpected"}, {"sync", "prepare", "--unknown"}} {
		if err := runOperational(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if err := runOperationalSync(context.Background(), "plan", []string{"--owner-path=", "--source-path="}); err == nil {
		t.Fatal("empty paths accepted")
	}
}

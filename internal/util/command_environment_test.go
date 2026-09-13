package util

import (
	"context"
	"strings"
	"testing"
)

func TestCommandEnvironmentIsCopiedAndDoesNotInherit(t *testing.T) {
	t.Setenv("PRAETOR_TEST_AMBIENT", "private-sentinel")
	env := []string{"PRAETOR_TEST_EXPLICIT=original"}
	ctx, err := WithCommandEnvironment(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	env[0] = "PRAETOR_TEST_EXPLICIT=mutated"
	output, err := RunCommand(ctx, "", "/usr/bin/env")
	if err != nil {
		t.Fatal(err)
	}
	if output != "PRAETOR_TEST_EXPLICIT=original" {
		t.Fatalf("environment leaked or mutated: %q", output)
	}
	ambient, err := RunCommand(context.Background(), "", "/usr/bin/env")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ambient, "PRAETOR_TEST_AMBIENT=private-sentinel") {
		t.Fatal("context override changed ambient process environment")
	}
}

func TestCommandEnvironmentEmptyAndBounds(t *testing.T) {
	ctx, err := WithCommandEnvironment(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	output, err := RunCommand(ctx, "", "/usr/bin/env")
	if err != nil || output != "" {
		t.Fatalf("explicit empty environment inherited values: %q, %v", output, err)
	}
	var nilContext context.Context
	if _, err := WithCommandEnvironment(nilContext, nil); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := WithCommandEnvironment(context.Background(), make([]string, 256)); err != nil {
		t.Fatal(err)
	}
	if _, err := WithCommandEnvironment(context.Background(), make([]string, 257)); err == nil {
		t.Fatal("oversized environment accepted")
	}
}

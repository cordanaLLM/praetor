package util

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestCommandEnvironmentIsCopiedAndDoesNotInherit(t *testing.T) {
	if _, err := os.Stat("/usr/bin/env"); err != nil {
		t.Skipf("/usr/bin/env is not present on this host (%v); the behaviour under test "+
			"is platform-independent and covered where the tool exists", err)
	}
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
	// Only this block needs /usr/bin/env to print the child's environment. The context and
	// bound checks below do not, so they run on every platform rather than being skipped
	// with it.
	if _, statErr := os.Stat("/usr/bin/env"); statErr != nil {
		t.Logf("/usr/bin/env is not present on this host (%v); empty-environment readback skipped", statErr)
	} else if output, err := RunCommand(ctx, "", "/usr/bin/env"); err != nil || output != "" {
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

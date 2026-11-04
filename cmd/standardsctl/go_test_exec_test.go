// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"bytes"
	"errors"
	"go/ast"
	"go/types"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/sandbox"
)

// TestGoTestAggregateDeadline prevents the local process from sharing the smaller per-stage bound.
func TestGoTestAggregateDeadline(t *testing.T) {
	decl := parseFuncs(t, ".")["runGoTest"]
	if decl == nil {
		t.Fatal("runGoTest missing")
	}
	contexts := 0
	ast.Inspect(decl.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || calledName(call) != "commandContext" {
			return true
		}
		contexts++
		if len(call.Args) != 1 || types.ExprString(call.Args[0]) != "goTestAggregateTimeout" {
			t.Error("runGoTest context must use goTestAggregateTimeout, not the per-stage bound")
		}
		return true
	})
	if contexts != 1 {
		t.Fatalf("runGoTest context calls = %d, want 1", contexts)
	}
}

// TestGoTestAggregateTimeoutBoundary keeps aggregate headroom at the gate's override ceiling.
func TestGoTestAggregateTimeoutBoundary(t *testing.T) {
	if goTestAggregateTimeout != 2*gating.MaxTestStageTimeout {
		t.Fatal("aggregate timeout must track twice the shared stage ceiling")
	}
	for _, raw := range []string{"", "25m", "30m", "31m", "0s"} {
		t.Run(raw, func(t *testing.T) {
			bound := gating.ResolveRunBudget(raw).StageBound
			if bound <= 0 || bound >= goTestAggregateTimeout {
				t.Fatalf("stage bound %s must be positive and below aggregate %s", bound, goTestAggregateTimeout)
			}
		})
	}
}

// TestGoTestExecArguments pins argument construction and rejection of manual -exec overrides.
func TestGoTestExecArguments(t *testing.T) {
	const wrapper = "'/path with spaces/engine' go test-exec"
	for _, tc := range []struct {
		args, want []string
		exec       string
	}{
		{[]string{"-race", "./..."}, []string{"test", "-exec", wrapper, "-race", "./..."}, wrapper},
		{[]string{"-race", "-timeout", "30m", "./..."}, []string{"test", "-exec", wrapper, "-race", "-timeout", "30m", "./..."}, wrapper},
		{[]string{"-timeout=0", "./pkg"}, []string{"test", "-exec", wrapper, "-timeout=0", "./pkg"}, wrapper},
		{[]string{"-C", "tools/module", "-count=1", "./..."}, []string{"-C", "tools/module", "test", "-exec", wrapper, "-count=1", "./..."}, wrapper},
		{[]string{"-race", "./..."}, []string{"test", "-race", "./..."}, ""},
		{[]string{"./pkg", "-args", "-exec=fixture-value"}, []string{"test", "-exec", wrapper, "./pkg", "-args", "-exec=fixture-value"}, wrapper},
	} {
		got, err := goTestArgs(tc.args, tc.exec)
		if err != nil || !slices.Equal(got, tc.want) {
			t.Fatalf("args=%q want=%q error=%v", got, tc.want, err)
		}
	}
	for _, args := range [][]string{{"-exec", "env", "./..."}, {"-exec=env", "./..."}, {"--exec", "env", "./..."}, {"--exec=env", "./..."}} {
		if _, err := goTestArgs(args, wrapper); err == nil || !strings.Contains(err.Error(), "owned by the gate wrapper") {
			t.Fatalf("accepted isolation override %q: %v", args, err)
		}
	}
}

// TestGoTestExecFailureByName pins exit code and diagnostic handling when namespace facilities are unavailable.
func TestGoTestExecFailureByName(t *testing.T) {
	denied := &sandbox.UnavailableError{Facility: "bwrap user/network namespaces (loopback-only)", Cause: errors.New("planted EPERM")}
	var diagnostic bytes.Buffer
	if code := commandExitCode(&diagnostic, agentSandboxResult(denied)); code == 0 || !strings.Contains(diagnostic.String(), denied.Error()) {
		t.Fatalf("code=%d diagnostic=%q", code, diagnostic.String())
	}
	if err := runGo([]string{"test-exec"}); err == nil {
		t.Fatal("empty binary accepted")
	}
}

// TestGoTestExecMakefileCoverage pins that Makefile test recipes invoke the standardsctl loopback wrapper.
func TestGoTestExecMakefileCoverage(t *testing.T) {
	data, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	if !strings.Contains(source, "GO_TEST := go run \"$(CURDIR)/cmd/standardsctl\" go test") || !strings.Contains(source, "$(GO_TEST) -C tools/schemacheck") {
		t.Fatal("Makefile test namespace entry point missing")
	}
	for _, line := range strings.Split(source, "\n") {
		if strings.HasPrefix(line, "\tgo test ") || strings.HasPrefix(line, "\tgo -C tools/schemacheck test ") {
			t.Fatalf("Go test recipe retains host loopback: %s", line)
		}
	}
}

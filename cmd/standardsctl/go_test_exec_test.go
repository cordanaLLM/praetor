// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"bytes"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/sandbox"
)

func TestGoTestExecArguments(t *testing.T) {
	const wrapper = "'/path with spaces/engine' go test-exec"
	for _, tc := range []struct {
		args, want []string
		exec       string
	}{
		{[]string{"-race", "./..."}, []string{"test", "-exec", wrapper, "-race", "./..."}, wrapper},
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

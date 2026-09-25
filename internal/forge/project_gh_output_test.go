// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// ghStub stands in for `gh project item-add`: PRAETOR_TEST_GH_MODE selects an update notice
// on standard error beside the JSON answer, or a refusal on standard error.
const ghStub = `package main

import (
	"fmt"
	"os"
)

func main() {
	if os.Getenv("PRAETOR_TEST_GH_MODE") == "refuse" {
		fmt.Fprintln(os.Stderr, "HTTP 401: Bad credentials")
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "A new release of gh is available: 2.0.0 -> 2.1.0")
	fmt.Println("{\"id\":\"PVTI_stdout\"}")
}
`

// installGhStub builds ghStub as the only gh on PATH, with the given mode.
func installGhStub(t *testing.T, mode string) {
	t.Helper()
	bin := t.TempDir()
	testsupport.BuildExecutable(t, bin, "gh", ghStub)
	t.Setenv("PATH", bin)
	t.Setenv("PRAETOR_TEST_GH_MODE", mode)
}

// gh prints update notices on standard error. They are not part of the JSON answer, and
// decoding them as such rejected every add made while a notice was pending (BUG-847).
func TestAddItem_Positive_DecodesStandardOutputBesideANotice(t *testing.T) {
	dir := setupTestProjectDir(t)
	installGhStub(t, "notice")
	pm := newTestProjectManager(t, "test-token", "")
	item, err := pm.AddItem(context.Background(), dir, 1, "https://github.com/cordanaLLM/praetor/issues/42")
	if err != nil {
		t.Fatalf("a notice on standard error broke the add: %v", err)
	}
	if item.ID != "PVTI_stdout" {
		t.Fatalf("item id = %q, want PVTI_stdout", item.ID)
	}
}

func TestAddItem_Negative_RefusalReportsStandardError(t *testing.T) {
	dir := setupTestProjectDir(t)
	installGhStub(t, "refuse")
	pm := newTestProjectManager(t, "test-token", "")
	_, err := pm.AddItem(context.Background(), dir, 1, "https://github.com/cordanaLLM/praetor/issues/42")
	if err == nil || !strings.Contains(err.Error(), "Bad credentials") {
		t.Fatalf("refusal reason lost: %v", err)
	}
}

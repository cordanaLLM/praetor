// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package apicompat

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/util"
)

// trickyReason carries every character a YAML double-quoted scalar or a JSON string treats
// specially, a non-ASCII letter and a workflow-looking token short of the expression opener.
const trickyReason = `needs "libudev.h" \ and a tab-free ünïcode reason: {x} # not a comment $ {{ y }}`

func mustRender(t *testing.T, settings Settings) string {
	t.Helper()
	rendered, err := RenderWorkflow(Workflow, settings)
	if err != nil {
		t.Fatal(err)
	}
	return rendered
}

// Positive: system packages render one install step, with a timeout, directly before the gate
// step, in the hosted gate shape, running apt-get update and then an install of exactly the
// declared names; the workflow still passes the hosted gate fault check.
// Boundary: the checker's step is the last one and the draft stop step stays the first.
func TestRenderWorkflow_Positive_InstallStepRunsBeforeTheChecker(t *testing.T) {
	rendered := mustRender(t, Settings{SystemPackages: []string{"libudev-dev", "libopenal-dev", "g++-14"}})
	spec, err := ghworkflow.Parse([]byte(rendered))
	if err != nil {
		t.Fatal(err)
	}
	if err := ghworkflow.HostedGateFault(&spec, "api-compatibility", ghworkflow.HostedGateDefaultBranch); err != nil {
		t.Fatalf("the rendered workflow departs from the hosted gate shape: %v", err)
	}
	runs, err := forge.WorkflowRuns([]byte(rendered))
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 || runs[0].Name != ghworkflow.HostedGateDraftStepName || runs[1].Name != InstallStepName ||
		runs[2].Script != "go run "+Directory+"/"+GateFile+" -base=\"$BASE\"" {
		t.Fatalf("workflow runs %+v, want the draft step, the install step, then the gate", runs)
	}
	wantScript := "sudo apt-get update\nsudo apt-get install -y --no-install-recommends \\\n  libudev-dev \\\n  libopenal-dev \\\n  g++-14"
	if runs[1].Script != wantScript {
		t.Fatalf("install script = %q, want %q", runs[1].Script, wantScript)
	}
	step := rendered[strings.Index(rendered, installStepOpen):strings.Index(rendered, compareStepMarker)]
	for _, want := range []string{ghworkflow.HostedGateStepIf, "timeout-minutes: 15\n"} {
		if !strings.Contains(step, want) {
			t.Errorf("install step lacks %q:\n%s", want, step)
		}
	}
	if strings.Contains(rendered, ExceptionsEnv) || strings.Contains(rendered, "CGO_ENABLED") {
		t.Fatal("packages alone must not add an exceptions variable, and nothing sets CGO_ENABLED")
	}
}

// Boundary: no packages and no exceptions render the locked bytes, which are the bytes before the
// settings existed; a nil and an empty list are the same.
func TestRenderWorkflow_Boundary_EmptySettingsKeepTheBytes(t *testing.T) {
	for _, settings := range []Settings{{}, {SystemPackages: []string{}}, {Exceptions: []ModuleException{}}} {
		if got := mustRender(t, settings); got != Workflow {
			t.Fatalf("settings %+v changed the workflow", settings)
		}
	}
	digest, _, err := util.CanonicalTextDigest([]byte(Workflow))
	if err != nil || digest != "d64b71821e056174c3127244483c916a3baa646b6f37f419eac44102c8c30dbd" {
		t.Fatalf("Workflow digest = %s (%v): the default rendering is no longer byte-identical to the shipped text", digest, err)
	}
}

// Positive: exceptions reach the gate step's environment as JSON that a YAML parser reads back
// exactly, whatever characters the reason carries; the line is exempt from yamllint's line length.
func TestRenderWorkflow_Positive_ExceptionsReachTheGateEnvironment(t *testing.T) {
	entries := []ModuleException{
		{Path: "hw/udev/go.mod", Reason: trickyReason, Expires: "2026-12-31"},
		{Path: "go.mod", Reason: "second", Expires: "2026-11-01"},
	}
	rendered := mustRender(t, Settings{Exceptions: entries})
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Env map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := util.DecodeYAMLDocument([]byte(rendered), &workflow, util.YAMLDocumentOptions{}); err != nil {
		t.Fatalf("the rendered workflow is no YAML of the expected shape: %v", err)
	}
	steps := workflow.Jobs["api-compatibility"].Steps
	raw, ok := steps[len(steps)-1].Env[ExceptionsEnv]
	if !ok {
		t.Fatalf("the gate step carries no %s: %v", ExceptionsEnv, steps[len(steps)-1].Env)
	}
	var got []ModuleException
	if err := json.Unmarshal([]byte(raw), &got); err != nil || !reflect.DeepEqual(got, entries) {
		t.Fatalf("%s = %q reads back as %+v (%v), want %+v", ExceptionsEnv, raw, got, err, entries)
	}
	if !strings.Contains(rendered, exceptionsComment+exceptionsKey) {
		t.Fatal("the long exceptions line is not exempt from the yamllint line-length rule")
	}
}

// Negative: a plain text without the gate step cannot take settings, and the bounds refuse a
// longer list than a manifest may declare.
func TestRenderWorkflow_Negative_RefusesWhatItCannotRender(t *testing.T) {
	if _, err := RenderWorkflow("name: other\n", Settings{SystemPackages: []string{"libudev-dev"}}); err == nil {
		t.Fatal("a workflow without the gate step took settings")
	}
	tooMany := make([]string, config.MaxAPISystemPackages+1)
	for index := range tooMany {
		tooMany[index] = "libx-dev"
	}
	if _, err := RenderWorkflow(Workflow, Settings{SystemPackages: tooMany}); err == nil {
		t.Fatal("more packages than the bound rendered")
	}
}

// Positive: StripSettings is RenderWorkflow's inverse for Praetor's own output, packages and
// exceptions alike, over any plain text: one with a hand-added comment strips to that text, which
// the family then finds is no text Praetor shipped. Negative: an edited rendering, a package list changed by hand, a hand-written
// install step and a doctored exceptions line are not recognised. Boundary: text with no settings
// strips to itself.
func TestStripSettings_RecognisesOnlyPraetorsRendering(t *testing.T) {
	settings := Settings{
		SystemPackages: []string{"libudev-dev", "libvorbis-dev"},
		Exceptions:     []ModuleException{{Path: "hw/udev/go.mod", Reason: trickyReason, Expires: "2026-12-31"}},
	}
	rendered := mustRender(t, settings)
	if plain, ok := StripSettings(rendered); !ok || plain != Workflow {
		t.Fatalf("StripSettings did not return the plain workflow (ok %v)", ok)
	}
	commented := strings.Replace(Workflow, "      - name: Setup Go", "      # note\n      - name: Setup Go", 1)
	if plain, ok := StripSettings(mustRenderOver(t, commented, settings)); !ok || plain != commented || plain == Workflow {
		t.Fatalf("a rendering over an edited plain text did not strip to that text (ok %v)", ok)
	}
	if plain, ok := StripSettings(Workflow); !ok || plain != Workflow {
		t.Fatalf("a text without settings did not strip to itself (ok %v)", ok)
	}
	for name, edited := range map[string]string{
		"extra step line": strings.Replace(rendered, "sudo apt-get update\n", "sudo apt-get update\n          echo hi\n", 1),
		"changed timeout": strings.Replace(rendered, "timeout-minutes: 15", "timeout-minutes: 60", 1),
		"no gate step":    strings.Replace(rendered, compareStepMarker, "      - name: Other", 1),
		"bad exceptions":  strings.Replace(rendered, ExceptionsEnv+": \"", ExceptionsEnv+": \"x", 1),
	} {
		if edited == rendered {
			t.Fatalf("%s: the mutation did not change the text", name)
		}
		if _, ok := StripSettings(edited); ok {
			t.Errorf("%s: an edited rendering was recognised", name)
		}
	}
}

// Boundary: the longest package name the manifest admits (config.MaxAPISystemPackageBytes) keeps
// every line of the install step inside the 80 columns the default yamllint line-length rule
// allows, and the rendering reads back through StripSettings.
func TestRenderWorkflow_Boundary_LongestPackageNameFitsEightyColumns(t *testing.T) {
	longest := "l" + strings.Repeat("x", config.MaxAPISystemPackageBytes-1)
	rendered := mustRender(t, Settings{SystemPackages: []string{longest, "libudev-dev"}})
	step := rendered[strings.Index(rendered, installStepOpen):strings.Index(rendered, compareStepMarker)]
	for _, line := range strings.Split(step, "\n") {
		if len(line) > 80 {
			t.Errorf("install step line is %d columns: %q", len(line), line)
		}
	}
	if plain, ok := StripSettings(rendered); !ok || plain != Workflow {
		t.Fatalf("the rendering with the longest name does not strip back (ok %v)", ok)
	}
}

func mustRenderOver(t *testing.T, plain string, settings Settings) string {
	t.Helper()
	rendered, err := RenderWorkflow(plain, settings)
	if err != nil {
		t.Fatal(err)
	}
	return rendered
}

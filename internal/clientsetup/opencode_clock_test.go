package clientsetup

import (
	"errors"
	"strings"
	"testing"
)

func TestRenderOpenCodeClockPluginPositive(t *testing.T) {
	content, err := RenderOpenCodeClockPlugin("praetorctl")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"experimental.chat.messages.transform",
		"messages.at(-1)",
		`"hook", "opencode", event`,
		"Only the newest message is changed",
		`const executable = "praetorctl";`,
	} {
		if !strings.Contains(string(content), want) {
			t.Errorf("plugin missing %q", want)
		}
	}
}

func TestRenderOpenCodeClockPluginNegativeRefusesLaneHiddenPath(t *testing.T) {
	path := "/workspace/.workingdir/bin/praetorctl"
	if _, err := RenderOpenCodeClockPlugin(path); !errors.Is(err, ErrLaneHiddenHookPath) || !strings.Contains(err.Error(), path) {
		t.Fatalf("hidden path was not named and refused: %v", err)
	}
}

func TestRenderOpenCodeClockPluginBoundaryAllowsSimilarVisiblePaths(t *testing.T) {
	for _, path := range []string{"praetorctl", "/workspace/.workingdir-cache/bin/praetorctl", "/usr/local/bin/praetorctl"} {
		if _, err := RenderOpenCodeClockPlugin(path); err != nil {
			t.Errorf("visible path %q refused: %v", path, err)
		}
	}
}

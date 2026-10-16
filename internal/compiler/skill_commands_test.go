package compiler

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type skillCommand struct {
	tool     string
	helpArgs []string
	flags    []string
}

type skillHelpRunner func(string, []string) (string, error)

func parseSkillCommandBlock(text string) ([]skillCommand, error) {
	var commands []skillCommand
	inside, found := false, false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if !inside && trimmed == "```commands" {
			inside, found = true, true
			continue
		}
		if inside && strings.HasPrefix(trimmed, "```") {
			inside = false
			continue
		}
		if inside && trimmed != "" {
			command, ok := parseSkillCommand(trimmed)
			if !ok {
				return nil, fmt.Errorf("invalid command line %q", trimmed)
			}
			commands = append(commands, command)
		}
	}
	if inside {
		return nil, fmt.Errorf("unclosed commands block")
	}
	if !found {
		return nil, nil
	}
	return commands, nil
}

func parseSkillCommand(line string) (skillCommand, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return skillCommand{}, false
	}
	command := skillCommand{tool: fields[0]}
	optionsStarted := false
	for _, field := range fields[1:] {
		if field == "--" {
			break
		}
		if strings.HasPrefix(field, "-") {
			optionsStarted = true
			if strings.HasPrefix(field, "--") || len(field) == 2 {
				command.flags = append(command.flags, field)
			}
			continue
		}
		if !optionsStarted {
			command.helpArgs = append(command.helpArgs, field)
		}
	}
	return command, true
}

func verifySkillCommandFlags(commands []skillCommand, runner skillHelpRunner) error {
	for _, command := range commands {
		helpArgs := append(append([]string(nil), command.helpArgs...), "--help")
		help, err := runner(command.tool, helpArgs)
		if err != nil {
			return fmt.Errorf("%s help: %w", command.tool, err)
		}
		for _, flag := range command.flags {
			if !strings.Contains(help, flag) {
				return fmt.Errorf("%s command names unsupported flag %s", command.tool, flag)
			}
		}
	}
	return nil
}

func runSkillHelp(tool string, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, tool, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("run %s %s: %w: %s", tool, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func readCanonicalSkillCommands(t *testing.T) map[string][]skillCommand {
	t.Helper()
	root := filepath.Clean(filepath.Join("..", ".."))
	dir := filepath.Join(root, filepath.FromSlash(CanonicalSkillsRel))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	byTool := make(map[string][]skillCommand)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name(), SkillEntryName)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		commands, err := parseSkillCommandBlock(string(data))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, command := range commands {
			byTool[command.tool] = append(byTool[command.tool], command)
		}
	}
	return byTool
}

func TestCanonicalSkillCommandFlags_PositiveToolsPresent(t *testing.T) {
	commands := readCanonicalSkillCommands(t)
	for tool, toolCommands := range commands {
		t.Run(tool, func(t *testing.T) {
			if _, err := exec.LookPath(tool); err != nil {
				t.Skipf("%s absent from PATH; command flags not checked", tool)
			}
			if err := verifySkillCommandFlags(toolCommands, runSkillHelp); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestSkillCommandFlags_PositiveFakeHelp(t *testing.T) {
	commands, err := readCommandFixture(t, "correct")
	if err != nil {
		t.Fatal(err)
	}
	runner := func(tool string, args []string) (string, error) { return "--output-format", nil }
	if err := verifySkillCommandFlags(commands, runner); err != nil {
		t.Fatal(err)
	}
}

func TestSkillCommandFlags_NegativePlantedUnsupportedFlag(t *testing.T) {
	commands, err := readCommandFixture(t, "broken")
	if err != nil {
		t.Fatal(err)
	}
	runner := func(tool string, args []string) (string, error) { return "--output-format", nil }
	err = verifySkillCommandFlags(commands, runner)
	if err == nil || !strings.Contains(err.Error(), "--output-formatt") {
		t.Fatalf("unsupported planted flag was not reported: %v", err)
	}
}

func readCommandFixture(t *testing.T, name string) ([]skillCommand, error) {
	t.Helper()
	path := filepath.Join("testdata", "python-dev-flags", name, SkillEntryName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseSkillCommandBlock(string(data))
}

func TestSkillCommandFlags_Boundaries(t *testing.T) {
	for _, text := range []string{"```commands\n```", "```commands\npytest tests/\n```", "```commands\nruff check -- file.py --fake\n```"} {
		commands, err := parseSkillCommandBlock(text)
		if err != nil {
			t.Fatal(err)
		}
		if len(commands) == 0 {
			continue
		}
		if len(commands[0].flags) != 0 {
			t.Fatalf("non-option or post-delimiter token treated as flag: %#v", commands[0].flags)
		}
	}
}

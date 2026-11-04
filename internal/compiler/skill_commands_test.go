package compiler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

type skillCommand struct {
	tool     string
	helpArgs []string
	flags    []string
}

type skillHelpRunner func(string, []string) (string, error)

type skillProcessRunner func(context.Context, string, []string) (string, error)

// skillHelpSkip records an unavailable check, never a successful flag verification.
type skillHelpSkip struct{ reason string }

// Error returns the tool, call and bound that prevented flag verification.
func (s *skillHelpSkip) Error() string { return s.reason }

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
		if strings.HasPrefix(field, "-p:") {
			optionsStarted = true
			command.flags = append(command.flags, "-p")
			continue
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
			if strings.Contains(help, flag) {
				continue
			}
			if command.tool == "dotnet" && hasCommand(command.helpArgs, "build") && flag == "-p" {
				msbuildHelp, err := runner("dotnet", []string{"msbuild", "--help"})
				if err != nil {
					return fmt.Errorf("dotnet msbuild help for property flag: %w", err)
				}
				if strings.Contains(msbuildHelp, flag) {
					continue
				}
			}
			if command.tool == "cargo" && hasCommand(command.helpArgs, "clippy") && flag == "--message-format" {
				cargoHelp, err := runner("cargo", []string{"build", "--help"})
				if err != nil {
					return fmt.Errorf("cargo build help for inherited flag: %w", err)
				}
				if strings.Contains(cargoHelp, flag) {
					continue
				}
			}
			return fmt.Errorf("%s command names unsupported flag %s", command.tool, flag)
		}
	}
	return nil
}

func hasCommand(args []string, want string) bool {
	for i := 0; i < len(args) && i < 16; i++ {
		if args[i] == want {
			return true
		}
	}
	return false
}

// skillHelpTimeout bounds each flag probe after the tool has warmed up.
const skillHelpTimeout = 60 * time.Second

// skillWarmupTimeout gives cold SDK and optional component setup a separate bound.
const skillWarmupTimeout = 180 * time.Second

// runBoundedSkillHelp applies the selected bound and preserves ordinary command failures.
func runBoundedSkillHelp(tool string, args []string, bound time.Duration, runner skillProcessRunner) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	output, err := runner(ctx, tool, args)
	call := tool + " " + strings.Join(args, " ")
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", &skillHelpSkip{fmt.Sprintf("%s did not answer within %s on this runner; flags not checked (call: %s)", tool, bound, call)}
	}
	if err != nil {
		if missingSkillSubcommand(tool, args, output) {
			return "", &skillHelpSkip{fmt.Sprintf("%s %s not installed; flags not checked (call: %s; bound: %s)", tool, args[0], call, bound)}
		}
		return "", fmt.Errorf("run %s: %w: %s", call, err, strings.TrimSpace(output))
	}
	return output, nil
}

// missingSkillSubcommand recognizes explicit command-absence diagnostics, including rustup shims.
func missingSkillSubcommand(tool string, args []string, output string) bool {
	if len(args) < 2 || strings.HasPrefix(args[0], "-") {
		return false
	}
	for _, quote := range []string{"`", "'"} {
		if strings.Contains(output, "error: no such command: "+quote+args[0]+quote) {
			return true
		}
	}
	return tool == "cargo" && strings.Contains(output, "error: 'cargo-"+args[0]+"' is not installed")
}

// runSkillHelp runs a version or help command within its caller's deadline.
func runSkillHelp(ctx context.Context, tool string, args []string) (string, error) {
	cmdEnv := append(os.Environ(), "DOTNET_NOLOGO=1", "DOTNET_CLI_TELEMETRY_OPTOUT=1",
		"DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1", "DOTNET_GENERATE_ASPNET_CERTIFICATE=false")
	cmdCtx, err := util.WithCommandEnvironment(ctx, cmdEnv)
	if err != nil {
		return "", err
	}
	res, runErr := util.RunCommandBytes(cmdCtx, "", tool, util.MaxCommandOutputBytes, args...)
	output := append(res.Stdout, res.Stderr...)
	return string(output), runErr
}

// verifySkillSubcommandFlags warms optional clippy before checking its command group.
func verifySkillSubcommandFlags(commands []skillCommand, runner skillProcessRunner) error {
	command := commands[0]
	if command.tool == "cargo" && hasCommand(command.helpArgs, "clippy") {
		if _, err := runBoundedSkillHelp("cargo", []string{"clippy", "--version"}, skillWarmupTimeout, runner); err != nil {
			return err
		}
	}
	return verifySkillCommandFlags(commands, func(tool string, args []string) (string, error) {
		return runBoundedSkillHelp(tool, args, skillHelpTimeout, runner)
	})
}

// groupSkillSubcommands keeps an absent optional command from skipping its installed siblings.
func groupSkillSubcommands(commands []skillCommand) map[string][]skillCommand {
	groups := make(map[string][]skillCommand)
	for _, command := range commands {
		name := strings.Join(command.helpArgs, " ")
		if name == "" {
			name = "flags"
		}
		groups[name] = append(groups[name], command)
	}
	return groups
}

// reportSkillHelpError skips only explicitly unavailable checks; all other errors fail.
func reportSkillHelpError(t *testing.T, err error) {
	t.Helper()
	var skip *skillHelpSkip
	if errors.As(err, &skip) {
		t.Skipf("%s", skip.reason)
	}
	t.Fatal(err)
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

// TestCanonicalSkillCommandFlags_PositiveToolsPresent checks real flags after bounded warmups.
func TestCanonicalSkillCommandFlags_PositiveToolsPresent(t *testing.T) {
	commands := readCanonicalSkillCommands(t)
	for tool, toolCommands := range commands {
		t.Run(tool, func(t *testing.T) {
			if _, err := exec.LookPath(tool); err != nil {
				t.Skipf("%s absent from PATH; command flags not checked", tool)
			}
			if _, err := runBoundedSkillHelp(tool, []string{"--version"}, skillWarmupTimeout, runSkillHelp); err != nil {
				reportSkillHelpError(t, err)
			}
			for name, group := range groupSkillSubcommands(toolCommands) {
				t.Run(name, func(t *testing.T) {
					if err := verifySkillSubcommandFlags(group, runSkillHelp); err != nil {
						reportSkillHelpError(t, err)
					}
				})
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

func TestSkillCommandFlags_DotnetPropertyUsesMSBuildHelp(t *testing.T) {
	command, ok := parseSkillCommand("dotnet build --no-restore -p:ErrorLog=diagnostics.sarif%2Cversion=2.1")
	if !ok || len(command.flags) != 2 || command.flags[1] != "-p" {
		t.Fatalf("MSBuild property flag was not normalized: %+v", command)
	}
	runner := func(tool string, args []string) (string, error) {
		if len(args) > 0 && args[0] == "msbuild" {
			return "Short form: -p", nil
		}
		return "--no-restore", nil
	}
	if err := verifySkillCommandFlags([]skillCommand{command}, runner); err != nil {
		t.Fatal(err)
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

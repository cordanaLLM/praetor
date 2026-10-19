package agenthook

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/router"
	"github.com/cordanaLLM/praetor/internal/util"
)

const pickerOutputLimit = 64 << 10
const pickerConfigLimit = 4096
const pickerTimeout = 20 * time.Second

func evaluateClaudeLaneOffload(ctx context.Context, client string, rule *router.OffloadConfig, brief string, getenv func(string) string) Verdict {
	if client != "claude" {
		return Verdict{Outcome: Allow}
	}
	return evaluateLaneOffload(ctx, rule, brief, getenv)
}

func appendLaneNotice(existing, additional string) string {
	if additional == "" {
		return existing
	}
	if existing == "" {
		return additional
	}
	return existing + "; " + additional
}

func evaluateLaneOffload(ctx context.Context, rule *router.OffloadConfig, brief string, getenv func(string) string) Verdict {
	if rule == nil {
		return Verdict{Outcome: Allow}
	}
	label, err := caveman.ExtractBriefTask(brief)
	if err != nil || !slices.Contains(rule.Labels, label) {
		return Verdict{Outcome: Allow}
	}
	path, configured, err := lanePickerPath(getenv)
	if !configured {
		return Verdict{Outcome: Allow}
	}
	if err != nil {
		return pickerUnavailable(err)
	}
	lane, instruction, keep, err := runLanePicker(ctx, path, label)
	if err != nil {
		return pickerUnavailable(err)
	}
	if keep {
		return Verdict{Outcome: Allow}
	}
	return Verdict{Outcome: Deny, Reason: fmt.Sprintf("[ROUTED] task %s runs on %s, not the Claude lane: %s (lane picker %s)",
		label, lane, instruction, path)}
}

func pickerUnavailable(err error) Verdict {
	return Verdict{Outcome: Allow, Notice: "lane picker unavailable: " + err.Error() + "; dispatch stays on the Claude lane"}
}

func lanePickerPath(getenv func(string) string) (string, bool, error) {
	if getenv == nil {
		return "", false, nil
	}
	if path := getenv("PRAETOR_LANE_PICKER"); path != "" {
		return path, true, nil
	}
	base := getenv("XDG_CONFIG_HOME")
	if base == "" {
		home := getenv("HOME")
		if home == "" {
			return "", false, nil
		}
		base = filepath.Join(home, ".config")
	}
	configPath := filepath.Join(base, "praetor", "lane-picker")
	data, err := util.ReadFileLimited(configPath, pickerConfigLimit)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", true, fmt.Errorf("read %s: %w", configPath, err)
	}
	line, _, _ := strings.Cut(string(data), "\n")
	return strings.TrimSpace(line), true, nil
}

func runLanePicker(ctx context.Context, path, label string) (string, string, bool, error) {
	if !filepath.IsAbs(path) || strings.IndexFunc(path, unicode.IsControl) >= 0 {
		return "", "", false, errors.New("picker path must be absolute and printable")
	}
	ctx, cancel := context.WithTimeout(ctx, pickerTimeout)
	defer cancel()
	// RunCommandBytes supplies a null stdin, bounded stdout and process-group cleanup.
	output, err := util.RunCommandBytes(ctx, "", path, pickerOutputLimit, "pick", label)
	if ctx.Err() != nil {
		return "", "", false, fmt.Errorf("picker timed out or was canceled: %w", ctx.Err())
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 3 {
		return "", "", true, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("run picker %s: %w", path, err)
	}
	lane, instruction, parseErr := parsePickerOutput(output.Stdout)
	if parseErr != nil {
		return "", "", false, parseErr
	}
	return lane, instruction, false, nil
}

func parsePickerOutput(data []byte) (string, string, error) {
	line, _, _ := strings.Cut(string(data), "\n")
	line = strings.TrimSuffix(line, "\r")
	lane, instruction, ok := strings.Cut(line, " ")
	if !utf8.ValidString(line) || !ok || lane == "" || strings.EqualFold(lane, "claude") ||
		instruction == "" || instruction != strings.TrimSpace(instruction) ||
		strings.IndexFunc(line, unicode.IsControl) >= 0 {
		return "", "", errors.New("picker stdout first line must be '<lane> <instruction>' for a lane off Claude")
	}
	return lane, instruction, nil
}

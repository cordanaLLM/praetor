package runner

import (
	"fmt"
	"strings"

	"github.com/cordanaLLM/standards/internal/config"
)

// ResolveRunner determines the appropriate runner spec for the given target OS, architecture, and GPU requirement.
func ResolveRunner(policy *config.RunnerPolicy, osName, arch string, isGPU bool) (config.RunnerSpec, error) {
	if policy == nil {
		return config.RunnerSpec{}, fmt.Errorf("runner policy cannot be nil")
	}

	normOS := normalizeOS(osName)
	normArch := normalizeArch(arch)
	key := fmt.Sprintf("%s/%s", normOS, normArch)

	if isGPU && normOS == "linux" {
		key = "linux/gpu"
	}

	spec, found := policy.Routing[key]
	if !found {
		// Fallback to default
		return config.RunnerSpec{
			Type:      "self-hosted-arc",
			RunsOn:    []string{policy.Default},
			Ephemeral: true,
		}, nil
	}

	if err := enforcePlatformConstraints(normOS, spec); err != nil {
		return config.RunnerSpec{}, err
	}

	return spec, nil
}

func normalizeOS(osName string) string {
	s := strings.ToLower(strings.TrimSpace(osName))
	switch s {
	case "macos", "osx", "darwin":
		return "darwin"
	default:
		return s
	}
}

func normalizeArch(arch string) string {
	s := strings.ToLower(strings.TrimSpace(arch))
	switch s {
	case "x86_64", "x64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	default:
		return s
	}
}

func enforcePlatformConstraints(osName string, spec config.RunnerSpec) error {
	if osName == "darwin" && spec.Type == "self-hosted-arc" {
		// Enforce constraint: Darwin builds cannot execute on Linux-only ARC clusters
		return fmt.Errorf("platform constraint violation: darwin/macOS target cannot run on self-hosted Linux ARC; route to github-hosted")
	}
	return nil
}

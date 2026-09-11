package builder

import (
	"errors"
	"fmt"
	"strings"
)

// NativeGPUConfig holds toolchain, standard, and accelerator settings.
type NativeGPUConfig struct {
	CStandard    string   `json:"c_standard" yaml:"c_standard"`         // "c11", "c17", "c23"
	CppStandard  string   `json:"cpp_standard" yaml:"cpp_standard"`     // "c++17", "c++20", "c++23"
	Accelerators []string `json:"accelerators" yaml:"accelerators"`     // "cuda", "rocm", "vulkan"
	Libraries    []string `json:"libraries" yaml:"libraries"`           // "ffmpeg", "libvmaf"
	Sanitizers   []string `json:"sanitizers" yaml:"sanitizers"`         // "address", "undefined", "thread"
	BuildSystem  string   `json:"build_system" yaml:"build_system"`     // "meson", "cmake"
	EnableLTO    bool     `json:"enable_lto" yaml:"enable_lto"`
}

// DefaultNativeGPUConfig returns a standard hardened configuration.
func DefaultNativeGPUConfig() *NativeGPUConfig {
	return &NativeGPUConfig{
		CStandard:    "c23",
		CppStandard:  "c++20",
		Accelerators: []string{"cuda", "vulkan"},
		Libraries:    []string{"ffmpeg", "libvmaf"},
		Sanitizers:   []string{"address", "undefined"},
		BuildSystem:  "meson",
		EnableLTO:    true,
	}
}

// GenerateMesonArgs converts the NativeGPUConfig into command line flags for meson setup.
func (n *NativeGPUConfig) GenerateMesonArgs() ([]string, error) {
	if n == nil {
		return nil, errors.New("native config cannot be nil")
	}

	args := []string{
		fmt.Sprintf("-Dc_std=%s", validateStandard(n.CStandard, "c23")),
		fmt.Sprintf("-Dcpp_std=%s", validateStandard(n.CppStandard, "c++20")),
		"-Dbuildtype=release",
		"-Dwarning_level=3",
		"-Dwerror=true",
	}

	if n.EnableLTO {
		args = append(args, "-Db_lto=true")
	}

	if len(n.Sanitizers) > 0 {
		args = append(args, fmt.Sprintf("-Db_sanitize=%s", strings.Join(n.Sanitizers, ",")))
	}

	for _, acc := range n.Accelerators {
		args = append(args, fmt.Sprintf("-Denable_%s=enabled", strings.ToLower(acc)))
	}

	for _, lib := range n.Libraries {
		args = append(args, fmt.Sprintf("-Denable_%s=enabled", strings.ToLower(lib)))
	}

	return args, nil
}

// GenerateCMakeArgs converts the NativeGPUConfig into cmake invocation flags.
func (n *NativeGPUConfig) GenerateCMakeArgs() ([]string, error) {
	if n == nil {
		return nil, errors.New("native config cannot be nil")
	}

	cStdNum := strings.TrimPrefix(n.CStandard, "c")
	cppStdNum := strings.TrimPrefix(n.CppStandard, "c++")

	args := []string{
		"-DCMAKE_BUILD_TYPE=Release",
		fmt.Sprintf("-DCMAKE_C_STANDARD=%s", cStdNum),
		fmt.Sprintf("-DCMAKE_CXX_STANDARD=%s", cppStdNum),
		"-DCMAKE_C_STANDARD_REQUIRED=ON",
		"-DCMAKE_CXX_STANDARD_REQUIRED=ON",
	}

	if n.EnableLTO {
		args = append(args, "-DCMAKE_INTERPROCEDURAL_OPTIMIZATION=TRUE")
	}

	for _, acc := range n.Accelerators {
		args = append(args, fmt.Sprintf("-DWITH_%s=ON", strings.ToUpper(acc)))
	}

	for _, lib := range n.Libraries {
		args = append(args, fmt.Sprintf("-DWITH_%s=ON", strings.ToUpper(lib)))
	}

	return args, nil
}

func validateStandard(std, fallback string) string {
	std = strings.TrimSpace(std)
	if std == "" {
		return fallback
	}
	return std
}

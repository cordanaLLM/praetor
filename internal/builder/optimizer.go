package builder

import (
	"errors"
	"strings"
)

// OptimizationPlan declares suggested compiler flags and pruning directives.
// These options are not evidence of execution, compatibility, or measured improvement.
type OptimizationPlan struct {
	Runtime       string   `json:"runtime" yaml:"runtime"`
	Flags         []string `json:"flags" yaml:"flags"`
	PrunedModules []string `json:"pruned_modules" yaml:"pruned_modules"`
	PurgeCSS      bool     `json:"purge_css" yaml:"purge_css"`
	StripSymbols  bool     `json:"strip_symbols" yaml:"strip_symbols"`
	BytecodeClean bool     `json:"bytecode_clean" yaml:"bytecode_clean"`
}

// PreBuildOptimizer prepares option declarations; it does not execute optimization.
type PreBuildOptimizer struct{}

// NewPreBuildOptimizer creates a ready-to-use pre-build optimizer.
func NewPreBuildOptimizer() *PreBuildOptimizer {
	return &PreBuildOptimizer{}
}

// Plan calculates the optimization plan for a specific runtime and active capability set.
func (p *PreBuildOptimizer) Plan(runtime string, activeCaps []string) *OptimizationPlan {
	plan := &OptimizationPlan{
		Runtime:       runtime,
		StripSymbols:  true,
		Flags:         make([]string, 0),
		PrunedModules: make([]string, 0),
	}

	capMap := make(map[string]bool, len(activeCaps))
	for _, c := range activeCaps {
		capMap[strings.ToLower(strings.TrimSpace(c))] = true
	}

	switch runtime {
	case "go":
		p.planGo(plan, capMap)
	case "svelte", "typescript", "node":
		p.planSvelte(plan, capMap)
	case "python":
		p.planPython(plan, capMap)
	case "rust":
		p.planRust(plan, capMap)
	case "native-gpu", "native", "c", "cpp":
		p.planNative(plan, capMap)
	default:
		plan.Flags = append(plan.Flags, "-O2")
	}

	return plan
}

func (p *PreBuildOptimizer) planGo(plan *OptimizationPlan, capMap map[string]bool) {
	plan.Flags = append(plan.Flags, "-ldflags=-s -w", "-trimpath")
	if !capMap["cgo"] {
		plan.Flags = append(plan.Flags, "CGO_ENABLED=0")
	}
	if !capMap["net/http"] && !capMap["api"] {
		plan.PrunedModules = append(plan.PrunedModules, "net/http/pprof")
	}
}

func (p *PreBuildOptimizer) planSvelte(plan *OptimizationPlan, capMap map[string]bool) {
	plan.PurgeCSS = true
	plan.Flags = append(plan.Flags, "--minify", "--treeshake=safest")
	if !capMap["charts"] && !capMap["visualization"] {
		plan.PrunedModules = append(plan.PrunedModules, "chart.js", "d3")
	}
	if !capMap["lucide"] && !capMap["icons"] {
		plan.PrunedModules = append(plan.PrunedModules, "lucide-svelte/full")
	}
}

func (p *PreBuildOptimizer) planPython(plan *OptimizationPlan, capMap map[string]bool) {
	plan.BytecodeClean = true
	plan.Flags = append(plan.Flags, "-OO", "--no-compile-debug")
	if !capMap["torch"] && !capMap["gpu"] {
		plan.PrunedModules = append(plan.PrunedModules, "torch", "torchvision", "cuda")
	}
	if !capMap["test"] && !capMap["qa"] {
		plan.PrunedModules = append(plan.PrunedModules, "pytest", "mypy")
	}
}

func (p *PreBuildOptimizer) planRust(plan *OptimizationPlan, capMap map[string]bool) {
	plan.Flags = append(plan.Flags, "opt-level=3", "lto=fat", "codegen-units=1", "panic=abort")
	if !capMap["async"] {
		plan.PrunedModules = append(plan.PrunedModules, "tokio/full")
	}
}

func (p *PreBuildOptimizer) planNative(plan *OptimizationPlan, capMap map[string]bool) {
	plan.Flags = append(plan.Flags, "-O3", "-ffunction-sections", "-fdata-sections", "-Wl,--gc-sections")
	if !capMap["cuda"] {
		plan.PrunedModules = append(plan.PrunedModules, "cuda_runtime")
	}
	if !capMap["vulkan"] {
		plan.PrunedModules = append(plan.PrunedModules, "vulkan_headers")
	}
}

// Optimize copies a plan into in-memory target options. No dependencies or artifacts
// are changed, and no compiler or optimization tool is executed.
func (p *PreBuildOptimizer) Optimize(target *TargetConfig, plan *OptimizationPlan) error {
	if target == nil {
		return errors.New("target configuration cannot be nil")
	}
	if plan == nil {
		return errors.New("optimization plan cannot be nil")
	}
	if target.Options == nil {
		target.Options = make(map[string]string)
	}

	target.Options["optimization_flags"] = strings.Join(plan.Flags, " ")
	if len(plan.PrunedModules) > 0 {
		target.Options["pruned_modules"] = strings.Join(plan.PrunedModules, ",")
	}
	if plan.PurgeCSS {
		target.Options["purge_css"] = "true"
	}
	if plan.StripSymbols {
		target.Options["strip_symbols"] = "true"
	}
	if plan.BytecodeClean {
		target.Options["bytecode_clean"] = "true"
	}

	return nil
}

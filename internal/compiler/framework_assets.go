package compiler

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// FrameworkKitConfig defines metadata and assets to compile for a language builder kit.
type FrameworkKitConfig struct {
	KitName     string   `json:"kit_name" yaml:"kit_name"` // e.g. "sveltesentio", "template-native-gpu"
	Language    string   `json:"language" yaml:"language"` // e.g. "svelte", "c", "python", "rust"
	Version     string   `json:"version" yaml:"version"`   // e.g. "1.0.0"
	Description string   `json:"description" yaml:"description"`
	Rules       []string `json:"rules" yaml:"rules"`           // Agent behavior rules
	Skills      []string `json:"skills" yaml:"skills"`         // Recommended agent skills
	Components  []string `json:"components" yaml:"components"` // Exported modules/components
}

// CompiledFrameworkAssets captures the paths and generated files from kit compilation.
type CompiledFrameworkAssets struct {
	KitName         string            `json:"kit_name"`
	LLMsTxtPath     string            `json:"llms_txt_path"`
	LLMsFullTxtPath string            `json:"llms_full_txt_path"`
	AgentRulePath   string            `json:"agent_rule_path"`
	Templates       map[string]string `json:"templates"`
}

// CompileFrameworkAssets generates dual-surface docs, agent rules, and starter templates.
func CompileFrameworkAssets(ctx context.Context, kit *FrameworkKitConfig, outputDir string) (*CompiledFrameworkAssets, error) {
	if ctx == nil {
		return nil, errors.New("context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if kit == nil {
		return nil, errors.New("framework kit config cannot be nil")
	}
	if kit.KitName == "" {
		return nil, errors.New("kit_name cannot be empty")
	}

	res := &CompiledFrameworkAssets{
		KitName:   kit.KitName,
		Templates: make(map[string]string),
	}

	if err := util.MkdirSecure(outputDir, 0o750); err != nil {
		return nil, fmt.Errorf("failed to create output dir: %w", err)
	}

	if err := generateDocsSurfaces(kit, outputDir, res); err != nil {
		return nil, err
	}

	if err := generateAgentRules(kit, outputDir, res); err != nil {
		return nil, err
	}

	if err := generateStarterTemplates(kit, outputDir, res); err != nil {
		return nil, err
	}

	return res, nil
}

func generateDocsSurfaces(kit *FrameworkKitConfig, outputDir string, res *CompiledFrameworkAssets) error {
	llmsTxt := fmt.Sprintf("# %s\n\n> %s\n\n## Language: %s (v%s)\n\n### Components\n",
		kit.KitName, kit.Description, kit.Language, kit.Version)
	for _, c := range kit.Components {
		llmsTxt += fmt.Sprintf("- [%s](file:///components/%s): Core component\n", c, c)
	}

	llmsPath := filepath.Join(outputDir, "llms.txt")
	if err := util.WriteFileSecure(llmsPath, []byte(llmsTxt), 0o600); err != nil {
		return fmt.Errorf("write llms.txt: %w", err)
	}
	res.LLMsTxtPath = llmsPath

	fullPath := filepath.Join(outputDir, "llms-full.txt")
	fullContent := llmsTxt + "\n### Agent Behavioral Guidelines\n"
	for _, r := range kit.Rules {
		fullContent += fmt.Sprintf("- %s\n", r)
	}
	if err := util.WriteFileSecure(fullPath, []byte(fullContent), 0o600); err != nil {
		return fmt.Errorf("write llms-full.txt: %w", err)
	}
	res.LLMsFullTxtPath = fullPath
	return nil
}

func generateAgentRules(kit *FrameworkKitConfig, outputDir string, res *CompiledFrameworkAssets) error {
	rulesDir := filepath.Join(outputDir, ".agents", "rules")
	if err := util.MkdirSecure(rulesDir, 0o750); err != nil {
		return fmt.Errorf("mkdir agent rules: %w", err)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s Autonomous Agent Operating Rules\n\n", kit.KitName)
	fmt.Fprintf(&sb, "Language Target: %s\n\n", kit.Language)
	sb.WriteString("## Directives\n")
	for i, r := range kit.Rules {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, r)
	}
	if len(kit.Skills) > 0 {
		sb.WriteString("\n## Recommended Skills\n")
		for _, s := range kit.Skills {
			fmt.Fprintf(&sb, "- `%s`\n", s)
		}
	}

	rulePath := filepath.Join(rulesDir, fmt.Sprintf("%s.md", kit.KitName))
	if err := util.WriteFileSecure(rulePath, []byte(sb.String()), 0o600); err != nil {
		return fmt.Errorf("write agent rule: %w", err)
	}
	res.AgentRulePath = rulePath
	return nil
}

func generateStarterTemplates(kit *FrameworkKitConfig, outputDir string, res *CompiledFrameworkAssets) error {
	tmplDir := filepath.Join(outputDir, "templates")
	if err := util.MkdirSecure(tmplDir, 0o750); err != nil {
		return fmt.Errorf("mkdir templates: %w", err)
	}

	buildYaml := fmt.Sprintf("version: 1\nproject: %s\noutput_dir: dist\noptimize: true\ntargets:\n  main:\n    runtime: %s\n    entrypoint: ./src\n", kit.KitName, kit.Language)
	buildPath := filepath.Join(tmplDir, ".framework-build.yaml")
	if err := util.WriteFileSecure(buildPath, []byte(buildYaml), 0o600); err != nil {
		return fmt.Errorf("write build template: %w", err)
	}
	res.Templates[".framework-build.yaml"] = buildPath

	readmeContent := fmt.Sprintf("# %s Starter Kit\n\n%s\n\nLanguage: %s\n", kit.KitName, kit.Description, kit.Language)
	readmePath := filepath.Join(tmplDir, "README.md")
	if err := util.WriteFileSecure(readmePath, []byte(readmeContent), 0o600); err != nil {
		return fmt.Errorf("write readme template: %w", err)
	}
	res.Templates["README.md"] = readmePath

	return nil
}

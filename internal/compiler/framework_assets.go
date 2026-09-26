package compiler

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// kitNamePattern keeps kit_name to one file-name component. kit_name names the agent rule
// file under .agents/rules/, so a path separator or a ".." would write outside that
// directory, and a leading dot would hide the file (BUG-618).
var kitNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// maxKitConfigBytes bounds the kit config read (HISS-02); a kit config is a short list of
// names and rules, so a larger file is a mistake, not a kit.
const maxKitConfigBytes = 1 << 20

// ErrInvalidKitName reports a kit_name that is not one safe file-name component.
var ErrInvalidKitName = errors.New("kit_name must be 1-64 characters of letters, digits, '.', '_' or '-', starting with a letter or digit")

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
	if err := validateFrameworkKit(kit); err != nil {
		return nil, err
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

// LoadFrameworkKitConfig reads one framework kit config: a single YAML document with the
// FrameworkKitConfig keys and no others. The kit is validated here as well as in
// CompileFrameworkAssets, so a caller that only loads learns about a bad kit_name early.
func LoadFrameworkKitConfig(path string) (*FrameworkKitConfig, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("framework kit config path cannot be empty")
	}
	data, err := util.ReadConfinedLimited(filepath.Dir(path), filepath.Base(path), maxKitConfigBytes)
	if err != nil {
		return nil, fmt.Errorf("read framework kit config %s: %w", path, err)
	}
	var kit FrameworkKitConfig
	if err := util.DecodeYAMLStrict(data, &kit); err != nil {
		return nil, fmt.Errorf("parse framework kit config %s: %w", path, err)
	}
	if err := validateFrameworkKit(&kit); err != nil {
		return nil, fmt.Errorf("framework kit config %s: %w", path, err)
	}
	return &kit, nil
}

// validateFrameworkKit refuses a kit before anything is written for it.
func validateFrameworkKit(kit *FrameworkKitConfig) error {
	if kit == nil {
		return errors.New("framework kit config cannot be nil")
	}
	if kit.KitName == "" {
		return errors.New("kit_name cannot be empty")
	}
	if !kitNamePattern.MatchString(kit.KitName) {
		return fmt.Errorf("%w: %q", ErrInvalidKitName, kit.KitName)
	}
	return nil
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

	// kit_name is validated as one file-name component; ConfinePath is the second guard
	// WriteFileSecure's contract asks for, and it also refuses a symlink that leaves rulesDir.
	rulePath, err := util.ConfinePath(rulesDir, kit.KitName+".md")
	if err != nil {
		return fmt.Errorf("confine agent rule path: %w", err)
	}
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

package harvester

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	MaxBundleEntries = 500
	MaxCopyBuffer    = 32 * 1024
)

// BundleOptions defines the parameters for harvesting a workstation state bundle.
type BundleOptions struct {
	WorkstationName string `json:"workstation_name"`
	OutputDir       string `json:"output_dir"`
	HomeDir         string `json:"home_dir"`
	DevDir          string `json:"dev_dir"`
	VaultDir        string `json:"vault_dir"`
}

// BundleFileRecord records file metadata and integrity checksum.
type BundleFileRecord struct {
	RelativePath string `json:"relative_path"`
	SizeBytes    int64  `json:"size_bytes"`
	SHA256       string `json:"sha256"`
	Category     string `json:"category"`
}

// WorkstationBundleReport summarizes the harvested bundle contents.
type WorkstationBundleReport struct {
	WorkstationName string             `json:"workstation_name"`
	CapturedAt      time.Time          `json:"captured_at"`
	TotalFiles      int                `json:"total_files"`
	TotalBytes      int64              `json:"total_bytes"`
	Categories      map[string]int     `json:"categories"`
	ManifestPath    string             `json:"manifest_path"`
	Records         []BundleFileRecord `json:"records"`
}

// IngestReport summarizes comparison and ingestion analysis of a bundle against local workstation.
type IngestReport struct {
	WorkstationName string   `json:"workstation_name"`
	BundlePath      string   `json:"bundle_path"`
	NovelSkills     []string `json:"novel_skills"`
	ExistingSkills  []string `json:"existing_skills"`
	NovelMemories   []string `json:"novel_memories"`
	NovelPatches    []string `json:"novel_patches"`
	ValidIntegrity  bool     `json:"valid_integrity"`
}

func copyFileWithHash(src, dst, category, baseDir string) (*BundleFileRecord, error) {
	sFile, err := os.Open(src)
	if err != nil {
		return nil, fmt.Errorf("open source file %s: %w", src, err)
	}
	defer sFile.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return nil, fmt.Errorf("mkdir dst dir: %w", err)
	}

	dFile, err := os.Create(dst)
	if err != nil {
		return nil, fmt.Errorf("create dest file %s: %w", dst, err)
	}
	defer dFile.Close()

	hasher := sha256.New()
	mw := io.MultiWriter(dFile, hasher)

	written, err := io.Copy(mw, sFile)
	if err != nil {
		return nil, fmt.Errorf("copy stream from %s to %s: %w", src, dst, err)
	}

	rel, rErr := filepath.Rel(baseDir, dst)
	if rErr != nil {
		rel = filepath.Base(dst)
	}

	return &BundleFileRecord{
		RelativePath: filepath.ToSlash(rel),
		SizeBytes:    written,
		SHA256:       hex.EncodeToString(hasher.Sum(nil)),
		Category:     category,
	}, nil
}

func bundleSkills(ctx context.Context, srcDir, dstDir, cat, baseDir string, records *[]BundleFileRecord) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return nil
	}

	count := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled during skill bundle: %w", err)
		}
		if count >= MaxBundleEntries {
			break
		}
		count++

		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		skillDir := filepath.Join(srcDir, entry.Name())
		files, fErr := os.ReadDir(skillDir)
		if fErr != nil {
			continue
		}

		for _, f := range files {
			if f.IsDir() {
				continue
			}
			srcFile := filepath.Join(skillDir, f.Name())
			dstFile := filepath.Join(dstDir, entry.Name(), f.Name())
			rec, cErr := copyFileWithHash(srcFile, dstFile, cat, baseDir)
			if cErr == nil {
				*records = append(*records, *rec)
			}
		}
	}
	return nil
}

func bundleClaudeMemories(ctx context.Context, claudeProjectsDir, dstDir, baseDir string, records *[]BundleFileRecord) error {
	entries, err := os.ReadDir(claudeProjectsDir)
	if err != nil {
		return nil
	}

	count := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled during memory bundle: %w", err)
		}
		if count >= MaxBundleEntries {
			break
		}
		count++

		if !entry.IsDir() {
			continue
		}

		memDir := filepath.Join(claudeProjectsDir, entry.Name(), "memory")
		memFiles, mErr := os.ReadDir(memDir)
		if mErr != nil {
			continue
		}

		for _, mf := range memFiles {
			if mf.IsDir() || !strings.HasSuffix(mf.Name(), ".md") {
				continue
			}
			src := filepath.Join(memDir, mf.Name())
			dst := filepath.Join(dstDir, entry.Name(), mf.Name())
			rec, cErr := copyFileWithHash(src, dst, "project-memory", baseDir)
			if cErr == nil {
				*records = append(*records, *rec)
			}
		}
	}
	return nil
}

func bundleTranscripts(ctx context.Context, brainDir, dstDir, baseDir string, records *[]BundleFileRecord) error {
	entries, err := os.ReadDir(brainDir)
	if err != nil {
		return nil
	}

	count := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled during transcript bundle: %w", err)
		}
		if count >= MaxTranscriptsScan {
			break
		}
		count++

		if !entry.IsDir() {
			continue
		}

		tPath := filepath.Join(brainDir, entry.Name(), ".system_generated", "logs", "transcript.jsonl")
		if _, statErr := os.Stat(tPath); statErr != nil {
			continue
		}

		dstFile := filepath.Join(dstDir, fmt.Sprintf("%s.transcript.jsonl", entry.Name()))
		rec, cErr := copyFileWithHash(tPath, dstFile, "agent-transcript", baseDir)
		if cErr == nil {
			*records = append(*records, *rec)
		}
	}
	return nil
}

func copyDirectoryContents(srcDir, dstDir, cat, baseDir string, records *[]BundleFileRecord) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return
	}

	count := 0
	for _, entry := range entries {
		if count >= MaxBundleEntries {
			break
		}
		count++

		src := filepath.Join(srcDir, entry.Name())
		dst := filepath.Join(dstDir, entry.Name())

		if entry.IsDir() {
			subEntries, sErr := os.ReadDir(src)
			if sErr != nil {
				continue
			}
			for _, sub := range subEntries {
				if !sub.IsDir() {
					subSrc := filepath.Join(src, sub.Name())
					subDst := filepath.Join(dst, sub.Name())
					rec, cErr := copyFileWithHash(subSrc, subDst, cat, baseDir)
					if cErr == nil {
						*records = append(*records, *rec)
					}
				}
			}
		} else {
			rec, cErr := copyFileWithHash(src, dst, cat, baseDir)
			if cErr == nil {
				*records = append(*records, *rec)
			}
		}
	}
}

func bundleCLIHistory(homeDir, dstDir, baseDir string, records *[]BundleFileRecord) {
	histPaths := []string{
		filepath.Join(homeDir, "AppData", "Roaming", "Microsoft", "Windows", "PowerShell", "PSReadLine", "ConsoleHost_history.txt"),
		filepath.Join(homeDir, ".bash_history"),
		filepath.Join(homeDir, ".zsh_history"),
	}

	for _, hp := range histPaths {
		if _, err := os.Stat(hp); err == nil {
			dst := filepath.Join(dstDir, filepath.Base(hp))
			rec, cErr := copyFileWithHash(hp, dst, "cli-history", baseDir)
			if cErr == nil {
				*records = append(*records, *rec)
			}
		}
	}
}

func bundlePlugins(ctx context.Context, pluginsDir, dstDir, baseDir string, records *[]BundleFileRecord) {
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		return
	}
	count := 0
	for _, entry := range entries {
		if count >= MaxBundleEntries {
			break
		}
		count++
		if !entry.IsDir() {
			continue
		}
		pDir := filepath.Join(pluginsDir, entry.Name())
		pJSON := filepath.Join(pDir, "plugin.json")
		if _, err := os.Stat(pJSON); err == nil {
			rec, cErr := copyFileWithHash(pJSON, filepath.Join(dstDir, entry.Name(), "plugin.json"), "plugin-manifest", baseDir)
			if cErr == nil {
				*records = append(*records, *rec)
			}
		}
		if err := bundleSkills(ctx, filepath.Join(pDir, "skills"), filepath.Join(dstDir, entry.Name(), "skills"), "plugin-skill", baseDir, records); err != nil {
			return
		}
	}
}

func bundleAgentConfigs(homeDir, dstDir, baseDir string, records *[]BundleFileRecord) {
	configs := []struct {
		path string
		cat  string
	}{
		{filepath.Join(homeDir, ".gemini", "config", "hooks.json"), "agent-config"},
		{filepath.Join(homeDir, ".gemini", "config", "mcp_config.json"), "agent-config"},
		{filepath.Join(homeDir, ".claude", "CLAUDE.md"), "agent-rule"},
		{filepath.Join(homeDir, ".claude", "settings.json"), "agent-config"},
		{filepath.Join(homeDir, ".hindsight", "coding-agent.json"), "hindsight-config"},
		{filepath.Join(homeDir, ".hindsight", "cordana-hindsight-tunnel.ps1"), "hindsight-script"},
		{filepath.Join(homeDir, ".hindsight", "current-workspace.txt"), "hindsight-state"},
	}

	for _, c := range configs {
		if _, err := os.Stat(c.path); err == nil {
			dst := filepath.Join(dstDir, filepath.Base(c.path))
			rec, cErr := copyFileWithHash(c.path, dst, c.cat, baseDir)
			if cErr == nil {
				*records = append(*records, *rec)
			}
		}
	}
}

func copyFilesMatching(dir, dstDir, ext, cat, baseDir string, records *[]BundleFileRecord) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	count := 0
	for _, entry := range entries {
		if count >= MaxBundleEntries {
			break
		}
		count++
		if entry.IsDir() {
			continue
		}
		if ext != "" && !strings.HasSuffix(entry.Name(), ext) {
			continue
		}
		src := filepath.Join(dir, entry.Name())
		dst := filepath.Join(dstDir, entry.Name())
		rec, cErr := copyFileWithHash(src, dst, cat, baseDir)
		if cErr == nil {
			*records = append(*records, *rec)
		}
	}
}

func bundleCodexState(homeDir, baseDir string, records *[]BundleFileRecord) {
	codexDir := filepath.Join(homeDir, ".codex")
	memDir := filepath.Join(codexDir, "memories")

	copyFilesMatching(memDir, filepath.Join(baseDir, "agent-memories", "codex"), ".md", "codex-memory", baseDir, records)
	rollouts := filepath.Join(memDir, "rollout_summaries")
	copyFilesMatching(rollouts, filepath.Join(baseDir, "agent-memories", "codex", "rollout_summaries"), ".md", "codex-rollout", baseDir, records)

	configs := []struct {
		src string
		dst string
		cat string
	}{
		{filepath.Join(codexDir, "config.toml"), filepath.Join(baseDir, "agent-configs", "codex", "config.toml"), "codex-config"},
		{filepath.Join(codexDir, "hooks.json"), filepath.Join(baseDir, "agent-configs", "codex", "hooks.json"), "codex-config"},
		{filepath.Join(codexDir, "session_index.jsonl"), filepath.Join(baseDir, "agent-configs", "codex", "session_index.jsonl"), "codex-session-index"},
		{filepath.Join(codexDir, "models_cache.json"), filepath.Join(baseDir, "agent-configs", "codex", "models_cache.json"), "codex-config"},
		{filepath.Join(codexDir, "external_agent_session_imports.json"), filepath.Join(baseDir, "agent-configs", "codex", "external_agent_session_imports.json"), "codex-config"},
		{filepath.Join(codexDir, "AGENTS.md"), filepath.Join(baseDir, "agent-rules", "codex", "AGENTS.md"), "codex-rule"},
		{filepath.Join(codexDir, "rules", "default.rules"), filepath.Join(baseDir, "agent-rules", "codex", "default.rules"), "codex-rule"},
		{filepath.Join(codexDir, "browser", "config.toml"), filepath.Join(baseDir, "agent-configs", "codex", "browser.toml"), "codex-config"},
		{filepath.Join(codexDir, "computer-use", "config.toml"), filepath.Join(baseDir, "agent-configs", "codex", "computer-use.toml"), "codex-config"},
		{filepath.Join(codexDir, "vendor_imports", "skills-curated-cache.json"), filepath.Join(baseDir, "agent-configs", "codex", "skills-curated-cache.json"), "codex-config"},
	}
	for _, c := range configs {
		if _, err := os.Stat(c.src); err == nil {
			rec, cErr := copyFileWithHash(c.src, c.dst, c.cat, baseDir)
			if cErr == nil {
				*records = append(*records, *rec)
			}
		}
	}
}

func bundleCodexPlugins(homeDir, baseDir string, records *[]BundleFileRecord) {
	cacheDir := filepath.Join(homeDir, ".codex", "plugins", "cache")
	types := []string{"openai-bundled", "openai-curated", "openai-curated-remote", "openai-primary-runtime"}
	for _, tName := range types {
		tDir := filepath.Join(cacheDir, tName)
		entries, err := os.ReadDir(tDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			pName := e.Name()
			srcManifest := filepath.Join(tDir, pName, ".codex-plugin", "plugin.json")
			if _, sErr := os.Stat(srcManifest); sErr == nil {
				dstManifest := filepath.Join(baseDir, "agent-plugins", "codex", tName, pName, "plugin.json")
				rec, cErr := copyFileWithHash(srcManifest, dstManifest, "codex-plugin-manifest", baseDir)
				if cErr == nil {
					*records = append(*records, *rec)
				}
			}
		}
	}
}

func bundleCopilotState(homeDir, baseDir string, records *[]BundleFileRecord) {
	copilotDir := filepath.Join(homeDir, ".copilot")

	configs := []struct {
		src string
		dst string
		cat string
	}{
		{filepath.Join(copilotDir, "config.json"), filepath.Join(baseDir, "agent-configs", "copilot", "config.json"), "copilot-config"},
		{filepath.Join(copilotDir, "mcp-config.json"), filepath.Join(baseDir, "agent-configs", "copilot", "mcp-config.json"), "copilot-config"},
		{filepath.Join(copilotDir, ".datacloud_skills_manifest"), filepath.Join(baseDir, "agent-configs", "copilot", ".datacloud_skills_manifest"), "copilot-manifest"},
	}
	for _, c := range configs {
		if _, err := os.Stat(c.src); err == nil {
			rec, cErr := copyFileWithHash(c.src, c.dst, c.cat, baseDir)
			if cErr == nil {
				*records = append(*records, *rec)
			}
		}
	}

	copyFilesMatching(filepath.Join(copilotDir, "hooks"), filepath.Join(baseDir, "agent-configs", "copilot", "hooks"), "", "copilot-hook", baseDir, records)
	copyFilesMatching(filepath.Join(copilotDir, "logs"), filepath.Join(baseDir, "cli-logs", "copilot"), ".log", "copilot-log", baseDir, records)
	copyFilesMatching(filepath.Join(copilotDir, "ide"), filepath.Join(baseDir, "agent-configs", "copilot", "ide"), ".lock", "copilot-ide-lock", baseDir, records)
}

func bundleClaudeAdditional(homeDir, baseDir string, records *[]BundleFileRecord) {
	claudeDir := filepath.Join(homeDir, ".claude")
	configs := []struct {
		src string
		dst string
		cat string
	}{
		{filepath.Join(homeDir, ".claude.json"), filepath.Join(baseDir, "agent-configs", "claude", "claude.json"), "claude-config"},
		{filepath.Join(homeDir, ".claude.json.backup"), filepath.Join(baseDir, "agent-configs", "claude", "claude.json.backup"), "claude-config"},
		{filepath.Join(homeDir, "AppData", "Roaming", "Claude", "claude_desktop_config.json"), filepath.Join(baseDir, "agent-configs", "claude", "claude_desktop_config.json"), "claude-desktop-config"},
		{filepath.Join(claudeDir, "settings.json"), filepath.Join(baseDir, "agent-configs", "claude", "settings.json"), "claude-config"},
		{filepath.Join(claudeDir, "CLAUDE.md"), filepath.Join(baseDir, "agent-rules", "claude", "CLAUDE.md"), "claude-rule"},
		{filepath.Join(claudeDir, "plugins", "installed_plugins.json"), filepath.Join(baseDir, "agent-plugins", "claude", "installed_plugins.json"), "claude-plugin"},
		{filepath.Join(claudeDir, "plugins", "known_marketplaces.json"), filepath.Join(baseDir, "agent-plugins", "claude", "known_marketplaces.json"), "claude-plugin"},
		{filepath.Join(claudeDir, "plugins", "blocklist.json"), filepath.Join(baseDir, "agent-plugins", "claude", "blocklist.json"), "claude-plugin"},
	}
	for _, c := range configs {
		if _, err := os.Stat(c.src); err == nil {
			rec, cErr := copyFileWithHash(c.src, c.dst, c.cat, baseDir)
			if cErr == nil {
				*records = append(*records, *rec)
			}
		}
	}
}

func harvestAllSkills(ctx context.Context, homeDir, baseDir string, records *[]BundleFileRecord) error {
	if err := bundleSkills(ctx, filepath.Join(homeDir, ".copilot", "skills"), filepath.Join(baseDir, "agent-skills", "copilot"), "skill", baseDir, records); err != nil {
		return fmt.Errorf("bundle copilot skills: %w", err)
	}
	if err := bundleSkills(ctx, filepath.Join(homeDir, ".codex", "skills"), filepath.Join(baseDir, "agent-skills", "codex"), "skill", baseDir, records); err != nil {
		return fmt.Errorf("bundle codex skills: %w", err)
	}
	if err := bundleSkills(ctx, filepath.Join(homeDir, ".codex", "skills", ".system"), filepath.Join(baseDir, "agent-skills", "codex-system"), "skill", baseDir, records); err != nil {
		return fmt.Errorf("bundle codex system skills: %w", err)
	}
	if err := bundleSkills(ctx, filepath.Join(homeDir, ".claude", "skills"), filepath.Join(baseDir, "agent-skills", "claude"), "skill", baseDir, records); err != nil {
		return fmt.Errorf("bundle claude skills: %w", err)
	}
	if err := bundleSkills(ctx, filepath.Join(homeDir, ".gemini", "config", "skills"), filepath.Join(baseDir, "agent-skills", "gemini"), "skill", baseDir, records); err != nil {
		return fmt.Errorf("bundle gemini skills: %w", err)
	}
	if err := bundleSkills(ctx, filepath.Join(homeDir, ".agents", "skills"), filepath.Join(baseDir, "agent-skills", "universal"), "skill", baseDir, records); err != nil {
		return fmt.Errorf("bundle universal skills: %w", err)
	}
	bundlePlugins(ctx, filepath.Join(homeDir, ".gemini", "config", "plugins"), filepath.Join(baseDir, "agent-plugins", "gemini"), baseDir, records)
	bundlePlugins(ctx, filepath.Join(homeDir, ".agents", "plugins"), filepath.Join(baseDir, "agent-plugins", "universal"), baseDir, records)
	return nil
}

func writeBundleManifest(report *WorkstationBundleReport) error {
	for _, r := range report.Records {
		report.TotalBytes += r.SizeBytes
		report.Categories[r.Category]++
	}

	manifestData, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal bundle manifest: %w", err)
	}

	if err := os.WriteFile(report.ManifestPath, manifestData, 0644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

// BundleWorkstation captures skills, memories, transcripts, patches and CLI history into a bundle.
func BundleWorkstation(ctx context.Context, opts BundleOptions) (*WorkstationBundleReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before workstation bundle: %w", err)
	}

	if opts.OutputDir == "" {
		return nil, fmt.Errorf("output directory is required")
	}

	baseDir := opts.OutputDir
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("create bundle output directory %s: %w", baseDir, err)
	}

	records := make([]BundleFileRecord, 0)

	if err := harvestAllSkills(ctx, opts.HomeDir, baseDir, &records); err != nil {
		return nil, err
	}

	bundleAgentConfigs(opts.HomeDir, filepath.Join(baseDir, "agent-configs"), baseDir, &records)
	bundleCodexState(opts.HomeDir, baseDir, &records)
	bundleCodexPlugins(opts.HomeDir, baseDir, &records)
	bundleCopilotState(opts.HomeDir, baseDir, &records)
	bundleClaudeAdditional(opts.HomeDir, baseDir, &records)

	if err := bundleClaudeMemories(ctx, filepath.Join(opts.HomeDir, ".claude", "projects"), filepath.Join(baseDir, "agent-memories", "claude"), baseDir, &records); err != nil {
		return nil, fmt.Errorf("bundle claude memories: %w", err)
	}
	if err := bundleTranscripts(ctx, filepath.Join(opts.HomeDir, ".gemini", "antigravity-ide", "brain"), filepath.Join(baseDir, "agent-transcripts"), baseDir, &records); err != nil {
		return nil, fmt.Errorf("bundle transcripts: %w", err)
	}

	bundleCLIHistory(opts.HomeDir, filepath.Join(baseDir, "cli-logs"), baseDir, &records)

	if opts.VaultDir != "" {
		copyDirectoryContents(filepath.Join(opts.VaultDir, "dev-inventory"), filepath.Join(baseDir, "dev-inventory"), "inventory", baseDir, &records)
		copyDirectoryContents(filepath.Join(opts.VaultDir, "dev-patches"), filepath.Join(baseDir, "dev-patches"), "patch", baseDir, &records)
	}

	report := &WorkstationBundleReport{
		WorkstationName: opts.WorkstationName,
		CapturedAt:      time.Now(),
		TotalFiles:      len(records),
		Categories:      make(map[string]int),
		ManifestPath:    filepath.Join(baseDir, "manifest.json"),
		Records:         records,
	}

	if err := writeBundleManifest(report); err != nil {
		return nil, err
	}

	return report, nil
}

func extractSkillInfo(relPath string) (string, string) {
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	if len(parts) >= 4 && parts[0] == "agent-skills" {
		name := parts[2]
		if strings.HasPrefix(name, ".") {
			return "", ""
		}
		return name, strings.Join(parts[3:], "/")
	}
	base := filepath.Base(filepath.Dir(relPath))
	if strings.HasPrefix(base, ".") {
		return "", ""
	}
	return base, filepath.Base(relPath)
}

func copyFileSimple(src, dst string) error {
	sFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sFile.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	dFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dFile.Close()

	_, err = io.Copy(dFile, sFile)
	return err
}

func copySkillFile(src, dst string, ingest *IngestReport) {
	if err := copyFileSimple(src, dst); err != nil {
		ingest.ValidIntegrity = false
	}
}

func processSkillRecord(r BundleFileRecord, bundleDir, localSkillsDir string, dryRun bool, ingest *IngestReport, seenNovel, seenExisting map[string]bool) {
	skillName, subPath := extractSkillInfo(r.RelativePath)
	if skillName == "" {
		return
	}

	targetDir := filepath.Join(localSkillsDir, skillName)
	if seenNovel[skillName] {
		if !dryRun && subPath != "" {
			src := filepath.Join(bundleDir, r.RelativePath)
			dst := filepath.Join(targetDir, subPath)
			copySkillFile(src, dst, ingest)
		}
		return
	}
	if seenExisting[skillName] {
		return
	}

	if _, sErr := os.Stat(targetDir); os.IsNotExist(sErr) {
		seenNovel[skillName] = true
		ingest.NovelSkills = append(ingest.NovelSkills, skillName)
		if !dryRun && subPath != "" {
			src := filepath.Join(bundleDir, r.RelativePath)
			dst := filepath.Join(targetDir, subPath)
			copySkillFile(src, dst, ingest)
		}
	} else {
		seenExisting[skillName] = true
		ingest.ExistingSkills = append(ingest.ExistingSkills, skillName)
	}
}

// IngestBundle parses a bundle manifest and cross-references against local workstation skills.
func IngestBundle(ctx context.Context, bundleDir, localSkillsDir string, dryRun bool) (*IngestReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before ingest: %w", err)
	}

	manifestPath := filepath.Join(bundleDir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read bundle manifest: %w", err)
	}

	var rep WorkstationBundleReport
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, fmt.Errorf("unmarshal bundle manifest: %w", err)
	}

	ingest := &IngestReport{
		WorkstationName: rep.WorkstationName,
		BundlePath:      bundleDir,
		NovelSkills:     make([]string, 0),
		ExistingSkills:  make([]string, 0),
		NovelMemories:   make([]string, 0),
		NovelPatches:    make([]string, 0),
		ValidIntegrity:  true,
	}

	seenNovel := make(map[string]bool)
	seenExisting := make(map[string]bool)
	seenMem := make(map[string]bool)
	seenPatch := make(map[string]bool)

	for _, r := range rep.Records {
		switch r.Category {
		case "skill":
			processSkillRecord(r, bundleDir, localSkillsDir, dryRun, ingest, seenNovel, seenExisting)
		case "project-memory":
			if !seenMem[r.RelativePath] {
				seenMem[r.RelativePath] = true
				ingest.NovelMemories = append(ingest.NovelMemories, r.RelativePath)
			}
		case "patch":
			if !seenPatch[r.RelativePath] {
				seenPatch[r.RelativePath] = true
				ingest.NovelPatches = append(ingest.NovelPatches, r.RelativePath)
			}
		}
	}

	sort.Strings(ingest.NovelSkills)
	sort.Strings(ingest.ExistingSkills)
	sort.Strings(ingest.NovelMemories)
	sort.Strings(ingest.NovelPatches)

	return ingest, nil
}

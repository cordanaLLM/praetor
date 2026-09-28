// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package managedasset is the one registry of managed asset families: sets of locked files
// compiled into praetorctl with go:embed that adoption writes into a repository, removes
// again when their facet is disabled, and refuses to touch once an operator edited them.
//
// Three consumers read the registry instead of naming a family: adoption
// (internal/adopt/managed_family.go) emits, removes and refuses the files; audit
// (cmd/standardsctl/audit_documentation.go) compares them byte for byte, one consistent
// checkout line-ending style allowed; and the devcontainer bootstrap
// (internal/devcontainer/bootstrap_source.go) captures the embedded files beside the Go source
// so a binary built from its archive embeds the same bytes. A new family is therefore one
// embedding package of its own plus one entry in Families, and no new code path (HISS-19).
package managedasset

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
	figureassets "github.com/cordanaLLM/praetor/tools/figures"
	markdownassets "github.com/cordanaLLM/praetor/tools/markdownlint"
	"gopkg.in/yaml.v3"
)

const (
	// MaxFamilies bounds every walk over the registry (HISS-02).
	MaxFamilies = 8
	// MaxPriorTexts bounds the earlier texts one family may recognise (HISS-02).
	MaxPriorTexts = 64
	// MaxWorkflowLines bounds the action scan of one hosted workflow (HISS-02).
	MaxWorkflowLines = 4096
	// MaxAttributes bounds the .gitattributes rules one family declares (HISS-02).
	MaxAttributes = 8
	// MaxActionlintLabels bounds the runner labels one family declares to actionlint (HISS-02).
	MaxActionlintLabels = 8
	// DocumentationFacet is the manifest facet that enables documentation governance.
	DocumentationFacet = "docs:seo-portal"
)

// Family is one managed asset family.
type Family struct {
	// Name labels the family in reports and bootstrap errors, e.g. "Markdown".
	Name string
	// Kind is the lowercase noun of the surface the family governs, e.g. "documentation";
	// removal, refusal and audit messages name it.
	Kind string
	// AssetNoun names one asset in adoption reports, e.g. "Markdown governance asset".
	AssetNoun string
	// WorkflowNoun names the hosted workflow in adoption reports.
	WorkflowNoun string
	// Facet is the manifest facet that enables the family.
	Facet string
	// Directory is the repository-relative home of the assets, in slash form.
	Directory string
	// Source is the Go file below Directory carrying the go:embed directive.
	Source string
	// FS is the family's embedded asset tree, rooted at Directory.
	FS fs.FS
	// Assets is the complete inventory, relative to Directory, in emission order.
	Assets []string
	// MaxAssets bounds every walk over Assets (HISS-02).
	MaxAssets int
	// WorkflowFile, StatusContext and Workflow describe the hosted gate: the file adoption
	// writes, the required check it reports, and its exact text. All three are empty for a
	// family without a hosted gate.
	WorkflowFile  string
	StatusContext string
	Workflow      string
	// RefuseForeign makes adoption refuse, even under --force, to overwrite a file at a
	// managed path while none of the family's paths yet holds its canonical bytes: such a
	// file predates adoption and belongs to the repository, not to Praetor.
	RefuseForeign bool
	// Attributes are the .gitattributes rules the family's files need, in order. Adoption
	// writes the rules of every enabled family into one managed block at the tail of
	// .gitattributes and removes the block once no enabled family declares a rule; audit
	// requires the block. Empty for a family whose files survive a line-ending conversion.
	Attributes []string
	// VendoredTree is a glob, relative to Directory, of vendored files that keep their
	// upstream license, and VendoredLicense that license's SPDX identifier. Audit warns when a
	// repository declares its licensing in REUSE.toml but labels the tree with no annotation
	// naming the license. Both are empty for a family that vendors nothing.
	VendoredTree    string
	VendoredLicense string
	// ActionlintLabels are runs-on labels of Workflow that actionlint's built-in table of
	// GitHub-hosted runners does not know. Adoption declares them under self-hosted-runner.labels
	// in the repository's actionlint configuration (internal/adopt/actionlint.go), so a
	// repository that lints its workflows with actionlint accepts a workflow it may not edit.
	// Each is a runs-on value of Workflow and reads back from YAML as the same plain string.
	// Empty for a family without a hosted gate or whose runners actionlint knows.
	ActionlintLabels []string
	// Prior maps the SHA-256, in lowercase hex, of every text an earlier Praetor shipped at
	// one of the family's managed paths to that path; the digest covers the text with LF line
	// endings (util.CanonicalTextDigest). A file holding exactly such a text is Praetor's own unedited output, so
	// adoption refreshes it without --force and a disabled facet removes it. Audit still
	// fails on it, naming plain adoption as the repair. An edited file matches no digest and
	// keeps the --force contract.
	Prior map[string]string
}

// Families returns the registry in its fixed order: the order adoption emits and audit
// checks the families in.
func Families() []Family {
	return []Family{markdown(), figureEngine()}
}

func markdown() Family {
	return Family{
		Name:          "Markdown",
		Kind:          "documentation",
		AssetNoun:     "Markdown governance asset",
		WorkflowNoun:  "documentation governance workflow",
		Facet:         DocumentationFacet,
		Directory:     markdownassets.Directory,
		Source:        markdownassets.SourceFile,
		FS:            markdownassets.FS(),
		Assets:        markdownassets.Names(),
		MaxAssets:     markdownassets.MaxAssets,
		WorkflowFile:  markdownassets.WorkflowFile,
		StatusContext: markdownassets.StatusContext,
		Workflow:      markdownassets.Workflow,
		Prior:         markdownassets.PriorDigests(),

		ActionlintLabels: markdownassets.ActionlintLabels(),
	}
}

// figureEngine is the interactive figure engine of the documentation facet
// (docs/adr/0016-figures-for-adopters.md, sections 2 and 5). tools/figures is a name a
// repository may already use, so adoption refuses to overwrite a file it finds there first.
// It has no hosted workflow of its own: the Markdown family's workflow runs its checks, under
// the one Documentation Governance context. Its engine files, specs and outputs are hashed, so
// its attribute rules keep them LF on every platform and the vendored interfig files unconverted,
// and audit warns while a REUSE.toml does not label those files MIT.
//
// Prior budget: Validate allows MaxPriorTexts (64) earlier texts per family, and every managed
// file a change rewrites costs one entry. A React, react-dom or scheduler bump rebuilds
// dist/player.js and dist/THIRD-PARTY-LICENSES.txt, two entries; an esbuild bump rebuilds
// dist/loader.js and dist/player.js, two; an interfig bump also moves the vendored render files
// and vendor.json, up to eight; an edit to one script or to README.md costs one. A change that
// would pass the bound fails TestFamiliesRegistryIsValid; the way out is an operator decision,
// either retiring the oldest entries, whose unedited copies then need --force, or raising
// MaxPriorTexts.
func figureEngine() Family {
	return Family{
		Name:            "Figure engine",
		Kind:            "documentation",
		AssetNoun:       "figure engine asset",
		Facet:           DocumentationFacet,
		Directory:       figureassets.Directory,
		Source:          figureassets.SourceFile,
		FS:              figureassets.FS(),
		Assets:          figureassets.Names(),
		MaxAssets:       figureassets.MaxAssets,
		RefuseForeign:   true,
		Attributes:      figureassets.Attributes(),
		VendoredTree:    figureassets.VendoredTree,
		VendoredLicense: figureassets.VendoredLicense,
		Prior:           figureassets.PriorDigests(),
	}
}

// ForFacet returns the families facet enables, in registry order.
func ForFacet(facet string) []Family {
	return selectFacet(Families(), facet)
}

// selectFacet returns the families of all that facet enables, reading at most MaxFamilies.
func selectFacet(all []Family, facet string) []Family {
	selected := make([]Family, 0, len(all))
	for index := 0; index < len(all) && index < MaxFamilies; index++ {
		if all[index].Facet == facet {
			selected = append(selected, all[index])
		}
	}
	return selected
}

// AttributesOf returns the .gitattributes rules of families, family by family in order.
func AttributesOf(families []Family) []string {
	rules := make([]string, 0, len(families)*MaxAttributes)
	for index := 0; index < len(families) && index < MaxFamilies; index++ {
		attributes := families[index].Attributes
		rules = append(rules, attributes[:min(len(attributes), MaxAttributes)]...)
	}
	return rules
}

// ActionlintLabelsOf returns the actionlint labels of families, each once, in family and
// declaration order, and at most MaxActionlintLabels of them.
func ActionlintLabelsOf(families []Family) []string {
	labels := make([]string, 0, MaxActionlintLabels)
	for index := 0; index < len(families) && index < MaxFamilies; index++ {
		declared := families[index].ActionlintLabels
		for item := 0; item < len(declared) && item < MaxActionlintLabels && len(labels) < MaxActionlintLabels; item++ {
			if !slices.Contains(labels, declared[item]) {
				labels = append(labels, declared[item])
			}
		}
	}
	return labels
}

// Names returns a copy of the inventory, cut at MaxAssets.
func (f Family) Names() []string {
	if f.MaxAssets <= 0 {
		return nil
	}
	return slices.Clone(f.Assets[:min(len(f.Assets), f.MaxAssets)])
}

// Read returns a private copy of one inventory asset.
func (f Family) Read(name string) ([]byte, error) {
	return util.ReadEmbeddedAsset(f.FS, path.Base(f.Directory), f.Names(), name)
}

// AssetPath returns the repository-relative slash path of one asset.
func (f Family) AssetPath(name string) string {
	return path.Join(f.Directory, name)
}

// AssetPaths returns every asset's repository-relative path, in inventory order.
func (f Family) AssetPaths() []string {
	names := f.Names()
	paths := make([]string, 0, len(names))
	for index := 0; index < len(names) && index < f.MaxAssets; index++ {
		paths = append(paths, f.AssetPath(names[index]))
	}
	return paths
}

// ManagedPaths returns every path the family alone owns: its workflow, when it has one,
// followed by its assets. At most MaxAssets+1 paths.
func (f Family) ManagedPaths() []string {
	if f.WorkflowFile == "" {
		return f.AssetPaths()
	}
	return append([]string{f.WorkflowFile}, f.AssetPaths()...)
}

// Canonical returns the exact bytes the family owns at the repository-relative path rel.
// owned is false, with no error, when rel is not one of the family's managed paths.
func (f Family) Canonical(rel string) (data []byte, owned bool, err error) {
	if f.WorkflowFile != "" && rel == f.WorkflowFile {
		return []byte(f.Workflow), true, nil
	}
	name, below := strings.CutPrefix(rel, f.Directory+"/")
	if !below || !slices.Contains(f.Names(), name) {
		return nil, false, nil
	}
	data, err = f.Read(name)
	return data, true, err
}

// PriorText reports whether actual is, in one consistent line-ending style, a text the family
// shipped at rel before its current canonical text (Prior).
func (f Family) PriorText(rel string, actual []byte) bool {
	known, _ := f.PriorRendering(rel, actual)
	return known
}

// PriorRendering reports whether actual is a text the family shipped at rel before its current
// canonical text (Prior), and whether actual is its CRLF checkout, so a refresh can keep the
// file's style. Prior is read with util.LookupCanonicalText, the lookup adoption applies to
// every other earlier-text set: an LF text and its CRLF checkout match, while an edit, mixed
// line endings or a lone carriage return match nothing.
func (f Family) PriorRendering(rel string, actual []byte) (known, crlf bool) {
	owner, known, crlf := util.LookupCanonicalText(actual, f.Prior)
	return known && owner == rel, crlf
}

// EmbedDirective returns the exact go:embed line Source must carry: the inventory, in order.
func (f Family) EmbedDirective() string {
	return "//go:embed " + strings.Join(f.Names(), " ")
}

// Validate reports the first structural defect of a family declaration: a missing label, an
// inventory that is empty, over its bound, duplicated or not a clean relative path, a Source
// outside Directory, a partial hosted-gate declaration, an actionlint label its workflow does
// not run on, or a Prior entry that is not a digest of an earlier text at one of its managed
// paths.
func (f Family) Validate() error {
	for _, field := range [][2]string{
		{"name", f.Name}, {"kind", f.Kind}, {"asset noun", f.AssetNoun}, {"facet", f.Facet},
		{"directory", f.Directory}, {"source", f.Source},
	} {
		if strings.TrimSpace(field[1]) == "" {
			return fmt.Errorf("managed asset family %q declares no %s", f.Name, field[0])
		}
	}
	if !cleanRelative(f.Directory) || path.Dir(f.Source) != f.Directory || f.FS == nil {
		return fmt.Errorf("managed asset family %q must embed its assets from a Source file directly in its clean relative Directory", f.Name)
	}
	if err := f.validateInventory(); err != nil {
		return err
	}
	if err := f.validateWorkflow(); err != nil {
		return err
	}
	if err := f.validateActionlintLabels(); err != nil {
		return err
	}
	if err := f.validateAttributes(); err != nil {
		return err
	}
	return f.validatePrior()
}

// validateAttributes requires at most MaxAttributes rules, each one non-blank line that is not a
// comment, and a VendoredTree declared together with its license as a clean relative glob.
func (f Family) validateAttributes() error {
	if len(f.Attributes) > MaxAttributes {
		return fmt.Errorf("managed asset family %q declares %d attribute rules, want at most %d", f.Name, len(f.Attributes), MaxAttributes)
	}
	for index := 0; index < len(f.Attributes) && index < MaxAttributes; index++ {
		if !attributeRuleLine(f.Attributes[index]) {
			return fmt.Errorf("managed asset family %q attribute rule %q is not one trimmed, non-comment line", f.Name, f.Attributes[index])
		}
	}
	return f.validateVendored()
}

// attributeRuleLine reports whether rule is one non-blank, trimmed line that is not a comment.
func attributeRuleLine(rule string) bool {
	return rule != "" && strings.TrimSpace(rule) == rule && !strings.HasPrefix(rule, "#") && !strings.ContainsAny(rule, "\r\n\x00")
}

// validateVendored requires VendoredTree and VendoredLicense together, the tree as a clean
// relative glob and the license as one SPDX term.
func (f Family) validateVendored() error {
	if (f.VendoredTree == "") != (f.VendoredLicense == "") {
		return fmt.Errorf("managed asset family %q declares a vendored tree without its license, or the reverse", f.Name)
	}
	if f.VendoredTree != "" && (!cleanRelative(f.VendoredTree) || strings.ContainsAny(f.VendoredLicense, " \t\r\n\x00")) {
		return fmt.Errorf("managed asset family %q vendored tree %q or license %q is malformed", f.Name, f.VendoredTree, f.VendoredLicense)
	}
	return nil
}

// VendoredGlob returns the repository-relative glob of the family's vendored tree, or "" when it
// vendors nothing.
func (f Family) VendoredGlob() string {
	if f.VendoredTree == "" {
		return ""
	}
	return f.AssetPath(f.VendoredTree)
}

func (f Family) validateInventory() error {
	if f.MaxAssets <= 0 || len(f.Assets) == 0 || len(f.Assets) > f.MaxAssets {
		return fmt.Errorf("managed asset family %q inventory holds %d assets, want 1..%d", f.Name, len(f.Assets), f.MaxAssets)
	}
	seen := make(map[string]bool, len(f.Assets))
	for index := 0; index < len(f.Assets) && index < f.MaxAssets; index++ {
		name := f.Assets[index]
		if !cleanRelative(name) || seen[name] {
			return fmt.Errorf("managed asset family %q asset %q is duplicated or not a clean relative path", f.Name, name)
		}
		seen[name] = true
	}
	return nil
}

func (f Family) validateWorkflow() error {
	declared := 0
	for _, value := range []string{f.WorkflowFile, f.StatusContext, f.Workflow, f.WorkflowNoun} {
		if value != "" {
			declared++
		}
	}
	if declared != 0 && declared != 4 {
		return fmt.Errorf("managed asset family %q declares its hosted gate partially", f.Name)
	}
	if declared == 4 && (!cleanRelative(f.WorkflowFile) || strings.HasPrefix(f.WorkflowFile, f.Directory+"/")) {
		return fmt.Errorf("managed asset family %q workflow %q must be a clean relative path outside its asset directory", f.Name, f.WorkflowFile)
	}
	return f.validateWorkflowPins()
}

// validateWorkflowPins refuses a hosted workflow with an action unpinnedActions reports.
func (f Family) validateWorkflowPins() error {
	unpinned, err := unpinnedActions(f.Workflow)
	if err != nil {
		return fmt.Errorf("managed asset family %q workflow: %w", f.Name, err)
	}
	if len(unpinned) > 0 {
		return fmt.Errorf("managed asset family %q workflow must pin every action by full commit SHA with its release as a trailing comment: %q", f.Name, unpinned)
	}
	return nil
}

// unpinnedActions returns every uses: line of workflow whose reference is neither a local
// action (./...) nor pinned by full commit SHA with its release as a trailing comment
// (util.ParsePinnedAction). An adopter cannot edit a locked workflow, so one tag-
// or branch-pinned action makes the whole gate fail under a SHA-pinning policy.
func unpinnedActions(workflow string) ([]string, error) {
	lines, uses, err := util.ScanActionUses(workflow, MaxWorkflowLines)
	if err != nil {
		return nil, err
	}
	var unpinned []string
	for _, use := range uses {
		if !use.Pinned && !strings.HasPrefix(use.Ref, "./") {
			unpinned = append(unpinned, strings.TrimSpace(lines[use.Line]))
		}
	}
	return unpinned, nil
}

// validateActionlintLabels requires at most MaxActionlintLabels labels, each declared once, each
// a runs-on value of Workflow, and each one YAML reads back as the same plain string, so the
// label adoption writes into an actionlint configuration is the label the workflow names.
func (f Family) validateActionlintLabels() error {
	if len(f.ActionlintLabels) > MaxActionlintLabels {
		return fmt.Errorf("managed asset family %q declares %d actionlint labels, want at most %d", f.Name, len(f.ActionlintLabels), MaxActionlintLabels)
	}
	runners := workflowRunners(f.Workflow)
	for index := 0; index < len(f.ActionlintLabels) && index < MaxActionlintLabels; index++ {
		label := f.ActionlintLabels[index]
		if slices.Index(f.ActionlintLabels, label) != index || !slices.Contains(runners, label) || !plainYAMLString(label) {
			return fmt.Errorf("managed asset family %q actionlint label %q is repeated, not a runs-on value of its workflow, or not a plain YAML string", f.Name, label)
		}
	}
	return nil
}

// workflowRunners returns the scalar runs-on values of workflow, in order, reading at most
// MaxWorkflowLines lines; a trailing comment is not part of the value.
func workflowRunners(workflow string) []string {
	lines := strings.Split(workflow, "\n")
	var runners []string
	for index := 0; index < len(lines) && index < MaxWorkflowLines; index++ {
		value, found := strings.CutPrefix(strings.TrimSpace(lines[index]), "runs-on:")
		if !found {
			continue
		}
		value, _, _ = strings.Cut(value, " #")
		if value = strings.TrimSpace(value); value != "" {
			runners = append(runners, value)
		}
	}
	return runners
}

// plainYAMLString reports whether label is a plain YAML scalar that decodes to label itself:
// not a number, boolean or null, and free of quoting, flow and comment characters.
func plainYAMLString(label string) bool {
	if label == "" || strings.ContainsAny(label, " \t\r\n\"'#:,[]{}&*!|>%@`") {
		return false
	}
	var decoded any
	if err := yaml.Unmarshal([]byte(label), &decoded); err != nil {
		return false
	}
	text, isString := decoded.(string)
	return isString && text == label
}

// validatePrior requires every Prior key to be a lowercase hex SHA-256 naming one of the
// family's managed paths, and none to be the digest of that path's current canonical text.
func (f Family) validatePrior() error {
	if len(f.Prior) > MaxPriorTexts {
		return fmt.Errorf("managed asset family %q declares %d prior texts, want at most %d", f.Name, len(f.Prior), MaxPriorTexts)
	}
	digests := slices.Sorted(maps.Keys(f.Prior))
	managed := f.ManagedPaths()
	for index := 0; index < len(digests) && index < MaxPriorTexts; index++ {
		digest, rel := digests[index], f.Prior[digests[index]]
		if !isTextDigest(digest) || !slices.Contains(managed, rel) {
			return fmt.Errorf("managed asset family %q prior text %q -> %q is not a lowercase SHA-256 of one of its managed paths", f.Name, digest, rel)
		}
		current, _, err := f.Canonical(rel)
		if err != nil {
			return err
		}
		if f.PriorText(rel, current) {
			return fmt.Errorf("managed asset family %q lists the current text of %s as a prior text", f.Name, rel)
		}
	}
	return nil
}

// isTextDigest reports whether digest is spelled as util.CanonicalTextDigest spells one.
func isTextDigest(digest string) bool {
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == digest
}

// cleanRelative reports whether name is a non-empty, clean, relative slash path that stays
// inside its root.
func cleanRelative(name string) bool {
	return name != "" && name != "." && path.Clean(name) == name && !path.IsAbs(name) && name != ".." &&
		!strings.HasPrefix(name, "../") && !strings.ContainsAny(name, "\\\x00\r\n")
}

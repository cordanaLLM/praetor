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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
	apiassets "github.com/cordanaLLM/praetor/tools/apicompat"
	figureassets "github.com/cordanaLLM/praetor/tools/figures"
	markdownassets "github.com/cordanaLLM/praetor/tools/markdownlint"
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
	// DocumentationFacet is the manifest facet that enables documentation governance.
	DocumentationFacet = "docs:seo-portal"
	// APIContractFacet is the manifest facet that enables the Go API compatibility gate.
	APIContractFacet = "api:public-contract"
	// WorkflowBranch is the default branch every family's Workflow text is written for: the
	// branch a repository that declares none and records none resolves
	// (forge.FallbackDefaultBranch). ForBranch renders the text for another one.
	WorkflowBranch = "main"
)

// pushBranchesPrefix opens the one line of a hosted workflow that names the default branch its
// push trigger runs on, at the indentation of a key under 'on': push:. The branch is
// single-quoted, so a branch name YAML would read as a number or a boolean stays a string;
// config.ValidBranchName admits no quote, glob character or space that would need escaping.
const pushBranchesPrefix = "\n    branches: ['"

// pushBranchesLine is the push trigger's branch line for branch, with the line breaks around it.
func pushBranchesLine(branch string) string {
	return pushBranchesPrefix + branch + "']\n"
}

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
	// Applies reports whether the family applies to the repository at repoPath once Facet is
	// declared; nil applies it to every such repository. Adoption emits, and audit locks, only a
	// family that applies (Enabled), and treats a declared family that does not as disabled.
	Applies func(ctx context.Context, repoPath string) (bool, error)
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
	// Prior maps the SHA-256, in lowercase hex, of every text an earlier Praetor shipped at
	// one of the family's managed paths to that path; the digest covers the text with LF line
	// endings (util.CanonicalTextDigest). A file holding exactly such a text is Praetor's own unedited output, so
	// adoption refreshes it without --force and a disabled facet removes it. Audit still
	// fails on it, naming plain adoption as the repair. An edited file matches no digest and
	// keeps the --force contract. The current workflow rendered for another default branch
	// counts as such a text too (PriorRendering), so a renamed default branch refreshes it.
	Prior map[string]string
	// branch is the default branch Workflow is rendered for; empty means WorkflowBranch.
	branch string
}

// Families returns the registry in its fixed order: the order adoption emits and audit
// checks the families in.
func Families() []Family {
	return []Family{markdown(), figureEngine(), apiCompatibility()}
}

// apiCompatibility is the Go API compatibility gate of the api:public-contract facet (#357):
// the gate program under tools/apicompat and the hosted workflow that runs it under the one
// Go API Compatibility context. tools/apicompat is a name a repository may already use, so
// adoption refuses to overwrite a file it finds there first. The program is a .go file, which
// gofmt and audit both accept in one consistent line-ending style, so it declares no attribute
// rule. The gate compares Go modules alone, so the family applies only where git tracks a go.mod
// the gate discovers (apiassets.TracksModule); a repository declaring the facet without one gets
// no gate, and audit says no API compatibility checker runs for its languages.
func apiCompatibility() Family {
	return Family{
		Name:          "API compatibility",
		Kind:          "API compatibility",
		AssetNoun:     "API compatibility gate program",
		WorkflowNoun:  "API compatibility workflow",
		Facet:         APIContractFacet,
		Applies:       apiassets.TracksModule,
		Directory:     apiassets.Directory,
		Source:        apiassets.SourceFile,
		FS:            apiassets.FS(),
		Assets:        apiassets.Names(),
		MaxAssets:     apiassets.MaxAssets,
		WorkflowFile:  apiassets.WorkflowFile,
		StatusContext: apiassets.StatusContext,
		Workflow:      apiassets.Workflow,
		RefuseForeign: true,
		Prior:         apiassets.PriorDigests(),
	}
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

// Enabled reports whether the family is enabled for the repository at repoPath: declared tells
// whether its Facet is declared, and a declared family is enabled while it applies (Applies).
func (f Family) Enabled(ctx context.Context, repoPath string, declared bool) (bool, error) {
	if !declared || f.Applies == nil {
		return declared, nil
	}
	applies, err := f.Applies(ctx, repoPath)
	if err != nil {
		return false, fmt.Errorf("resolve whether the %s family applies to %s: %w", f.Name, repoPath, err)
	}
	return applies, nil
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

// Branch returns the default branch the family's workflow is rendered for (ForBranch).
func (f Family) Branch() string {
	if f.branch == "" {
		return WorkflowBranch
	}
	return f.branch
}

// BranchDependent reports whether the family's workflow names its default branch on its push
// trigger: exactly one push branch line (pushBranchesLine) for Branch, the line ForBranch
// rewrites. A family without a workflow, or whose workflow runs on every branch, is not.
func (f Family) BranchDependent() bool {
	return f.WorkflowFile != "" && strings.Count(f.Workflow, pushBranchesLine(f.Branch())) == 1
}

// ForBranch returns the family with its workflow rendered for the default branch branch: the
// push trigger's branch line names it. It is the one rendering adoption writes and audit locks
// (FamilyForRepository in internal/adopt). A family that is not BranchDependent is returned
// unchanged, and a branch config.ValidBranchName refuses is an error.
func (f Family) ForBranch(branch string) (Family, error) {
	if !f.BranchDependent() {
		return f, nil
	}
	if !config.ValidBranchName(branch) {
		return Family{}, fmt.Errorf("managed asset family %q workflow cannot be rendered for default branch %q: not a branch name", f.Name, branch)
	}
	f.Workflow = strings.Replace(f.Workflow, pushBranchesLine(f.Branch()), pushBranchesLine(branch), 1)
	f.branch = branch
	return f, nil
}

// otherBranchRendering reports whether actual, in one consistent line-ending style, is the
// family's workflow at rel rendered for a default branch other than Branch, and whether actual
// is its CRLF checkout. Such a file is Praetor's unedited output for a branch the repository no
// longer resolves, such as after a default branch rename.
func (f Family) otherBranchRendering(rel string, actual []byte) (known, crlf bool) {
	if rel != f.WorkflowFile || !f.BranchDependent() {
		return false, false
	}
	text, crlf, err := util.NormalizeLineEndingsStrict(string(actual))
	if err != nil {
		return false, false
	}
	branch, found := pushBranch(text)
	if !found || branch == f.Branch() {
		return false, false
	}
	other, err := f.ForBranch(branch)
	if err != nil {
		return false, false
	}
	return other.Workflow == text, crlf
}

// pushBranch returns the branch the first push branch line of text names (pushBranchesLine).
func pushBranch(text string) (string, bool) {
	_, rest, found := strings.Cut(text, pushBranchesPrefix)
	if !found {
		return "", false
	}
	branch, _, closed := strings.Cut(rest, "']\n")
	return branch, closed
}

// Canonical returns the exact bytes the family owns at the repository-relative path rel, its
// workflow rendered for Branch. owned is false, with no error, when rel is not one of the
// family's managed paths.
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
// canonical text (Prior) or its current workflow rendered for another default branch
// (otherBranchRendering), and whether actual is its CRLF checkout, so a refresh can keep the
// file's style. Prior is read with util.LookupCanonicalText, the lookup adoption applies to
// every other earlier-text set: an LF text and its CRLF checkout match, while an edit, mixed
// line endings or a lone carriage return match nothing.
func (f Family) PriorRendering(rel string, actual []byte) (known, crlf bool) {
	owner, known, crlf := util.LookupCanonicalText(actual, f.Prior)
	if known && owner == rel {
		return true, crlf
	}
	if rendered, renderedCRLF := f.otherBranchRendering(rel, actual); rendered {
		return true, renderedCRLF
	}
	return false, crlf
}

// EmbedDirective returns the exact go:embed line Source must carry: the inventory, in order.
func (f Family) EmbedDirective() string {
	return "//go:embed " + strings.Join(f.Names(), " ")
}

// Validate reports the first structural defect of a family declaration: a missing label, an
// inventory that is empty, over its bound, duplicated or not a clean relative path, a Source
// outside Directory, a partial hosted-gate declaration, or a Prior entry that is not a digest
// of an earlier text at one of its managed paths.
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
	if strings.Count(f.Workflow, pushBranchesPrefix) > 1 {
		return fmt.Errorf("managed asset family %q workflow names its default branch on more than one push branch line", f.Name)
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

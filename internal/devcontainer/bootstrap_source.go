package devcontainer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	praetor "github.com/cordanaLLM/praetor"
	"github.com/cordanaLLM/praetor/internal/clientschema"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/gomanifest"
	"github.com/cordanaLLM/praetor/internal/managedasset"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/templates"
	figureassets "github.com/cordanaLLM/praetor/tools/figures"
	secretsassets "github.com/cordanaLLM/praetor/tools/secrets"
)

const (
	maxBootstrapFiles       = 4096
	maxBootstrapSourceBytes = 16 * 1024 * 1024
	maxBootstrapPathBytes   = 256
	bundleSourceRepair      = "the bundle is generated: regenerate it with praetorctl devcontainer generate --source-root <reviewed Praetor checkout> --force"
)

type bootstrapSourceFile struct {
	Name string
	Data []byte
}

type sourceRules struct {
	families func() ([]bootstrapAssetFamily, error)
	repair   string
}

var (
	captureRules = sourceRules{families: bootstrapAssetFamilies, repair: ""}
	archiveRules = sourceRules{families: archivedAssetFamilies, repair: bundleSourceRepair}
)

// SourceRootForms is what every flag or parameter selecting a Praetor source root accepts, and
// SourceRootGitNote the clause a command that prepares the bootstrap adds (ErrSourceNotGitCheckout).
// The CLI and the MCP server both describe their source roots with them, so no two help texts
// disagree on what a source root may be.
const (
	SourceRootForms   = "Praetor Git checkout or source bundle"
	SourceRootGitNote = "; one holding go.mod must be a Git checkout, as the DevContainer bootstrap lists it with git ls-files"
)

// ErrSourceNotGitCheckout refuses a bootstrap source that holds go.mod outside any Git
// checkout: the capture lists its build inputs with git ls-files (bootstrapSourcePaths), so
// such a source can never be captured. A source export without go.mod is no bootstrap source
// at all and is not refused (bootstrapSourceCapturable).
var ErrSourceNotGitCheckout = errors.New("not a Git checkout; the bootstrap inventories it with git ls-files")

// SourceError is a failure to capture the Praetor build source at Root, and only that: every
// error captureBootstrapSource and CheckSource return is one, and no other error of this package
// is. A caller that selected Root through a flag or parameter names it with NameSource, so the
// refusal names the input at fault while every other preparation error keeps its own subject.
type SourceError struct {
	Root string
	Err  error
}

func (e *SourceError) Error() string { return fmt.Sprintf("bootstrap source %q: %v", e.Root, e.Err) }

func (e *SourceError) Unwrap() error { return e.Err }

// NameSource prefixes err with name, the flag or parameter that selected the bootstrap source,
// when err wraps a SourceError. Any other error, and a nil one, is returned unchanged, so a
// refusal about the target repository or the images never names the source input.
func NameSource(err error, name string) error {
	var source *SourceError
	if name == "" || !errors.As(err, &source) {
		return err
	}
	return fmt.Errorf("%s: %w", name, err)
}

// CheckSource applies the checks PrepareBundle applies to a source root before it lists a
// file of it: the root is a readable directory and, when it holds go.mod, declares the Praetor
// module and sits in a Git checkout. A caller that writes other files before it prepares a
// bundle runs it first, so an unusable source stops a run that has written nothing. An empty
// root or one without go.mod passes: PrepareBundle then records the bootstrap as unavailable.
func CheckSource(ctx context.Context, root string) error {
	if ctx == nil {
		return errors.New("bootstrap source check requires context")
	}
	if _, err := bootstrapSourceCapturable(ctx, root); err != nil {
		return &SourceError{Root: root, Err: err}
	}
	return nil
}

// captureBootstrapSource captures the build source at root (captureBootstrapFiles); every
// failure is a SourceError naming root.
func captureBootstrapSource(ctx context.Context, root string) ([]bootstrapSourceFile, error) {
	files, err := captureBootstrapFiles(ctx, root)
	if err != nil {
		return nil, &SourceError{Root: root, Err: err}
	}
	return files, nil
}

func captureBootstrapFiles(ctx context.Context, root string) ([]bootstrapSourceFile, error) {
	ready, err := bootstrapSourceCapturable(ctx, root)
	if err != nil || !ready {
		return nil, err
	}
	paths, err := bootstrapSourcePaths(ctx, root)
	if err != nil {
		return nil, err
	}
	files, err := captureBootstrapClosure(ctx, root, paths)
	if err != nil {
		return nil, err
	}
	if err := validateBootstrapSourceSet(files); err != nil {
		return nil, err
	}
	return files, nil
}

// bootstrapSourcePathspec, together with the declared family assets that
// bootstrapSourcePaths appends, lists exactly what validateBootstrapSourceName accepts. It
// previously also globbed *.s, *.S, *.c, *.h and *.syso, which that validator rejects as
// "non-Go build inputs", so the first native file committed anywhere in the tree --
// including a scanner fixture under testdata -- failed the bootstrap. Asking git for files
// the validator refuses is a contradiction that can only ever produce an error.
//
// The exclusions drop Go's test surface (util.IsGoTestSurface) from every pathspec, the
// appended asset paths included: the recorded Dockerfile
// only runs go build ./cmd/standardsctl, which never reads a _test.go file or a testdata
// directory. Carrying them roughly doubled the compressed archive and pushed it to 96% of
// the then four-frame cap, so one added test failed every bootstrap (BUG-985). The glob
// magic makes the leading ** match testdata at the root as well as at any depth.
//
// The listing is the candidate inventory, not the capture: captureBootstrapClosure keeps
// only the packages go build ./cmd/standardsctl reads (#501).
var bootstrapSourcePathspec = []string{"*.go", "go.mod", "go.sum", "LICENSE", ":(exclude)*_test.go", ":(exclude,glob)**/testdata/**"}

func runSourceGitAssets(ctx context.Context, root string, families []bootstrapAssetFamily) (string, error) {
	var assetArgs []string
	for _, family := range families {
		assetArgs = append(assetArgs, family.assets...)
	}
	if len(assetArgs) == 0 {
		return "", nil
	}
	args := append([]string{"ls-files", "--cached", "--others", "--exclude-standard", "-z", "--"}, assetArgs...)
	return runSourceGit(ctx, root, args...)
}

func parseBootstrapSourcePaths(data string) ([]string, error) {
	unique := make(map[string]bool)
	for _, path := range strings.Split(data, "\x00") {
		if path == "" {
			continue
		}
		if err := validateBootstrapSourceName(path, captureRules); err != nil {
			return nil, err
		}
		unique[path] = true
		if len(unique) > maxBootstrapFiles {
			return nil, errors.New("bootstrap source exceeds 4096 files")
		}
	}
	paths := make([]string, 0, len(unique))
	for path := range unique {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func bootstrapSourcePaths(ctx context.Context, root string) ([]string, error) {
	families, err := bootstrapAssetFamilies()
	if err != nil {
		return nil, err
	}
	args := append([]string{"ls-files", "--cached", "--others", "--exclude-standard", "-z", "--"}, bootstrapSourcePathspec...)
	data, err := runSourceGit(ctx, root, args...)
	if err != nil {
		return nil, err
	}
	assetData, err := runSourceGitAssets(ctx, root, families)
	if err != nil {
		return nil, err
	}
	if assetData != "" {
		if data != "" {
			data += "\x00" + assetData
		} else {
			data = assetData
		}
	}
	return parseBootstrapSourcePaths(data)
}

// runSourceGit runs one git command in the source root. A failure names the command and carries
// git's own standard error (util.CommandDiagnostic), and claims no cause: git missing from PATH,
// the deadline, output over the bound and a refused ownership are all reported as git reported
// them. Whether root is a Git checkout at all is settled before, in bootstrapSourceCapturable.
func runSourceGit(ctx context.Context, root string, args ...string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("bootstrap source git command requires arguments")
	}
	// Disable repository-configured filesystem monitor hooks for read-only inventory.
	command := append([]string{"-c", "core.fsmonitor=false"}, args...)
	result, err := util.RunGitBytes(ctx, root, contextopt.MaxSourceBytes, command...)
	if err != nil {
		return "", fmt.Errorf("inventory with git %s: %w", args[0], util.CommandDiagnostic(err, result.Stderr))
	}
	return string(result.Stdout), nil
}

func containsBootstrapSource(files []bootstrapSourceFile, name string) bool {
	for _, file := range files {
		if file.Name == name {
			return true
		}
	}
	return false
}

func validateBootstrapSourceName(name string, rules sourceRules) error {
	if name == "" || len(name) > maxBootstrapPathBytes || filepath.ToSlash(filepath.Clean(name)) != name || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\\x00\r\n") {
		return errors.New("invalid bootstrap source path")
	}
	return validateBootstrapSourceKind(name, rules)
}

// validateBootstrapSourceKind admits the module files, the assets of every declared go:embed
// family (rules.families), and non-test Go source: the inputs go build
// ./cmd/standardsctl reads. bootstrapSourcePathspec plus the family assets asks git for
// exactly these. Declared assets are admitted first, then Go's test surface is refused,
// so undeclared test files cannot enter the captured set.
func validateBootstrapSourceKind(name string, rules sourceRules) error {
	families, err := rules.families()
	if err != nil {
		return err
	}
	if isBootstrapAsset(name, families) {
		return nil
	}
	switch {
	case util.IsGoTestSurface(name):
		return fmt.Errorf("bootstrap source %s is test-only; go build never reads _test.go files or testdata directories", name)
	case name == "go.mod", name == "go.sum", name == "LICENSE", strings.HasSuffix(name, ".go"):
		return nil
	}
	return errors.New("unsupported bootstrap source file; non-Go build inputs require explicit capture support")
}

func validateBootstrapSourceFile(name string, data []byte, rules sourceRules) error {
	_, err := parseBootstrapSourceFile(name, data, rules)
	return err
}

// parseBootstrapSourceFile applies the name rule and, for Go source, the embed-directive
// rule, and returns the parsed file so the build closure reads its imports without a
// second parse. A non-Go member returns a nil file.
func parseBootstrapSourceFile(name string, data []byte, rules sourceRules) (*ast.File, error) {
	if err := validateBootstrapSourceName(name, rules); err != nil {
		return nil, err
	}
	if !strings.HasSuffix(name, ".go") {
		return nil, nil
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), name, data, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse bootstrap source %s: %w", name, err)
	}
	for _, group := range parsed.Comments {
		for _, comment := range group.List {
			if err := validateEmbedDirective(name, comment.Text, rules); err != nil {
				return nil, err
			}
		}
	}
	return parsed, nil
}

// validateEmbedDirective refuses a go:embed directive unless it is exactly the directive of
// a declared asset family, in that family's source file: any other one would build a binary
// missing the files it embeds, since the archive carries only the declared assets.
func validateEmbedDirective(name, text string, rules sourceRules) error {
	if !strings.HasPrefix(text, "//go:embed ") && !strings.HasPrefix(text, "//go:embed\t") {
		return nil
	}
	families, err := rules.families()
	if err != nil {
		return err
	}
	for _, family := range families {
		if name == family.source && text == family.directive {
			return nil
		}
	}
	if rules.repair != "" {
		return fmt.Errorf("bootstrap source %s embeds assets; explicit asset capture is required; %s", name, rules.repair)
	}
	return fmt.Errorf("bootstrap source %s embeds assets; explicit asset capture is required", name)
}

func bootstrapSourceDigest(files []bootstrapSourceFile) string {
	h := sha256.New()
	for _, file := range files {
		h.Write([]byte(file.Name))
		h.Write([]byte{0})
		sum := sha256.Sum256(file.Data)
		h.Write(sum[:])
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func bootstrapDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validBootstrapDigest(digest string) bool {
	if len(digest) != 71 || !strings.HasPrefix(digest, "sha256:") {
		return false
	}
	for _, c := range digest[7:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// bootstrapSourceAvailable reports whether root holds the Praetor module source: false without
// error for an empty root or one without go.mod, true for a go.mod declaring the Praetor module,
// and an error for an unreadable root or any other module.
func bootstrapSourceAvailable(ctx context.Context, root string) (bool, error) {
	if root == "" {
		return false, nil
	}
	directory, err := contextopt.OpenDirectory(ctx, root)
	if err != nil {
		return false, err
	}
	if err := directory.Close(); err != nil {
		return false, err
	}
	module, err := contextopt.ReadSnapshot(ctx, filepath.Join(root, "go.mod"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !declaresPraetorModule(module) {
		return false, errors.New("bootstrap source must declare the Praetor module")
	}
	return true, nil
}

// bootstrapSourceCapturable is bootstrapSourceAvailable for a source the bootstrap captures: a
// Praetor module must also sit in a Git checkout, since the capture lists its build inputs with
// git ls-files (ErrSourceNotGitCheckout). The legacy self-host check (verifyLegacyBootstrapInputs)
// asks only bootstrapSourceAvailable: its container builds with go run and lists nothing.
func bootstrapSourceCapturable(ctx context.Context, root string) (bool, error) {
	ready, err := bootstrapSourceAvailable(ctx, root)
	if err != nil || !ready {
		return false, err
	}
	present, err := util.GitWorktreePresent(ctx, root)
	if err != nil {
		return false, fmt.Errorf("detect a Git checkout: %w", err)
	}
	if !present {
		return false, ErrSourceNotGitCheckout
	}
	return true, nil
}

// validateBootstrapSourceSet is the one check every captured or decoded set passes. It
// re-applies the name rule to each member, so a test-only or otherwise refused path cannot
// enter a set through any caller, whatever produced the list.
func validateBootstrapSourceSet(files []bootstrapSourceFile) error {
	if len(files) > maxBootstrapFiles {
		return errors.New("bootstrap source exceeds 4096 files")
	}
	for _, required := range []string{bootstrapBuildPackage + "/main.go", "go.mod", "go.sum", "LICENSE"} {
		if !containsBootstrapSource(files, required) {
			return errors.New("bootstrap requires CLI sources, go.mod, go.sum and LICENSE")
		}
	}
	for _, file := range files {
		if err := validateBootstrapSourceName(file.Name, captureRules); err != nil {
			return err
		}
		if file.Name == "go.mod" && !declaresPraetorModule(file.Data) {
			return errors.New("bootstrap archive must declare the Praetor module")
		}
	}
	return requireFamilyAssets(files)
}

// requireFamilyAssets holds a set carrying a family's embedding source to every asset that
// source embeds, so an archive never builds a binary whose go:embed has nothing to embed.
func requireFamilyAssets(files []bootstrapSourceFile) error {
	families, err := bootstrapAssetFamilies()
	if err != nil {
		return err
	}
	for _, family := range families {
		if !containsBootstrapSource(files, family.source) {
			continue
		}
		for _, asset := range family.assets {
			if !containsBootstrapSource(files, asset) {
				return fmt.Errorf("bootstrap %s asset %s is missing", family.name, asset)
			}
		}
	}
	return nil
}

// bootstrapAssetFamily is one go:embed directive whose assets the bootstrap captures beside
// the Go source: the file carrying the directive, its exact text, and the repository-relative
// paths it embeds. A directive not declared here is refused.
type bootstrapAssetFamily struct {
	name      string
	source    string
	directive string
	assets    []string
	// optional are assets a recorded archive may lack: templatesSinceRelease for the template
	// family of an archived bundle.
	optional []string
	preMove  bool
	moved    map[string]map[string]string
}

// preMoveBootstrapFamily derives the pre-move declaration for a family with Moved entries:
// the same source file, the directive with each moved asset at its old path in the old order,
// and the old repository-relative asset paths.
func preMoveBootstrapFamily(family managedasset.Family) (bootstrapAssetFamily, bool) {
	if len(family.Moved) == 0 {
		return bootstrapAssetFamily{}, false
	}
	if family.Directory == figureassets.Directory {
		return figureBootstrapVariant(family, " (pre-move)", "dist/", false), true
	}
	movedBase := make(map[string]string, len(family.Moved))
	for oldRepoPath := range family.Moved {
		oldRel := strings.TrimPrefix(oldRepoPath, family.Directory+"/")
		movedBase[path.Base(oldRepoPath)] = oldRel
	}
	names := family.Names()
	oldNames := make([]string, 0, len(names))
	oldAssetPaths := make([]string, 0, len(names))
	for index := 0; index < len(names) && index < family.MaxAssets; index++ {
		name := names[index]
		if oldRel, ok := movedBase[path.Base(name)]; ok {
			oldNames = append(oldNames, oldRel)
			oldAssetPaths = append(oldAssetPaths, family.Directory+"/"+oldRel)
		} else {
			oldNames = append(oldNames, name)
			oldAssetPaths = append(oldAssetPaths, family.AssetPath(name))
		}
	}
	return bootstrapAssetFamily{
		name:      family.Name + " (pre-move)",
		source:    family.Source,
		directive: "//go:embed " + strings.Join(oldNames, " "),
		assets:    oldAssetPaths,
		preMove:   true,
		moved:     family.Moved,
	}, true
}

func figureBootstrapVariant(family managedasset.Family, suffix, playerPrefix string, includeESLint bool) bootstrapAssetFamily {
	names := family.Names()
	playerNames := figureassets.PlayerNames()
	oldNames := make([]string, 0, len(names)+len(playerNames))
	oldAssetPaths := make([]string, 0, len(names)+len(playerNames))
	for index := 0; index < len(names) && index < family.MaxAssets; index++ {
		name := names[index]
		if name == "figures.css" {
			for pIdx := 0; pIdx < len(playerNames) && pIdx < figureassets.MaxPlayerAssets; pIdx++ {
				oldRel := playerPrefix + path.Base(playerNames[pIdx])
				oldNames = append(oldNames, oldRel)
				oldAssetPaths = append(oldAssetPaths, family.Directory+"/"+oldRel)
			}
		}
		if name == "eslint.figures.mjs" && !includeESLint {
			continue
		}
		oldNames = append(oldNames, name)
		oldAssetPaths = append(oldAssetPaths, family.AssetPath(name))
	}
	return bootstrapAssetFamily{
		name:      family.Name + suffix,
		source:    family.Source,
		directive: "//go:embed " + strings.Join(oldNames, " "),
		assets:    oldAssetPaths,
		preMove:   true,
		moved:     family.Moved,
	}
}

// templatesSinceRelease are the template bodies added after the latest release, which a bundle
// recorded at that release cannot hold. An archived bundle verifies without them; a current one
// must still carry every template. TestPriorReleaseBundleVerifiesAgainstCurrentEngine fails
// until a new template is listed here, and the list empties once a release records them.
var templatesSinceRelease = []string{
	"templates/rust/ci-rust-library.yml.tmpl", // rust-library flavor (#67)
}

// archivedAssetFamilies yields the asset families recognized when validating a recorded
// archive: the current families plus pre-move declarations for families with Moved entries.
func archivedAssetFamilies() ([]bootstrapAssetFamily, error) {
	current, err := bootstrapAssetFamilies()
	if err != nil {
		return nil, err
	}
	managed := managedasset.Families()
	families := slices.Clone(current)
	for index := range families {
		if families[index].name == "template" {
			families[index].optional = templatesSinceRelease
		}
	}
	for index := 0; index < len(managed) && index < managedasset.MaxFamilies; index++ {
		if preMove, ok := preMoveBootstrapFamily(managed[index]); ok {
			families = append(families, preMove)
		}
		if managed[index].Directory == figureassets.Directory {
			families = append(
				families,
				figureBootstrapVariant(managed[index], " (pre-ADR-0021)", "player/", true),
				figureBootstrapVariant(managed[index], " (pre-eslint)", "player/", false),
			)
		}
	}
	return families, nil
}

// bootstrapAssetFamilies declares the embedded asset sets go build ./cmd/standardsctl needs:
// every managed asset family adoption emits (internal/managedasset), in registry order, the
// template bodies flavor apply scaffolds, the gate's devcontainer CLI pins (cli.go) and this
// package's prior-default image list.
func bootstrapAssetFamilies() ([]bootstrapAssetFamily, error) {
	templateAssets, err := templateBootstrapAssetPaths()
	if err != nil {
		return nil, err
	}
	managed := managedasset.Families()
	families := make([]bootstrapAssetFamily, 0, len(managed)+5)
	for index := 0; index < len(managed) && index < managedasset.MaxFamilies; index++ {
		family := managed[index]
		families = append(families, bootstrapAssetFamily{
			name: family.Name, source: family.Source, directive: family.EmbedDirective(), assets: family.AssetPaths(),
		})
	}
	return append(families, bootstrapAssetFamily{
		name: "figure player", source: figureassets.PlayerSourceFile, directive: figureassets.PlayerEmbedDirective, assets: figureassets.PlayerAssetPaths(),
	}, bootstrapAssetFamily{
		name: "template", source: templates.SourceFile, directive: "//go:embed " + templates.Pattern, assets: templateAssets,
	}, cliBootstrapFamily(), bootstrapAssetFamily{
		name: "client schemas", source: clientschema.SourceFile, directive: clientschema.EmbedDirective, assets: clientschema.AssetPaths(),
	}, bootstrapAssetFamily{
		name: "reviewed images", source: reviewedImagesSource, directive: priorImagesDirective, assets: []string{PriorImagesFile},
	}, bootstrapAssetFamily{
		name: "secrets gate", source: secretsassets.SourceFile, directive: secretsassets.EmbedDirective, assets: secretsassets.AssetPaths(),
	}, bootstrapAssetFamily{
		name: "hiss coverage corpus", source: praetor.CorpusSourceFile, directive: praetor.CorpusEmbedDirective, assets: praetor.AssetPaths(),
	}), nil
}

// templateBootstrapAssetPaths names every embedded template body as a repository path.
func templateBootstrapAssetPaths() ([]string, error) {
	names, err := templates.Names()
	if err != nil {
		return nil, fmt.Errorf("bootstrap template assets: %w", err)
	}
	paths := make([]string, 0, len(names))
	for index := 0; index < len(names) && index < maxBootstrapFiles; index++ {
		paths = append(paths, templates.Directory+"/"+names[index])
	}
	return paths, nil
}

// isBootstrapAsset reports whether name is an asset of one of the given families.
func isBootstrapAsset(name string, families []bootstrapAssetFamily) bool {
	for _, family := range families {
		for _, candidate := range family.assets {
			if name == candidate {
				return true
			}
		}
	}
	return false
}

func declaresPraetorModule(data []byte) bool {
	count := 0
	valid := false
	lines := strings.Split(string(data), "\n")
	if len(lines) > 4096 {
		return false
	}
	for _, line := range lines {
		directive, _, _ := strings.Cut(line, "//")
		fields := strings.Fields(directive)
		if len(fields) > 0 && fields[0] == "module" {
			count++
			valid = len(fields) == 2 && fields[1] == gomanifest.EngineModulePath
		}
	}
	return count == 1 && valid
}

func parseSourceEmbedDirective(file bootstrapSourceFile) (string, error) {
	if !strings.HasSuffix(file.Name, ".go") {
		return "", nil
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), file.Name, file.Data, parser.ParseComments)
	if err != nil {
		return "", fmt.Errorf("parse bootstrap source %s: %w", file.Name, err)
	}
	var directive string
	for _, group := range parsed.Comments {
		for _, comment := range group.List {
			text := comment.Text
			if strings.HasPrefix(text, "//go:embed ") || strings.HasPrefix(text, "//go:embed\t") {
				if directive != "" {
					return "", fmt.Errorf("bootstrap source %s carries multiple go:embed directives", file.Name)
				}
				directive = text
			}
		}
	}
	return directive, nil
}

func selectFamilyDeclaration(source, directive string, archived []bootstrapAssetFamily) (*bootstrapAssetFamily, []*bootstrapAssetFamily, error) {
	var selected *bootstrapAssetFamily
	var unselected []*bootstrapAssetFamily
	for i := range archived {
		fam := &archived[i]
		if fam.source != source {
			continue
		}
		if fam.directive == directive {
			selected = fam
		} else {
			unselected = append(unselected, fam)
		}
	}
	if selected == nil {
		return nil, nil, fmt.Errorf("bootstrap source %s embeds assets; explicit asset capture is required; %s", source, bundleSourceRepair)
	}
	return selected, unselected, nil
}

func admitSelectedFamilyAssets(selected *bootstrapAssetFamily, files []bootstrapSourceFile, admitted map[string]bool) error {
	for _, asset := range selected.assets {
		if !containsBootstrapSource(files, asset) {
			if slices.Contains(selected.optional, asset) {
				continue
			}
			return fmt.Errorf("bootstrap %s asset %s is missing", selected.name, asset)
		}
		admitted[asset] = true
	}
	return nil
}

func rejectUnselectedFamilyAssets(selected *bootstrapAssetFamily, unselected []*bootstrapAssetFamily, files []bootstrapSourceFile, admitted map[string]bool) error {
	for _, other := range unselected {
		for _, asset := range other.assets {
			if !admitted[asset] && containsBootstrapSource(files, asset) {
				return fmt.Errorf("bootstrap source %s does not belong to the selected %s declaration", asset, selected.name)
			}
		}
	}
	return nil
}

func verifyPreMoveAssetDigests(family bootstrapAssetFamily, files []bootstrapSourceFile) error {
	if !family.preMove || len(family.moved) == 0 {
		return nil
	}
	for _, file := range files {
		digests, ok := family.moved[file.Name]
		if !ok {
			continue
		}
		sum := sha256.Sum256(file.Data)
		hexSum := hex.EncodeToString(sum[:])
		if digests[hexSum] == "" {
			return fmt.Errorf("pre-move asset %s bytes match no release digest", file.Name)
		}
	}
	return nil
}

func verifyAdmittedNonGoFiles(files []bootstrapSourceFile, admitted map[string]bool) error {
	for _, file := range files {
		if file.Name == "go.mod" || file.Name == "go.sum" || file.Name == "LICENSE" || strings.HasSuffix(file.Name, ".go") {
			continue
		}
		if !admitted[file.Name] {
			return fmt.Errorf("bootstrap source %s does not belong to any declared asset family", file.Name)
		}
	}
	return nil
}

func uniqueFamilySources(families []bootstrapAssetFamily) []string {
	var sources []string
	seen := make(map[string]bool)
	for _, fam := range families {
		if !seen[fam.source] {
			seen[fam.source] = true
			sources = append(sources, fam.source)
		}
	}
	return sources
}

func findBootstrapSource(files []bootstrapSourceFile, name string) (bootstrapSourceFile, bool) {
	for _, file := range files {
		if file.Name == name {
			return file, true
		}
	}
	return bootstrapSourceFile{}, false
}

func checkArchivedFileLimits(files []bootstrapSourceFile) error {
	if len(files) > maxBootstrapFiles {
		return errors.New("bootstrap source exceeds 4096 files")
	}
	for _, required := range []string{bootstrapBuildPackage + "/main.go", "go.mod", "go.sum", "LICENSE"} {
		if !containsBootstrapSource(files, required) {
			return errors.New("bootstrap requires CLI sources, go.mod, go.sum and LICENSE")
		}
	}
	for _, file := range files {
		if err := validateBootstrapSourceName(file.Name, archiveRules); err != nil {
			return err
		}
		if file.Name == "go.mod" && !declaresPraetorModule(file.Data) {
			return errors.New("bootstrap archive must declare the Praetor module")
		}
	}
	return nil
}

type selectedSourceFamily struct {
	selected   *bootstrapAssetFamily
	unselected []*bootstrapAssetFamily
}

func resolveArchivedSelections(files []bootstrapSourceFile, archived []bootstrapAssetFamily, admitted map[string]bool) ([]selectedSourceFamily, error) {
	sources := uniqueFamilySources(archived)
	selections := make([]selectedSourceFamily, 0, len(sources))
	for _, source := range sources {
		file, ok := findBootstrapSource(files, source)
		if !ok {
			continue
		}
		directive, err := parseSourceEmbedDirective(file)
		if err != nil {
			return nil, err
		}
		selected, other, err := selectFamilyDeclaration(source, directive, archived)
		if err != nil {
			return nil, err
		}
		if err := admitSelectedFamilyAssets(selected, files, admitted); err != nil {
			return nil, err
		}
		selections = append(selections, selectedSourceFamily{selected: selected, unselected: other})
	}
	return selections, nil
}

func validateArchivedBootstrapSourceSet(files []bootstrapSourceFile) error {
	if err := checkArchivedFileLimits(files); err != nil {
		return err
	}
	archived, err := archivedAssetFamilies()
	if err != nil {
		return err
	}
	admitted := make(map[string]bool)
	selections, err := resolveArchivedSelections(files, archived, admitted)
	if err != nil {
		return err
	}
	for _, sel := range selections {
		if err := rejectUnselectedFamilyAssets(sel.selected, sel.unselected, files, admitted); err != nil {
			return err
		}
		if err := verifyPreMoveAssetDigests(*sel.selected, files); err != nil {
			return err
		}
	}
	return verifyAdmittedNonGoFiles(files, admitted)
}

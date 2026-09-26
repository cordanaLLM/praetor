// Package cavemansource extracts runtime-equivalent agent text from bounded,
// explicitly governed non-Markdown source scopes.
package cavemansource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

const (
	maxSelectionBytes = 64 * 1024
	maxSelectionLines = 1024
)

// Source is one decoded runtime text value plus exact extraction provenance.
type Source struct {
	Path     string
	Text     string
	Surface  config.RegisterSurface
	Kind     caveman.MessageKind
	Format   config.RegisterSourceFormat
	Selector string
	Line     int
	SHA256   string
	// NotApplicable records a mechanically classified protocol or pass-through callsite.
	// The coverage contract binds it, while Caveman prose lint skips its non-agent-owned text.
	NotApplicable string
}

// Provenance renders the parser, selector and digest which produced a Source.
func (s Source) Provenance() string {
	selector := s.Selector
	if selector == "" {
		selector = fmt.Sprintf("line:%d", s.Line)
	}
	applicability := "applicable"
	if s.NotApplicable != "" {
		applicability = "not_applicable:" + s.NotApplicable
	}
	return fmt.Sprintf("source_selector=%s source_sha256=%s extraction=%s surface=%s applicability=%s",
		selector, s.SHA256, s.Format, s.Surface, applicability)
}

// Result carries the complete sorted inventory and its aggregate coverage digest.
type Result struct {
	Sources       []Source
	Applicable    int
	NotApplicable int
	SHA256        string
}

// ExtractDeclared extracts and verifies the canonical register.sources contract.
func ExtractDeclared(ctx context.Context, root string, declared *config.RegisterSources) (Result, error) {
	if declared == nil {
		return Result{}, errors.New("register.sources is not configured")
	}
	result, files, err := extractInputs(ctx, root, declared.Inputs, nil)
	if err != nil {
		return Result{}, err
	}
	if err := requireTracked(ctx, root, files); err != nil {
		return Result{}, err
	}
	return verifyDeclaredResult(result, declared)
}

// ExtractDeclaredContent verifies declared coverage against current source bytes without
// requiring those bytes in Git; documents overlay repository files as in
// ExtractInputsWithDocuments. Adoption uses this during the pre-commit rerun window and
// before it has written a planned file; repository gates use ExtractDeclared and retain
// tracked-source enforcement.
func ExtractDeclaredContent(ctx context.Context, root string, declared *config.RegisterSources,
	documents map[string][]byte,
) (Result, error) {
	if declared == nil {
		return Result{}, errors.New("register.sources is not configured")
	}
	result, _, err := extractInputs(ctx, root, declared.Inputs, documents)
	if err != nil {
		return Result{}, err
	}
	return verifyDeclaredResult(result, declared)
}

func verifyDeclaredResult(result Result, declared *config.RegisterSources) (Result, error) {
	if result.Applicable != declared.Expected {
		return Result{}, fmt.Errorf("register.sources expected %d applicable values, extracted %d", declared.Expected, result.Applicable)
	}
	if result.NotApplicable != declared.NotApplicable {
		return Result{}, fmt.Errorf("register.sources expected %d not-applicable values, extracted %d",
			declared.NotApplicable, result.NotApplicable)
	}
	if result.SHA256 != declared.SHA256 {
		return Result{}, fmt.Errorf("register.sources sha256 mismatch: declared %s, actual %s", declared.SHA256, result.SHA256)
	}
	return result, nil
}

// ExtractInputs extracts ad-hoc CLI scopes without requiring Git tracking or a coverage
// declaration. It still fails when no runtime text is found.
func ExtractInputs(ctx context.Context, root string, inputs []config.RegisterSourceInput) (Result, error) {
	return ExtractInputsWithDocuments(ctx, root, inputs, nil)
}

// ExtractInputsWithDocuments extracts inputs like ExtractInputs, but a file input whose path
// is a key of documents reads those bytes in place of the repository file, which need not
// exist. Adoption uses it to re-bind a declared contract to a harness it has not written
// yet. An empty root reads documents only and fails on any input they do not supply.
func ExtractInputsWithDocuments(ctx context.Context, root string, inputs []config.RegisterSourceInput,
	documents map[string][]byte,
) (Result, error) {
	result, _, err := extractInputs(ctx, root, inputs, documents)
	return result, err
}

// CoverageFromDocuments computes the inventory of inputs served entirely from in-memory
// documents. Adoption uses it before the generated harness exists on disk.
func CoverageFromDocuments(ctx context.Context, inputs []config.RegisterSourceInput, documents map[string][]byte) (Result, error) {
	return ExtractInputsWithDocuments(ctx, "", inputs, documents)
}

func extractInputs(ctx context.Context, root string, inputs []config.RegisterSourceInput,
	documents map[string][]byte,
) (Result, []string, error) {
	if ctx == nil {
		return Result{}, nil, errors.New("caveman source extraction requires context")
	}
	items, files, err := discoverInputs(ctx, root, inputs, documents)
	if err != nil {
		return Result{}, nil, err
	}
	reader := sourceReader{ctx: ctx, root: root, documents: documents, cache: make(map[string][]byte, len(files))}
	packageGoverned, err := collectMCPGovernedFunctions(items, &reader)
	if err != nil {
		return Result{}, nil, err
	}
	accumulator := sourceAccumulator{sources: make([]Source, 0, len(items)), inventory: sourceInventory(items)}
	for index := range items {
		data, err := reader.read(items[index].path)
		if err != nil {
			return Result{}, nil, err
		}
		extracted, extractErr := extractItem(ctx, items[index], data, packageGoverned)
		if extractErr != nil {
			return Result{}, nil, extractErr
		}
		if err := accumulator.add(extracted); err != nil {
			return Result{}, nil, err
		}
	}
	result, err := accumulator.result()
	return result, files, err
}

type sourceReader struct {
	ctx       context.Context
	root      string
	documents map[string][]byte
	cache     map[string][]byte
	total     int
}

func (r *sourceReader) read(path string) ([]byte, error) {
	if data, ok := r.cache[path]; ok {
		return data, nil
	}
	data, err := r.load(path)
	if err != nil {
		return nil, err
	}
	r.total += len(data)
	if r.total > contextopt.MaxTotalBytes {
		return nil, fmt.Errorf("caveman source files exceed %d bytes", contextopt.MaxTotalBytes)
	}
	r.cache[path] = data
	return data, nil
}

// load returns the caller-supplied document for path, or the bounded repository snapshot.
func (r *sourceReader) load(path string) ([]byte, error) {
	if data, ok := r.documents[path]; ok {
		if len(data) > contextopt.MaxSourceBytes {
			return nil, fmt.Errorf("caveman source %s exceeds %d bytes", path, contextopt.MaxSourceBytes)
		}
		return data, nil
	}
	abs, err := confinedSourcePath(r.root, path)
	if err != nil {
		return nil, err
	}
	data, err := contextopt.ReadSnapshot(r.ctx, abs)
	if err != nil {
		return nil, fmt.Errorf("read caveman source %s: %w", path, err)
	}
	return data, nil
}

type sourceAccumulator struct {
	sources       []Source
	inventory     []string
	total         int
	applicable    int
	notApplicable int
}

func (a *sourceAccumulator) add(extracted []Source) error {
	for index := range extracted {
		if err := validateSource(&extracted[index]); err != nil {
			return err
		}
		a.total += len(extracted[index].Text)
		if a.total > contextopt.MaxSourceBytes {
			return fmt.Errorf("caveman source selections exceed %d bytes", contextopt.MaxSourceBytes)
		}
		a.sources = append(a.sources, extracted[index])
		if extracted[index].NotApplicable == "" {
			a.applicable++
			if a.applicable > config.MaxRegisterSourceOutputs {
				return fmt.Errorf("caveman sources exceed %d applicable values", config.MaxRegisterSourceOutputs)
			}
		} else {
			a.notApplicable++
			if a.notApplicable > config.MaxRegisterSourceOutputs {
				return fmt.Errorf("caveman sources exceed %d not-applicable values", config.MaxRegisterSourceOutputs)
			}
		}
	}
	return nil
}

func (a *sourceAccumulator) result() (Result, error) {
	if len(a.sources) == 0 {
		return Result{}, errors.New("caveman source extraction checked zero runtime text values")
	}
	sort.Slice(a.sources, func(i, j int) bool { return sourceKey(a.sources[i]) < sourceKey(a.sources[j]) })
	return Result{Sources: a.sources, Applicable: a.applicable, NotApplicable: a.notApplicable,
		SHA256: coverageDigest(a.sources, a.inventory)}, nil
}

func validateSource(source *Source) error {
	source.Text = normalizeNewlines(source.Text)
	if source.Text == "" || len(source.Text) > maxSelectionBytes {
		return fmt.Errorf("caveman source %s value must contain 1..%d bytes", source.Path, maxSelectionBytes)
	}
	if strings.ContainsRune(source.Text, '\x00') || logicalLines(source.Text) > maxSelectionLines {
		return fmt.Errorf("caveman source %s value contains NUL or exceeds %d lines", source.Path, maxSelectionLines)
	}
	source.SHA256 = sourceDigest(source.Text)
	return nil
}

func logicalLines(value string) int {
	if value == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(value, "\n"), "\n") + 1
}

func sourceKey(source Source) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s", source.Path, source.Selector,
		source.Surface, source.Kind)
}

// coverageDigest binds every selected file identity plus every extracted value and its provenance.
func coverageDigest(sources []Source, inventory []string) string {
	records := make([]string, 0, len(inventory)+len(sources))
	for _, item := range inventory {
		records = append(records, "input\x00"+item)
	}
	for index := range sources {
		records = append(records, "source\x00"+strings.Join([]string{sources[index].Path, sources[index].Selector,
			string(sources[index].Surface), string(sources[index].Kind),
			string(sources[index].Format), sources[index].SHA256, sources[index].NotApplicable}, "\x00"))
	}
	sort.Strings(records)
	digest := sha256.Sum256([]byte(strings.Join(records, "\n") + "\n"))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func sourceInventory(items []discoveredInput) []string {
	records := make([]string, len(items))
	for index := range items {
		records[index] = inventoryRecord(items[index])
	}
	return records
}

func inventoryRecord(item discoveredInput) string {
	return strings.Join([]string{filepath.ToSlash(item.path), item.input.Selector,
		string(item.input.Surface), item.input.Kind, string(item.input.Format)}, "\x00")
}

func sourceNotApplicable(item discoveredInput, text, selector string, line int, reason string) Source {
	source := sourceFrom(item, text, selector, line)
	source.NotApplicable = reason
	return source
}

func extractItem(ctx context.Context, item discoveredInput, data []byte, packageGoverned map[string]bool) ([]Source, error) {
	switch item.input.Format {
	case config.SourceFormatShell:
		return extractShell(item, string(data))
	case config.SourceFormatPython:
		return extractPython(item, string(data))
	case config.SourceFormatGo:
		return extractGo(item, data, packageGoverned)
	case config.SourceFormatJSON, config.SourceFormatYAML:
		return extractStructured(ctx, item, data)
	default:
		return nil, fmt.Errorf("caveman source %s: unsupported format %q", item.path, item.input.Format)
	}
}

func normalizeNewlines(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
}

func sourceDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func sourceFrom(item discoveredInput, text, selector string, line int) Source {
	return Source{Path: filepath.ToSlash(item.path), Text: text, Surface: item.input.Surface,
		Kind: caveman.MessageKind(item.input.Kind), Format: item.input.Format,
		Selector: selector, Line: line}
}

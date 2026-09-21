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
	"strconv"
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
}

// Provenance renders the parser, selector and digest which produced a Source.
func (s Source) Provenance() string {
	selector := s.Selector
	if selector == "" {
		selector = fmt.Sprintf("line:%d", s.Line)
	}
	return fmt.Sprintf("source_selector=%s source_sha256=%s extraction=%s surface=%s",
		selector, s.SHA256, s.Format, s.Surface)
}

// Result carries the complete sorted inventory and its aggregate coverage digest.
type Result struct {
	Sources []Source
	SHA256  string
}

// ExtractDeclared extracts and verifies the canonical register.sources contract.
func ExtractDeclared(ctx context.Context, root string, declared *config.RegisterSources) (Result, error) {
	if declared == nil {
		return Result{}, errors.New("register.sources is not configured")
	}
	result, files, err := extractInputs(ctx, root, declared.Inputs)
	if err != nil {
		return Result{}, err
	}
	if err := requireTracked(ctx, root, files); err != nil {
		return Result{}, err
	}
	if len(result.Sources) != declared.Expected {
		return Result{}, fmt.Errorf("register.sources expected %d values, extracted %d", declared.Expected, len(result.Sources))
	}
	if result.SHA256 != declared.SHA256 {
		return Result{}, fmt.Errorf("register.sources sha256 mismatch: declared %s, actual %s", declared.SHA256, result.SHA256)
	}
	return result, nil
}

// ExtractInputs extracts ad-hoc CLI scopes without requiring Git tracking or a coverage
// declaration. It still fails when no runtime text is found.
func ExtractInputs(ctx context.Context, root string, inputs []config.RegisterSourceInput) (Result, error) {
	result, _, err := extractInputs(ctx, root, inputs)
	return result, err
}

// CoverageFromDocuments computes the same structured-source inventory from in-memory
// documents. Adoption uses it before the generated harness exists on disk.
func CoverageFromDocuments(inputs []config.RegisterSourceInput, documents map[string][]byte) (Result, error) {
	accumulator := sourceAccumulator{}
	budget := documentBudget{seen: make(map[string]bool, len(documents))}
	for index := range inputs {
		extracted, err := extractDocumentInput(inputs[index], documents, &budget)
		if err != nil {
			return Result{}, err
		}
		if err := accumulator.add(extracted); err != nil {
			return Result{}, err
		}
	}
	return accumulator.result()
}

func extractInputs(ctx context.Context, root string, inputs []config.RegisterSourceInput) (Result, []string, error) {
	if ctx == nil {
		return Result{}, nil, errors.New("caveman source extraction requires context")
	}
	items, files, err := discoverInputs(ctx, root, inputs)
	if err != nil {
		return Result{}, nil, err
	}
	reader := sourceReader{ctx: ctx, root: root, cache: make(map[string][]byte, len(files))}
	accumulator := sourceAccumulator{sources: make([]Source, 0, len(items))}
	for index := range items {
		data, err := reader.read(items[index].path)
		if err != nil {
			return Result{}, nil, err
		}
		extracted, extractErr := extractItem(items[index], data)
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

type documentBudget struct {
	seen  map[string]bool
	total int
}

func (b *documentBudget) observe(path string, data []byte) error {
	if b.seen[path] {
		return nil
	}
	b.total += len(data)
	if len(data) > contextopt.MaxSourceBytes || b.total > contextopt.MaxTotalBytes {
		return errors.New("in-memory source documents exceed byte bounds")
	}
	b.seen[path] = true
	return nil
}

func extractDocumentInput(input config.RegisterSourceInput, documents map[string][]byte, budget *documentBudget) ([]Source, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	if input.Format != config.SourceFormatJSON && input.Format != config.SourceFormatYAML {
		return nil, errors.New("in-memory coverage supports JSON/YAML documents only")
	}
	data, ok := documents[input.Path]
	if !ok {
		return nil, fmt.Errorf("in-memory source %s is missing", input.Path)
	}
	if err := budget.observe(input.Path, data); err != nil {
		return nil, err
	}
	return extractStructured(discoveredInput{input: input, path: input.Path}, data)
}

type sourceReader struct {
	ctx   context.Context
	root  string
	cache map[string][]byte
	total int
}

func (r *sourceReader) read(path string) ([]byte, error) {
	if data, ok := r.cache[path]; ok {
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
	r.total += len(data)
	if r.total > contextopt.MaxTotalBytes {
		return nil, fmt.Errorf("caveman source files exceed %d bytes", contextopt.MaxTotalBytes)
	}
	r.cache[path] = data
	return data, nil
}

type sourceAccumulator struct {
	sources []Source
	total   int
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
		if len(a.sources) > config.MaxRegisterSourceOutputs {
			return fmt.Errorf("caveman sources exceed %d extracted values", config.MaxRegisterSourceOutputs)
		}
	}
	return nil
}

func (a *sourceAccumulator) result() (Result, error) {
	if len(a.sources) == 0 {
		return Result{}, errors.New("caveman source extraction checked zero runtime text values")
	}
	sort.Slice(a.sources, func(i, j int) bool { return sourceKey(a.sources[i]) < sourceKey(a.sources[j]) })
	return Result{Sources: a.sources, SHA256: CoverageDigest(a.sources)}, nil
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
	return fmt.Sprintf("%s\x00%s\x00%09d\x00%s\x00%s", source.Path, source.Selector,
		source.Line, source.Surface, source.Kind)
}

// CoverageDigest binds every extracted value and its provenance in deterministic order.
func CoverageDigest(sources []Source) string {
	records := make([]string, len(sources))
	for index := range sources {
		records[index] = strings.Join([]string{sources[index].Path, sources[index].Selector,
			strconv.Itoa(sources[index].Line), string(sources[index].Surface), string(sources[index].Kind),
			string(sources[index].Format), sources[index].SHA256}, "\x00")
	}
	digest := sha256.Sum256([]byte(strings.Join(records, "\n") + "\n"))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func extractItem(item discoveredInput, data []byte) ([]Source, error) {
	switch item.input.Format {
	case config.SourceFormatShell:
		return extractShell(item, string(data))
	case config.SourceFormatPython:
		return extractPython(item, string(data))
	case config.SourceFormatJSON, config.SourceFormatYAML:
		return extractStructured(item, data)
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

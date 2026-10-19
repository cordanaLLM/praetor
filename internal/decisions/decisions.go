// Package decisions exports repository ADRs as a pinned AsDecided corpus and
// runs the corresponding stdio MCP server.
package decisions

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/adr"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// Version is the pinned AsDecided release used by decisions serve.
	Version = "0.31.2"
	// DecidedMCPHash is the SHA-256 of the pinned Linux x86-64 decided-mcp binary.
	DecidedMCPHash = "ae8cd44eca1730b09a08f497d4988732c918ca0a0f350fe0d1a0b60494bb269a"
	// DecidedHash is the SHA-256 of the pinned Linux x86-64 decided CLI binary.
	DecidedHash = "d915ddcdf41012a50a91f1341d31688948ab2e5a69177511ca2f3af33c30c574"
	// ArchiveHash is the SHA-256 of the pinned Linux x86-64 release archive.
	ArchiveHash = "a61bd78567836a9a60bd03fa291fb2c3b88f4a0ffa634cdfc648863c303b61d1"
	// ReleaseURL identifies the release whose checksums supply these pins.
	ReleaseURL = "https://github.com/asdecided/core/releases/tag/v0.31.2"
	// MaxRecords bounds ADR Markdown files exported in one run.
	MaxRecords = 2000
	// MaxRecordBytes bounds each source ADR file.
	MaxRecordBytes = 1 << 20
	maxScanEntries = 20000
	maxBinaryBytes = 512 << 20
)

// decided-mcp 0.31.2 hashes were checked against the release SHA256SUMS on 2026-10-16.
var optionsHeading = regexp.MustCompile(`(?im)^##[ \t]+(?:considered options|options considered|alternatives|options)[ \t]*$`)
var statusSection = regexp.MustCompile(`(?im)^##[ \t]+Status[ \t]*\n+([^\n]+)`)

// Report names source records written to the corpus and scaffold files skipped.
type Report struct {
	Written []string `json:"written"`
	Skipped []string `json:"skipped"`
}

// Transform applies the ADR-to-RAC heading and status-note conversion.
func Transform(text string) string {
	text = optionsHeading.ReplaceAllString(text, "## Alternatives Considered")
	match := statusSection.FindStringSubmatchIndex(text)
	if match == nil {
		return text
	}
	line := text[match[2]:match[3]]
	status := statusValue(line)
	if status == "" || len(line) <= len(status) {
		return text
	}
	updated := status + "\n\n## Status Note\n\n" + line
	return text[:match[2]] + updated + text[match[1]:]
}

func statusValue(line string) string {
	values := []string{"Proposed", "Accepted", "Superseded", "Deprecated"}
	for i := 0; i < len(values) && i < 4; i++ {
		value := values[i]
		if len(line) >= len(value) && strings.EqualFold(line[:len(value)], value) {
			return value
		}
	}
	return ""
}

// Export rebuilds outDir/decisions from ADR records. Empty outDir selects the
// repository cache; a nonempty outDir is an explicit caller-selected destination.
func Export(ctx context.Context, repoRoot, outDir string) (Report, error) {
	if ctx == nil {
		return Report{}, errors.New("decision export requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	repo, err := filepath.Abs(repoRoot)
	if err != nil {
		return Report{}, fmt.Errorf("resolve repository root: %w", err)
	}
	root, err := contextopt.OpenDirectory(ctx, repo)
	if err != nil {
		return Report{}, fmt.Errorf("open repository root: %w", err)
	}
	if err := root.Close(); err != nil {
		return Report{}, fmt.Errorf("close repository root: %w", err)
	}
	if outDir == "" {
		outDir = filepath.Join(repo, ".standards", "cache", "decisions")
	} else if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(repo, outDir)
	}
	if err := exportRecords(ctx, repo, outDir); err != nil {
		return Report{}, fmt.Errorf("export ADR decision corpus: %w", err)
	}
	return collectExportReport(ctx, repo)
}

func collectExportReport(ctx context.Context, repo string) (report Report, err error) {
	report = Report{Written: []string{}, Skipped: []string{"README.md", "0000-template.md"}}
	root, err := contextopt.OpenDirectoryIn(ctx, repo, adr.RecordDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return report, nil
	}
	if err != nil {
		return Report{}, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	names, err := adrNames(root)
	if err != nil {
		return Report{}, err
	}
	for i := 0; i < len(names) && i < maxScanEntries; i++ {
		if names[i] != "README.md" && names[i] != "0000-template.md" {
			report.Written = append(report.Written, names[i])
		}
	}
	return report, nil
}

// readExportedRecord reads and transforms one ADR entry if it is a valid decision document.
func readExportedRecord(ctx context.Context, source *os.Root, name string) (*exportedRecord, error) {
	if name == "README.md" || name == "0000-template.md" {
		return nil, nil
	}
	data, readErr := contextopt.ReadRootSnapshot(ctx, source, name)
	if readErr != nil {
		return nil, fmt.Errorf("read %s/%s: %w", adr.RecordDirectory, name, readErr)
	}
	if len(data) > MaxRecordBytes {
		return nil, fmt.Errorf("%s/%s exceeds %d bytes", adr.RecordDirectory, name, MaxRecordBytes)
	}
	return &exportedRecord{name: "adr-" + strings.TrimSuffix(name, ".md") + ".md", content: []byte(Transform(string(data)))}, nil
}

func exportRecords(ctx context.Context, repo, outDir string) (err error) {
	source, err := contextopt.OpenDirectoryIn(ctx, repo, adr.RecordDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return rebuildCorpus(ctx, outDir, nil)
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, source.Close()) }()
	names, err := adrNames(source)
	if err != nil {
		return err
	}
	records := make([]exportedRecord, 0, len(names))
	for i := 0; i < len(names) && i < maxScanEntries; i++ {
		record, readErr := readExportedRecord(ctx, source, names[i])
		if readErr != nil {
			return readErr
		}
		if record == nil {
			continue
		}
		if len(records) >= MaxRecords {
			return fmt.Errorf("ADR records exceed %d", MaxRecords)
		}
		records = append(records, *record)
	}
	return rebuildCorpus(ctx, outDir, records)
}

type exportedRecord struct {
	name    string
	content []byte
}

func adrNames(root *os.Root) (names []string, err error) {
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	entries, err := dir.ReadDir(maxScanEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > maxScanEntries {
		return nil, fmt.Errorf("ADR directory exceeds %d entries", maxScanEntries)
	}
	names = make([]string, 0, len(entries))
	for i := 0; i < len(entries) && i < maxScanEntries; i++ {
		if !entries[i].IsDir() && strings.HasSuffix(entries[i].Name(), ".md") {
			names = append(names, entries[i].Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func rebuildCorpus(ctx context.Context, outDir string, records []exportedRecord) (err error) {
	if err := contextopt.EnsureDirectory(ctx, outDir, 0o755); err != nil {
		return err
	}
	root, err := contextopt.OpenDirectory(ctx, outDir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	if err := root.RemoveAll("decisions"); err != nil {
		return fmt.Errorf("remove previous decisions corpus: %w", err)
	}
	if err := root.MkdirAll("decisions/adr", 0o755); err != nil {
		return fmt.Errorf("create ADR corpus directory: %w", err)
	}
	for i := 0; i < len(records) && i < MaxRecords; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := writeCorpusRecord(root, records[i]); err != nil {
			return err
		}
	}
	return nil
}

func writeCorpusRecord(root *os.Root, record exportedRecord) error {
	path := filepath.Join("decisions", "adr", record.name)
	file, err := root.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create corpus record %s: %w", record.name, err)
	}
	written, writeErr := file.Write(record.content)
	if writeErr == nil && written != len(record.content) {
		writeErr = io.ErrShortWrite
	}
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return fmt.Errorf("write corpus record %s: %w", record.name, err)
	}
	return nil
}

// PinCheck requires the PATH binary to match the checked release digest.
func PinCheck(ctx context.Context) error {
	_, err := verifiedBinary(ctx)
	return err
}

// verifiedBinary resolves decided-mcp on PATH and returns the resolved file whose digest
// matched the pin, so Serve starts the bytes it checked rather than a second PATH lookup.
func verifiedBinary(ctx context.Context) (string, error) {
	if ctx == nil {
		return "", errors.New("decision pin check requires a context")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	name, err := exec.LookPath("decided-mcp")
	if err != nil {
		return "", fmt.Errorf("decided-mcp %s missing; expected sha256 %s; release: %s", Version, DecidedMCPHash[:12], ReleaseURL)
	}
	resolved, err := filepath.EvalSymlinks(name)
	if err != nil {
		return "", fmt.Errorf("resolve decided-mcp %s at %s: %w", Version, name, err)
	}
	hash, _, err := contextopt.DigestBinarySnapshot(ctx, resolved, maxBinaryBytes)
	if err != nil {
		return "", fmt.Errorf("check decided-mcp %s at %s: %w", Version, resolved, err)
	}
	if hash != DecidedMCPHash {
		return "", fmt.Errorf("decided-mcp %s at %s has sha256 %s; expected %s; release: %s", Version, resolved, hash[:12], DecidedMCPHash[:12], ReleaseURL)
	}
	return resolved, nil
}

// Serve exports the current repository corpus, verifies decided-mcp, then runs
// it with the MCP stdio streams connected directly to the terminal client.
func Serve(ctx context.Context, repoRoot, outDir string) error {
	return serve(ctx, repoRoot, outDir, verifiedBinary, runChild)
}

type serverRunner func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error

func serve(ctx context.Context, repoRoot, outDir string, check func(context.Context) (string, error), runner serverRunner) error {
	if ctx == nil || check == nil || runner == nil {
		return errors.New("decision serve requires a context, pin check and server runner")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	repo, err := filepath.Abs(repoRoot)
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	if _, err := Export(ctx, repo, outDir); err != nil {
		return err
	}
	binary, err := check(ctx)
	if err != nil {
		return err
	}
	return runner(ctx, binary, []string{"--root", corpusRoot(repo, outDir)}, os.Stdin, os.Stdout, os.Stderr)
}

func corpusRoot(repoRoot, outDir string) string {
	if outDir == "" {
		return filepath.Join(repoRoot, ".standards", "cache", "decisions")
	}
	if !filepath.IsAbs(outDir) {
		return filepath.Join(repoRoot, outDir)
	}
	return outDir
}

func runChild(ctx context.Context, binary string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if err := util.RunCommandPassthrough(ctx, "", binary, stdin, stdout, stderr, args...); err != nil {
		return fmt.Errorf("run decided-mcp: %w", err)
	}
	return nil
}

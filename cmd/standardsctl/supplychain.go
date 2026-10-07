package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/supplychain"
)

// sbomUsage heads `sbom -h`. The generator reads a Go module's go.mod and nothing else, which
// neither the help nor the command list used to say (#573).
const sbomUsage = `Usage: praetorctl sbom [--path=.] [--out=FILE] [--module-version=VERSION]
       praetorctl sbom notices [--path=.] [--check]

Generates a CycloneDX 1.5 SBOM of the Go module at --path from its go.mod. Go only: a
directory without a go.mod is refused; catalogue other ecosystems with a generator such
as Syft or cdxgen. 'sbom notices' regenerates THIRD-PARTY-NOTICES.md of a Praetor checkout.`

func runSBOM(args []string) error {
	if len(args) > 0 && args[0] == "notices" {
		return runSBOMNotices(args[1:])
	}
	fs := flag.NewFlagSet("sbom", flag.ContinueOnError)
	path := fs.String("path", ".", "Go module root to generate the SBOM for (its go.mod is read)")
	out := fs.String("out", "", "Output file path (default stdout)")
	moduleVersion := fs.String("module-version", "",
		"Version of the scanned module to record (default: its release tag on HEAD, omitted when HEAD has none)")
	var usageErr error
	fs.Usage = func() {
		_, usageErr = fmt.Fprintf(fs.Output(), "%s\n\n", sbomUsage)
		fs.PrintDefaults()
	}
	if len(args) > 0 && isHelpToken(args[0]) {
		fs.SetOutput(os.Stdout)
		fs.Usage()
		return usageErr
	}

	if _, err := parseInterspersed(fs, args); err != nil {
		return errors.Join(err, usageErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bom, err := supplychain.GenerateCycloneDX(ctx, *path, supplychain.SBOMOptions{ModuleVersion: *moduleVersion})
	if err != nil {
		return fmt.Errorf("failed generating CycloneDX SBOM: %w", err)
	}

	data, err := json.MarshalIndent(bom, "", "  ")
	if err != nil {
		return fmt.Errorf("failed formatting SBOM: %w", err)
	}

	if *out != "" {
		if err := writeCommandArtifact(ctx, *out, data, 0644); err != nil {
			return fmt.Errorf("failed writing SBOM to %s: %w", *out, err)
		}
		fmt.Printf("CycloneDX 1.5 SBOM written to %s (%d components)\n", *out, len(bom.Components))
		return nil
	}

	fmt.Println(string(data))
	return nil
}

// sbomNoticesTimeout bounds one notices render: eight small file reads and one write (HISS-02).
const sbomNoticesTimeout = 30 * time.Second

// runSBOMNotices regenerates THIRD-PARTY-NOTICES.md at the top of a Praetor checkout from its
// go.mod, root Dockerfile, Markdown gate npm lock and embedded figure engine
// (supplychain.ReadNoticeSources, supplychain.RenderNotices), or with
// --check fails when the committed file is not what the render writes. A Renovate or lock
// maintenance bump is fixed by running it; a new component or an unreviewed license stops it
// with the row a person has to write.
func runSBOMNotices(args []string) error {
	fs := flag.NewFlagSet("sbom notices", flag.ContinueOnError)
	path := fs.String("path", ".", "Top of the Praetor checkout that holds "+supplychain.NoticesFile)
	check := fs.Bool("check", false, "Fail when "+supplychain.NoticesFile+" differs from the render instead of writing it")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(positional) > 0 {
		return fmt.Errorf("sbom notices accepts no positional arguments, got %q", positional)
	}
	ctx, cancel := commandContext(sbomNoticesTimeout)
	defer cancel()
	target := filepath.Join(*path, supplychain.NoticesFile)
	current, err := contextopt.ReadSnapshot(ctx, target)
	if err != nil {
		return fmt.Errorf("read %s: %w", target, err)
	}
	sources, err := supplychain.ReadNoticeSources(ctx, *path)
	if err != nil {
		return err
	}
	if *check {
		if err := supplychain.CheckNotices(string(current), sources); err != nil {
			return fmt.Errorf("[FAIL] %w", err)
		}
		fmt.Printf("[PASS] %s matches go.mod, the Dockerfile, the npm locks and the figure engine.\n", supplychain.NoticesFile)
		return nil
	}
	return writeSBOMNotices(ctx, target, string(current), sources)
}

// writeSBOMNotices renders the notices at target from sources and writes them through the
// shared snapshot publisher when they changed.
func writeSBOMNotices(ctx context.Context, target, current string, sources supplychain.NoticeSources) error {
	rendered, err := supplychain.RenderNotices(current, sources)
	if err != nil {
		return err
	}
	if rendered == current {
		fmt.Printf("[OK] %s is already current.\n", supplychain.NoticesFile)
		return nil
	}
	if err := writeCommandArtifact(ctx, target, []byte(rendered), 0644); err != nil {
		return fmt.Errorf("failed writing %s: %w", target, err)
	}
	fmt.Printf("[OK] %s regenerated from go.mod, the Dockerfile, the npm locks and the figure engine.\n", supplychain.NoticesFile)
	return nil
}

// unsignedProvenanceWarning goes to stderr on every provenance run, so the JSON on stdout
// stays parseable while nobody mistakes the statement for a signed attestation.
const unsignedProvenanceWarning = "warning: the SLSA provenance statement is UNSIGNED; it is not an attestation " +
	"until it is wrapped in a signed DSSE envelope whose signature and signer identity are verified"

// provenanceFlags are the provenance command's subject sources, builder, content check,
// UKI declarations and output.
type provenanceFlags struct {
	file, artifact, builder, digest, checksums, out string
	contentCheck                                    supplychain.ContentCheck
	uki                                             repeatedStringFlag
}

// contentCheckHint names the opt-out on a refusal for content that is not what its name says.
const contentCheckHint = "rerun with -content-check=report to attest the file and report its content as unverified"

func runProvenance(args []string) error {
	flags, err := parseProvenanceFlags(args)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), contextopt.MaxDigestDuration+contextopt.MaxDuration)
	defer cancel()

	stmt, verdicts, err := provenanceStatement(ctx, flags)
	if errors.Is(err, supplychain.ErrContentMismatch) {
		return fmt.Errorf("failed generating SLSA provenance: %w (%s)", err, contentCheckHint)
	}
	if err != nil {
		return fmt.Errorf("failed generating SLSA provenance: %w", err)
	}

	data, err := json.MarshalIndent(stmt, "", "  ")
	if err != nil {
		return fmt.Errorf("failed formatting SLSA statement: %w", err)
	}
	if err := supplychain.CheckInTotoStatement(data); err != nil {
		return fmt.Errorf("refusing a statement cosign verify-blob-attestation would reject: %w", err)
	}
	fmt.Fprint(os.Stderr, contentVerdictReport(verdicts))
	fmt.Fprintln(os.Stderr, unsignedProvenanceWarning)

	if flags.out != "" {
		if err := writeCommandArtifact(ctx, flags.out, data, 0644); err != nil {
			return fmt.Errorf("failed writing provenance to %s: %w", flags.out, err)
		}
		fmt.Printf("Unsigned SLSA v1.0 provenance statement written to %s (%s)\n", flags.out, subjectSummary(stmt.Subject))
		return nil
	}

	fmt.Println(string(data))
	return nil
}

// parseProvenanceFlags reads the provenance flags, written in any order, and refuses a run
// with a positional argument, with no subject source, or with -checksums beside the
// single-artifact flags it replaces.
func parseProvenanceFlags(args []string) (provenanceFlags, error) {
	var f provenanceFlags
	fs := flag.NewFlagSet("provenance", flag.ContinueOnError)
	fs.StringVar(&f.file, "file", "", "Artifact file whose bytes the subject digest is computed from (required unless -checksums)")
	fs.StringVar(&f.artifact, "artifact", "", "Subject name (default: base name of -file)")
	fs.StringVar(&f.builder, "builder", "", "Builder identifier (default in GitHub Actions: $GITHUB_SERVER_URL/$GITHUB_WORKFLOW_REF; required elsewhere)")
	fs.StringVar(&f.digest, "digest", "", "Optional expected SHA-256 hex digest; the run fails unless -file hashes to it")
	fs.StringVar(&f.checksums, "checksums", "",
		"sha256sum manifest, such as GoReleaser's checksums.txt: every listed file beside it becomes a subject, "+
			"digested from its bytes and cross-checked against its line (excludes -file, -artifact and -digest)")
	fs.StringVar(&f.out, "out", "", "Output file path (default stdout)")
	contentCheck := fs.String("content-check", string(supplychain.ContentCheckEnforce),
		"What a file whose bytes are not the type its name declares (.deb, .udeb, .ddeb, .efi, a Unified Kernel Image) does: "+
			"enforce refuses the statement, report attests the file and warns that its content is unverified")
	fs.Var(&f.uki, "uki",
		"Glob declaring every subject whose name it matches a Unified Kernel Image, which then needs a .linux section "+
			"whatever its name; slash-separated, * stays in one segment, ** spans segments, each glob must match a subject (repeatable)")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return f, err
	}
	if f.contentCheck, err = supplychain.ParseContentCheck(*contentCheck); err != nil {
		return f, fmt.Errorf("flag -content-check: %w", err)
	}
	if err := checkProvenanceSources(f, positional); err != nil {
		return f, err
	}
	builder, err := resolveBuilder(f.builder)
	if err != nil {
		return f, err
	}
	f.builder = builder
	return f, nil
}

// checkProvenanceSources refuses a positional argument, a run with no subject source, and
// -checksums beside the single-artifact flags it replaces.
func checkProvenanceSources(f provenanceFlags, positional []string) error {
	switch {
	case len(positional) > 0:
		return fmt.Errorf("provenance accepts no positional arguments, got %q", positional)
	case f.checksums != "" && (f.file != "" || f.artifact != "" || f.digest != ""):
		return fmt.Errorf("flag -checksums names every subject itself and excludes -file, -artifact and -digest")
	case f.checksums == "" && f.file == "":
		return fmt.Errorf("flag -file is required (or -checksums for every file a sha256sum manifest lists): " +
			"the subject digest is computed from the artifact bytes, and -digest is only a cross-check against them")
	default:
		return nil
	}
}

// resolveBuilder returns the -builder value, or in GitHub Actions the running workflow's
// identity ($GITHUB_SERVER_URL/$GITHUB_WORKFLOW_REF). Outside GitHub Actions there is no
// identity to derive, and no default names someone else's builder, so the flag is required.
func resolveBuilder(flagValue string) (string, error) {
	if flagValue != "" {
		trimmed := strings.TrimSpace(flagValue)
		if trimmed == "" {
			return "", fmt.Errorf("flag -builder cannot be blank")
		}
		return trimmed, nil
	}
	serverURL := strings.TrimSpace(os.Getenv("GITHUB_SERVER_URL"))
	workflowRef := strings.TrimSpace(os.Getenv("GITHUB_WORKFLOW_REF"))
	if serverURL != "" && workflowRef != "" {
		return strings.TrimRight(serverURL, "/") + "/" + strings.TrimLeft(workflowRef, "/"), nil
	}
	return "", fmt.Errorf("flag -builder is required outside GitHub Actions (GITHUB_SERVER_URL and GITHUB_WORKFLOW_REF are unset)")
}

// provenanceStatement builds the statement from the one subject source f names, with each
// subject's content check verdict.
func provenanceStatement(ctx context.Context, f provenanceFlags) (*supplychain.SLSAStatement, []supplychain.ContentVerdict, error) {
	if f.checksums != "" {
		return supplychain.GenerateSLSAProvenanceFromChecksums(ctx, supplychain.ChecksumsRequest{
			ManifestPath: f.checksums, BuilderID: f.builder, ContentCheck: f.contentCheck, UKIGlobs: f.uki,
		})
	}
	return supplychain.GenerateSLSAProvenance(ctx, supplychain.ProvenanceRequest{
		ArtifactPath: f.file, ArtifactName: f.artifact, BuilderID: f.builder, ExpectedSHA256: f.digest,
		ContentCheck: f.contentCheck, UKIGlobs: f.uki,
	})
}

// contentVerdictReport returns one warning line per subject whose content is unverified and
// one note naming every subject no content rule covers, so a checksummed file of an unknown
// type is never mistaken for a verified one. Verified subjects need no line.
func contentVerdictReport(verdicts []supplychain.ContentVerdict) string {
	var report strings.Builder
	var unchecked []string
	for _, verdict := range verdicts {
		switch verdict.Status {
		case supplychain.ContentUnverified:
			report.WriteString("warning: content of " + verdict.Name + " is UNVERIFIED: expected " + verdict.Format + ", but " + verdict.Reason + "\n")
		case supplychain.ContentUnchecked:
			unchecked = append(unchecked, verdict.Name)
		}
	}
	if len(unchecked) > 0 {
		fmt.Fprintf(&report, "note: content unchecked for %d subject(s) of a file type praetorctl has no content rule for: %s\n",
			len(unchecked), strings.Join(unchecked, ", "))
	}
	return report.String()
}

// subjectSummary names the one subject of a single-artifact statement, or counts the
// subjects of a manifest statement.
func subjectSummary(subjects []supplychain.Subject) string {
	if len(subjects) == 1 {
		return fmt.Sprintf("subject %s sha256:%s", subjects[0].Name, subjects[0].Digest["sha256"])
	}
	return fmt.Sprintf("%d subjects", len(subjects))
}

// writeCommandArtifact preserves explicit public/private output modes and rejects
// stale observations, symlinks, and partial writes through the shared snapshot publisher.
func writeCommandArtifact(ctx context.Context, path string, data []byte, mode os.FileMode) error {
	before, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return contextopt.ReplaceSnapshot(ctx, path, data, contextopt.ReplaceOptions{Expected: before, Exists: err == nil, Mode: mode})
}

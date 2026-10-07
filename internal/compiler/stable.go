package compiler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// UnlayeredWarning is the line an AGENTS.md without band markers earns. The compiled files
// keep the source order, so a client's prompt cache loses every byte after the first edit.
// The warning lasts one release; the next makes the missing markers a failure.
const UnlayeredWarning = "[WARN] AGENTS.md carries no cache band markers; add " +
	agentcontext.BandHeadMarker + ", " + agentcontext.BandConfigMarker + " and " +
	agentcontext.BandTailMarker + " to keep a stable prompt prefix (the next release fails without them)"

// stableClocks and stableSeeds are the two environments the stability check renders under.
// Both pairs differ, so a render that reads either one produces different bytes and fails.
var (
	stableClocks = [2]time.Time{time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC), time.Date(2040, 11, 12, 13, 14, 15, 0, time.UTC)}
	stableSeeds  = [2]int64{2, 5}
)

// VerifyStableContext checks that the compiled context is a stable cache prefix: AGENTS.md
// rendered twice, under different injected clocks and shuffled visit orders, gives identical
// bytes for every vendor file, and the head band carries no volatile token (timestamp,
// sha256 digest, absolute path, run counter). A source without band markers has no head to
// scan; it earns UnlayeredWarning, written to w. Both checks run and both failures return.
func VerifyStableContext(ctx context.Context, w io.Writer, tr *Transpiler, source string) error {
	data, err := contextopt.ReadSnapshot(ctx, source)
	if err != nil {
		return fmt.Errorf("stability check: failed to read source %s: %w", source, err)
	}
	selected, err := tr.forRepository(ctx, filepath.Dir(source))
	if err != nil {
		return fmt.Errorf("stability check: %w", err)
	}
	content := string(data)
	renderErr := verifyRenderTwice(selected, content)
	head, layered := agentcontext.HeadBand(content)
	if !layered {
		_, werr := fmt.Fprintln(w, UnlayeredWarning)
		return errors.Join(renderErr, werr)
	}
	return errors.Join(renderErr, headVolatileError(source, head))
}

// verifyRenderTwice compiles content under the two stability environments and names each
// vendor file whose bytes differ.
func verifyRenderTwice(tr *Transpiler, content string) error {
	var results [2]*CompileResult
	for i := range results {
		run := *tr
		clock := stableClocks[i]
		run.Env = &agentcontext.RenderEnv{Now: func() time.Time { return clock }, Seed: stableSeeds[i]}
		res, err := run.CompileContent(content)
		if err != nil {
			return fmt.Errorf("stability check: render %d: %w", i+1, err)
		}
		results[i] = res
	}
	drift, err := driftedFiles(results[0], results[1])
	if err != nil {
		return err
	}
	if len(drift) > 0 {
		return fmt.Errorf("stability check: two renders under different clocks and visit orders differ in %s", strings.Join(drift, ", "))
	}
	return nil
}

// driftedFiles names the vendor files whose bytes differ between two renders, and fails when
// the renders emitted different file sets.
func driftedFiles(a, b *CompileResult) ([]string, error) {
	if len(a.Files) != len(b.Files) {
		return nil, fmt.Errorf("stability check: renders emitted %d and %d files", len(a.Files), len(b.Files))
	}
	var drift []string
	for i, file := range a.Files {
		other := b.Files[i]
		if file.RelativePath != other.RelativePath || file.Content != other.Content {
			drift = append(drift, file.RelativePath)
		}
	}
	return drift, nil
}

// headVolatileError lists the volatile tokens in head, or returns nil.
func headVolatileError(source, head string) error {
	tokens := agentcontext.ScanVolatile(head)
	if len(tokens) == 0 {
		return nil
	}
	lines := make([]string, len(tokens))
	for i, token := range tokens {
		lines[i] = token.String()
	}
	return fmt.Errorf("stability check: %s head band holds volatile text that breaks the prompt cache: %s",
		source, strings.Join(lines, "; "))
}

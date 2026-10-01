package adopt

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/editor"
	"github.com/cordanaLLM/praetor/internal/util"
)

// maxQuotedEditorMembers bounds how many managed JSON members an editor file's report entry
// names; the rest are counted.
const maxQuotedEditorMembers = 5

// reconcileEditorFile creates f when it is absent. An existing file is resolved through
// editor.ResolveExisting with drift kept, the rule `editors generate` applies except that
// adoption never overwrites an editor file nothing audits (#502):
//
//   - a file holding every managed value is verified, and a developer-owned file
//     (editor.IsPreservedEditorFile) is preserved;
//   - a JSON file missing managed values is kept on a plain run, with a warning naming them, and
//     merged under --force (mergeEditorFile): every adopter key stays, the managed ones are added;
//   - a JSON file adoption cannot merge (JSONC comments, invalid JSON, a conflicting managed
//     value), a differing non-JSON file and an unreadable one are kept with a warning, and
//     adoption continues.
func (s *adoptSession) reconcileEditorFile(ctx context.Context, f editor.GeneratedFile) error {
	full, err := repoFile(s.repoPath, f.Path)
	if err != nil {
		return err
	}
	existing, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err != nil {
		s.keepEditorFile(f.Path, fmt.Sprintf("it could not be read: %v", err))
		return nil
	}
	if !exists {
		return s.createEditorFile(full, f)
	}
	resolution, err := editor.ResolveExisting(f, existing, true)
	if err != nil {
		s.keepEditorFile(f.Path, unmergedEditorReason(err, s.forceCommand()))
		return nil
	}
	return s.applyEditorResolution(ctx, full, f, existing, resolution)
}

// createEditorFile writes f's template at full, which does not exist yet.
func (s *adoptSession) createEditorFile(full string, f editor.GeneratedFile) error {
	if err := s.write(full, []byte(f.Content), filePerm); err != nil {
		return err
	}
	s.report.recordCreated(f.Path, fmt.Sprintf("Synthesized %s IDE configuration for archetype '%s'", f.Editor, s.arch))
	return nil
}

// applyEditorResolution records what editor.ResolveExisting decided for the existing file at
// full, which held existing, and merges a JSON file under --force. Adoption resolves with drift
// kept, so WriteRewritten, or any outcome not listed, is an error rather than a file reported as
// verified.
func (s *adoptSession) applyEditorResolution(ctx context.Context, full string, f editor.GeneratedFile, existing []byte, r editor.Resolution) error {
	switch r.Outcome {
	case editor.WriteMerged:
		return s.mergeEditorFile(ctx, full, f, existing, r)
	case editor.WriteKept:
		s.keepEditorFile(f.Path, "it differs from the "+f.Editor+" template (regenerating would change "+
			describeLineDelta(existing, []byte(f.Content))+") and adoption does not overwrite it. Delete it and "+
			"re-run adopt to regenerate it")
	case editor.WritePreserved:
		s.report.recordReconciled(f.Path, fmt.Sprintf("Existing developer-owned %s file preserved, not verified", f.Editor))
	case editor.WritePresent:
		s.report.recordReconciled(f.Path, fmt.Sprintf("Existing %s IDE configuration holds every managed value", f.Editor))
	default:
		return fmt.Errorf("editor file %s: unexpected resolution %s with drift kept", f.Path, r.Outcome)
	}
	return nil
}

// mergeEditorFile adds the managed members r.Added names to the existing JSON file at full.
// A plain run keeps the file and names them. Under --force the merged document replaces the
// file through replaceExisting, bound to the observed bytes, so the prior bytes are backed up
// and the report lists the file as merged with its line delta; the merge re-indents the file
// and orders its keys, and every adopter key and list entry stays.
func (s *adoptSession) mergeEditorFile(ctx context.Context, full string, f editor.GeneratedFile, existing []byte, r editor.Resolution) error {
	members := quoteFirst(r.Added, len(r.Added), maxQuotedEditorMembers, strconv.Quote)
	if !s.opts.Force {
		s.keepEditorFile(f.Path, "it lacks the managed "+f.Editor+" values "+members+
			". Re-run "+s.forceCommand()+" to merge them; every other key is kept")
		return nil
	}
	merged := []byte(r.Content)
	err := s.replaceExisting(ctx, replacement{
		rel: f.Path, before: existing, after: merged, merge: true,
		detail: "Merged the managed " + f.Editor + " values " + members + ", every other key kept",
		publish: func(ctx context.Context) error {
			return contextopt.ReplaceSnapshot(ctx, full, merged, contextopt.ReplaceOptions{
				Expected: existing, Exists: true, Mode: filePerm,
			})
		},
	})
	if err != nil {
		return fmt.Errorf("merge %s: %w", f.Path, err)
	}
	return nil
}

// keepEditorFile records the existing editor file rel as kept unchanged for reason, with a
// warning: `editors verify` fails it until it is reconciled.
func (s *adoptSession) keepEditorFile(rel, reason string) {
	s.report.recordReconciled(rel, "Existing IDE configuration kept unchanged, not verified: "+reason)
	s.report.addWarning("%s: kept unchanged, not verified: %s", rel, reason)
}

// unmergedEditorReason says why editor.ResolveExisting refused to merge an existing JSON file
// and what the operator can do about it: fix it, then run rerun, the forced re-adoption
// (ForceCommand).
func unmergedEditorReason(err error, rerun string) string {
	if errors.Is(err, editor.ErrExistingJSONInvalid) {
		return "it is not strict JSON (" + err.Error() + "). Adoption and `" + util.PraetorCLI + " editors verify` " +
			"read strict JSON only, and a merge would drop its comments or other non-JSON content, so it never " +
			"verifies as it is. Make it strict JSON (remove its comments, trailing commas and duplicate keys) and " +
			"run " + rerun + " to merge the managed values, or delete it and re-run adopt to regenerate it"
	}
	return "its managed values were not merged (" + err.Error() + "). Resolve the conflict, then run " + rerun
}

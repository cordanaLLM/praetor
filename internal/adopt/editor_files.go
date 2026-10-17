package adopt

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/editor"
	"github.com/cordanaLLM/praetor/internal/strictjson"
	"github.com/cordanaLLM/praetor/internal/util"
)

// maxQuotedEditorMembers bounds how many managed JSON members an editor file's report entry
// names; the rest are counted.
const maxQuotedEditorMembers = 5

const jetBrainsInspectionProfile = ".idea/inspectionProfiles/standards.xml"

// priorJetBrainsProfileDigests records every distinct profile the generator shipped, and the
// current renderings used by the boundary check. Fixtures reproduce each digest.
var priorJetBrainsProfileDigests = map[string]string{
	"0bf9fd966885d96c44b010880daa108d7507fc59b0d518340687445890659806": "current framework profile",
	"22f502f2d1f05fe61a9ea75d9e9d02c3eea1a3ce9a5efc435e98bf3fabfc5b53": "current app-service profile",
	"3dc0f5b2ece34da72f32c1b31594e26ca843769a1bdc0629ce99f7c149bdae78": "current native-gpu-systems profile",
	"7b5e6ebfa0d16ff657ab879f06b25a7bb6a219e31e56aacd7e3ba4ec5b67f082": "HISS inspections before #476, framework, resolved 60-line limit",
	"b8dca54ff4a2c30065c3837eb927137c7d25a9b9c97864930d8997cc8606691b": "HISS inspections before #476, app-service, resolved 60-line limit",
	"8de313f1d551e2834af66923d58851e23d09e47e0b60528c6636473e13d627ac": "HISS inspections before #476, native-gpu-systems, resolved 60-line limit",
	"5bef7fae916b419bb7fc99a72ea4d0c6a23a1ac69e5064d1801eba905cbf212c": "first release and pre-#439 framework profile, 75-line limit",
	"31c119f8597a0b199e600608a35e0d8f385cfcd23fc4310e821000fca1b7b2f5": "pre-#439 app-service profile, 75-line limit",
	"1e27fe78fdd0c04bdde70b079f11eef69f4050b16341f06d230ff96eef047708": "pre-#439 native-gpu-systems profile, 75-line limit",
}

// reconcileEditorFile creates f when it is absent. An existing file is resolved through
// editor.ResolveExisting with drift kept, the rule `editors generate` applies except that
// adoption never overwrites an editor file nothing audits (#502):
//
//   - a file holding every managed value is verified, and a developer-owned file
//     (editor.IsPreservedEditorFile) is preserved;
//   - a JSON file missing managed values is kept on a plain run, with a warning naming them, and
//     merged under --force (mergeEditorFile): every adopter key stays, the managed ones are added;
//   - a JSON file adoption cannot merge (invalid JSON, a conflicting managed value, or a
//     .vscode file whose comments or trailing commas the merge would lose), a differing non-JSON
//     file and an unreadable one are kept with a warning, and adoption continues. A .vscode file
//     VS Code reads as JSON with Comments is read that way (strictjson.DialectOf), so a
//     commented one holding every managed value is verified, not kept.
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
	refreshed, err := s.refreshPriorJetBrainsProfile(ctx, full, f, existing)
	if err != nil {
		return fmt.Errorf("refresh editor file %s: %w", f.Path, err)
	}
	if refreshed {
		return nil
	}
	resolution, err := editor.ResolveExisting(f, existing, true)
	if err != nil {
		s.keepEditorFile(f.Path, unmergedEditorReason(f.Path, err, s.forceCommand()))
		return nil
	}
	return s.applyEditorResolution(ctx, full, f, existing, resolution)
}

// refreshPriorJetBrainsProfile replaces a recorded unedited profile with the current generator
// output. It keeps the existing consistent line-ending style and binds the write to the bytes
// observed before reconciliation; an edit or a current profile falls through unchanged.
func (s *adoptSession) refreshPriorJetBrainsProfile(ctx context.Context, full string, f editor.GeneratedFile, existing []byte) (bool, error) {
	if f.Path != jetBrainsInspectionProfile {
		return false, nil
	}
	known, crlf := priorRendering(existing, priorJetBrainsProfileDigests)
	if !known {
		return false, nil
	}
	current, err := util.CanonicalTextEquivalent(existing, []byte(f.Content))
	if err != nil {
		return false, fmt.Errorf("compare current profile: %w", err)
	}
	if current {
		return false, nil
	}
	content := []byte(util.RestoreLineEndings(f.Content, crlf))
	if err := contextopt.ReplaceSnapshot(ctx, full, content, contextopt.ReplaceOptions{
		Expected: existing, Exists: true, Mode: filePerm,
	}); err != nil {
		return false, fmt.Errorf("publish current profile: %w", err)
	}
	s.report.recordReconciled(f.Path, "Refreshed an unedited earlier JetBrains inspection profile")
	return true, nil
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

// unmergedEditorReason says why editor.ResolveExisting refused to merge the existing JSON file
// rel and what the operator can do about it: fix it, then run rerun, the forced re-adoption
// (ForceCommand). The advice follows the dialect the file's editor reads it in
// (strictjson.DialectOf): a .vscode file VS Code reads as JSON with Comments keeps its comments,
// so it is never told to remove them to become valid, only that a merge cannot write around
// them (editor.CommentedJSONError).
func unmergedEditorReason(rel string, err error, rerun string) string {
	var commented *editor.CommentedJSONError
	switch {
	case errors.As(err, &commented):
		return "it carries comments or trailing commas, which a merge would lose because it rewrites the file, and lacks the managed values " +
			quoteFirst(commented.Lacks, len(commented.Lacks), maxQuotedEditorMembers, strconv.Quote) +
			". Add them by hand and re-run adopt, or remove the comments and trailing commas and run " + rerun + " to merge them"
	case !errors.Is(err, editor.ErrExistingJSONInvalid):
		return "its managed values were not merged (" + err.Error() + "). Resolve the conflict, then run " + rerun
	case strictjson.DialectOf(rel) == strictjson.JSONC:
		return "it is not valid " + strictjson.JSONC.String() + " (" + err.Error() + "), the format its editor reads it in, " +
			"so it never verifies as it is. Fix what the error names (comments and trailing commas may stay, a " +
			"duplicate key may not) and run " + rerun + " to merge the managed values, or delete it and re-run adopt " +
			"to regenerate it"
	default:
		return "it is not strict JSON (" + err.Error() + "). Adoption and `" + util.PraetorCLI + " editors verify` " +
			"read strict JSON there, and a merge would drop its comments or other non-JSON content, so it never " +
			"verifies as it is. Make it strict JSON (remove its comments, trailing commas and duplicate keys) and " +
			"run " + rerun + " to merge the managed values, or delete it and re-run adopt to regenerate it"
	}
}

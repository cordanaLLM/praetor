package adopt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Each lock permits at most 256 profiles and 256 facets.
const maxAdoptPolicyFiles = 512

// priorCatalogDigests are the digests (priorRendering) of every .config/archetypes file Praetor
// shipped before the catalog passed yamllint's default rules (BUG-782), keyed to the file. Each
// decoded to exactly the values of the text that replaced it then; only the layout moved. A
// later value change does not carry over: membership alone never authorizes a change, and a
// text listed here is replaced without --force only by a source text with exactly its values
// (isLayoutOnlySuccessor), both when the lock is re-pinned (pinsEarlierCatalog) and when the
// file is rewritten (prepareCatalogWrites). The rewrite writes the pinned source bytes, whose
// digest the lock checks byte for byte, whatever line-ending style the earlier text had.
// testdata/catalog-prior reproduces each digest (policy_catalog_prior_test.go).
var priorCatalogDigests = map[string]string{
	"0ef3ebf5423873d200e12ba405ca93b3064c57eedf9091852f2d69316dfb7c63": "facets/agent-sandboxed.yaml",
	"4de328ae5f1c4a993f45c258758d105395b92b2974abf52023fc685b61dafdd9": "facets/api-public.yaml", // gitleaks:allow: a digest, no credential
	"3381e1b7ab5cfb1029fa1b96cd84c7d3d5a890bc2d1f340a7b4e8b5bf61e2256": "facets/docs-seoportal.yaml",
	"96077842e27cf814ee7e26610d4697f17987f06b560dfd917923e65121f864b1": "facets/perf-hotpath.yaml",
	"34de9dc4fe2b7da48c152c2e7457891ebc41ace8e671fd1db0f49e1fafcb567f": "facets/security-high.yaml",
	"c6c06e4f951b2c6d72df15f7d4c095892db3d64a1d616b5cfdae462d6ba9c26d": "facets/tooling-vscode-extension.yaml",
	"f9bf5a186f252fc3ed5bd75f4e2167538135e7f2577547b07d7632cde4e224e5": "app-service.yaml",
	"9c031ec962998fceb63b47ffdca84f4915c5b98e9a42468bc0fba2d54224108e": "closed-private.yaml",
	"5b442bab492d62fa1bf7b7f0835a63c6278490f0e49be8b3c26e3b2493ac688d": "container-image.yaml",
	"91d601b8481143fa9621ab0f61d2d6e261af7a97d71ecc911868067e30ff0406": "framework.yaml",
	"cf4e44806b73f90a85179739a52e20927ae443cf1732114e9b9652a10ce1ca8c": "gitops-infra.yaml",
	"12857e00e09953e0e489852ca3acb7266bcd91c4151db814cb9f856ed719687e": "library-client.yaml",
	"aabdfa13501105032bbecfb39eac49b482be4f426d7b409999e12e28f17b59c9": "native-gpu-systems.yaml",
	"4fb8a98fc3e6b3bfaf4b6258a9dddaf58e68eb1b4db23d94f6fe7655f9b4db5c": "org-health.yaml",
	"236ba3a1bb7dcdde43e86a46cb49dd935ef8070ecd39584045510df842ff136f": "os-image.yaml",
	"f5e2e8aa975abc509cdd7b9ae21ea71dfec73a01fcb947508c856fd25285165b": "pages-site.yaml",
	"55141334e1d4499096d2a7561f1a6f5776a8b84fa8affa06a77249f1d4711817": "planning-artifacts.yaml",
	"6fb2c0dc63886be95afa1444850ae2c650ab946b83ae53bdaa1d8325f74431ea": "template-seed.yaml",
	"4a406a9d957078d5082bc906ca58d74501ce43ee19b5e6123ace83d3511b4a36": "upstream-fork.yaml",
	"22483af9e97cb2adb4cc024024ee54efb1d223e7f85858da05f05c17099dcae1": "web-package.yaml",
}

// isLayoutOnlySuccessor reports whether before is an unmodified earlier Praetor catalog text
// (priorCatalogDigests) and after holds exactly its values, so replacing before with after
// moves the catalog's layout and never a policy value.
func isLayoutOnlySuccessor(before, after []byte) bool {
	return isPriorRendering(before, priorCatalogDigests) && util.YAMLEquivalent(before, after) == nil
}

// mayReplaceCatalogText reports whether adoption may publish content over the existing catalog
// text before: it already holds content, --force was given, or before is an unmodified earlier
// Praetor text whose values content keeps (isLayoutOnlySuccessor).
func mayReplaceCatalogText(s *adoptSession, before, content []byte) bool {
	return bytes.Equal(before, content) || s.opts.Force || isLayoutOnlySuccessor(before, content)
}

type catalogWrite struct {
	artifact config.PolicyArtifact
	path     string
	before   []byte
	exists   bool
}

// Materialize the verified pins so generated plain audit commands are usable
// without the original workstation source path or any private external layers.
func reconcilePolicyCatalog(ctx context.Context, s *adoptSession) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.opts.DryRun {
		return planPolicyCatalog(ctx, s)
	}
	policy, err := config.LoadEffectivePolicyContext(ctx, config.EffectiveOptions{
		Root: s.repoPath, CatalogRoot: s.opts.LockSourceRoot,
	})
	if err != nil {
		return fmt.Errorf("resolve adoption catalog (select --lock-source-root for missing pinned profiles): %w", err)
	}
	writes, err := prepareCatalogWrites(ctx, s, policy.CatalogArtifacts)
	if err != nil {
		return err
	}
	if err := config.ValidateCatalogProjectionContext(ctx, s.repoPath, policy.CatalogArtifacts); err != nil {
		return fmt.Errorf("validate prospective adoption catalog: %w", err)
	}
	if err := publishCatalogWrites(ctx, s, writes); err != nil {
		return err
	}
	s.policy, err = config.LoadEffectivePolicyContext(ctx, config.EffectiveOptions{Root: s.repoPath, Audit: true})
	if err != nil {
		return fmt.Errorf("read back repository-local pinned policy: %w", err)
	}
	return nil
}

// Legacy standalone scan callers use the scanner's own default ceiling. Planned and applied
// adoption use the same resolved ceiling as the generated CLI/MCP audit.
func adoptionScanLimit(s *adoptSession) int {
	if s.policy != nil {
		return s.policy.Policy.Complexity.MaxFuncLOC
	}
	return hiss.DefaultMaxFuncLOC
}

// Inspect every destination before publishing any catalog entry. Force permits
// explicit replacement, and an unmodified earlier Praetor text is replaced without it by a
// text holding exactly its values (isLayoutOnlySuccessor); otherwise differing user files are
// always preserved. Whether git ignores a destination is checked with every other written file
// once the chain has run (reportIgnoredWrites), after the git-ignore step's rules exist.
func prepareCatalogWrites(ctx context.Context, s *adoptSession, artifacts []config.PolicyArtifact) ([]catalogWrite, error) {
	if len(artifacts) > maxAdoptPolicyFiles {
		return nil, errors.New("adoption catalog exceeds 512 pinned files")
	}
	writes := make([]catalogWrite, 0, len(artifacts))
	for i := 0; i < len(artifacts) && i < maxAdoptPolicyFiles; i++ {
		artifact := artifacts[i]
		if fmt.Sprintf("%x", sha256.Sum256(artifact.Content)) != artifact.SHA256 {
			return nil, errors.New("adoption catalog snapshot differs from its verified hash")
		}
		path, err := repoFile(s.repoPath, artifact.RelativePath)
		if err != nil {
			return nil, err
		}
		before, exists, err := contextopt.ObserveSnapshot(ctx, path)
		if err != nil {
			return nil, err
		}
		if exists && !mayReplaceCatalogText(s, before, artifact.Content) {
			return nil, fmt.Errorf("pinned catalog destination differs: %s; inspect it before explicit forced adoption", artifact.RelativePath)
		}
		writes = append(writes, catalogWrite{artifact: artifact, path: path, before: before, exists: exists})
	}
	return writes, nil
}

// publishCatalogFile writes one pinned catalog file bound to the bytes prepareCatalogWrites
// observed. A file that already holds the pinned bytes is verified; an absent one is created;
// an unmodified earlier Praetor text is refreshed to its layout-only successor; any other
// existing file (--force) is replaced through replaceExisting, so the report lists it as
// replaced with its line delta and backup. A dry run (profile set --dry-run) records the same
// entries and writes nothing.
func publishCatalogFile(ctx context.Context, s *adoptSession, write catalogWrite) error {
	rel := write.artifact.RelativePath
	if write.exists && bytes.Equal(write.before, write.artifact.Content) {
		s.report.recordReconciled(rel, "Verified unchanged repository-local pinned policy")
		return nil
	}
	if replace, ok := catalogReplacement(write); ok {
		return s.replaceExisting(ctx, replace)
	}
	if !s.opts.DryRun {
		if err := write.publish(ctx); err != nil {
			return err
		}
	}
	if write.exists {
		s.report.recordReconciled(rel, "Refreshed an unmodified earlier Praetor catalog text; its values are unchanged, only the layout moved")
	} else {
		s.report.recordCreated(rel, "Materialized exact pinned policy for repository-local audit")
	}
	return nil
}

// catalogReplacement returns the replacement of write when it overwrites adopter bytes: an
// existing file that holds neither the pinned bytes nor an earlier Praetor text they succeed
// (isLayoutOnlySuccessor). prepareCatalogWrites admits one only under --force. The real run and
// the dry-run plan share it through publishCatalogFile.
func catalogReplacement(write catalogWrite) (replacement, bool) {
	content := write.artifact.Content
	if !write.exists || bytes.Equal(write.before, content) || isLayoutOnlySuccessor(write.before, content) {
		return replacement{}, false
	}
	return replacement{
		rel: write.artifact.RelativePath, before: write.before, after: content,
		detail: "Replaced pinned policy from explicitly selected source", publish: write.publish,
	}, true
}

// publish writes the pinned bytes over the ones observed at write.path.
func (write catalogWrite) publish(ctx context.Context) error {
	if err := contextopt.EnsureDirectory(ctx, filepath.Dir(write.path), dirPerm); err != nil {
		return err
	}
	return contextopt.ReplaceSnapshot(ctx, write.path, write.artifact.Content, contextopt.ReplaceOptions{
		Expected: write.before, Exists: write.exists, Mode: filePerm,
	})
}

// publishCatalogWrites publishes and records every prepared catalog write (publishCatalogFile).
// The real run and the dry-run plan (planPolicyCatalog) both go through it, so a dry run lists
// each pinned file as created, verified, refreshed or replaced, exactly as the run it previews
// records it, and writes none. A dry run used to record one directory entry for the catalog
// and list only the replacements, so the files the run created went unnamed (#366).
func publishCatalogWrites(ctx context.Context, s *adoptSession, writes []catalogWrite) error {
	for i := 0; i < len(writes) && i < maxAdoptPolicyFiles; i++ {
		if err := publishCatalogFile(ctx, s, writes[i]); err != nil {
			return err
		}
	}
	return nil
}

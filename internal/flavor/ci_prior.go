// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor

// The CI workflow templates moved to the hosted gate shape (#817): they run on a push to the
// default branch and on the pull request activities that can change what merges, and fail a
// draft in their first step instead of running it (templates/hostedgate.go). Every text the
// templates rendered before, since flavor apply first scaffolded them from templates/ (#463), is
// recorded here (TemplateItem.Prior), so a plain apply or adoption refreshes an unedited copy and
// leaves an edited one until --force. testdata/ci-prior/<template directory> holds each text, and
// TestCIWorkflowPriorTextsAreRecorded keeps the fixtures and these digests one set.
var (
	// priorGoCIDigests are the earlier texts of go/ci-go.yml.tmpl.
	priorGoCIDigests = map[string]string{
		"36d5e19301f02efef3788fe3a1a7246fec40ea3a83c0358143d437652aba8cbc": "the Go CI job before its go.mod manifest guard",
		"cfe4c9e934d122e5588bde13252a1c17cb92c4231bf3528778134c0ec3dbad98": "the first embedded Go CI job, an unquoted 'on' key (#463)",
		"68921b5bd34b95510d973496d267ddf900709afe0c56ecd9938d785e823f6072": "the Go CI job on every push to main and every draft pull request (#520 to #817)",
	}
	// priorRustCIDigests are the earlier texts of rust/ci-rust.yml.tmpl.
	priorRustCIDigests = map[string]string{
		"06f6c49b38af50835f7c93678828d94e1a84a7d1a99a3523d576157912c57410": "the Rust CI job before its Cargo.toml manifest guard",
		"f66a2652591c6a3a1236a581b1d23dc92110d7d8b2f9164e4b8d0846c10357a4": "the first embedded Rust CI job, an unquoted 'on' key (#463)",
		"b850058606140c1e50ef0b27ba7f94c73d77ef3fb53b0c39b7f716fc6f5f517a": "the Rust CI job on every push to main and every draft pull request (#520 to #817)",
		"6f52fae7deba2f315727c6dab6051c8168b110852878394f903788079b15b32c": "the Rust CI job with build warnings as errors (#816)",
	}
	// priorFlutterCIDigests are the earlier texts of flutter/ci-flutter.yml.tmpl.
	priorFlutterCIDigests = map[string]string{
		"13ff5b2a881217b981aef5b8c2a4a732849611253f546afb1abf3c3fb9c661c8": "the Flutter CI job before its pubspec.yaml manifest guard",
		"047cebc48db2d96f0a7ce8feea7cedbe6b03c36a2fdb11962e22bd6fc3a351c6": "the first embedded Flutter CI job, an unquoted 'on' key (#463)",
		"d8992aca758301dc1f48783dfa1167bd462e8ce164367682b793df028d262e23": "the Flutter CI job on every push to main and every draft pull request (#520 to #817)",
	}
	// priorJVMCIDigests are the earlier texts of jvm/ci-jvm.yml.tmpl.
	priorJVMCIDigests = map[string]string{
		"64b84c2ecf75ac611c8802a615ba6026f622ac4f9341c2c8efa764f4d98d1bbd": "the JVM CI job before its Maven or Gradle manifest guard",
		"93b9e4332cb8a9f9b7cc83e0ac1d52ef62ef5d261a4ad0c022d54b8fc1b602bc": "the first embedded JVM CI job, an unquoted 'on' key (#463)",
		"391e27708b76d0883b9c96750bbc3d0441b6ca994c85fc5263faab2fabbc4e51": "the JVM CI job that ran a wrapper only with its executable bit set (#520)",
		"e5a22fdc467b7514aecc0e04f29bb496b62024e1a33f5026fb9e47763c9f1d47": "the JVM CI job on every push to main and every draft pull request (#527 to #817)",
	}
	// priorNodeCIDigests are the earlier texts of node/ci-node.yml.tmpl: the npm job before the
	// body branched on the package manager, then one text per manager and Yarn facts.
	priorNodeCIDigests = map[string]string{
		"41c5dbc778b5ad99a3125119b6ba2996d2d753860933a40754e2ac6cb7fbc037": "the npm CI job before its package.json manifest guard",
		"7c2b68d9bb04d729f546cf1ed3125661e8c2cf0118f5bcf293f2e4784e37ddb4": "the pnpm CI job before its package.json manifest guard",
		"8dffa892746924bdac73bdd8c20927ee74741d507c11e6bd2fef498cfda35197": "the Bun CI job before its package.json manifest guard",
		"f4a6d68eb25d8f4387dd95612f299d96784d64ff99bd65ea1f4a95509b10fb1b": "the Yarn CI job before its package.json manifest guard, variant 0",
		"14ce19d9006a68b6a650804a0160f3d45e8ff5d2bdcdb42cb5c9e5dfeb8e1858": "the Yarn CI job before its package.json manifest guard, variant 1",
		"22fc5e7995c867de4e36c41791275f6e0b5027b6a044985aa5c477c846e5770f": "the Yarn CI job before its package.json manifest guard, variant 2",
		"c6082778dc93690d1c1360733d5f0e89014b4ef4f3fb7e84e5d14aa27b81027c": "the Yarn CI job before its package.json manifest guard, variant 3",
		"cdb31d7e28b457f956c14bb2582660d620d5743d71d0f5caf8416d5eddcdfb3d": "the Yarn CI job before its package.json manifest guard, variant 4",
		"a6865a95a10009820242b2dfe418f78bc0463b9613e2f929a64b44956ad37cd3": "the Yarn CI job before its package.json manifest guard, variant 5",
		"8d9a4fd87fda6b31f4675608460808d296e915712c39b7f879d0e6746b6c0b6d": "the Yarn CI job before its package.json manifest guard, variant 6",
		"70183b8c67ae2a3ef69db41e6bf2aba6da1d83638465422fa5c5377c95cd4179": "the Yarn CI job before its package.json manifest guard, variant 7",
		"92a76d8edd42bc00028becad3cac4986002764249ae6a98ffc7c9127bcb670c0": "the first embedded npm CI job, an unquoted 'on' key (#463)",
		"b60fd879db705586a64b245467eb59ca90e7041b38ee59f64928e796e88283ac": "the npm CI job on every push to main and every draft pull request (#520 to #817)",
		"056d18d93aaf17b20eb7e6efbb7f3f04b49dba8498f9c3d96af40da117a66523": "the pnpm CI job on every push to main and every draft pull request (#527 to #817)",
		"aaa6727f153216283f70ded514fa8b4c83b1a9dbc8b08be04604f8000e308a55": "the Bun CI job on every push to main and every draft pull request (#527 to #817)",
		"e3b0381092e293cca7fad8418670b67730a7c537fbe92c31654c1b2889bea227": "the Yarn 1 CI job without lint or build on every draft pull request (#527 to #817)",
		"87b8a559432a33558bdf2da6f70d13c85ed5499c8b4fb1b7397a4d5268655448": "the Yarn 1 CI job with lint on every draft pull request (#527 to #817)",
		"cf28d208a46eb33bd682671de700a58e5547cde7e51633d7ce46ab3fd97cd31a": "the Yarn 1 CI job with build on every draft pull request (#527 to #817)",
		"0cf10fd4c93f30e366ae98173fc2961cc5b2a7faca9066b2ce131d8d278cf28b": "the Yarn 1 CI job with lint and build on every draft pull request (#527 to #817)",
		"e569c2b4a9b3624e732a0186f51ee99ddd6774e1b8fe8d410d6ba4676a2cdf66": "the Yarn 2+ CI job without lint or build on every draft pull request (#527 to #817)",
		"15e52245c97cbd52bcd03d37461724bf78ab772f258aec3d08a6c352936098b7": "the Yarn 2+ CI job with lint on every draft pull request (#527 to #817)",
		"0d524b23fa6d94d64898ce5194e391f75568476565bfa42693e0b6bb30e7e015": "the Yarn 2+ CI job with build on every draft pull request (#527 to #817)",
		"3bb57bf4ad8987cd8cf48c8002c37d97aadad4df9efa0ec0076497258eb98926": "the Yarn 2+ CI job with lint and build on every draft pull request (#527 to #817)",
	}
)

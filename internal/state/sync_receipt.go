package state

import "github.com/cordanaLLM/praetor/internal/util"

// gateReceiptExclude keeps the gate's Exit-0 receipt at the ledger root out of the state
// binding, the way the ledger directory itself is kept out.
//
// `praetorctl gate run` writes the receipt after the agent synced, so while it was bound every
// gate run staled the ledger the run had just found current, and the agent synced again
// (#136). The criterion for excluding rather than having the gate sync the ledger: the receipt
// carries nothing the ledger must bind. Its signed content is derived from the HEAD commit and
// the clean tree the binding already covers, it is never committed, and a gate that wrote the
// ledger would be a second writer of a file only state sync owns.
//
// util.GitLiteralExclude is the gate's own spelling (gating.treeProblem), so the two agree on
// which file is the receipt: exactly the root one, matched literally; the same name in a
// subdirectory still binds.
var gateReceiptExclude = util.GitLiteralExclude(util.GateReceiptFile)

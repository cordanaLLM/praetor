package util

// The gate's Exit-0 receipt is named here, below every package that has to agree on it. The
// gate (internal/gating) writes it and re-exports these values; the PR checklist validator, the
// state ledger binding and operational sync sit below or beside the gate in the import graph and
// cannot import it, yet each must name exactly the file and the command the gate uses (#136).
const (
	// GateReceiptFile is the Exit-0 receipt `praetorctl gate run` writes at the repository
	// root. .gitignore deliberately leaves it untracked evidence and nothing commits it: it
	// certifies the commit it was minted for, so committing it would move HEAD off that commit.
	GateReceiptFile = ".standards-receipt.json"
	// GateReceiptCommand is the canonical command string recorded in every gate receipt.
	GateReceiptCommand = PraetorCLI + " gate run"
	// GateRepoRunCommand is GateReceiptCommand aimed at the repository in the working directory,
	// the spelling generated personas, task bodies and recovery hints print.
	GateRepoRunCommand = GateReceiptCommand + " --path=."
)

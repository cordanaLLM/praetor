package state

// bugMetaName is the sidecar that holds each bug's Context, CreatedAt and
// ResolvedAt, keyed by bug ID. BUGS.md rows keep only a short v2 reference, so
// the file an agent reads carries no Base64 metadata. The envelope and record
// codec are shared with every ledger sidecar (ledger_meta.go).
const (
	bugMetaName        = "bugs.meta.json"
	bugMetaPendingName = "bugs.meta.json.pending"
)

var bugSidecar = sidecarSpec{name: bugMetaName, member: "bugs", checkID: checkBugID}

// decodeBugMeta reads bugs.meta.json strictly.
func decodeBugMeta(data []byte) (ledgerMetaIndex, error) {
	return decodeSidecar(bugSidecar, data)
}

// encodeBugMeta writes bugs.meta.json deterministically.
func encodeBugMeta(index ledgerMetaIndex) ([]byte, error) {
	return encodeSidecar(bugSidecar, index)
}

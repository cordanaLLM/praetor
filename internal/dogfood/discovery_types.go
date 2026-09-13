package dogfood

const (
	maxDiscoveryRules      = 128
	maxDiscoveryEvidence   = 32
	maxDiscoveryKeyBytes   = 128
	maxDiscoveryTitleBytes = 256
	maxDiscoveryMatchBytes = 256
	maxDiscoveryMatches    = 32
)

type DiscoveryPolicy struct {
	Version int             `json:"version"`
	Rules   []DiscoveryRule `json:"rules"`
}

type DiscoveryRule struct {
	Key      string   `json:"key"`
	Title    string   `json:"title"`
	Kind     string   `json:"kind"`
	Matches  []string `json:"matches"`
	Analyzer string   `json:"analyzer,omitempty"`
}

type CapabilityObservation struct {
	Key           string               `json:"key"`
	Title         string               `json:"title"`
	Kind          string               `json:"kind"`
	Status        string               `json:"status"`
	Basis         string               `json:"basis"`
	EvidenceCount int                  `json:"evidence_count"`
	Evidence      []CapabilityEvidence `json:"evidence,omitempty"`
}

type CapabilityEvidence struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type CapabilityDiscovery struct {
	Version       int                     `json:"version"`
	Status        string                  `json:"status"`
	TreeSHA256    string                  `json:"tree_sha256"`
	FilesObserved int                     `json:"files_observed"`
	FilesMatched  int                     `json:"files_matched"`
	Observations  []CapabilityObservation `json:"observations"`
	Errors        []string                `json:"errors,omitempty"`
}

const (
	discoveryScannerExtension   = "scanner_extension"
	discoveryNeedsMarker        = "needs_marker"
	discoveryVerificationMarker = "verification_marker"
)

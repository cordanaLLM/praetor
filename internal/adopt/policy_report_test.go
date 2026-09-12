package adopt

import (
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

const reportPrivateManifestValue = "report-private-manifest-content"

func adoptionPolicyReportFixture(t *testing.T) AdoptOptions {
	t.Helper()
	root := newTestRepo(t, "policy-report")
	mustWrite(t, filepath.Join(root, manifestFile), "version: 1\nrepository:\n  owner: fixture\n  name: policy-report\n  visibility: "+reportPrivateManifestValue+"\nprofiles: [framework]\nfacets: []\noverrides:\n  complexity:\n    max_func_loc: 5\n")
	return AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t), RecordBaseline: true}
}

func TestAdoptReportCarriesMatchingPlannedAndAppliedPolicy(t *testing.T) {
	opts := adoptionPolicyReportFixture(t)
	opts.DryRun = true
	planned, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.DryRun = false
	applied, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	for name, report := range map[string]*AdoptReport{"planned": planned, "applied": applied} {
		if report.EffectivePolicy == nil {
			t.Fatalf("%s report omitted its verified policy", name)
		}
		policy := report.EffectivePolicy
		if policy.SHA256 == "" || policy.Policy.Complexity.MaxFuncLOC != 5 {
			t.Fatalf("%s report does not describe the selected strict policy: %+v", name, policy)
		}
		if len(policy.Sources) == 0 || len(policy.Fields["max_func_loc"]) == 0 {
			t.Fatalf("%s report omitted policy provenance", name)
		}
	}
	if planned.EffectivePolicy.SHA256 != applied.EffectivePolicy.SHA256 {
		t.Fatal("dry run and apply reported different policy identities for the same pinned inputs")
	}
}

func TestAdoptReportOmitsExplicitlyUnavailablePolicy(t *testing.T) {
	report, err := Adopt(t.Context(), AdoptOptions{
		Path: newTestRepo(t, "unavailable-policy"), Profile: "framework", DryRun: true, RecordBaseline: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.EffectivePolicy != nil {
		t.Fatal("unavailable policy was reported as verified defaults")
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if _, exists := wire["effective_policy"]; exists {
		t.Fatal("JSON included an effective policy despite absent verified inputs")
	}
}

func TestAdoptReportJSONRetainsProvenanceWithoutRawInputs(t *testing.T) {
	report, err := Adopt(t.Context(), adoptionPolicyReportFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	policy := report.EffectivePolicy
	if policy == nil || policy.Manifest == nil || len(policy.CatalogArtifacts) == 0 {
		t.Fatal("fixture must retain real manifest and catalog snapshots in memory")
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{reportPrivateManifestValue, string(policy.CatalogArtifacts[0].Content)} {
		if strings.Contains(string(data), raw) || strings.Contains(string(data), base64.StdEncoding.EncodeToString([]byte(raw))) {
			t.Fatal("report JSON exposed raw policy input content")
		}
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	var encodedPolicy map[string]json.RawMessage
	if err := json.Unmarshal(document["effective_policy"], &encodedPolicy); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"manifest", "Manifest", "catalog_artifacts", "CatalogArtifacts"} {
		if _, exists := encodedPolicy[key]; exists {
			t.Fatalf("report JSON exposed private snapshot field %s", key)
		}
	}
	var decoded struct {
		EffectivePolicy *config.EffectivePolicy `json:"effective_policy"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.EffectivePolicy == nil || decoded.EffectivePolicy.SHA256 != policy.SHA256 || decoded.EffectivePolicy.Policy.Complexity.MaxFuncLOC != 5 {
		t.Fatal("report JSON lost the verified policy identity or applied limit")
	}
	if !reflect.DeepEqual(decoded.EffectivePolicy.Sources, policy.Sources) || !reflect.DeepEqual(decoded.EffectivePolicy.Fields, policy.Fields) {
		t.Fatal("report JSON lost typed source or field provenance")
	}
}

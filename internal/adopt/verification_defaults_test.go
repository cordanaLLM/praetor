package adopt

import "testing"

func TestDefaultVerificationLimitsMatchNormalizedNil(t *testing.T) {
	normalized, err := NormalizeVerificationLimits(nil)
	if err != nil {
		t.Fatal(err)
	}
	if normalized != DefaultVerificationLimits() {
		t.Fatalf("nil limits normalize to the exported defaults: %+v vs %+v", normalized, DefaultVerificationLimits())
	}
	defaults := DefaultVerificationLimits()
	if defaults.MaxEntries > VerificationEntriesCeiling || defaults.MaxFiles > VerificationFilesCeiling || defaults.MaxDepth > VerificationDepthCeiling {
		t.Fatalf("every ceiling must admit its default: %+v", defaults)
	}
}

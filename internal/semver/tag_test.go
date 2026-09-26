package semver

import "testing"

// Positive: full versions and moving major or major.minor tags parse, each with the
// precision it names.
func TestParseTag_Positive_FullAndMovingTags(t *testing.T) {
	cases := []struct {
		in        string
		want      Version
		precision int
	}{
		{"v4", Version{Major: 4}, PrecisionMajor},
		{"v4.1", Version{Major: 4, Minor: 1}, PrecisionMinor},
		{"v4.1.2", Version{Major: 4, Minor: 1, Patch: 2}, PrecisionPatch},
		{"0.24.2-rc.1", Version{Minor: 24, Patch: 2, Prerelease: "rc.1"}, PrecisionPatch},
	}
	for _, tc := range cases {
		got, precision, ok := ParseTag(tc.in)
		if !ok || got != tc.want || precision != tc.precision {
			t.Errorf("ParseTag(%q) = %+v, %d, %v; want %+v, %d", tc.in, got, precision, ok, tc.want, tc.precision)
		}
	}
}

// Negative: branches, commit digests, leading zeros and empty input are not tags.
func TestParseTag_Negative_RejectsNonTags(t *testing.T) {
	for _, in := range []string{"main", "latest", "8e5e7e5ab8b370d6c329ec480221332ada57f0ab", "v04", "v4.", "", "v4.1.2.3"} {
		if _, _, ok := ParseTag(in); ok {
			t.Errorf("ParseTag(%q) accepted", in)
		}
	}
}

// Boundary: Truncate keeps only the components a precision names and drops the
// prerelease below full precision.
func TestTruncate_Boundary_Precisions(t *testing.T) {
	v := Version{Major: 4, Minor: 1, Patch: 2, Prerelease: "rc.1", Build: "b"}
	if got := v.Truncate(PrecisionMajor); got != (Version{Major: 4}) {
		t.Errorf("major = %+v", got)
	}
	if got := v.Truncate(PrecisionMinor); got != (Version{Major: 4, Minor: 1}) {
		t.Errorf("minor = %+v", got)
	}
	if got := v.Truncate(PrecisionPatch); got != v {
		t.Errorf("patch = %+v", got)
	}
	if got := v.Truncate(0); got != (Version{Major: 4}) {
		t.Errorf("below major = %+v", got)
	}
}

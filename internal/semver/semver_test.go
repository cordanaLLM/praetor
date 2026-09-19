package semver

import "testing"

func TestParse_Positive_MajorMinorPatchAndVPrefix(t *testing.T) {
	v, ok := Parse("v1.2.3")
	if !ok {
		t.Fatal("expected v1.2.3 to parse")
	}
	if v.Major != 1 || v.Minor != 2 || v.Patch != 3 || v.Prerelease != "" || v.Build != "" {
		t.Errorf("unexpected parse: %+v", v)
	}

	v2, ok := Parse("0.2.0")
	if !ok || v2.Major != 0 || v2.Minor != 2 || v2.Patch != 0 {
		t.Errorf("unexpected parse for unprefixed version: %+v ok=%v", v2, ok)
	}
}

func TestParse_Negative_RejectsNonSemVer(t *testing.T) {
	for _, s := range []string{"", "not-a-version", "v1.2", "v1.2.3.4", "vfoo", "latest", "v01.2.3"} {
		if _, ok := Parse(s); ok {
			t.Errorf("expected %q to be rejected as non-SemVer", s)
		}
	}
}

func TestParse_Boundary_PrereleaseAndBuildMetadata(t *testing.T) {
	v, ok := Parse("v0.2.0-rc.1+build.5")
	if !ok {
		t.Fatal("expected v0.2.0-rc.1+build.5 to parse")
	}
	if v.Prerelease != "rc.1" || v.Build != "build.5" || !v.IsPrerelease() {
		t.Errorf("unexpected parse: %+v", v)
	}

	stable, ok := Parse("v1.0.0")
	if !ok || stable.IsPrerelease() {
		t.Errorf("v1.0.0 must not be classified as a prerelease: %+v ok=%v", stable, ok)
	}
}

func TestCompare_Positive_OrdersByMajorMinorPatch(t *testing.T) {
	lower, _ := Parse("v0.1.0")
	higher, _ := Parse("v0.2.0")
	if Compare(lower, higher) >= 0 {
		t.Errorf("expected v0.1.0 < v0.2.0")
	}
	if Compare(higher, lower) <= 0 {
		t.Errorf("expected v0.2.0 > v0.1.0")
	}
	if Compare(lower, lower) != 0 {
		t.Errorf("expected a version to equal itself")
	}
}

func TestCompare_Negative_PrereleaseNeverOutranksRelease(t *testing.T) {
	rc, _ := Parse("v0.2.0-rc.1")
	release, _ := Parse("v0.1.0")
	// v0.2.0-rc.1 carries a higher major.minor.patch than v0.1.0 but is a prerelease: the
	// flavors resolver excludes it before comparing, but Compare itself must still rank it
	// correctly against a same-version release for anyone comparing directly.
	sameVersionRelease, _ := Parse("v0.2.0")
	if Compare(rc, sameVersionRelease) >= 0 {
		t.Errorf("expected v0.2.0-rc.1 < v0.2.0")
	}
	if Compare(sameVersionRelease, rc) <= 0 {
		t.Errorf("expected v0.2.0 > v0.2.0-rc.1")
	}
	_ = release
}

func TestCompare_Boundary_BuildMetadataIgnoredAndPrereleaseOrdering(t *testing.T) {
	a, _ := Parse("v1.0.0+build.1")
	b, _ := Parse("v1.0.0+build.2")
	if Compare(a, b) != 0 {
		t.Errorf("build metadata must not affect precedence: Compare = %d", Compare(a, b))
	}

	// Canonical SemVer 2.0.0 spec (section 11) ordering example.
	order := []string{
		"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
		"1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0",
	}
	for i := 0; i < len(order)-1; i++ {
		lo, ok := Parse(order[i])
		if !ok {
			t.Fatalf("expected %q to parse", order[i])
		}
		hi, ok := Parse(order[i+1])
		if !ok {
			t.Fatalf("expected %q to parse", order[i+1])
		}
		if Compare(lo, hi) >= 0 {
			t.Errorf("expected %q < %q, got Compare = %d", order[i], order[i+1], Compare(lo, hi))
		}
	}
}

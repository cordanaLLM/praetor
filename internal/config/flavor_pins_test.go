package config

import (
	"strings"
	"testing"
)

func TestValidateFlavorPins_Positive(t *testing.T) {
	pins := []FlavorPin{{Name: "go-service", Path: "api"}, {Name: "frontend-svelte", Path: "web/"}, {Name: "go-library"}}
	if err := ValidateFlavorPins(pins); err != nil {
		t.Fatalf("valid pins refused: %v", err)
	}
	if got := (FlavorPin{Name: "x", Path: "web/"}).CleanPath(); got != "web" {
		t.Errorf("CleanPath = %q; want web", got)
	}
	if got := (FlavorPin{Name: "x"}).CleanPath(); got != "." {
		t.Errorf("CleanPath of an empty path = %q; want .", got)
	}
}

func TestValidateFlavorPins_Negative(t *testing.T) {
	cases := map[string][]FlavorPin{
		"missing name":      {{Path: "api"}},
		"escaping path":     {{Name: "go-service", Path: "../outside"}},
		"absolute path":     {{Name: "go-service", Path: "/etc"}},
		"drive path":        {{Name: "go-service", Path: "C:/x"}},
		"duplicate":         {{Name: "go-service", Path: "api"}, {Name: "go-service", Path: "api/"}},
		"duplicate at root": {{Name: "go-service"}, {Name: "go-service", Path: "."}},
	}
	for name, pins := range cases {
		if err := ValidateFlavorPins(pins); err == nil {
			t.Errorf("%s: accepted %+v", name, pins)
		}
	}
}

func TestValidateFlavorPins_Boundary(t *testing.T) {
	pins := make([]FlavorPin, 0, MaxFlavorPins+1)
	for i := 0; i < MaxFlavorPins; i++ {
		pins = append(pins, FlavorPin{Name: "go-service", Path: "d" + strings.Repeat("x", i)})
	}
	if err := ValidateFlavorPins(pins); err != nil {
		t.Fatalf("exactly %d pins refused: %v", MaxFlavorPins, err)
	}
	pins = append(pins, FlavorPin{Name: "go-service", Path: "extra"})
	if err := ValidateFlavorPins(pins); err == nil {
		t.Fatalf("%d pins accepted", len(pins))
	}
	long := FlavorPin{Name: "go-service", Path: strings.Repeat("a", MaxFlavorPinPathBytes+1)}
	if err := ValidateFlavorPins([]FlavorPin{long}); err == nil {
		t.Fatal("an over-long path was accepted")
	}
}

func TestParseManifest_FlavorPinsDecodeAndValidate(t *testing.T) {
	ok := "version: 1\nflavors:\n  - name: go-service\n    path: api\n"
	m, err := ParseManifest("m", []byte(ok))
	if err != nil || len(m.Flavors) != 1 || m.Flavors[0].Name != "go-service" {
		t.Fatalf("manifest = %+v, %v", m, err)
	}
	if _, err := ParseManifest("m", []byte("version: 1\nflavors:\n  - path: api\n")); err == nil {
		t.Fatal("a pin without a name passed manifest validation")
	}
}

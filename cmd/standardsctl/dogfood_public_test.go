package main

import (
	"flag"
	"testing"
)

func TestPublicDogfoodCLIRejectsInertFlags(t *testing.T) {
	for _, args := range [][]string{{"--artifacts=/tmp/unused"}, {"--source-root=."}, {"--attempts=3"}, {"--public-loop", "--targets=."}, {"--public-loop", "--report=unused"}, {"unexpected"}} {
		if err := runDogfood(args); err == nil {
			t.Fatalf("inert/incompatible flags accepted: %v", args)
		}
	}
}

func TestPublicDogfoodCLIApplyDefaultsAndBounds(t *testing.T) {
	fs := flag.NewFlagSet("public-fixture", flag.ContinueOnError)
	flags := addPublicDogfoodFlags(fs)
	if *flags.enabled || *flags.attempts != 2 {
		t.Fatal("unsafe/default public flags")
	}
	if err := fs.Parse([]string{"--public-loop", "--artifacts=retained", "--source-root=bundle", "--attempts=3"}); err != nil {
		t.Fatal(err)
	}
	if !*flags.enabled || *flags.artifacts != "retained" || *flags.source != "bundle" || *flags.attempts != 3 {
		t.Fatal("public controls not parsed")
	}
}

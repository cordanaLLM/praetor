package p

// HISS-11: this file imports a module whose checksum is not recorded in the module's
// go.sum. The Go toolchain refuses to build it -- "missing go.sum entry for module
// providing package ..." -- which is the lockfile-pinning half of HISS-11 being
// enforced by the toolchain rather than by any HISS rule. Verified by `go build`
// (run inside `make test` by `make verify-all`), not by internal/hiss.
import "gopkg.in/yaml.v3"

func Marshal(v map[string]int) ([]byte, error) { return yaml.Marshal(v) }

package p

import "log"

// log.Fatal calls os.Exit after logging, so it is the same abort, but the scanner resolves
// os.Exit only and log.Fatal goes unreported.
func Fail(err error) {
	log.Fatal(err)
}

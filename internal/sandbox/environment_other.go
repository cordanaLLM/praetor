//go:build !linux

package sandbox

import "errors"

// ManagerEnvironment refuses platforms without the Linux sandbox runtime.
func ManagerEnvironment() ([]string, error) {
	return nil, &UnavailableError{Facility: "Linux", Cause: errors.New("sandbox requires Linux user runtime directories")}
}

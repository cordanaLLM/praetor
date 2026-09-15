package p

import "os"

// Spawn bypasses the audited exec boundary and carries no context.
func Spawn(name string) error {
	_, err := os.StartProcess(name, nil, &os.ProcAttr{})
	return err
}

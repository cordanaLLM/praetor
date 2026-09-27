package p

import "os/exec"

// exec.Command takes no context, so nothing bounds how long git may run.
func Status(dir string) ([]byte, error) {
	cmd := exec.Command("git", "status")
	cmd.Dir = dir
	return cmd.Output()
}

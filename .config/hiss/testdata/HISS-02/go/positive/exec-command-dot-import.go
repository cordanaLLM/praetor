package p

import . "os/exec"

// The dot import binds Command into the file block; it is still exec.Command.
func Status() ([]byte, error) {
	return Command("git", "status").Output()
}

package p

import (
	"context"
	"os/exec"
)

type runner interface{ Command(name string) error }

type factory interface{ Background() context.Context }

// The parameters shadow the imported package names, so neither call reaches the package.
func Run(exec runner, context factory) error {
	if err := exec.Command("git"); err != nil {
		return err
	}
	return use(context.Background())
}

func use(any) error { return nil }

var _ = exec.ErrNotFound

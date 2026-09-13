//go:build !unix

package repairrun

import (
	"errors"
	"os"
)

func providerOpenHelper(_ string) (*os.File, error) {
	return nil, errors.New("repair credential helper requires Unix ownership and nofollow support")
}

func providerHelperInfo(_ os.FileInfo) bool { return false }

package config

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// ReadYAMLDocument reads the repository configuration file at path and decodes its single
// YAML document into out. It is the read every reader of .standards.yaml and .needs.yaml goes
// through, so they all see the same document under the same rules:
//
//   - only a regular file is opened (util.ReadConfinedLimited): a FIFO planted at the path is
//     refused instead of blocking the reader past every deadline (BUG-822);
//   - at most contextopt.MaxSourceBytes are read, the bound the manifest writers enforce;
//   - a second YAML document is refused (util.DecodeYAMLDocument). A reader that decoded only
//     the first document acted on part of a file another reader refuses (BUG-857).
//
// opts.KnownFields is for a reader whose target is the file's complete schema; a reader of one
// section leaves it unset so unrelated keys do not fail it. The context is checked before and
// after the bounded read.
func ReadYAMLDocument(ctx context.Context, path string, out any, opts util.YAMLDocumentOptions) error {
	if ctx == nil {
		return errors.New("reading a YAML document requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, contextopt.MaxDuration)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := readConfigFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := util.DecodeYAMLDocument(data, out, opts); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

// readConfigFile is the bounded regular-file read behind ReadYAMLDocument and LoadManifest.
func readConfigFile(path string) ([]byte, error) {
	return util.ReadConfinedLimited(filepath.Dir(path), filepath.Base(path), contextopt.MaxSourceBytes)
}

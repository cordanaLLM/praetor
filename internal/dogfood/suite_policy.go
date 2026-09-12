package dogfood

import (
	"errors"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/util"
)

func validateSuitePolicy(config *SuiteConfig, opts SuiteOptions) error {
	if len(config.PublicRepositories) > 0 {
		if opts.SourceRoot == "" {
			return errors.New("public suite cases require source_root")
		}
		if opts.Stage == "verify" && !opts.AllowRemote {
			return errors.New("public suite verification requires remote access opt-in")
		}
	}
	if opts.InputRoot == "" {
		return nil
	}
	root, err := filepath.Abs(opts.InputRoot)
	if err != nil {
		return err
	}
	for i := 0; i < len(config.Transcripts); i++ {
		relative, err := filepath.Rel(root, config.Transcripts[i].SourcePath)
		if err != nil {
			return err
		}
		if _, err := util.ConfinePath(root, relative); err != nil {
			return err
		}
	}
	return nil
}

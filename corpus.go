// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package praetor

import (
	"embed"
	"io/fs"
)

// EmbeddedCorpus embeds the HISS coverage catalog and fixture corpus into the binary.
//
//go:embed all:.config/hiss/coverage.yaml all:.config/hiss/testdata
var EmbeddedCorpus embed.FS

const (
	// CorpusSourceFile is the source file embedding the HISS fixture corpus.
	CorpusSourceFile = "corpus.go"
	// CorpusEmbedDirective is the exact go:embed directive declared by CorpusSourceFile.
	CorpusEmbedDirective = "//go:embed all:.config/hiss/coverage.yaml all:.config/hiss/testdata"
)

// CorpusFS returns the embedded filesystem holding the HISS catalog and fixture corpus.
func CorpusFS() fs.FS {
	return EmbeddedCorpus
}

// AssetPaths returns the repository-relative paths of all embedded corpus fixtures and the catalog.
func AssetPaths() []string {
	var paths []string
	walkErr := fs.WalkDir(EmbeddedCorpus, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	if walkErr != nil {
		return nil
	}
	return paths
}

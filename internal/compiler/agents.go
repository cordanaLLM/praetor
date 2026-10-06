package compiler

import (
	"context"
	"errors"
	"path"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
)

const (
	// MaxAgentFiles bounds the canonical personas one persona directory holds and the vendor
	// files one compile writes (HISS-02).
	MaxAgentFiles       = 50
	DefaultAgentTimeout = 10 * time.Second
)

// SelectPersonaDirs returns the persona directories agent_clients in the manifest at root keeps
// and the ones it leaves out, in registry order (agentcontext.PersonaDirs). A root without a
// manifest, or a manifest without the key, keeps every directory. compile-context, its
// --verify, the audit and adoption all resolve the selection here, so they agree on which
// persona copies exist.
func SelectPersonaDirs(ctx context.Context, root string) (selected, excluded []string, err error) {
	clients, err := declaredAgentClients(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	return agentcontext.PersonaDirs(clients)
}

// SelectSkillDirs returns the skill directories agent_clients in the manifest at root keeps
// besides the canonical .agents/skills, and the ones it leaves out (agentcontext.SkillDirs),
// under the same selection rules as SelectPersonaDirs.
func SelectSkillDirs(ctx context.Context, root string) (selected, excluded []string, err error) {
	clients, err := declaredAgentClients(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	return agentcontext.SkillDirs(clients)
}

func projectionPath(root, relative string) (string, error) {
	// Vendor targets are declared as slash paths (".cursor/rules/hiss-invariants.mdc"),
	// so cleanliness is a slash-path property. filepath.Clean returns backslashes on
	// Windows and would never equal the declared value, which rejected every
	// projection and left compile-context unable to write on that platform.
	if !filepath.IsLocal(relative) || path.Clean(relative) != relative || relative == "." {
		return "", errors.New("compiled output requires a clean relative file path")
	}
	return filepath.Join(root, relative), nil
}

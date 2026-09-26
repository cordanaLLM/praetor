package config

import "context"

// DeclaredTooling is what a repository's manifest declares about the editors and agent
// clients it uses. A nil list means the key is absent and every known id applies; a non-nil
// list, empty included, is the declared selection. Validation belongs to the packages that
// own each id set (internal/editor and internal/agentcontext), not to this parse.
//
// It is the repository's declaration. ClientSettings is a different thing: which clients one
// workstation governs.
type DeclaredTooling struct {
	Editors      []string
	AgentClients []string
}

// LoadDeclaredTooling reads editors and agent_clients from the manifest at root through the
// same bounded snapshot and strict parse the text register uses (LoadRegisterAuthority), so
// the two never read different bytes. A root without a manifest declares nothing.
func LoadDeclaredTooling(ctx context.Context, root string) (DeclaredTooling, error) {
	authority, err := LoadRegisterAuthority(ctx, root)
	if err != nil {
		return DeclaredTooling{}, err
	}
	manifest, err := authority.Manifest()
	if err != nil || manifest == nil {
		return DeclaredTooling{}, err
	}
	return DeclaredTooling{Editors: manifest.Editors, AgentClients: manifest.AgentClients}, nil
}

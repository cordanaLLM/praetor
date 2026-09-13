package operationalsync

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

func remoteIdentity(raw string) (string, error) {
	if strings.HasPrefix(raw, "git@github.com:") {
		raw = "https://github.com/" + strings.TrimPrefix(raw, "git@github.com:")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("remote must be a credential-free GitHub HTTPS or git@github.com URL")
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), ".git"), "/")
	if len(parts) != 2 || !identityPart.MatchString(parts[0]) || !identityPart.MatchString(parts[1]) {
		return "", errors.New("invalid GitHub owner/repository remote")
	}
	return strings.Join(parts, "/"), nil
}

func (op *operation) checkManifestIdentity(ctx context.Context, source identity) error {
	if !identityPart.MatchString(op.owner.Owner) || !identityPart.MatchString(op.owner.Name) {
		return errors.New("invalid owner manifest identity")
	}
	if op.owner.Name != source.Name || op.owner.Owner == source.Owner {
		return errors.New("operational repository must have a distinct owner and the same repository name")
	}
	if op.owner.Visibility != "private" || source.Visibility != "public" {
		return errors.New("this stage supports private owner repositories of public sources")
	}
	return nil
}

func (op *operation) checkIdentity(ctx context.Context, source identity) error {
	if err := op.checkManifestIdentity(ctx, source); err != nil {
		return err
	}
	var err error
	op.origin, err = op.remote(ctx, op.opts.OwnerPath, "origin")
	if err != nil {
		return err
	}
	op.upstream, err = op.remote(ctx, op.opts.OwnerPath, "upstream")
	if err != nil {
		return err
	}
	public, err := op.remote(ctx, op.opts.SourcePath, "origin")
	if err != nil {
		return err
	}
	if op.origin != op.owner.Owner+"/"+op.owner.Name || op.upstream != source.Owner+"/"+source.Name || public != op.upstream {
		return errors.New("configured origins/upstream disagree with reviewed manifest identities")
	}
	return nil
}

func (op *operation) remote(ctx context.Context, dir, name string) (string, error) {
	raw, err := op.git.text(ctx, dir, "config", "--includes", "--get", "remote."+name+".url")
	if err != nil {
		return "", fmt.Errorf("missing %s remote: %w", name, err)
	}
	return remoteIdentity(raw)
}

package history

import (
	"context"
	"errors"

	"deepzora/internal/agent"
	"deepzora/internal/session"
)

func childHistorySource(parent Source) (Source, error) {
	query, err := parent.Query.Subagents()
	if err != nil {
		return Source{}, err
	}
	return Source{Query: query, Scope: parent.Scope, Membership: func(ctx context.Context) (map[string]bool, error) {
		var parents map[string]bool
		if parent.Membership != nil {
			var err error
			parents, err = parent.Membership(ctx)
			if err != nil {
				return nil, err
			}
		}
		artifacts, err := agent.ListSubagentArtifacts(query.StorageRoot())
		if err != nil {
			return nil, err
		}
		allowed := map[string]bool{}
		verified := map[string]bool{}
		for _, artifact := range artifacts {
			id := artifact.Meta.ParentSession
			if parent.Membership != nil && !parents[id] {
				continue
			}
			valid, known := verified[id]
			if !known {
				ref, err := parent.Query.ResolveSessionID(ctx, id)
				if err == nil {
					_, err = parent.Query.Stat(ctx, ref)
				}
				if err != nil && !errors.Is(err, session.ErrSessionNotFound) {
					return nil, err
				}
				valid = err == nil
				verified[id] = valid
			}
			if valid {
				allowed[artifact.Ref] = true
			}
		}
		return allowed, nil
	}}, nil
}

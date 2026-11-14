package store

import (
	"context"
	"strings"
	"sync"
	"unsafe"

	"flow/api/internal/domain"
)

const scaleP21SW19GitLabConfigsMaxWork = 64 << 20

// GitLabConnectionConfigsRead owns only the installed workspace URLKey and
// connection ID/provider/config. These partial connections are not wire
// records. Keep the handle through config use, side effects and response.
type GitLabConnectionConfigsRead struct {
	WorkspaceURLKey string
	Connections     []domain.IntegrationConnection
	release         func()
	once            sync.Once
}

func (r *GitLabConnectionConfigsRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.WorkspaceURLKey = ""
			r.Connections = nil
			if r.release != nil {
				r.release()
			}
		})
	}
}

// BeginGitLabConnectionConfigsRead selects the installed source, never
// canonical records. It selects all exact GitLab configs in source order for
// the trusted rotation sweep. It does not authorize a request or make an
// unloaded connection family available.
func (s *SQLiteStore) BeginGitLabConnectionConfigsRead(ctx context.Context, workspace string) (*GitLabConnectionConfigsRead, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		s.mu.RLock()
		selected := workspace
		if selected == "" {
			selected = s.lastWorkspaceKey
		}
		source, found := s.workspaces[selected]
		cost, err := scaleP21SW19GitLabConfigsCost(ctx, source)
		s.mu.RUnlock()
		if !found || err != nil {
			return nil, found, err
		}
		_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, cost)
		if err != nil {
			return nil, true, err
		}
		s.mu.RLock()
		if workspace == "" {
			selected = s.lastWorkspaceKey
		}
		source, found = s.workspaces[selected]
		if !found {
			s.mu.RUnlock()
			release()
			return nil, false, nil
		}
		current, err := scaleP21SW19GitLabConfigsCost(ctx, source)
		if err != nil || current > cost {
			s.mu.RUnlock()
			release()
			if err != nil {
				return nil, true, err
			}
			continue
		}
		read := &GitLabConnectionConfigsRead{
			WorkspaceURLKey: strings.Clone(source.Workspace.URLKey),
			release:         release,
		}
		for _, item := range source.IntegrationConnections {
			if item.Provider != "gitlab" {
				continue
			}
			connection := domain.IntegrationConnection{ID: strings.Clone(item.ID), Provider: "gitlab"}
			if item.Config != nil {
				connection.Config = make(map[string]string, len(item.Config))
				for key, value := range item.Config {
					connection.Config[strings.Clone(key)] = strings.Clone(value)
				}
			}
			read.Connections = append(read.Connections, connection)
		}
		s.mu.RUnlock()
		if err := ctx.Err(); err != nil {
			read.Close()
			return nil, true, err
		}
		return read, true, nil
	}
}

func scaleP21SW19GitLabConfigsCost(ctx context.Context, source domain.Bootstrap) (int, error) {
	cost := int64(128<<10) + 8*int64(len(source.Workspace.URLKey))
	for _, item := range source.IntegrationConnections {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if item.Provider != "gitlab" {
			continue
		}
		// Include slice growth, full typed slots, map buckets/sorted keys,
		// detached strings and downstream encoding/argument copies.
		cost += 2*int64(unsafe.Sizeof(domain.IntegrationConnection{})) + 1024 + 33*int64(len(item.ID))
		cost += 256 * int64(len(item.Config))
		for key, value := range item.Config {
			cost += 33 * int64(len(key)+len(value))
			if cost > scaleP21SW19GitLabConfigsMaxWork {
				return 0, ErrMetadataWorkBudget
			}
		}
		if cost > scaleP21SW19GitLabConfigsMaxWork {
			return 0, ErrMetadataWorkBudget
		}
	}
	if cost > scaleP21SW19GitLabConfigsMaxWork {
		return 0, ErrMetadataWorkBudget
	}
	return int(cost), nil
}

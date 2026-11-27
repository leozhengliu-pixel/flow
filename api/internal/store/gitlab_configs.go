package store

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"unsafe"

	"flow/api/internal/domain"
)

const gitLabConfigsMaxWork = 64 << 20

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
	if read, found, handled, err := s.installedGitLabConnectionConfigs(ctx, workspace); handled {
		return read, found, err
	}
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
		cost, err := gitLabConfigsCost(ctx, source)
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
		current, err := gitLabConfigsCost(ctx, source)
		if err != nil || current > cost {
			s.mu.RUnlock()
			release()
			if err != nil {
				return nil, true, err
			}
			continue
		}
		read := gitLabConfigsProject(source, release)
		s.mu.RUnlock()
		if err := ctx.Err(); err != nil {
			read.Close()
			return nil, true, err
		}
		return read, true, nil
	}
}

// gitLabConfigsProject detaches the workspace URLKey and every
// GitLab connection's ID/provider/config of source, in source order.
func gitLabConfigsProject(source domain.Bootstrap, release func()) *GitLabConnectionConfigsRead {
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
	return read
}

// installedGitLabConnectionConfigs is the read on a bounded store. Its image
// holds no integration connections (a record field), so the rotation sweep
// found none and never renewed a token. It decodes the workspace and
// integrationConnections fields of the workspace's installed logical source
// (the legacy image's values) and projects them as the resident read does.
// handled is false flag off and on a store without an installed source of
// the workspace (a test view), which read the image as before.
func (s *SQLiteStore) installedGitLabConnectionConfigs(ctx context.Context, workspace string) (_ *GitLabConnectionConfigsRead, found, handled bool, err error) {
	r := s.metadataResidency
	if r == nil || s.coldParent != nil {
		return nil, false, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, false, true, err
	}
	s.mu.RLock()
	selected := workspace
	if selected == "" {
		selected = s.lastWorkspaceKey
	}
	_, exists := s.workspaces[selected]
	r.mu.Lock()
	logical := r.logical[selected] != nil
	r.mu.Unlock()
	s.mu.RUnlock()
	if !exists {
		return nil, false, true, nil
	}
	if !logical {
		// No installed logical image of this workspace (a hand-built or
		// test view store): the resident read as before, as coldAccessorView.
		return nil, false, false, nil
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	installed, found, err := s.BeginInstalledMetadataRead(ctx, selected)
	if err != nil || !found {
		return nil, found, true, err
	}
	defer installed.Close()
	owner := &ResourceRead{store: s}
	defer owner.Close()
	var source domain.Bootstrap
	target := reflect.ValueOf(&source).Elem()
	err = installed.source.VisitFields(ctx, func(name string, field metadataLogicalNode) error {
		if name != "workspace" && name != "integrationConnections" {
			return nil
		}
		// The wire value: a timestamp whose Location the source keeps only
		// as its wire output still decodes (only IDs and configs are kept).
		value, err := owner.node(ctx, field, true)
		if err != nil {
			return err
		}
		index, _, _ := metadataLogicalField(name)
		target.Field(index).Set(value)
		return nil
	})
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, true, true, err
	}
	cost, err := gitLabConfigsCost(ctx, source)
	if err != nil {
		return nil, true, true, err
	}
	_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, cost)
	if err != nil {
		return nil, true, true, err
	}
	read := gitLabConfigsProject(source, release)
	if err := ctx.Err(); err != nil {
		read.Close()
		return nil, true, true, err
	}
	return read, true, true, nil
}

func gitLabConfigsCost(ctx context.Context, source domain.Bootstrap) (int, error) {
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
			if cost > gitLabConfigsMaxWork {
				return 0, ErrMetadataWorkBudget
			}
		}
		if cost > gitLabConfigsMaxWork {
			return 0, ErrMetadataWorkBudget
		}
	}
	if cost > gitLabConfigsMaxWork {
		return 0, ErrMetadataWorkBudget
	}
	return int(cost), nil
}

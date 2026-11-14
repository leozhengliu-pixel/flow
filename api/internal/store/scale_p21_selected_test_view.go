package store

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// BoundedMetadataViewForWorkspacesForTesting loads only explicitly selected
// owner-installed workspaces. An empty key selects the owner's default; an
// absent exact key returns sql.ErrNoRows. With no keys, it retains the existing
// all-workspace test view behavior. This cannot activate production coverage.
// Keep the owning store alive and do not Close the returned shared-SQL view.
func (s *SQLiteStore) BoundedMetadataViewForWorkspacesForTesting(ctx context.Context, workspaces ...string) (*SQLiteStore, error) {
	if !testing.Testing() {
		return nil, ErrBoundedMetadataCoverage
	}
	if len(workspaces) == 0 {
		return s.BoundedMetadataViewForTesting(ctx)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	view := &SQLiteStore{db: s.db, dialect: s.dialect, maxStateBytes: s.maxStateBytes, workspaces: map[string]domain.Bootstrap{}, metadataResidency: newMetadataResidency()}
	view.metadataResidency.requireCoverage = false
	ctx, release, err := view.admitMetadataWork(ctx, metadataPolicyBytes+2<<20)
	if err != nil {
		return nil, err
	}
	defer release()
	selected := map[string]bool{}
	s.mu.RLock()
	last := s.lastWorkspaceKey
	for _, key := range workspaces {
		if key == "" {
			key = last
		}
		if _, found := s.workspaces[key]; !found || s.dialect == "postgres" && strings.ContainsRune(key, '\x00') {
			s.mu.RUnlock()
			return nil, sql.ErrNoRows
		}
		selected[key] = true
	}
	viewer := s.viewer
	viewerCost, err := metadataTestViewViewerCost(viewer, view.metadataResidency.policyLimit)
	if err != nil {
		s.mu.RUnlock()
		return nil, err
	}
	viewer.AppScopes, viewer.AppTeamIDs = slices.Clone(viewer.AppScopes), slices.Clone(viewer.AppTeamIDs)
	if viewer.OutOfOfficeUntil != nil {
		value := *viewer.OutOfOfficeUntil
		viewer.OutOfOfficeUntil = &value
	}
	if viewer.Via != nil {
		value := *viewer.Via
		viewer.Via = &value
	}
	s.mu.RUnlock()
	view.viewer = viewer
	if err := view.metadataResidency.replacePolicyCosts(map[string]int{"": viewerCost}); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(selected))
	for key := range selected {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if err := view.reloadResidentWorkspace(ctx, key); err != nil {
			return nil, fmt.Errorf("recover selected test workspace %q: %w", key, err)
		}
	}
	if !selected[last] {
		last = firstWorkspaceKey(view.workspaces)
	}
	if view.viewer.ID == "" {
		view.viewer = view.workspaces[last].Viewer
		if view.viewer.ID == "" {
			view.viewer = bootstrapViewer()
		}
	}
	view.lastWorkspaceKey = last
	return view, nil
}

func metadataTestViewViewerCost(viewer domain.User, limit int) (int, error) {
	cost := 0
	add := func(bytes int) error {
		if bytes < 0 || bytes > limit-cost {
			return ErrMetadataWorkBudget
		}
		cost += bytes
		return nil
	}
	if err := add(4096); err != nil {
		return 0, err
	}
	var visit func(reflect.Value) error
	visit = func(value reflect.Value) error {
		switch value.Kind() {
		case reflect.String:
			if value.Len() > (limit-cost)/2 {
				return ErrMetadataWorkBudget
			}
			return add(value.Len() * 2)
		case reflect.Bool:
			return nil
		case reflect.Slice:
			if value.Type() != reflect.TypeFor[[]string]() {
				return ErrBoundedMetadataCoverage
			}
			if value.Len() > (limit-cost)/64 {
				return ErrMetadataWorkBudget
			}
			if err := add(value.Len() * 64); err != nil {
				return err
			}
			for index := range value.Len() {
				if err := visit(value.Index(index)); err != nil {
					return err
				}
			}
			return nil
		case reflect.Pointer:
			switch value.Type() {
			case reflect.TypeFor[*time.Time]():
				return add(256)
			case reflect.TypeFor[*domain.ActorBot]():
				if value.IsNil() {
					return nil
				}
				if err := add(256); err != nil {
					return err
				}
				return visit(value.Elem())
			default:
				return ErrBoundedMetadataCoverage
			}
		case reflect.Struct:
			if value.Type() != reflect.TypeFor[domain.User]() && value.Type() != reflect.TypeFor[domain.ActorBot]() {
				return ErrBoundedMetadataCoverage
			}
			for index := range value.NumField() {
				if err := visit(value.Field(index)); err != nil {
					return err
				}
			}
			return nil
		default:
			return ErrBoundedMetadataCoverage
		}
	}
	if err := visit(reflect.ValueOf(viewer)); err != nil {
		return 0, err
	}
	return cost, nil
}

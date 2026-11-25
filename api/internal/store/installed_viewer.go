package store

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"time"
	"unsafe"

	"flow/api/internal/domain"
)

const scaleP21SW20ViewerMaxWork = 64 << 20

// InstalledViewerRead owns a snapshot of the installed core Viewer. Retain the
// handle through every use of Viewer; it never authorizes or reloads SQL state.
// OutOfOfficeUntil preserves JSON and wall-clock instant comparisons, nil and
// IsZero. Its native location and monotonic readings are not part of this API.
type InstalledViewerRead struct {
	viewer  domain.User
	release func()
	once    sync.Once
}

func (r *InstalledViewerRead) Viewer() domain.User { return r.viewer }
func (r *InstalledViewerRead) Close() {
	if r != nil {
		r.once.Do(r.release)
	}
}

func (s *SQLiteStore) BeginInstalledViewerRead(ctx context.Context, workspace string) (*InstalledViewerRead, bool, error) {
	for {
		s.mu.RLock()
		selected := workspace
		if selected == "" {
			selected = s.lastWorkspaceKey
		}
		_, found := s.workspaces[selected]
		cost, err := scaleP21SW20ViewerCost(s.workspaces[selected].Viewer)
		s.mu.RUnlock()
		if !found || err != nil {
			return nil, found, err
		}
		// Do not queue context-insensitive legacy reads after their request
		// has left. This owned allowance is independent of a caller's lease.
		admitted, release, err := s.tryAdmitAdditionalMetadataWork(ctx, cost)
		if err != nil {
			return nil, true, err
		}
		s.mu.RLock()
		selected = workspace
		if selected == "" {
			selected = s.lastWorkspaceKey
		}
		source, found := s.workspaces[selected]
		current, err := scaleP21SW20ViewerCost(source.Viewer)
		if !found || err != nil || current > cost {
			s.mu.RUnlock()
			release()
			if !found || err != nil {
				return nil, found, err
			}
			continue
		}
		viewer := scaleP21SW20CloneViewer(source.Viewer)
		s.mu.RUnlock()
		if err := admitted.Err(); err != nil {
			release()
			return nil, true, err
		}
		return &InstalledViewerRead{viewer: viewer, release: release}, true, nil
	}
}

func scaleP21SW20ViewerCost(viewer domain.User) (int, error) {
	cost := int64(128<<10) + int64(unsafe.Sizeof(viewer)) + int64(unsafe.Sizeof(InstalledViewerRead{}))
	value := reflect.ValueOf(viewer)
	for index := range value.NumField() {
		field := value.Type().Field(index)
		if field.Tag.Get("json") == "-" {
			continue
		}
		item := value.Field(index)
		switch {
		case item.Kind() == reflect.String:
			cost += 33 * int64(item.Len())
		case item.Kind() == reflect.Bool:
		case item.Type() == reflect.TypeFor[[]string]():
			cost += 64 * int64(item.Len())
			for at := range item.Len() {
				cost += 33 * int64(item.Index(at).Len())
			}
		case item.Type() == reflect.TypeFor[*time.Time]():
			if !item.IsNil() {
				cost += 4096
			}
		default:
			return 0, ErrBoundedMetadataCoverage
		}
		if cost > scaleP21SW20ViewerMaxWork {
			return 0, ErrMetadataWorkBudget
		}
	}
	return int(cost), nil
}

func scaleP21SW20CloneViewer(viewer domain.User) domain.User {
	value := reflect.ValueOf(&viewer).Elem()
	for index := range value.NumField() {
		field := value.Type().Field(index)
		item := value.Field(index)
		switch {
		case field.Tag.Get("json") == "-":
			item.SetZero()
		case item.Kind() == reflect.String:
			item.SetString(strings.Clone(item.String()))
		case item.Type() == reflect.TypeFor[[]string]() && !item.IsNil():
			cloned := make([]string, item.Len())
			for at := range cloned {
				cloned[at] = strings.Clone(item.Index(at).String())
			}
			item.Set(reflect.ValueOf(cloned))
		case item.Type() == reflect.TypeFor[*time.Time]() && !item.IsNil():
			stamp := *item.Interface().(*time.Time)
			if stamp.Location() != time.UTC {
				_, offset := stamp.Zone()
				stamp = stamp.In(time.FixedZone("", offset))
			}
			item.Set(reflect.ValueOf(&stamp))
		}
	}
	return viewer
}

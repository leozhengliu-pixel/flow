package store

import (
	"reflect"

	"flow/api/internal/domain"
)

const HydratedSnapshotReservationMaxBytes int64 = hydrationSnapshotBufferLimit

// HydratedSnapshotReservation accounts for four owned container copies,
// including nested maps and slices. Strings remain immutable and shared.
// Stop walking once the download needs the entire compatibility allowance.
func HydratedSnapshotReservation(data domain.Bootstrap) int64 {
	left := HydratedSnapshotReservationMaxBytes / 4
	var charge func(reflect.Value)
	consume := func(count, size int64) {
		if count > 0 && size > left/count {
			left = 0
		} else {
			left = max(0, left-count*size)
		}
	}
	charge = func(value reflect.Value) {
		if left <= 0 {
			return
		}
		switch value.Kind() {
		case reflect.Pointer, reflect.Interface:
			if !value.IsNil() {
				consume(1, int64(value.Elem().Type().Size())+64)
				charge(value.Elem())
			}
		case reflect.Slice:
			consume(int64(value.Len()), int64(value.Type().Elem().Size())+16)
			if !typeCloneFlat(value.Type().Elem()) {
				for i := 0; i < value.Len() && left > 0; i++ {
					charge(value.Index(i))
				}
			}
		case reflect.Map:
			consume(int64(value.Len()), int64(value.Type().Key().Size()+value.Type().Elem().Size())+128)
			if !typeCloneFlat(value.Type().Elem()) {
				iter := value.MapRange()
				for left > 0 && iter.Next() {
					charge(iter.Value())
				}
			}
		case reflect.Struct:
			for _, index := range structPlanFor(value.Type()).deep {
				charge(value.Field(index))
			}
		case reflect.Array:
			if !typeCloneFlat(value.Type().Elem()) {
				for i := 0; i < value.Len() && left > 0; i++ {
					charge(value.Index(i))
				}
			}
		}
	}
	value := reflect.ValueOf(data)
	consume(1, int64(value.Type().Size()))
	charge(value)
	return max(1<<20, HydratedSnapshotReservationMaxBytes-4*left)
}

func (s *SQLiteStore) WorkspaceReadSnapshotReservation(workspace string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if workspace == "" {
		workspace = s.lastWorkspaceKey
	}
	data, ok := s.workspaces[workspace]
	if !ok || s.metadataResidency != nil {
		return HydratedSnapshotReservationMaxBytes
	}
	return HydratedSnapshotReservation(data)
}

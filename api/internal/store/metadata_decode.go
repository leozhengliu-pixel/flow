package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"

	"flow/api/internal/domain"
)

// Decode split metadata records directly into their typed destinations. Do not
// rebuild a second giant JSON blob just to parse it again during startup/reload.
func (s *SQLiteStore) decodeWorkspaceMetadata(ctx context.Context, workspace string, raw []byte, reader metadataReader) (domain.Bootstrap, error) {
	var data domain.Bootstrap
	if err := json.Unmarshal(raw, &data); err != nil {
		return data, err
	}
	var header struct {
		Collections map[string]string `json:"_flowCollections"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return data, err
	}
	if len(header.Collections) == 0 {
		return data, nil
	}
	root := reflect.ValueOf(&data).Elem()
	fields := map[string]reflect.Value{}
	rawObjects := map[string]map[string]json.RawMessage{}
	for i := 0; i < root.NumField(); i++ {
		tag := strings.Split(root.Type().Field(i).Tag.Get("json"), ",")[0]
		if _, ok := header.Collections[tag]; ok {
			fields[tag] = root.Field(i)
		}
	}
	recordRows, err := s.loadMetadataRecordRows(ctx, workspace, reader, header.Collections)
	if err != nil {
		return data, err
	}
	// Array collections hold most records (hundreds of thousands in large
	// tenants); decode them in parallel into preallocated slices. Rows arrive
	// ordered by field and collection order.
	arrays := map[string][]json.RawMessage{}
	for _, record := range recordRows {
		if target, ok := fields[record.Field]; ok && header.Collections[record.Field] == "array" {
			if target.Kind() != reflect.Slice {
				return data, fmt.Errorf("invalid array field %s", record.Field)
			}
			arrays[record.Field] = append(arrays[record.Field], record.Data)
		}
	}
	if err := decodeArrayFields(fields, arrays); err != nil {
		return data, err
	}
	for _, record := range recordRows {
		field, key, value := record.Field, record.Key, record.Data
		target, ok := fields[field]
		if !ok {
			continue
		}
		switch header.Collections[field] {
		case "array":
			// Decoded above.
		case "map":
			if target.Type() == reflect.TypeFor[json.RawMessage]() {
				object := rawObjects[field]
				if object == nil {
					object = map[string]json.RawMessage{}
					if len(target.Bytes()) > 0 {
						if err := json.Unmarshal(target.Bytes(), &object); err != nil {
							return data, err
						}
					}
					if object == nil {
						object = map[string]json.RawMessage{}
					}
					rawObjects[field] = object
				}
				object[key] = value
				continue
			}
			if target.Kind() == reflect.Struct {
				encoded, err := json.Marshal(map[string]json.RawMessage{key: value})
				if err != nil {
					return data, err
				}
				if err := json.Unmarshal(encoded, target.Addr().Interface()); err != nil {
					return data, err
				}
				continue
			}
			if target.Kind() == reflect.Ptr && target.Type().Elem().Kind() == reflect.Struct {
				if target.IsNil() {
					target.Set(reflect.New(target.Type().Elem()))
				}
				// Map-shaped collections store one JSON value per struct field
				// (same as WorkspaceSettings). Merge each record into the
				// pointed-to struct instead of treating the value as a full
				// WorkspaceInviteLink document.
				encoded, err := json.Marshal(map[string]json.RawMessage{key: value})
				if err != nil {
					return data, err
				}
				if err := json.Unmarshal(encoded, target.Interface()); err != nil {
					return data, err
				}
				continue
			}
			if target.Kind() != reflect.Map || target.Type().Key().Kind() != reflect.String {
				return data, fmt.Errorf("invalid map field %s", field)
			}
			if target.IsNil() {
				target.Set(reflect.MakeMap(target.Type()))
			}
			item := reflect.New(target.Type().Elem())
			if err := json.Unmarshal(value, item.Interface()); err != nil {
				return data, err
			}
			target.SetMapIndex(reflect.ValueOf(key).Convert(target.Type().Key()), item.Elem())
		case "updates":
			if target.Kind() != reflect.Map || target.Type().Elem().Kind() != reflect.Slice {
				return data, fmt.Errorf("invalid update field %s", field)
			}
			if target.IsNil() {
				target.Set(reflect.MakeMap(target.Type()))
			}
			var update metadataUpdate
			if err := json.Unmarshal(value, &update); err != nil {
				return data, err
			}
			key := reflect.ValueOf(update.Parent).Convert(target.Type().Key())
			items := target.MapIndex(key)
			if len(update.Item) > 0 {
				if !items.IsValid() || items.IsNil() {
					items = reflect.MakeSlice(target.Type().Elem(), 0, 1)
				}
				item := reflect.New(items.Type().Elem())
				if err := json.Unmarshal(update.Item, item.Interface()); err != nil {
					return data, err
				}
				target.SetMapIndex(key, reflect.Append(items, item.Elem()))
			} else if !items.IsValid() {
				if update.Null {
					target.SetMapIndex(key, reflect.Zero(target.Type().Elem()))
				} else {
					target.SetMapIndex(key, reflect.MakeSlice(target.Type().Elem(), 0, 0))
				}
			}
		default:
			return data, fmt.Errorf("unknown collection shape %s", header.Collections[field])
		}
	}
	for field, object := range rawObjects {
		encoded, err := json.Marshal(object)
		if err != nil {
			return data, err
		}
		fields[field].SetBytes(encoded)
	}
	return data, nil
}

func (s *SQLiteStore) loadMetadataRecordRows(ctx context.Context, workspace string, reader metadataReader, collections map[string]string) ([]metadataCacheRow, error) {
	if cached, ok := s.metadataRecordsFromCache(ctx, workspace, collections); ok {
		sort.Slice(cached, func(i, j int) bool {
			if cached[i].Field != cached[j].Field {
				return cached[i].Field < cached[j].Field
			}
			if cached[i].Order != cached[j].Order {
				return cached[i].Order < cached[j].Order
			}
			return cached[i].Key < cached[j].Key
		})
		return cached, nil
	}
	rows, err := reader.QueryContext(ctx, `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,collection_order,record_key`, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []metadataCacheRow{}
	for rows.Next() {
		var row metadataCacheRow
		if err := rows.Scan(&row.Field, &row.Key, &row.Order, &row.Data); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s.fillMetadataRecordsCache(ctx, workspace, result)
	return result, nil
}

// decodeArrayFields decodes each array field's records into a new slice
// (appended to any value the root document held), spreading the work over
// the available CPUs.
func decodeArrayFields(fields map[string]reflect.Value, arrays map[string][]json.RawMessage) error {
	type job struct {
		items  []json.RawMessage
		target reflect.Value
		offset int
	}
	jobs := []job{}
	for field, items := range arrays {
		target := fields[field]
		base := target.Len()
		grown := reflect.MakeSlice(target.Type(), base+len(items), base+len(items))
		reflect.Copy(grown, target)
		target.Set(grown)
		const chunk = 2048
		for start := 0; start < len(items); start += chunk {
			jobs = append(jobs, job{items: items[start:min(start+chunk, len(items))], target: grown, offset: base + start})
		}
	}
	if len(jobs) == 0 {
		return nil
	}
	workers := min(runtime.GOMAXPROCS(0), len(jobs))
	next := make(chan job)
	errs := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for work := range next {
				for index, raw := range work.items {
					if err := json.Unmarshal(raw, work.target.Index(work.offset+index).Addr().Interface()); err != nil {
						errs <- err
						for range next {
						}
						return
					}
				}
			}
		}()
	}
	for _, work := range jobs {
		next <- work
	}
	close(next)
	wait.Wait()
	select {
	case err := <-errs:
		return err
	default:
		return nil
	}
}

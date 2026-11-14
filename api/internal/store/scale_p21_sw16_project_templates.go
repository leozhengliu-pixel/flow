package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
)

const scaleP21SW16ProjectTemplateMaxWork = 128 << 20

// ProjectTemplatesRead owns a wire-only installed template snapshot and its
// cloning/encoding allowance. Keep the handle through the complete response.
// Times retain their instant and JSON offset with detached fixed zone backing.
type ProjectTemplatesRead struct {
	templates []domain.ProjectTemplate
	release   func()
	once      sync.Once
}

type scaleP21SW16TemplateValidationHeader struct {
	id, resource, stamp string
	order, size         int64
}

func (r *ProjectTemplatesRead) Templates() []domain.ProjectTemplate { return r.templates }

func (r *ProjectTemplatesRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.templates = nil
			if r.release != nil {
				r.release()
			}
		})
	}
}

// BeginProjectTemplatesRead reads the exact installed source used by
// BootstrapFor, including warm null slices and values sanitized only in SQL.
// It does not authorize or replace the route's existing middleware.
func (s *SQLiteStore) BeginProjectTemplatesRead(ctx context.Context, workspace string) (*ProjectTemplatesRead, bool, error) {
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
		cost, err := scaleP21SW16ProjectTemplateSourceCost(source)
		// Do not pin the borrowed complete graph after leaving its lock.
		source = domain.Bootstrap{}
		selected = ""
		s.mu.RUnlock()
		if !found || err != nil {
			return nil, found, err
		}
		_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, cost)
		if err != nil {
			return nil, true, err
		}
		s.mu.RLock()
		selected = workspace
		if workspace == "" {
			selected = s.lastWorkspaceKey
		}
		source, found = s.workspaces[selected]
		current, err := scaleP21SW16ProjectTemplateSourceCost(source)
		if !found || err != nil || current > cost {
			source = domain.Bootstrap{}
			selected = ""
			s.mu.RUnlock()
			release()
			if !found || err != nil {
				return nil, found, err
			}
			continue
		}
		value := scaleP21SW16CloneTemplateValue(reflect.ValueOf(source.ProjectTemplates))
		templates := value.Interface().([]domain.ProjectTemplate)
		logical := strings.Clone(source.Workspace.URLKey)
		issues := source.Issues == nil
		kinds := []string{"import_input", "migration_input"}
		if source.Activities == nil {
			kinds = append(kinds, "activity")
		}
		if source.Comments == nil {
			kinds = append(kinds, "comment")
		}
		if source.Notifications == nil {
			kinds = append(kinds, "notification")
		}
		if source.NotificationDeliveries == nil {
			kinds = append(kinds, "delivery")
		}
		source = domain.Bootstrap{}
		selected = ""
		s.mu.RUnlock()
		if err := ctx.Err(); err != nil {
			release()
			return nil, true, err
		}
		// BootstrapFor also reads these unrelated canonical collections.
		// Preserve its workspace-not-found result on their hydration errors,
		// while retaining at most one complete typed row at a time.
		if issues {
			err = s.scaleP21SW16ValidateTemplateBootstrapRows(ctx, logical, "", func() any { return new(domain.Issue) })
		}
		for _, kind := range kinds {
			if err != nil {
				break
			}
			var target func() any
			switch kind {
			case "import_input", "migration_input":
				target = func() any { return new(importInput) }
			case "activity":
				target = func() any { return new(domain.ActivityEvent) }
			case "comment":
				target = func() any { return new(domain.Comment) }
			case "notification":
				target = func() any { return new(domain.Notification) }
			case "delivery":
				target = func() any { return new(domain.NotificationDelivery) }
			}
			err = s.scaleP21SW16ValidateTemplateBootstrapRows(ctx, logical, kind, target)
		}
		if err != nil {
			release()
			if errors.Is(err, ErrMetadataWorkBudget) {
				return nil, true, err
			}
			return nil, false, nil
		}
		return &ProjectTemplatesRead{templates: templates, release: release}, true, nil
	}
}

func scaleP21SW16ProjectTemplateSourceCost(source domain.Bootstrap) (int, error) {
	cost, err := scaleP21SW16ProjectTemplateCost(source.ProjectTemplates)
	extra := int64(33)*int64(len(source.Workspace.URLKey)) + 4096
	if err != nil {
		return 0, err
	}
	if extra > int64(scaleP21SW16ProjectTemplateMaxWork-cost) {
		return 0, ErrMetadataWorkBudget
	}
	return cost + int(extra), nil
}

func (s *SQLiteStore) scaleP21SW16ValidateTemplateBootstrapRows(ctx context.Context, workspace, kind string, target func() any) error {
	ctx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, 128<<10)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var cursor *scaleP21SW16TemplateValidationHeader
	var releasePrevious func()
	defer func() {
		if releasePrevious != nil {
			releasePrevious()
		}
	}()
	length := "OCTET_LENGTH(data)"
	if s.dialect == "sqlite" {
		length = "LENGTH(CAST(data AS BLOB))"
	}
	for {
		headers, releaseHeaders, err := s.scaleP21SW16TemplateValidationHeaders(ctx, tx, workspace, kind, cursor, length)
		if err != nil {
			return err
		}
		cursor = nil
		if releasePrevious != nil {
			releasePrevious()
		}
		releasePrevious = releaseHeaders
		for _, header := range headers {
			table, where := "issue_records", "workspace_key=? AND "+s.metadataRecordKeyEquals("id", "?")
			args := []any{workspace, header.id}
			if kind != "" {
				table = "workspace_content_records"
				where += " AND kind=? AND " + s.metadataRecordKeyEquals("resource_id", "?")
				args = append(args, kind, header.resource)
			}
			if err := s.scaleP21SW16ValidateTemplateBootstrapRow(ctx, tx, " FROM "+table+" WHERE "+where, length, args, header.size, target); err != nil {
				return err
			}
		}
		if len(headers) < 64 {
			return nil
		}
		cursor = &headers[len(headers)-1]
	}
}

func (s *SQLiteStore) scaleP21SW16TemplateValidationHeaders(ctx context.Context, tx *sqlTx, workspace, kind string, cursor *scaleP21SW16TemplateValidationHeader, length string) ([]scaleP21SW16TemplateValidationHeader, func(), error) {
	table, where, order := "issue_records", "workspace_key=?", "collection_order,id"
	columns := "id,collection_order," + length
	keyBytes := s.scaleP21SW16AttachmentBytes("id")
	args := []any{workspace}
	if kind == "" && cursor != nil {
		where += " AND (collection_order>? OR (collection_order=? AND id>?))"
		args = append(args, cursor.order, cursor.order, cursor.id)
	}
	if kind != "" {
		table, where, order = "workspace_content_records", "workspace_key=? AND kind=?", "created_at,id,resource_id"
		columns = "id,resource_id,created_at," + length
		keyBytes += "+" + s.scaleP21SW16AttachmentBytes("resource_id") + "+" + s.scaleP21SW16AttachmentBytes("created_at")
		args = append(args, kind)
		if cursor != nil {
			where += " AND (created_at>? OR (created_at=? AND id>?) OR (created_at=? AND id=? AND resource_id>?))"
			args = append(args, cursor.stamp, cursor.stamp, cursor.id, cursor.stamp, cursor.id, cursor.resource)
		}
	}
	page := " FROM " + table + " WHERE " + where + " ORDER BY " + order + " LIMIT 64"
	var count, bytes int64
	query := "SELECT COUNT(*),COALESCE(SUM(key_bytes),0) FROM (SELECT " + keyBytes + " key_bytes" + page + ") sw16_template_headers"
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&count, &bytes); err != nil {
		return nil, nil, err
	}
	if count < 0 || count > 64 || bytes < 0 || bytes > (metadataWorkBytes-count*256-(64<<10))/3 {
		return nil, nil, ErrMetadataWorkBudget
	}
	if count == 0 {
		return nil, func() {}, nil
	}
	headerCtx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, int(bytes)*3+int(count)*256+64<<10)
	if err != nil {
		return nil, nil, err
	}
	rows, err := tx.QueryContext(headerCtx, "SELECT "+columns+page, args...)
	if err != nil {
		release()
		return nil, nil, err
	}
	headers := make([]scaleP21SW16TemplateValidationHeader, 0, int(count))
	for rows.Next() {
		var header scaleP21SW16TemplateValidationHeader
		var err error
		if kind == "" {
			err = rows.Scan(&header.id, &header.order, &header.size)
		} else {
			err = rows.Scan(&header.id, &header.resource, &header.stamp, &header.size)
		}
		if err != nil {
			rows.Close()
			release()
			return nil, nil, err
		}
		headers = append(headers, header)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil || int64(len(headers)) != count {
		release()
		if err == nil {
			err = ErrMetadataRecordQuery
		}
		return nil, nil, err
	}
	return headers, release, nil
}

func (s *SQLiteStore) scaleP21SW16ValidateTemplateBootstrapRow(ctx context.Context, tx *sqlTx, source, length string, args []any, size int64, target func() any) error {
	if size < 0 || size > (metadataWorkBytes-(64<<10))/3 {
		return ErrMetadataWorkBudget
	}
	rawCtx, releaseRaw, err := s.tryAdmitAdditionalMetadataWork(ctx, int(size)*3+64<<10)
	if err != nil {
		return err
	}
	defer releaseRaw()
	var raw []byte
	query := "SELECT CASE WHEN " + length + "<=? THEN data ELSE NULL END" + source
	params := append([]any{size}, args...)
	if err := tx.QueryRowContext(rawCtx, query, params...).Scan(&raw); err != nil {
		return err
	}
	if len(raw) != int(size) {
		return ErrMetadataWorkBudget
	}
	budget := metadataHydrationBudget{bytes: len(raw), work: metadataWorkBytes}
	if err := budget.consume(raw); err != nil {
		return ErrMetadataWorkBudget
	}
	_, releaseTyped, err := s.tryAdmitAdditionalMetadataWork(rawCtx, metadataWorkBytes-budget.work+64<<10)
	if err != nil {
		return err
	}
	defer releaseTyped()
	return json.Unmarshal(raw, target())
}

func scaleP21SW16ProjectTemplateCost(templates []domain.ProjectTemplate) (int, error) {
	remaining := scaleP21SW16ProjectTemplateMaxWork - 128<<10
	nodes := 0
	err := scaleP21SW16TemplateValueCost(reflect.ValueOf(templates), 0, &remaining, &nodes)
	return scaleP21SW16ProjectTemplateMaxWork - remaining, err
}

func scaleP21SW16TemplateValueCost(value reflect.Value, depth int, remaining, nodes *int) error {
	if depth > 64 || *nodes >= 1<<20 {
		return ErrMetadataWorkBudget
	}
	*nodes++
	cost := int64(128) + int64(value.Type().Size())*2
	if value.Kind() == reflect.String {
		// Cloned native strings, maximum JSON escaping, encoder growth,
		// signing scratch and returned wire bytes remain covered together.
		cost += int64(value.Len()) * 33
	}
	if cost > int64(*remaining) {
		return ErrMetadataWorkBudget
	}
	*remaining -= int(cost)
	if value.Type() == reflect.TypeFor[time.Time]() {
		// This wire-only snapshot replaces arbitrary location backing with a
		// fixed offset at this instant. MarshalJSON depends on that offset,
		// not a location's zone names or transition history.
		if *remaining < 1024 {
			return ErrMetadataWorkBudget
		}
		*remaining -= 1024
		return nil
	}
	switch value.Kind() {
	case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return nil
	case reflect.Pointer:
		if value.IsNil() {
			return nil
		}
		return scaleP21SW16TemplateValueCost(value.Elem(), depth+1, remaining, nodes)
	case reflect.Slice, reflect.Array:
		if value.Kind() == reflect.Slice && value.IsNil() {
			return nil
		}
		storage := int64(value.Len()) * int64(value.Type().Elem().Size()) * 2
		if storage > int64(*remaining) {
			return ErrMetadataWorkBudget
		}
		*remaining -= int(storage)
		for i := range value.Len() {
			if err := scaleP21SW16TemplateValueCost(value.Index(i), depth+1, remaining, nodes); err != nil {
				return err
			}
		}
		return nil
	case reflect.Struct:
		for i := range value.NumField() {
			field := value.Type().Field(i)
			if field.Tag.Get("json") == "-" {
				continue
			}
			if field.PkgPath != "" {
				return ErrBoundedMetadataCoverage
			}
			if err := scaleP21SW16TemplateValueCost(value.Field(i), depth+1, remaining, nodes); err != nil {
				return err
			}
		}
		return nil
	default:
		return ErrBoundedMetadataCoverage
	}
}

func scaleP21SW16CloneTemplateValue(value reflect.Value) reflect.Value {
	if value.Type() == reflect.TypeFor[time.Time]() {
		stamp := value.Interface().(time.Time)
		_, offset := stamp.Zone()
		return reflect.ValueOf(stamp.In(time.FixedZone("", offset)))
	}
	switch value.Kind() {
	case reflect.String:
		return reflect.ValueOf(strings.Clone(value.String())).Convert(value.Type())
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		owned := reflect.New(value.Type().Elem())
		owned.Elem().Set(scaleP21SW16CloneTemplateValue(value.Elem()))
		return owned
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		owned := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for i := range value.Len() {
			owned.Index(i).Set(scaleP21SW16CloneTemplateValue(value.Index(i)))
		}
		return owned
	case reflect.Array:
		owned := reflect.New(value.Type()).Elem()
		for i := range value.Len() {
			owned.Index(i).Set(scaleP21SW16CloneTemplateValue(value.Index(i)))
		}
		return owned
	case reflect.Struct:
		owned := reflect.New(value.Type()).Elem()
		for i := range value.NumField() {
			if value.Type().Field(i).Tag.Get("json") != "-" {
				owned.Field(i).Set(scaleP21SW16CloneTemplateValue(value.Field(i)))
			}
		}
		return owned
	default:
		return value
	}
}

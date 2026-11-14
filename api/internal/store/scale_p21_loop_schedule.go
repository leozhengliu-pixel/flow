package store

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
)

// LoopScheduleRead owns the installed scalar inputs used before run dispatch.
// It does not load instructions, versions, or execute the separate run boundary.
type LoopScheduleRead struct {
	Data     domain.Bootstrap
	HasLoops bool
	source   *metadataLogicalSource
	store    *SQLiteStore
	release  []func()
	once     sync.Once
}

func (r *LoopScheduleRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Data = domain.Bootstrap{}
			if r.source != nil {
				r.source.Release()
				r.source = nil
			}
			for _, release := range r.release {
				release()
			}
			r.release = nil
		})
	}
}

// BeginLoopScheduleRead selects schedule, event, or event-probe inputs from
// the same physical/default installed registry as WorkspaceMetadataFields.
func (s *SQLiteStore) BeginLoopScheduleRead(ctx context.Context, workspace, mode string) (_ *LoopScheduleRead, found bool, err error) {
	if mode != "schedule" && mode != "event" && mode != "event-probe" {
		return nil, false, ErrMetadataRecordQuery
	}
	if !s.BoundedMetadataEnabled() {
		return nil, false, ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	owner, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return nil, found, err
	}
	defer owner.Close()
	source, err := owner.retainSource(ctx, owner.workspace)
	if err != nil {
		return nil, true, err
	}
	r := &LoopScheduleRead{source: source, store: s}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	if err := r.reserve(ctx, 64<<10); err != nil {
		return nil, true, err
	}
	err = r.field("loops").Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		enabled, err := loopScheduleChild(ctx, record, "Enabled")
		if err != nil {
			return err
		}
		value, err := enabled.Value(ctx, 4096)
		if err != nil || !value.Bool() {
			return err
		}
		status, err := loopScheduleChild(ctx, record, "Status")
		if err != nil {
			return err
		}
		draft, err := status.StringEqual(ctx, "draft")
		if err != nil || draft {
			return err
		}
		trigger, err := loopScheduleChild(ctx, record, "TriggerType")
		if err != nil {
			return err
		}
		scheduled, err := trigger.StringEqual(ctx, "schedule")
		if err != nil || scheduled != (mode == "schedule") {
			return err
		}
		r.HasLoops = true
		if mode == "event-probe" {
			return errMetadataAggregateStop
		}
		loop := domain.Loop{}
		fields := "ID Enabled Status TriggerType CreatedAt NextRunAt"
		configFields := "startDate starting interval unit time weekdays timezone"
		if mode == "event" {
			fields = "ID Enabled Status TriggerType TeamID TeamAccess Level TrustedSourceKeys"
			configFields = "event action includeCreated value filters filter filterField filterOperator filterValue teamIds"
		}
		if err := r.project(ctx, record, reflect.ValueOf(&loop).Elem(), strings.Fields(fields)); err != nil {
			return err
		}
		config, err := loopScheduleChild(ctx, record, "TriggerConfig")
		if err != nil {
			return err
		}
		if err := config.MapEntries(ctx, func(key, node metadataLogicalNode) error {
			for _, name := range strings.Fields(configFields) {
				match, err := key.StringEqual(ctx, name)
				if err != nil {
					return err
				}
				if !match {
					continue
				}
				if err := r.reserveNode(ctx, node); err != nil {
					return err
				}
				value, err := node.Value(ctx, int(node.cost))
				if err != nil {
					return err
				}
				if loop.TriggerConfig == nil {
					loop.TriggerConfig = map[string]any{}
				}
				loop.TriggerConfig[name] = value.Interface()
				break
			}
			return nil
		}); err != nil {
			return err
		}
		r.Data.Loops = append(r.Data.Loops, loop)
		return nil
	})
	if err == errMetadataAggregateStop {
		err = nil
	}
	if err != nil {
		return nil, true, err
	}
	return r, true, nil
}

func (r *LoopScheduleRead) reserve(ctx context.Context, cost uint64) error {
	if cost > metadataWorkBytes {
		return ErrMetadataWorkBudget
	}
	_, release, err := r.store.tryAdmitAdditionalMetadataWork(ctx, int(cost))
	if err == nil {
		r.release = append(r.release, release)
	}
	return err
}

func (r *LoopScheduleRead) reserveNode(ctx context.Context, node metadataLogicalNode) error {
	if node.cost > (metadataWorkBytes-4096)/8 {
		return ErrMetadataWorkBudget
	}
	return r.reserve(ctx, node.cost*8+4096)
}

func (r *LoopScheduleRead) field(name string) metadataLogicalNode {
	index, _, _ := metadataLogicalField(name)
	return r.source.fields[index]
}

func loopScheduleChild(ctx context.Context, record metadataLogicalNode, name string) (metadataLogicalNode, error) {
	var result metadataLogicalNode
	err := record.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
		if key.String() == name {
			result = node
		}
		return nil
	})
	return result, err
}

func (r *LoopScheduleRead) project(ctx context.Context, record metadataLogicalNode, target reflect.Value, fields []string) error {
	cost := uint64(4096)
	if err := record.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
		if slices.Contains(fields, key.String()) {
			if node.cost > (metadataWorkBytes-cost)/8 {
				return ErrMetadataWorkBudget
			}
			cost += node.cost * 8
		}
		return nil
	}); err != nil {
		return err
	}
	if err := r.reserve(ctx, cost); err != nil {
		return err
	}
	return record.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
		if slices.Contains(fields, key.String()) {
			var value reflect.Value
			var err error
			if node.typ == reflect.TypeFor[time.Time]() || node.typ == reflect.TypeFor[*time.Time]() {
				value, err = node.WireValue(ctx, int(node.cost))
			} else {
				value, err = node.Value(ctx, int(node.cost))
			}
			if err != nil {
				return err
			}
			target.FieldByName(key.String()).Set(value)
		}
		return nil
	})
}

// LoadTriggerEntity selects only the event's entity references before the
// server resolves an issue/customer request and applies the original matchers.
func (r *LoopScheduleRead) LoadTriggerEntity(ctx context.Context, event domain.DomainEvent) error {
	entity, _, _ := strings.Cut(event.Type, ".")
	field, fields := "", ""
	switch entity {
	case "customer_request":
		field, fields = "customerRequests", "ID IssueID"
	case "project":
		field, fields = "projects", "ID Name TeamIDs"
	case "initiative":
		field, fields = "initiatives", "ID Name"
	case "release":
		field, fields = "releases", "ID Name"
	case "cycle":
		field, fields = "cycles", "ID Name Number TeamID"
	}
	if field == "" {
		return nil
	}
	index, _, _ := metadataLogicalField(field)
	target := reflect.ValueOf(&r.Data).Elem().Field(index)
	return r.field(field).Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		id, err := loopScheduleChild(ctx, record, "ID")
		if err != nil {
			return err
		}
		match, err := id.StringEqual(ctx, event.AggregateID)
		if err != nil || !match {
			return err
		}
		value := reflect.New(target.Type().Elem()).Elem()
		if err := r.project(ctx, record, value, strings.Fields(fields)); err != nil {
			return err
		}
		target.Set(reflect.Append(target, value))
		return nil
	})
}

// LoadTriggerContext retains only team policy and matching source references.
// It keeps native order/keys, including duplicate asks and member entries.
func (r *LoopScheduleRead) LoadTriggerContext(ctx context.Context, issue *domain.Issue, action string, previous map[string]json.RawMessage) error {
	if err := r.project(ctx, r.field("workspaceSettings"), reflect.ValueOf(&r.Data.WorkspaceSettings).Elem(),
		strings.Fields("ExternalLoopTriggers TrustedSourcesMode TrustedSourcesAllowlist")); err != nil {
		return err
	}
	needed := map[string]bool{}
	if err := r.field("teams").Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
		var team domain.Team
		if err := r.project(ctx, node, reflect.ValueOf(&team).Elem(), strings.Fields("ID Name Private ArchivedAt RetiredAt")); err != nil {
			return err
		}
		r.Data.Teams = append(r.Data.Teams, team)
		needed[team.ID] = true
		return nil
	}); err != nil {
		return err
	}
	if issue != nil {
		needed[issue.Team.ID] = true
		var team struct {
			ID string `json:"id"`
		}
		if raw := previous["team"]; len(raw) != 0 {
			if uint64(len(raw)) > (metadataWorkBytes-4096)/8 {
				return ErrMetadataWorkBudget
			}
			if err := r.reserve(ctx, uint64(len(raw))*8+4096); err != nil {
				return err
			}
			_ = json.Unmarshal(raw, &team)
		}
		needed[team.ID] = true
	}
	r.Data.TeamSettings = map[string]domain.TeamSettings{}
	loaded := map[string]bool{}
	for {
		added := false
		var maxKeyCost uint64
		for id := range needed {
			maxKeyCost = max(maxKeyCost, uint64(len(id))*8+1024)
		}
		if err := r.field("teamSettings").MapEntries(ctx, func(key, record metadataLogicalNode) error {
			if key.cost > maxKeyCost {
				return nil
			}
			if key.cost > (metadataWorkBytes-4096)/4 {
				return ErrMetadataWorkBudget
			}
			_, release, err := r.store.tryAdmitAdditionalMetadataWork(ctx, int(key.cost*4+4096))
			if err != nil {
				return err
			}
			defer release()
			value, err := key.Value(ctx, int(key.cost))
			if err != nil {
				return err
			}
			id := value.String()
			if !needed[id] || loaded[id] {
				return nil
			}
			var settings domain.TeamSettings
			if err := r.project(ctx, record, reflect.ValueOf(&settings).Elem(),
				strings.Fields("TeamID Access TriageEnabled ParentTeamID InheritIssueEstimation EstimateType InheritWorkflowStatuses")); err != nil {
				return err
			}
			if err := r.reserveNode(ctx, key); err != nil {
				return err
			}
			r.Data.TeamSettings[id], loaded[id] = settings, true
			if settings.ParentTeamID != "" && (settings.InheritIssueEstimation || settings.InheritWorkflowStatuses) && !needed[settings.ParentTeamID] {
				needed[settings.ParentTeamID], added = true, true
			}
			return nil
		}); err != nil {
			return err
		}
		if !added {
			break
		}
	}
	if err := r.field("states").Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
		var state domain.WorkflowState
		if err := r.project(ctx, node, reflect.ValueOf(&state).Elem(), strings.Fields("ID Name TeamID Position Default")); err != nil {
			return err
		}
		r.Data.States = append(r.Data.States, state)
		return nil
	}); err != nil {
		return err
	}
	if issue == nil || action != "created" {
		return nil
	}
	if err := r.field("asks").Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
		id, err := loopScheduleChild(ctx, node, "IssueID")
		if err != nil {
			return err
		}
		match, err := id.StringEqual(ctx, issue.ID)
		if err != nil || !match {
			return err
		}
		var ask domain.Ask
		if err := r.project(ctx, node, reflect.ValueOf(&ask).Elem(), strings.Fields("IssueID Source")); err != nil {
			return err
		}
		r.Data.Asks = append(r.Data.Asks, ask)
		return nil
	}); err != nil {
		return err
	}
	return r.field("members").Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
		user, err := loopScheduleChild(ctx, node, "User")
		if err != nil {
			return err
		}
		id, err := loopScheduleChild(ctx, user, "ID")
		if err != nil {
			return err
		}
		match, err := id.StringEqual(ctx, issue.Creator.ID)
		if err != nil || !match {
			return err
		}
		var member domain.WorkspaceMember
		if err := r.project(ctx, node, reflect.ValueOf(&member).Elem(), []string{"Role"}); err != nil {
			return err
		}
		if err := r.project(ctx, user, reflect.ValueOf(&member.User).Elem(), []string{"ID"}); err != nil {
			return err
		}
		r.Data.Members = append(r.Data.Members, member)
		return nil
	})
}

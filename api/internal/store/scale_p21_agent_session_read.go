package store

import (
	"context"
	"errors"
	"reflect"

	"flow/api/internal/domain"
)

// AgentSessionMatch uses nil as a wildcard. A nonnil empty ID or owner matches
// that exact installed value, including deleted and duplicate session rows.
type AgentSessionMatch struct {
	ID     *string
	UserID *string
}

// AgentSessionRead pins trusted installed sessions, without granting access or
// loading message bodies. Returned point values own independent lifetimes.
type AgentSessionRead struct {
	store  *SQLiteStore
	source *InstalledMetadataRead
}

// AgentSessionHeaderRead owns an incomplete, read-only session projection.
// Messages contain only ID, Status and native UpdatedAt, in installed order.
type AgentSessionHeaderRead struct {
	Data   domain.AgentSession
	source *InstalledMetadataRead
	owner  *ResourceRead
}

// AgentMessageRead owns one complete native message, including any unhydrated
// offloaded references. It is not a session mutation preimage.
type AgentMessageRead struct {
	Data   domain.AgentMessage
	source *InstalledMetadataRead
	owner  *ResourceRead
}

func (r *AgentSessionRead) Close() {
	if r != nil && r.source != nil {
		r.source.Close()
		r.source = nil
	}
}

func (r *AgentSessionHeaderRead) Close() {
	if r != nil {
		r.Data = domain.AgentSession{}
		if r.owner != nil {
			r.owner.Close()
			r.owner = nil
		}
		if r.source != nil {
			r.source.Close()
			r.source = nil
		}
	}
}

func (r *AgentMessageRead) Close() {
	if r != nil {
		r.Data = domain.AgentMessage{}
		if r.owner != nil {
			r.owner.Close()
			r.owner = nil
		}
		if r.source != nil {
			r.source.Close()
			r.source = nil
		}
	}
}

func (s *SQLiteStore) BeginAgentSessionRead(ctx context.Context, workspace string) (*AgentSessionRead, bool, error) {
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	source, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return nil, found, err
	}
	return &AgentSessionRead{store: s, source: source}, true, nil
}

func (r *AgentSessionRead) agentSessionPointSource(ctx context.Context) (*InstalledMetadataRead, error) {
	if r == nil || r.source == nil {
		return nil, errMetadataLogicalClosed
	}
	bound, err := r.source.Context(ctx)
	if err != nil {
		return nil, err
	}
	source, _, err := r.store.BeginInstalledMetadataRead(bound, r.source.workspace)
	return source, err
}

func (r *AgentSessionRead) visitAgentSessions(ctx context.Context, match AgentSessionMatch, visit func(metadataLogicalNode) error) error {
	if r == nil || r.source == nil {
		return errMetadataLogicalClosed
	}
	err := r.source.source.VisitFields(ctx, func(name string, sessions metadataLogicalNode) error {
		if name != "agentSessions" {
			return nil
		}
		return sessions.Children(ctx, func(_ reflect.Value, session metadataLogicalNode) error {
			matched := true
			err := session.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
				var wanted *string
				switch key.String() {
				case "ID":
					wanted = match.ID
				case "UserID":
					wanted = match.UserID
				}
				if wanted == nil {
					return nil
				}
				equal, err := node.StringEqual(ctx, *wanted)
				matched = matched && equal
				return err
			})
			if err != nil || !matched {
				return err
			}
			return visit(session)
		})
	})
	if err == nil {
		err = ctx.Err()
	}
	return err
}

func (r *AgentSessionRead) agentSessionHeader(ctx context.Context, node metadataLogicalNode) (_ *AgentSessionHeaderRead, err error) {
	source, err := r.agentSessionPointSource(ctx)
	if err != nil {
		return nil, err
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	header := &AgentSessionHeaderRead{source: source, owner: &ResourceRead{store: r.store}}
	defer func() {
		if err != nil {
			header.Close()
		}
	}()
	if err = header.owner.reserve(ctx, 64<<10); err != nil {
		return nil, err
	}
	value, err := header.owner.projectNode(ctx, node, "ID", "UserID")
	if err != nil {
		return nil, err
	}
	header.Data = value.Interface().(domain.AgentSession)
	err = node.Children(ctx, func(key reflect.Value, messages metadataLogicalNode) error {
		if key.String() != "Messages" {
			return nil
		}
		absent, err := messages.isNil(ctx)
		if err != nil {
			return err
		}
		if !absent {
			header.Data.Messages = []domain.AgentMessage{}
		}
		return messages.Children(ctx, func(_ reflect.Value, message metadataLogicalNode) error {
			value, err := header.owner.projectNode(ctx, message, "ID", "Status", "UpdatedAt")
			if err == nil {
				header.Data.Messages = append(header.Data.Messages, value.Interface().(domain.AgentMessage))
			}
			return err
		})
	})
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	return header, nil
}

// FirstHeader selects the first matching session occurrence, without slug or
// deletion filtering. Close the returned value after its final use.
func (r *AgentSessionRead) FirstHeader(ctx context.Context, match AgentSessionMatch) (header *AgentSessionHeaderRead, found bool, err error) {
	err = r.visitAgentSessions(ctx, match, func(node metadataLogicalNode) error {
		header, err = r.agentSessionHeader(ctx, node)
		if err != nil {
			return err
		}
		found = true
		return ErrMetadataIterationDone
	})
	if errors.Is(err, ErrMetadataIterationDone) {
		err = nil
	}
	return header, found, err
}

// VisitHeaders lends an independently admitted header only through its
// callback. It visits every matching occurrence and releases each before the
// next; retaining Data after the callback is outside this contract.
func (r *AgentSessionRead) VisitHeaders(ctx context.Context, match AgentSessionMatch, visit func(domain.AgentSession) error) error {
	if visit == nil {
		return ErrBoundedMetadataCoverage
	}
	return r.visitAgentSessions(ctx, match, func(node metadataLogicalNode) error {
		header, err := r.agentSessionHeader(ctx, node)
		if err != nil {
			return err
		}
		defer header.Close()
		if err := visit(header.Data); err != nil {
			return err
		}
		return ctx.Err()
	})
}

// FirstMessage searches all exact session/owner occurrences until the first
// matching message. An earlier duplicate session without it does not hide it.
func (r *AgentSessionRead) FirstMessage(ctx context.Context, sessionID, userID, messageID string) (message *AgentMessageRead, found bool, err error) {
	err = r.visitAgentSessions(ctx, AgentSessionMatch{ID: &sessionID, UserID: &userID}, func(session metadataLogicalNode) error {
		return session.Children(ctx, func(key reflect.Value, messages metadataLogicalNode) error {
			if key.String() != "Messages" {
				return nil
			}
			return messages.Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
				matched := false
				if err := node.Children(ctx, func(key reflect.Value, id metadataLogicalNode) error {
					if key.String() == "ID" {
						var err error
						matched, err = id.StringEqual(ctx, messageID)
						return err
					}
					return nil
				}); err != nil || !matched {
					return err
				}
				source, err := r.agentSessionPointSource(ctx)
				if err != nil {
					return err
				}
				point := &AgentMessageRead{source: source, owner: &ResourceRead{store: r.store}}
				value, err := point.owner.node(context.WithValue(ctx, metadataWorkLeaseKey{}, nil), node, false)
				if err == nil {
					err = ctx.Err()
				}
				if err != nil {
					point.Close()
					return err
				}
				point.Data = value.Interface().(domain.AgentMessage)
				message, found = point, true
				return ErrMetadataIterationDone
			})
		})
	})
	if errors.Is(err, ErrMetadataIterationDone) {
		err = nil
	}
	return message, found, err
}

package store

import (
	"bufio"
	"context"
	"errors"
	"io"
	"reflect"
	"unicode"
)

func (s *SQLiteStore) logicalWebhookNeeded(ctx context.Context, source *metadataLogicalSource, workspace string) (bool, error) {
	needed := false
	err := source.VisitFields(ctx, func(name string, collection metadataLogicalNode) error {
		if name != "webhooks" && name != "loops" && name != "integrationConnections" {
			return nil
		}
		return collection.Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
			node := metadataAggregateStoredNode{record}
			boolean := func(field string) (bool, error) {
				value, err := metadataAggregateChild(ctx, node, field)
				if err != nil || value == nil {
					return false, err
				}
				owned, err := value.nodeValue(ctx, 4096)
				if err != nil {
					return false, err
				}
				return owned.Kind() == reflect.Bool && owned.Bool(), nil
			}
			equal := func(field, want string) (bool, error) {
				value, err := metadataAggregateChild(ctx, node, field)
				if err != nil {
					return false, err
				}
				return metadataAggregateStringEqual(ctx, value, want)
			}
			switch name {
			case "webhooks":
				enabled, err := boolean("Enabled")
				if err != nil || !enabled {
					return err
				}
				url, err := metadataAggregateChild(ctx, node, "URL")
				if err != nil {
					return err
				}
				stored, ok := url.(metadataAggregateStoredNode)
				if !ok {
					return ErrMetadataRecordQuery
				}
				needed, err = metadataLogicalNonSpace(ctx, stored.value)
				if err != nil {
					return err
				}
			case "loops":
				enabled, err := boolean("Enabled")
				if err != nil || !enabled {
					return err
				}
				draft, err := equal("Status", "draft")
				if err != nil || draft {
					return err
				}
				empty, err := equal("TriggerType", "")
				if err != nil || empty {
					return err
				}
				schedule, err := equal("TriggerType", "schedule")
				if err != nil {
					return err
				}
				needed = !schedule
			case "integrationConnections":
				feishu, err := equal("Provider", "feishu")
				if err != nil {
					return err
				}
				if !feishu {
					lark, err := equal("Provider", "lark")
					if err != nil || !lark {
						return err
					}
				}
				connected, err := equal("Status", "connected")
				if err != nil {
					return err
				}
				needed = connected
			}
			if needed {
				return errMetadataAggregateStop
			}
			return nil
		})
	})
	if errors.Is(err, errMetadataAggregateStop) {
		err = nil
	}
	if err != nil || needed {
		return needed, err
	}
	return s.chatSnapshotFlag(ctx, workspace), nil
}

func metadataLogicalNonSpace(ctx context.Context, value metadataLogicalNode) (bool, error) {
	nonspace := false
	err := value.StreamBytes(ctx, func(input io.Reader) error {
		reader := bufio.NewReaderSize(input, 4096)
		for {
			r, _, err := reader.ReadRune()
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
			if !unicode.IsSpace(r) {
				nonspace = true
				return nil
			}
		}
	})
	return nonspace, err
}

package main

import (
	"encoding/json"

	"flow/api/internal/jsonwire"
	"flow/api/internal/store"
)

func (key syncV2Key) jsonBytes() int64 {
	return int64(len(`{"kind":,"id":}`)) + jsonwire.StringBytes(key.Kind) + jsonwire.StringBytes(key.ID)
}

func syncV2ArrayBytes[T any](items []T) int64 {
	if items == nil {
		return int64(len("null"))
	}
	return int64(2 + max(0, len(items)-1))
}

func syncV2BoolBytes(value bool) int64 {
	if value {
		return int64(len("true"))
	}
	return int64(len("false"))
}

// Measure the journal transaction before encoding. An oversized upsert
// becomes an invalidation without first allocating a throwaway transaction.
func (tx syncV2Transaction) jsonBytes() (int64, error) {
	size := int64(len(`{"txId":,"seq":,"upserts":,"removals":,"invalidations":}`)) +
		jsonwire.StringBytes(tx.TxID) + jsonwire.StringBytes(tx.Seq) +
		syncV2ArrayBytes(tx.Upserts) + syncV2ArrayBytes(tx.Removals) + syncV2ArrayBytes(tx.Invalidations)
	for _, item := range tx.Upserts {
		width := len("null")
		if item.Data != nil {
			if !json.Valid(item.Data) {
				return 0, store.ErrBoundedMetadataCoverage
			}
			width, _ = jsonwire.Compact(nil, item.Data)
		}
		size += int64(len(`{"key":,"version":,"data":,"projection":,"detailStale":}`)+width) +
			item.Key.jsonBytes() + jsonwire.StringBytes(item.Version) + jsonwire.StringBytes(item.Projection) + syncV2BoolBytes(item.DetailStale)
	}
	for _, item := range tx.Removals {
		size += int64(len(`{"key":,"version":}`)) + item.Key.jsonBytes() + jsonwire.StringBytes(item.Version)
	}
	for _, kind := range tx.Invalidations {
		size += jsonwire.StringBytes(kind)
	}
	return size, nil
}

func (page syncV2ChangesResponse) jsonBytes() (int64, error) {
	size := int64(len(`{"next":,"head":,"authzEpoch":,"transactions":,"hasMore":,"resyncRequired":}`)) +
		jsonwire.StringBytes(page.Next) + jsonwire.StringBytes(page.Head) + jsonwire.StringBytes(page.AuthzEpoch) +
		syncV2ArrayBytes(page.Transactions) + syncV2BoolBytes(page.HasMore) + syncV2BoolBytes(page.ResyncRequired)
	for _, tx := range page.Transactions {
		width, err := tx.jsonBytes()
		if err != nil {
			return 0, err
		}
		size += width
	}
	return size, nil
}

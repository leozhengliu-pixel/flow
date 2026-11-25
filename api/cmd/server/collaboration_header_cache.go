package main

import (
	"context"
	"math"
	"strings"
	"time"
)

// Join and cache ownership commit under one hub lock so revocation cannot
// release the header before a later join reinserts its key. The queue lock
// excludes socket teardown; identifiers use the socket budget.
func (h *realtimeHub) joinDocumentHeader(ctx context.Context, client *realtimeSocketClient, collaborationID, documentID string, canEdit bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	client.queueMu.Lock()
	defer client.queueMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if client.queueClosed {
		return "", errRealtimeBudget
	}
	cost := uint64(len(collaborationID)) + uint64(len(documentID))
	if cost > (math.MaxInt64-512)/4 {
		return "", errRealtimeBudget
	}
	bytes := int64(cost*4 + 512)
	if !client.budget.acquire(bytes) {
		return "", errRealtimeBudget
	}
	key, id := strings.Clone(collaborationID), strings.Clone(documentID)
	client.releaseDocumentHeader(key)
	delete(client.documentAccess, key)
	delete(client.documents, key)
	client.documents[key] = struct{}{}
	client.documentAccess[key] = &documentSocketAccess{
		documentID: id, canEdit: canEdit, checkedAt: time.Now(), ownedBytes: bytes,
	}
	return key, nil
}

// Call only while holding the hub lock.
func (client *realtimeSocketClient) releaseDocumentHeader(id string) {
	if access := client.documentAccess[id]; access != nil && access.ownedBytes != 0 {
		client.budget.release(access.ownedBytes)
		access.ownedBytes = 0
		access.documentID = ""
	}
}

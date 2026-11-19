package store

import (
	"log/slog"
	"sync"
)

// With bounded metadata on, the live store's installed image holds only the
// resident core and policy fields. The legacy whole-workspace accessors
// (WorkspaceMetadata, BootstrapFor, WorkspaceMetadataFields of a non-resident
// field) read that image directly, so a consumer that reaches one outside a
// registered COLD facade would see empty collections, not an error: the API
// key, authorized application and MCP revocation routes answered empty lists
// and no-ops that way. A production bounded store (OpenDatabase, coverage
// required) therefore refuses such a read: the accessor reports the
// workspace as missing and logs the accessor, so an unregistered COLD path
// fails closed instead of answering from the skeleton. COLD facades and the
// flag-off store hold complete images and are unaffected. Test views keep
// the skeleton read (the differentials compare it) and report it to an
// observer (SW530).

var skeletalReadObservers sync.Map // *SQLiteStore -> func(accessor string)

// SetSkeletalReadObserverForTesting reports each legacy whole-workspace read
// of the live bounded image to observe (nil removes it).
func (s *SQLiteStore) SetSkeletalReadObserverForTesting(observe func(accessor string)) {
	if observe == nil {
		skeletalReadObservers.Delete(s)
		return
	}
	skeletalReadObservers.Store(s, observe)
}

// skeletalRead reports whether accessor must refuse: it reads the live
// bounded image of a store that requires coverage.
func (s *SQLiteStore) skeletalRead(accessor string) bool {
	if s.metadataResidency == nil {
		return false
	}
	if observe, ok := skeletalReadObservers.Load(s); ok {
		observe.(func(string))(accessor)
	}
	if !s.metadataResidency.requireCoverage {
		return false
	}
	slog.Error("bounded metadata: legacy whole-workspace read outside a registered COLD facade refused", "accessor", accessor)
	return true
}

// skeletalFields is skeletalRead for a field-only read that names a field
// the bounded image does not hold.
func (s *SQLiteStore) skeletalFields(fields []string) bool {
	if s.metadataResidency == nil {
		return false
	}
	residency := metadataResidencyFields()
	for _, name := range fields {
		if mode := residency[name]; mode != "core" && mode != "policy" {
			return s.skeletalRead("WorkspaceMetadataFields(" + name + ")")
		}
	}
	return false
}

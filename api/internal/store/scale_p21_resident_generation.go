package store

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// The resident decoded generation (bounded metadata on; design in
// docs/bounded-metadata-performance.md).
//
// The decoded-image cache of the COLD path (scale_p21_cold_decode_cache.go)
// keeps, per workspace, the decoded image of the installed generation and
// builds the image of a later generation incrementally (only frames whose
// origin changed are decoded again). COLD facades receive deep copies of it.
// HOT readers that only read take the image itself: residentImage hands out
// the cached image of exactly the generation the caller pinned, shared by
// every reader of that generation and never copied.
//
// A shared image is immutable. Nothing writes into image.data or anything it
// reaches: a reader that needs to change a value works on a copy (the paged
// projection clones what it returns, as flag-off clones the resident
// workspace). Writers never edit an image; installing a new generation makes
// the next reader build the next image, which shares every unchanged field
// and record value with the previous one (structural sharing by frame
// origin).
//
// Images are bounded by the cache's budget (FLOW_COLD_DECODE_CACHE_BYTES):
// beyond it the least recently used workspaces are dropped, and a workspace
// whose image does not fit is not served from it (residentImage reports
// false and the caller reads the spool as before). A dropped image stays
// alive only while a reader that took it still runs.

// residentImageBuilds serializes image builds per workspace, so readers that
// arrive together after a write build the new image once instead of each
// decoding it.
type residentImageBuilds struct {
	mu    sync.Mutex
	locks map[string]*residentImageBuild
}

type residentImageBuild struct {
	mu      sync.Mutex
	waiters int
}

func (b *residentImageBuilds) lock(workspace string) func() {
	b.mu.Lock()
	if b.locks == nil {
		b.locks = map[string]*residentImageBuild{}
	}
	build := b.locks[workspace]
	if build == nil {
		build = &residentImageBuild{}
		b.locks[workspace] = build
	}
	build.waiters++
	b.mu.Unlock()
	build.mu.Lock()
	return func() {
		build.mu.Unlock()
		b.mu.Lock()
		build.waiters--
		if build.waiters == 0 {
			delete(b.locks, workspace)
		}
		b.mu.Unlock()
	}
}

// Resident image counters (load measurement and tests).
var (
	residentImageHits     atomic.Int64
	residentImageBuilt    atomic.Int64
	residentImageRefusals atomic.Int64
)

// ResidentImageStatsForTesting reports the resident image counters.
func ResidentImageStatsForTesting() map[string]int64 {
	return map[string]int64{"hits": residentImageHits.Load(), "builds": residentImageBuilt.Load(), "refusals": residentImageRefusals.Load()}
}

// residentImage returns the shared decoded image of workspace's generation
// source, which the caller has retained and keeps retained while it uses the
// image. ok is false when the cache is off or the image does not fit its
// budget; the caller then reads source itself. The image is read-only.
func (r *metadataResidency) residentImage(ctx context.Context, workspace string, source *metadataLogicalSource) (_ *coldDecodedImage, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	budget := coldDecodeCacheBudget()
	if budget <= 0 || source == nil {
		return nil, false, nil
	}
	if image, err := r.currentResidentImage(ctx, workspace, source); err != nil || image != nil {
		return image, image != nil, err
	}
	unlock := r.residentBuilds.lock(workspace)
	defer unlock()
	// Another reader may have built it while this one waited.
	if image, err := r.currentResidentImage(ctx, workspace, source); err != nil || image != nil {
		return image, image != nil, err
	}
	cache := &r.coldImages
	cache.mu.Lock()
	previous := cache.images[workspace]
	cache.mu.Unlock()
	image, err := buildColdDecodedImage(ctx, source, previous, nil, true)
	if err != nil {
		return nil, false, err
	}
	residentImageBuilt.Add(1)
	if image.bytes > budget {
		residentImageRefusals.Add(1)
		return nil, false, ctx.Err()
	}
	r.storeColdImage(workspace, image, budget)
	return image, true, ctx.Err()
}

// currentResidentImage is the cached image of workspace when it decodes
// source exactly, else nil.
func (r *metadataResidency) currentResidentImage(ctx context.Context, workspace string, source *metadataLogicalSource) (*coldDecodedImage, error) {
	cache := &r.coldImages
	cache.mu.Lock()
	image := cache.images[workspace]
	if image != nil {
		image.used = time.Now()
	}
	cache.mu.Unlock()
	if image == nil {
		return nil, nil
	}
	current, err := coldImageCurrent(ctx, image, source, nil)
	if err != nil || !current {
		return nil, err
	}
	residentImageHits.Add(1)
	return image, nil
}

type residentViewSize struct {
	mu       sync.Mutex
	measured bool
	bytes    uint64
}

// viewBytes is the decoded size of view (residentBootstrapView of image),
// measured on first use: what a copy of it allocates.
func (image *coldDecodedImage) viewBytes(ctx context.Context, view domain.Bootstrap) (uint64, error) {
	image.view.mu.Lock()
	defer image.view.mu.Unlock()
	if image.view.measured {
		return image.view.bytes, nil
	}
	bytes, err := bootstrapNativeBytes(ctx, reflect.ValueOf(view), 0)
	if err != nil {
		return 0, err
	}
	image.view.bytes, image.view.measured = bytes, true
	return bytes, nil
}

// residentBootstrapView is the paged bootstrap's input from a shared image:
// a struct copy whose omitted fields are zero and whose heavy collections are
// empty, sharing every other container with the image (read-only). Like the
// body projection it replaces (beginBootstrapBodyProjection), it leaves every
// json:"-" field but NextIssueNumber zero.
func residentBootstrapView(image *coldDecodedImage) domain.Bootstrap {
	data := image.data
	value := reflect.ValueOf(&data).Elem()
	for index, field := range metadataStructFields(value.Type()) {
		if field.jsonOmitted && field.name != "NextIssueNumber" {
			value.Field(index).SetZero()
		}
	}
	data.ProjectUpdates, data.InitiativeUpdates = nil, nil
	data.ProviderJobs, data.CommentActions = nil, nil
	data.LoopRuns, data.LoopVersions, data.LoopAttachments = nil, nil, nil
	data.Issues = []domain.Issue{}
	data.Comments = map[string][]domain.Comment{}
	data.Activities = map[string][]domain.ActivityEvent{}
	data.Notifications = []domain.Notification{}
	data.NotificationDeliveries = []domain.NotificationDelivery{}
	return data
}

// ResidentImageCheckForTesting compares workspace's cached image with a fresh
// decode of the installed generation. cached is false when no image of that
// generation is cached; err names the first field that differs.
func (s *SQLiteStore) ResidentImageCheckForTesting(ctx context.Context, workspace string) (cached bool, err error) {
	if !testing.Testing() || s.metadataResidency == nil {
		return false, ErrBoundedMetadataCoverage
	}
	source, err := s.retainMetadataLogicalSource(metadataLogicalUnpinnedContext(ctx), workspace)
	if err != nil {
		return false, err
	}
	defer source.Release()
	image, err := s.metadataResidency.currentResidentImage(ctx, workspace, source)
	if err != nil || image == nil {
		return false, err
	}
	fresh, err := buildColdDecodedImage(ctx, source, nil, nil, false)
	if err != nil {
		return true, err
	}
	cachedValue, freshValue := reflect.ValueOf(image.data), reflect.ValueOf(fresh.data)
	for index, field := range metadataStructFields(cachedValue.Type()) {
		if !reflect.DeepEqual(cachedValue.Field(index).Interface(), freshValue.Field(index).Interface()) {
			return true, fmt.Errorf("resident image field %s differs from a fresh decode of its generation", field.name)
		}
	}
	return true, nil
}

// ResidentImageForTesting returns the cached decoded image of workspace's
// installed generation (nil when none is cached). Tests only read it.
func (s *SQLiteStore) ResidentImageForTesting(ctx context.Context, workspace string) (*domain.Bootstrap, error) {
	if !testing.Testing() || s.metadataResidency == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	source, err := s.retainMetadataLogicalSource(metadataLogicalUnpinnedContext(ctx), workspace)
	if err != nil {
		return nil, err
	}
	defer source.Release()
	image, err := s.metadataResidency.currentResidentImage(ctx, workspace, source)
	if err != nil || image == nil {
		return nil, err
	}
	return &image.data, nil
}

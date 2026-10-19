//go:build envtest

package kubernetes

// Snapshot strategies, orphan GC, the warm pool and the sandbox reconciler
// over the real provider and API server, with an in-memory store.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/objectstore"
	"flow/api/internal/sandbox"
)

// envStore is an in-memory sandbox.ReconcilerStore + MaintenanceStore with
// the SQL store's lease semantics (a trimmed copy of the sandbox package's
// test store).
type envStore struct {
	mu        sync.Mutex
	now       func() time.Time
	rows      map[string]*envRow
	snapshots map[string]string
	mints     int
}

type envRow struct {
	record     sandbox.Record
	leaseOwner string
	leaseUntil time.Time
}

func newEnvStore(now func() time.Time) *envStore {
	return &envStore{now: now, rows: map[string]*envRow{}, snapshots: map[string]string{}}
}

func envSettled(r sandbox.Record) bool {
	return r.Desired == sandbox.DesiredStopped && r.Phase == sandbox.PhaseMissing
}

func (m *envStore) add(record sandbox.Record) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = m.now()
	}
	if record.Phase == "" {
		record.Phase = sandbox.PhasePending
	}
	m.rows[record.ID] = &envRow{record: record}
}

func (m *envStore) get(id string) sandbox.Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rows[id].record
}

// update changes a row and makes it due now.
func (m *envStore) update(id string, mutate func(*sandbox.Record)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mutate(&m.rows[id].record)
	m.rows[id].leaseUntil, m.rows[id].leaseOwner = time.Time{}, ""
}

func (m *envStore) ids() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []string{}
	for id := range m.rows {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func (m *envStore) LeaseSandboxes(_ context.Context, owner string, lease time.Duration, limit int) ([]sandbox.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	ids := []string{}
	for id, row := range m.rows {
		if !envSettled(row.record) && !row.leaseUntil.After(now) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := []sandbox.Record{}
	for _, id := range ids {
		if len(out) == limit {
			break
		}
		row := m.rows[id]
		row.leaseOwner, row.leaseUntil = owner, now.Add(lease)
		out = append(out, row.record)
	}
	return out, nil
}

func (m *envStore) SaveSandboxObservation(ctx context.Context, owner, id string, status sandbox.SandboxStatus, attempts int) error {
	return m.SaveSandboxObservationAt(ctx, owner, id, status, attempts, m.now())
}

func (m *envStore) SaveSandboxObservationAt(_ context.Context, owner, id string, status sandbox.SandboxStatus, attempts int, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[id]
	if !ok || row.leaseOwner != owner {
		return errors.New("lease lost")
	}
	r := &row.record
	r.Phase, r.Reason, r.Message, r.Attempts = status.Phase, status.Reason, status.Message, attempts
	if status.ProviderRef != "" {
		r.ProviderRef = status.ProviderRef
	}
	if r.StartedAt == nil && status.StartedAt != nil {
		r.StartedAt = status.StartedAt
	}
	if r.EndedAt == nil && status.Phase.Terminal() {
		end := m.now()
		r.EndedAt = &end
	}
	// Due again immediately, so tests can drive passes without a clock.
	row.leaseOwner, row.leaseUntil = "", time.Time{}
	return nil
}

func (m *envStore) SetSandboxLogsKey(context.Context, string, string, string) error { return nil }

func (m *envStore) AddSandboxBilledSeconds(_ context.Context, _, id string, from, to int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows[id].record.BilledSeconds != from {
		return false, nil
	}
	m.rows[id].record.BilledSeconds = to
	return true, nil
}

func (m *envStore) MintSandboxToken(_ context.Context, record sandbox.Record, _ time.Duration) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mints++
	return fmt.Sprintf("fcs_%s_%d", record.ID, m.mints), nil
}

func (m *envStore) KnownSandboxIDs(_ context.Context, ids []string) (map[string]bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]bool{}
	for _, id := range ids {
		if row, ok := m.rows[id]; ok && !envSettled(row.record) {
			out[id] = true
		}
	}
	return out, nil
}

func (m *envStore) WarmSandboxes(context.Context) (map[string]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]int{}
	for _, row := range m.rows {
		r := row.record
		if r.Kind == sandbox.KindWarm && r.SessionID == "" && r.Desired == sandbox.DesiredRunning && !r.Phase.Terminal() {
			out[sandbox.WarmKey(r.Size, r.Region)]++
		}
	}
	return out, nil
}

func (m *envStore) CreateWarmSandbox(_ context.Context, record sandbox.Record) error {
	m.add(record)
	return nil
}

func (m *envStore) RecycleWarmSandboxes(context.Context, time.Time) (int, error) { return 0, nil }

func (m *envStore) DeleteExpiredSandboxTokens(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func (m *envStore) SnapshotStates(_ context.Context, ids []string) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	for _, id := range ids {
		if status, ok := m.snapshots[id]; ok {
			out[id] = status
		}
	}
	return out, nil
}

func (m *envStore) SandboxCounts(context.Context) (map[sandbox.Kind]map[sandbox.Phase]int, error) {
	return map[sandbox.Kind]map[sandbox.Phase]int{}, nil
}

var (
	_ sandbox.ReconcilerStore  = (*envStore)(nil)
	_ sandbox.MaintenanceStore = (*envStore)(nil)
)

func snapshotConfig(ns string) config.CodingConfig {
	coding := envConfig(ns)
	coding.Snapshot.Strategy = sandbox.StrategyVolumeSnapshot
	coding.Snapshot.VolumeSnapshotClass = "csi-snapclass"
	coding.Kubernetes.StorageClass = "fast"
	return coding
}

// markSnapshotReady simulates the snapshot controller.
func (c *cluster) markSnapshotReady(t *testing.T, ns, name string, status map[string]any) {
	t.Helper()
	ctx := ctxT(t)
	api := c.dynamic.Resource(volumeSnapshotGVR).Namespace(ns)
	object, err := api.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	object.Object["status"] = status
	if _, err := api.UpdateStatus(ctx, object, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update volumesnapshot status: %v", err)
	}
}

// TestEnvtestVolumeSnapshotStrategy runs the volumesnapshot strategy against
// the external-snapshotter CRDs: build volume, VolumeSnapshot, restore PVC
// with a dataSource, GC and deletion.
func TestEnvtestVolumeSnapshotStrategy(t *testing.T) {
	c := envtestCluster(t)
	c.ensureClusterObjects(t)
	ns := c.namespace(t, "vsnap")
	ctx := ctxT(t)
	coding := snapshotConfig(ns)

	// Without the CRDs (they are installed once per run, so only check when
	// nothing installed them yet) the strategy is unsupported, not broken.
	if _, err := c.client.Discovery().ServerResourcesForGroupVersion(snapshotGroup + "/v1"); err != nil {
		provider := c.provider(t, coding, nil, nil)
		if _, err := provider.EnsureVolume(ctx, sandbox.VolumeSpec{ID: "nocrd"}); err != nil {
			t.Fatal(err)
		}
		if _, err := provider.CreateSnapshot(ctx, sandbox.SnapshotSpec{ID: "nocrd", VolumeID: "nocrd"}); !errors.Is(err, sandbox.ErrUnsupported) {
			t.Fatalf("create without CRD = %v", err)
		}
		if _, err := provider.SnapshotStatus(ctx, "nocrd"); !errors.Is(err, sandbox.ErrUnsupported) {
			t.Fatalf("status without CRD = %v", err)
		}
		if err := provider.DeleteSnapshot(ctx, "nocrd"); err != nil {
			t.Fatalf("delete without CRD = %v", err)
		}
		if _, err := provider.ListManaged(ctx, sandbox.ListFilter{}); err != nil {
			t.Fatalf("list without CRD = %v", err)
		}
		for _, result := range provider.Preflight(ctx) {
			if result.Name == "volume-snapshots" && result.OK {
				t.Fatal("preflight must report the missing CRD")
			}
		}
	}
	c.installSnapshotCRDs(t)
	class := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": snapshotGroup + "/v1", "kind": "VolumeSnapshotClass",
		"metadata": map[string]any{"name": "csi-snapclass"}, "driver": "example.com/csi", "deletionPolicy": "Delete",
	}}
	if _, err := c.dynamic.Resource(volumeSnapshotClassGVR).Create(ctx, class, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	provider := c.provider(t, coding, nil, nil)
	results := map[string]sandbox.PreflightResult{}
	for _, result := range provider.Preflight(ctx) {
		results[result.Name] = result
	}
	if !results["volume-snapshots"].OK || !results["volume-snapshot-class"].OK || !results["storage-class"].OK {
		t.Fatalf("preflight = %+v", results)
	}

	strategy, err := sandbox.NewStrategy(coding, provider, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshotID, workspaceKey := "snap1", "ws-snap"
	plan, err := strategy.PlanBuild(ctx, snapshotID, workspaceKey)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := strategy.PlanBuild(ctx, snapshotID, workspaceKey); err != nil || again.StorageRef != plan.StorageRef {
		t.Fatalf("plan is not idempotent: %+v %v", again, err)
	}
	// The build sandbox mounts the build volume; its PVC is kept on stop.
	build := envSpec("vbuild1", "small")
	build.Kind, build.SessionID, build.SnapshotID, build.Workspace = sandbox.KindSnapshot, "", snapshotID, plan.Workspace
	if _, err := provider.StartSandbox(ctx, build); err != nil {
		t.Fatal(err)
	}
	pod, _ := c.client.CoreV1().Pods(ns).Get(ctx, PodName(build.ID), metav1.GetOptions{})
	if volume := volumeNamed(pod, "workspace"); volume.PersistentVolumeClaim == nil || volume.PersistentVolumeClaim.ClaimName != PVCName(snapshotID) {
		t.Fatalf("build workspace = %+v", volume)
	}
	if err := provider.StopSandbox(ctx, build.ID, sandbox.StopOptions{Grace: time.Second, KeepVolume: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.client.CoreV1().PersistentVolumeClaims(ns).Get(ctx, PVCName(snapshotID), metav1.GetOptions{}); err != nil {
		t.Fatalf("the build volume must survive the build sandbox: %v", err)
	}

	// Finish creates the VolumeSnapshot and waits for readyToUse.
	if _, _, err := strategy.Finish(ctx, snapshotID, plan); !errors.Is(err, sandbox.ErrPending) {
		t.Fatalf("finish before ready = %v", err)
	}
	if _, _, err := strategy.Finish(ctx, snapshotID, plan); !errors.Is(err, sandbox.ErrPending) {
		t.Fatalf("finish must be repeatable: %v", err)
	}
	object, err := c.dynamic.Resource(volumeSnapshotGVR).Namespace(ns).Get(ctx, SnapshotName(snapshotID), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if source, _, _ := unstructured.NestedString(object.Object, "spec", "source", "persistentVolumeClaimName"); source != PVCName(snapshotID) {
		t.Fatalf("snapshot source = %q", source)
	}
	if className, _, _ := unstructured.NestedString(object.Object, "spec", "volumeSnapshotClassName"); className != "csi-snapclass" {
		t.Fatalf("snapshot class = %q", className)
	}
	if object.GetLabels()[sandbox.LabelSnapshotID] != snapshotID || object.GetLabels()[sandbox.LabelInstance] != "flow" {
		t.Fatalf("snapshot labels = %v", object.GetLabels())
	}
	c.markSnapshotReady(t, ns, SnapshotName(snapshotID), map[string]any{"readyToUse": true, "restoreSize": "25Gi", "creationTime": "2026-10-18T10:00:00Z"})
	ref, size, err := strategy.Finish(ctx, snapshotID, plan)
	if err != nil || ref != SnapshotName(snapshotID) || size != 25<<30 {
		t.Fatalf("finish = %q %d %v", ref, size, err)
	}
	if pvc, err := c.client.CoreV1().PersistentVolumeClaims(ns).Get(ctx, PVCName(snapshotID), metav1.GetOptions{}); err == nil && pvc.DeletionTimestamp == nil {
		t.Fatal("finish must delete the build volume")
	}
	if status, err := provider.SnapshotStatus(ctx, snapshotID); err != nil || !status.Ready || status.CreatedAt.Year() != 2026 {
		t.Fatalf("snapshot status = %+v %v", status, err)
	}

	// Restore: a PVC whose dataSource is the snapshot, at least restoreSize.
	session := envSpec("vsess1", "small")
	session.WorkspaceKey = workspaceKey
	source, err := strategy.Restore(ctx, ref, session.ID, workspaceKey)
	if err != nil || source.VolumeID != session.ID {
		t.Fatalf("restore = %+v %v", source, err)
	}
	if again, err := strategy.Restore(ctx, ref, session.ID, workspaceKey); err != nil || again != source {
		t.Fatalf("restore is not idempotent: %+v %v", again, err)
	}
	restored, _ := c.client.CoreV1().PersistentVolumeClaims(ns).Get(ctx, PVCName(session.ID), metav1.GetOptions{})
	if ds := restored.Spec.DataSource; ds == nil || ds.Kind != "VolumeSnapshot" || *ds.APIGroup != snapshotGroup || ds.Name != SnapshotName(snapshotID) {
		t.Fatalf("restored dataSource = %+v", restored.Spec.DataSource)
	}
	if ref := restored.Spec.DataSourceRef; ref == nil || ref.Name != SnapshotName(snapshotID) {
		t.Fatalf("the API server mirrors dataSource into dataSourceRef: %+v", ref)
	}
	if got := restored.Spec.Resources.Requests[corev1.ResourceStorage]; got.Cmp(resource.MustParse("25Gi")) != 0 {
		t.Fatalf("restored size = %s, want the restoreSize 25Gi", got.String())
	}
	session.Workspace = source
	if _, err := provider.StartSandbox(ctx, session); err != nil {
		t.Fatal(err)
	}
	// A restore from a snapshot that is not ready is pending.
	if _, err := provider.CreateSnapshot(ctx, sandbox.SnapshotSpec{ID: "snap2", VolumeID: session.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.RestoreWorkspace(ctx, sandbox.RestoreSpec{VolumeID: "vsess2", SnapshotID: "snap2"}); !errors.Is(err, sandbox.ErrPending) {
		t.Fatalf("restore of an unready snapshot = %v", err)
	}
	// A failed snapshot fails the build.
	c.markSnapshotReady(t, ns, SnapshotName("snap2"), map[string]any{"readyToUse": false, "error": map[string]any{"message": "csi: snapshot failed"}})
	if _, _, err := strategy.Finish(ctx, "snap2", sandbox.BuildPlan{Volume: &sandbox.VolumeSpec{ID: session.ID}}); err == nil || !strings.Contains(err.Error(), "csi: snapshot failed") {
		t.Fatalf("finish of a failed snapshot = %v", err)
	}
	if _, err := provider.CreateSnapshot(ctx, sandbox.SnapshotSpec{ID: "snap3", VolumeID: "missing"}); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("snapshot of a missing volume = %v", err)
	}

	// ListManaged and GC see the snapshot objects.
	objects, err := provider.ListManaged(ctx, sandbox.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	snapshots := 0
	for _, object := range objects {
		if object.Type == TypeVolumeSnapshot {
			snapshots++
		}
	}
	if snapshots != 2 {
		t.Fatalf("listed %d volumesnapshots: %+v", snapshots, objects)
	}
	store := newEnvStore(time.Now)
	store.snapshots[snapshotID] = domain.CodingSnapshotReady
	store.add(sandbox.Record{ID: session.ID, Kind: sandbox.KindSession, Desired: sandbox.DesiredRunning})
	gc := &sandbox.GC{Store: store, Provider: provider, Config: coding, Now: func() time.Time { return time.Now().Add(time.Hour) }}
	if _, err := gc.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.SnapshotStatus(ctx, snapshotID); err != nil {
		t.Fatalf("a ready snapshot must survive GC: %v", err)
	}
	if _, err := provider.SnapshotStatus(ctx, "snap2"); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("an orphan snapshot must be collected: %v", err)
	}

	if err := strategy.Delete(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if err := strategy.Delete(ctx, ref); err != nil {
		t.Fatalf("delete must be idempotent: %v", err)
	}
	if _, err := provider.SnapshotStatus(ctx, snapshotID); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("status after delete = %v", err)
	}
}

// TestEnvtestArchiveStrategy builds with an emptyDir and restores through
// the restore init container (archive strategy, local object store).
func TestEnvtestArchiveStrategy(t *testing.T) {
	c := envtestCluster(t)
	ns := c.namespace(t, "archive")
	ctx := ctxT(t)
	coding := envConfig(ns)
	objects, err := objectstore.Open(ctx, objectstore.Config{Driver: "local", LocalPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	provider := c.provider(t, coding, nil, nil)
	strategy, err := sandbox.NewStrategy(coding, provider, objects)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := strategy.PlanBuild(ctx, "asnap1", "ws-a")
	if err != nil || plan.Volume != nil || plan.UploadURL == "" {
		t.Fatalf("plan = %+v %v", plan, err)
	}
	build := envSpec("abuild1", "medium")
	build.Kind, build.SessionID, build.SnapshotID, build.Workspace = sandbox.KindSnapshot, "", "asnap1", plan.Workspace
	if _, err := provider.StartSandbox(ctx, build); err != nil {
		t.Fatal(err)
	}
	if claims, _ := c.client.CoreV1().PersistentVolumeClaims(ns).List(ctx, metav1.ListOptions{}); len(claims.Items) != 0 {
		t.Fatalf("the archive strategy must not create PVCs: %d", len(claims.Items))
	}
	archive := strategy.(*sandbox.ArchiveStrategy)
	if _, _, _, err := archive.Store(ctx, "asnap1", "ws-a", bytes.NewReader([]byte{0x28, 0xb5, 0x2f, 0xfd, 1, 2, 3})); err != nil {
		t.Fatal(err)
	}
	ref, _, err := strategy.Finish(ctx, "asnap1", plan)
	if err != nil {
		t.Fatal(err)
	}
	source, err := strategy.Restore(ctx, ref, "asess1", "ws-a")
	if err != nil || source.ArchiveURL == "" {
		t.Fatalf("restore = %+v %v", source, err)
	}
	session := envSpec("asess1", "medium")
	session.Workspace = source
	if _, err := provider.StartSandbox(ctx, session); err != nil {
		t.Fatal(err)
	}
	pod, _ := c.client.CoreV1().Pods(ns).Get(ctx, PodName(session.ID), metav1.GetOptions{})
	if len(pod.Spec.InitContainers) != 1 || envMap(pod.Spec.InitContainers[0].Env)[sandbox.EnvArchiveURL] != source.ArchiveURL {
		t.Fatalf("restore init container = %+v", pod.Spec.InitContainers)
	}
	if env := envMap(pod.Spec.InitContainers[0].Env); env["HTTPS_PROXY"] != "" {
		t.Fatal("the restore step must not use the egress proxy")
	}
}

// TestEnvtestOrphanGC deletes objects no live row owns and never touches
// another instance's objects.
func TestEnvtestOrphanGC(t *testing.T) {
	c := envtestCluster(t)
	c.ensureClusterObjects(t)
	ns := c.namespace(t, "gc")
	ctx := ctxT(t)
	coding := envConfig(ns)
	coding.Kubernetes.WorkspaceVolume = "pvc"
	coding.Kubernetes.StorageClass = "fast"
	coding.OrphanGrace = 10 * time.Minute
	provider := c.provider(t, coding, nil, nil)
	other := coding
	other.InstanceID = "flow-staging"
	foreign := c.provider(t, other, nil, nil)
	for _, id := range []string{"live1", "orphan1", "stopped1"} {
		if _, err := provider.StartSandbox(ctx, envSpec(id, "small")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := foreign.StartSandbox(ctx, envSpec("foreign1", "small")); err != nil {
		t.Fatal(err)
	}
	// A secret left behind by a start that crashed before the Pod.
	if err := provider.ensureTokenSecret(ctx, envSpec("crashed1", "small")); err != nil {
		t.Fatal(err)
	}
	// A build volume of a snapshot whose row is gone.
	if _, err := provider.EnsureVolume(ctx, sandbox.VolumeSpec{ID: "gone1", Labels: map[string]string{sandbox.LabelSnapshotID: "gone1"}}); err != nil {
		t.Fatal(err)
	}

	store := newEnvStore(time.Now)
	store.add(sandbox.Record{ID: "live1", Kind: sandbox.KindSession, Desired: sandbox.DesiredRunning})
	store.add(sandbox.Record{ID: "stopped1", Kind: sandbox.KindSession, Desired: sandbox.DesiredStopped, Phase: sandbox.PhaseMissing})

	// Within the grace nothing is touched.
	young := &sandbox.GC{Store: store, Provider: provider, Config: coding}
	if deleted, err := young.RunOnce(ctx); err != nil || deleted != 0 {
		t.Fatalf("young objects: deleted %d %v", deleted, err)
	}
	gc := &sandbox.GC{Store: store, Provider: provider, Config: coding, Now: func() time.Time { return time.Now().Add(time.Hour) }}
	deleted, err := gc.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// orphan1 and stopped1: pod, secret, pvc each; crashed1's secret; gone1's pvc.
	if deleted != 8 {
		t.Fatalf("deleted %d objects", deleted)
	}
	left, err := provider.ListManaged(ctx, sandbox.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, object := range left {
		names = append(names, object.Type+"/"+object.Name)
	}
	slices.Sort(names)
	want := []string{"pod/" + PodName("live1"), "pvc/" + PVCName("live1"), "secret/" + TokenSecretName("live1")}
	if !slices.Equal(names, want) {
		t.Fatalf("left = %v, want %v", names, want)
	}
	if objects, _ := foreign.ListManaged(ctx, sandbox.ListFilter{}); len(objects) != 3 {
		t.Fatalf("the other instance's objects must be untouched: %+v", objects)
	}
	// DeleteManaged refuses an object labelled for another instance.
	for _, object := range must(foreign.ListManaged(ctx, sandbox.ListFilter{})) {
		if err := provider.DeleteManaged(ctx, object); err != nil {
			t.Fatal(err)
		}
	}
	if objects, _ := foreign.ListManaged(ctx, sandbox.ListFilter{}); len(objects) != 3 {
		t.Fatal("DeleteManaged deleted a foreign object")
	}
	if again, err := gc.RunOnce(ctx); err != nil || again != 0 {
		t.Fatalf("a second pass deleted %d %v", again, err)
	}
}

func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

// TestEnvtestWarmPoolReconcileAfterRestart runs the warm pool and the
// sandbox reconciler, restarts them mid-flight, binds a warm sandbox and
// stops it.
func TestEnvtestWarmPoolReconcileAfterRestart(t *testing.T) {
	c := envtestCluster(t)
	ns := c.namespace(t, "warm")
	ctx := ctxT(t)
	coding := envConfig(ns)
	coding.WarmPool = map[string]int{"small": 1}
	coding.Timeouts.Start = 10 * time.Minute
	store := newEnvStore(time.Now)
	pool := &sandbox.WarmPool{Store: store, Config: coding}
	if err := pool.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	ids := store.ids()
	if len(ids) != 1 {
		t.Fatalf("warm rows = %v", ids)
	}
	warmID := ids[0]

	rec1 := &recorder{}
	first := &sandbox.Reconciler{Store: store, Provider: c.provider(t, coding, rec1.wrap(c.admin), nil), Config: coding, Owner: "replica-1"}
	if _, err := first.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if record := store.get(warmID); record.Phase != sandbox.PhasePending || record.ProviderRef != PodName(warmID) || record.Attempts != 1 {
		t.Fatalf("after start: %+v", record)
	}
	pod, err := c.client.CoreV1().Pods(ns).Get(ctx, PodName(warmID), metav1.GetOptions{})
	if err != nil || pod.Labels[sandbox.LabelKind] != "warm" || pod.Labels[sandbox.LabelSize] != "small" || *pod.Spec.ActiveDeadlineSeconds < int64((coding.WarmPoolMaxAge+coding.Timeouts.MaxSession).Seconds())-120 {
		t.Fatalf("warm pod = %+v %v", pod, err)
	}

	// Flow crashed after creating the Pod but before saving: the row looks
	// never started. A new process (new provider, new reconciler) adopts.
	store.update(warmID, func(r *sandbox.Record) { r.Phase, r.ProviderRef, r.Attempts = sandbox.PhasePending, "", 0 })
	rec2 := &recorder{}
	second := &sandbox.Reconciler{Store: store, Provider: c.provider(t, coding, rec2.wrap(c.admin), nil), Config: coding, Owner: "replica-2"}
	if _, err := second.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if n := rec2.count("POST", "/pods"); n != 0 {
		t.Fatalf("the restarted reconciler created %d pods", n)
	}
	if record := store.get(warmID); record.ProviderRef != PodName(warmID) || record.Phase != sandbox.PhasePending {
		t.Fatalf("adopted: %+v", record)
	}
	started := metav1.NewTime(time.Now().Truncate(time.Second))
	c.setPodStatus(t, ns, PodName(warmID), func(s *corev1.PodStatus) {
		s.Phase = corev1.PodRunning
		s.ContainerStatuses = []corev1.ContainerStatus{{Name: runnerContainer, Image: "x", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: started}}}}
	})
	if _, err := second.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if record := store.get(warmID); record.Phase != sandbox.PhaseRunning || record.StartedAt == nil {
		t.Fatalf("running: %+v", record)
	}

	// Bind it to a session (BindWarmCodingSandbox); the pool refills.
	store.update(warmID, func(r *sandbox.Record) { r.SessionID = "sess1" })
	if err := pool.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(store.ids()) != 2 {
		t.Fatalf("the pool must refill after a bind: %v", store.ids())
	}
	if _, err := second.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if pods, _ := c.client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: sandbox.LabelKind + "=warm"}); len(pods.Items) != 2 {
		t.Fatalf("warm pods = %d", len(pods.Items))
	}
	// The bound sandbox is live for GC even though its Pod still says warm.
	gc := &sandbox.GC{Store: store, Provider: second.Provider, Config: coding, Now: func() time.Time { return time.Now().Add(time.Hour) }}
	if deleted, err := gc.RunOnce(ctx); err != nil || deleted != 0 {
		t.Fatalf("GC deleted %d live objects: %v", deleted, err)
	}

	// The session ends: desired=stopped deletes the Pod and its Secret.
	store.update(warmID, func(r *sandbox.Record) { r.Desired = sandbox.DesiredStopped })
	for range 2 {
		if _, err := second.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if record := store.get(warmID); record.Phase != sandbox.PhaseMissing {
		t.Fatalf("stopped: %+v", record)
	}
	if _, err := c.client.CoreV1().Pods(ns).Get(ctx, PodName(warmID), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("the bound warm pod must be deleted: %v", err)
	}
	if _, err := c.client.CoreV1().Secrets(ns).Get(ctx, TokenSecretName(warmID), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("its token secret must be deleted: %v", err)
	}
}

// TestEnvtestServerErrorsBecomeErrorCodes runs the reconciler against
// API-server rejections and checks the session error code each one ends in.
func TestEnvtestServerErrorsBecomeErrorCodes(t *testing.T) {
	c := envtestCluster(t)
	c.ensureClusterObjects(t)
	ctx := ctxT(t)

	run := func(t *testing.T, ns string, coding config.CodingConfig, record sandbox.Record, clock *time.Time) (*envStore, *sandbox.Reconciler) {
		store := newEnvStore(func() time.Time { return *clock })
		store.add(record)
		reconciler := &sandbox.Reconciler{Store: store, Provider: c.provider(t, coding, nil, nil), Config: coding, Owner: "r", Now: func() time.Time { return *clock }}
		if _, err := reconciler.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		return store, reconciler
	}
	code := func(record sandbox.Record) domain.CodingErrorCode {
		return sandbox.ErrorCode(record.Kind, record.Phase, record.Reason)
	}

	t.Run("missing RuntimeClass", func(t *testing.T) {
		ns := c.namespace(t, "err-rc")
		coding := envConfig(ns)
		coding.Kubernetes.RuntimeClassName = "does-not-exist"
		now := time.Now()
		store, _ := run(t, ns, coding, sandbox.Record{ID: "e1", Kind: sandbox.KindSession, Desired: sandbox.DesiredRunning, Size: "small"}, &now)
		record := store.get("e1")
		if record.Phase != sandbox.PhaseFailed || record.Reason != sandbox.ReasonStartFailed || code(record) != domain.CodingErrSandboxStartFailed || !strings.Contains(record.Message, "RuntimeClass") {
			t.Fatalf("record = %+v (%s)", record, code(record))
		}
		for _, result := range c.provider(t, coding, nil, nil).Preflight(ctx) {
			if result.Name == "runtime-class" && result.OK {
				t.Fatal("preflight must report the missing RuntimeClass")
			}
		}
	})

	t.Run("invalid object", func(t *testing.T) {
		ns := c.namespace(t, "err-invalid")
		coding := envConfig(ns)
		now := time.Now()
		// A region that is not a valid label value: 422 Invalid.
		store, _ := run(t, ns, coding, sandbox.Record{ID: "e2", Kind: sandbox.KindSession, Desired: sandbox.DesiredRunning, Size: "small", Region: "eu west/1"}, &now)
		record := store.get("e2")
		if record.Phase != sandbox.PhaseFailed || code(record) != domain.CodingErrSandboxStartFailed || record.Attempts != 1 {
			t.Fatalf("record = %+v (%s)", record, code(record))
		}
	})

	t.Run("LimitRange maximum", func(t *testing.T) {
		ns := c.namespace(t, "err-limits")
		limits := &corev1.LimitRange{ObjectMeta: metav1.ObjectMeta{Name: "max"}, Spec: corev1.LimitRangeSpec{Limits: []corev1.LimitRangeItem{{
			Type: corev1.LimitTypeContainer, Max: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")},
		}}}}
		if _, err := c.client.CoreV1().LimitRanges(ns).Create(ctx, limits, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		store, _ := run(t, ns, envConfig(ns), sandbox.Record{ID: "e3", Kind: sandbox.KindSession, Desired: sandbox.DesiredRunning, Size: "large"}, &now)
		if record := store.get("e3"); record.Phase != sandbox.PhaseFailed || code(record) != domain.CodingErrSandboxStartFailed {
			t.Fatalf("record = %+v (%s)", record, code(record))
		}
	})

	quota := func(t *testing.T, ns string, hard, used corev1.ResourceList) {
		t.Helper()
		object, err := c.client.CoreV1().ResourceQuotas(ns).Create(ctx, &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "q"}, Spec: corev1.ResourceQuotaSpec{Hard: hard}}, metav1.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		// The quota controller is not running: write its status.
		object.Status = corev1.ResourceQuotaStatus{Hard: hard, Used: used}
		if _, err := c.client.CoreV1().ResourceQuotas(ns).UpdateStatus(ctx, object, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		used corev1.ResourceList
	}{
		{"quota exceeded", corev1.ResourceList{corev1.ResourcePods: resource.MustParse("1")}},
		// Right after a quota is created or changed, before the quota
		// controller computed usage: also capacity, not a broken sandbox.
		{"quota status unknown", corev1.ResourceList{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ns := c.namespace(t, "err-quota")
			quota(t, ns, corev1.ResourceList{corev1.ResourcePods: resource.MustParse("1")}, tc.used)
			coding := envConfig(ns)
			coding.Timeouts.Start = 5 * time.Minute
			now := time.Now()
			store, reconciler := run(t, ns, coding, sandbox.Record{ID: "e4", Kind: sandbox.KindSession, Desired: sandbox.DesiredRunning, Size: "small", CreatedAt: now}, &now)
			record := store.get("e4")
			if record.Phase != sandbox.PhasePending || record.Reason != sandbox.ReasonQuota || record.Attempts != 0 {
				t.Fatalf("quota rejection must wait without spending attempts: %+v", record)
			}
			// Past the start budget it fails as capacityUnavailable.
			now = now.Add(coding.Timeouts.Start + time.Hour)
			if _, err := reconciler.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			if record := store.get("e4"); record.Phase != sandbox.PhaseFailed || code(record) != domain.CodingErrCapacityUnavailable {
				t.Fatalf("record = %+v (%s)", record, code(record))
			}
		})
	}

	t.Run("unschedulable and image pull", func(t *testing.T) {
		ns := c.namespace(t, "err-sched")
		coding := envConfig(ns)
		coding.Timeouts.Start = 5 * time.Minute
		for _, tc := range []struct {
			id     string
			status corev1.PodStatus
			want   domain.CodingErrorCode
		}{
			{"e5", corev1.PodStatus{Phase: corev1.PodPending, Conditions: []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: corev1.PodReasonUnschedulable, Message: "0/3 nodes: untolerated taint"}}}, domain.CodingErrUnschedulable},
			{"e6", corev1.PodStatus{Phase: corev1.PodPending, ContainerStatuses: []corev1.ContainerStatus{{Name: runnerContainer, Image: "x", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}}}}}, domain.CodingErrImagePullFailed},
		} {
			now := time.Now()
			store, reconciler := run(t, ns, coding, sandbox.Record{ID: tc.id, Kind: sandbox.KindSession, Desired: sandbox.DesiredRunning, Size: "small", CreatedAt: now}, &now)
			c.setPodStatus(t, ns, PodName(tc.id), func(s *corev1.PodStatus) { *s = tc.status })
			now = now.Add(coding.Timeouts.Start + time.Minute)
			if _, err := reconciler.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			record := store.get(tc.id)
			if record.Phase != sandbox.PhaseFailed || code(record) != tc.want {
				t.Fatalf("%s: record = %+v (%s), want %s", tc.id, record, code(record), tc.want)
			}
			if _, err := c.client.CoreV1().Pods(ns).Get(ctx, PodName(tc.id), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
				t.Fatalf("%s: a sandbox past its start budget must be stopped: %v", tc.id, err)
			}
		}
	})

	t.Run("pod deleted while running", func(t *testing.T) {
		ns := c.namespace(t, "err-lost")
		coding := envConfig(ns)
		now := time.Now()
		store, reconciler := run(t, ns, coding, sandbox.Record{ID: "e7", Kind: sandbox.KindSession, Desired: sandbox.DesiredRunning, Size: "small"}, &now)
		c.setPodStatus(t, ns, PodName("e7"), func(s *corev1.PodStatus) {
			s.Phase = corev1.PodRunning
			s.ContainerStatuses = []corev1.ContainerStatus{{Name: runnerContainer, Image: "x", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.Now()}}}}
		})
		if _, err := reconciler.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		zero := int64(0)
		if err := c.client.CoreV1().Pods(ns).Delete(ctx, PodName("e7"), metav1.DeleteOptions{GracePeriodSeconds: &zero}); err != nil {
			t.Fatal(err)
		}
		if _, err := reconciler.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		if record := store.get("e7"); record.Phase != sandbox.PhaseMissing || code(record) != domain.CodingErrSandboxLost {
			t.Fatalf("record = %+v (%s)", record, code(record))
		}
	})
}

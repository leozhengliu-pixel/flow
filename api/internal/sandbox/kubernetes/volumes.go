package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"flow/api/internal/sandbox"
)

// VolumeSnapshot is a CRD (snapshot.storage.k8s.io/v1, GA since
// external-snapshotter v4); it is used through the dynamic client so Flow
// needs no snapshot client library.
const snapshotGroup = "snapshot.storage.k8s.io"

var (
	volumeSnapshotGVR      = schema.GroupVersionResource{Group: snapshotGroup, Version: "v1", Resource: "volumesnapshots"}
	volumeSnapshotClassGVR = schema.GroupVersionResource{Group: snapshotGroup, Version: "v1", Resource: "volumesnapshotclasses"}
)

// ensurePVC creates a workspace PVC if missing and returns it.
func (p *Provider) ensurePVC(ctx context.Context, volumeID, size, storageClass string, labels map[string]string, fromSnapshot string) (*corev1.PersistentVolumeClaim, error) {
	if !sandbox.ValidID(volumeID) {
		return nil, fmt.Errorf("kubernetes: invalid volume id %q", volumeID)
	}
	pvc, err := p.BuildPVC(volumeID, size, storageClass, labels, fromSnapshot)
	if err != nil {
		return nil, err
	}
	api := p.client.CoreV1().PersistentVolumeClaims(p.k8s.Namespace)
	created, err := api.Create(ctx, pvc, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		created, err = api.Get(ctx, pvc.Name, metav1.GetOptions{})
	}
	if err != nil {
		return nil, wrap("create pvc", err)
	}
	if err := usablePVC(created); err != nil {
		return nil, err
	}
	return created, nil
}

// usablePVC refuses a claim that is being deleted: the
// kubernetes.io/pvc-protection finalizer keeps it until its last Pod is gone,
// and a new Pod that mounts it never starts. Retry once it is gone.
func usablePVC(pvc *corev1.PersistentVolumeClaim) error {
	if pvc.DeletionTimestamp != nil {
		return fmt.Errorf("kubernetes: pvc %s is being deleted: %w", pvc.Name, sandbox.ErrTransient)
	}
	return nil
}

func volumeStatus(id string, pvc *corev1.PersistentVolumeClaim) sandbox.VolumeStatus {
	status := sandbox.VolumeStatus{
		ID:          id,
		ProviderRef: pvc.Name,
		// WaitForFirstConsumer classes keep a claim Pending until a Pod uses
		// it, so callers must not wait for Ready before starting the Pod.
		Ready:  pvc.Status.Phase == corev1.ClaimBound,
		Phase:  string(pvc.Status.Phase),
		Labels: maps.Clone(pvc.Labels),
	}
	if pvc.DeletionTimestamp != nil {
		status.Ready, status.Phase = false, "Terminating"
	}
	return status
}

func (p *Provider) EnsureVolume(ctx context.Context, spec sandbox.VolumeSpec) (sandbox.VolumeStatus, error) {
	labels := maps.Clone(spec.Labels)
	if labels == nil {
		labels = map[string]string{}
	}
	if ws := sandbox.WorkspaceLabel(spec.WorkspaceKey); ws != "" {
		labels[sandbox.LabelWorkspace] = ws
	}
	pvc, err := p.ensurePVC(ctx, spec.ID, spec.Size, spec.StorageClass, labels, "")
	if err != nil {
		return sandbox.VolumeStatus{}, err
	}
	return volumeStatus(spec.ID, pvc), nil
}

// RestoreWorkspace creates a PVC whose dataSource is a ready VolumeSnapshot.
func (p *Provider) RestoreWorkspace(ctx context.Context, spec sandbox.RestoreSpec) (sandbox.VolumeStatus, error) {
	if !p.Capabilities().VolumeSnapshots {
		return sandbox.VolumeStatus{}, fmt.Errorf("kubernetes: %w: restore needs FLOW_CODING_SNAPSHOT_STRATEGY=volumesnapshot", sandbox.ErrUnsupported)
	}
	if existing, err := p.client.CoreV1().PersistentVolumeClaims(p.k8s.Namespace).Get(ctx, PVCName(spec.VolumeID), metav1.GetOptions{}); err == nil {
		if err := usablePVC(existing); err != nil {
			return sandbox.VolumeStatus{}, err
		}
		return volumeStatus(spec.VolumeID, existing), nil
	}
	snapshot, err := p.SnapshotStatus(ctx, spec.SnapshotID)
	if err != nil {
		return sandbox.VolumeStatus{}, err
	}
	if !snapshot.Ready {
		return sandbox.VolumeStatus{}, fmt.Errorf("kubernetes: snapshot %s is not ready: %w", spec.SnapshotID, sandbox.ErrPending)
	}
	size := spec.Size
	if size == "" {
		size = p.workspace.String()
	}
	// The claim must be at least the snapshot's restore size.
	if snapshot.SizeBytes > 0 {
		if requested, err := resource.ParseQuantity(size); err == nil && requested.Value() < snapshot.SizeBytes {
			size = resource.NewQuantity(snapshot.SizeBytes, resource.BinarySI).String()
		}
	}
	labels := maps.Clone(spec.Labels)
	if labels == nil {
		labels = map[string]string{}
	}
	labels[sandbox.LabelSnapshotID] = spec.SnapshotID
	if ws := sandbox.WorkspaceLabel(spec.WorkspaceKey); ws != "" {
		labels[sandbox.LabelWorkspace] = ws
	}
	pvc, err := p.ensurePVC(ctx, spec.VolumeID, size, spec.StorageClass, labels, spec.SnapshotID)
	if err != nil {
		return sandbox.VolumeStatus{}, err
	}
	return volumeStatus(spec.VolumeID, pvc), nil
}

func (p *Provider) VolumeStatus(ctx context.Context, volumeID string) (sandbox.VolumeStatus, error) {
	pvc, err := p.client.CoreV1().PersistentVolumeClaims(p.k8s.Namespace).Get(ctx, PVCName(volumeID), metav1.GetOptions{})
	if err != nil {
		return sandbox.VolumeStatus{}, wrap("get pvc", err)
	}
	return volumeStatus(volumeID, pvc), nil
}

func (p *Provider) DeleteVolume(ctx context.Context, volumeID string) error {
	err := p.client.CoreV1().PersistentVolumeClaims(p.k8s.Namespace).Delete(ctx, PVCName(volumeID), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return wrap("delete pvc", err)
	}
	return nil
}

func (p *Provider) snapshotsEnabled() error {
	if !p.Capabilities().VolumeSnapshots || p.dynamic == nil {
		return fmt.Errorf("kubernetes: %w: VolumeSnapshots need FLOW_CODING_SNAPSHOT_STRATEGY=volumesnapshot", sandbox.ErrUnsupported)
	}
	return nil
}

// BuildVolumeSnapshot is the VolumeSnapshot object of a snapshot.
func (s settings) BuildVolumeSnapshot(spec sandbox.SnapshotSpec) *unstructured.Unstructured {
	class := spec.Class
	if class == "" {
		class = s.coding.Snapshot.VolumeSnapshotClass
	}
	body := map[string]any{"source": map[string]any{"persistentVolumeClaimName": PVCName(spec.VolumeID)}}
	if class != "" {
		body["volumeSnapshotClassName"] = class
	}
	labels := map[string]any{}
	for key, value := range s.managedLabels(mergeSnapshotLabel(spec)) {
		labels[key] = value
	}
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": snapshotGroup + "/v1",
		"kind":       "VolumeSnapshot",
		"metadata":   map[string]any{"name": SnapshotName(spec.ID), "namespace": s.k8s.Namespace, "labels": labels},
		"spec":       body,
	}}
	if annotations := s.annotations(nil); len(annotations) > 0 {
		values := map[string]any{}
		for key, value := range annotations {
			values[key] = value
		}
		_ = unstructured.SetNestedField(object.Object, values, "metadata", "annotations")
	}
	return object
}

func mergeSnapshotLabel(spec sandbox.SnapshotSpec) map[string]string {
	labels := maps.Clone(spec.Labels)
	if labels == nil {
		labels = map[string]string{}
	}
	labels[sandbox.LabelSnapshotID] = spec.ID
	return labels
}

// CreateSnapshot creates a VolumeSnapshot of a workspace PVC.
func (p *Provider) CreateSnapshot(ctx context.Context, spec sandbox.SnapshotSpec) (sandbox.SnapshotStatus, error) {
	if err := p.snapshotsEnabled(); err != nil {
		return sandbox.SnapshotStatus{}, err
	}
	if !sandbox.ValidID(spec.ID) {
		return sandbox.SnapshotStatus{}, fmt.Errorf("kubernetes: invalid snapshot id %q", spec.ID)
	}
	api := p.dynamic.Resource(volumeSnapshotGVR).Namespace(p.k8s.Namespace)
	if existing, err := api.Get(ctx, SnapshotName(spec.ID), metav1.GetOptions{}); err == nil {
		return snapshotStatus(spec.ID, existing), nil
	}
	if _, err := p.client.CoreV1().PersistentVolumeClaims(p.k8s.Namespace).Get(ctx, PVCName(spec.VolumeID), metav1.GetOptions{}); err != nil {
		return sandbox.SnapshotStatus{}, wrap("snapshot source volume "+spec.VolumeID, err)
	}
	created, err := api.Create(ctx, p.BuildVolumeSnapshot(spec), metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		created, err = api.Get(ctx, SnapshotName(spec.ID), metav1.GetOptions{})
	}
	if err != nil {
		return sandbox.SnapshotStatus{}, wrapSnapshot("create volumesnapshot", err)
	}
	return snapshotStatus(spec.ID, created), nil
}

func (p *Provider) SnapshotStatus(ctx context.Context, snapshotID string) (sandbox.SnapshotStatus, error) {
	if err := p.snapshotsEnabled(); err != nil {
		return sandbox.SnapshotStatus{}, err
	}
	object, err := p.dynamic.Resource(volumeSnapshotGVR).Namespace(p.k8s.Namespace).Get(ctx, SnapshotName(snapshotID), metav1.GetOptions{})
	if err != nil {
		return sandbox.SnapshotStatus{}, wrapSnapshot("get volumesnapshot", err)
	}
	return snapshotStatus(snapshotID, object), nil
}

func (p *Provider) DeleteSnapshot(ctx context.Context, snapshotID string) error {
	if err := p.snapshotsEnabled(); err != nil {
		return err
	}
	err := p.dynamic.Resource(volumeSnapshotGVR).Namespace(p.k8s.Namespace).Delete(ctx, SnapshotName(snapshotID), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) && !meta.IsNoMatchError(err) {
		return wrapSnapshot("delete volumesnapshot", err)
	}
	return nil
}

// wrapSnapshot also maps a missing CRD to ErrUnsupported.
func wrapSnapshot(op string, err error) error {
	if meta.IsNoMatchError(err) || (apierrors.IsNotFound(err) && strings.Contains(err.Error(), "the server could not find the requested resource")) {
		return fmt.Errorf("%s: %w: VolumeSnapshot CRD is not installed", op, sandbox.ErrUnsupported)
	}
	return wrap(op, err)
}

func snapshotStatus(id string, object *unstructured.Unstructured) sandbox.SnapshotStatus {
	status := sandbox.SnapshotStatus{ID: id, ProviderRef: object.GetName(), CreatedAt: object.GetCreationTimestamp().Time}
	if ready, found, _ := unstructured.NestedBool(object.Object, "status", "readyToUse"); found {
		status.Ready = ready
	}
	if size, found, _ := unstructured.NestedString(object.Object, "status", "restoreSize"); found {
		if quantity, err := resource.ParseQuantity(size); err == nil {
			status.SizeBytes = quantity.Value()
		}
	}
	if message, found, _ := unstructured.NestedString(object.Object, "status", "error", "message"); found {
		status.Error = message
	}
	if created, found, _ := unstructured.NestedString(object.Object, "status", "creationTime"); found {
		var parsed metav1.Time
		if err := parsed.UnmarshalQueryParameter(created); err == nil && !parsed.IsZero() {
			status.CreatedAt = parsed.Time
		}
	}
	return status
}

var errNoRestConfig = errors.New("kubernetes: exec needs a REST config")

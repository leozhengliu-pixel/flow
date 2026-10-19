package kubernetes

import (
	"context"
	"maps"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"flow/api/internal/sandbox"
)

// Managed object types (sandbox.ManagedObject.Type).
const (
	TypePod            = "pod"
	TypeSecret         = "secret"
	TypePVC            = "pvc"
	TypeVolumeSnapshot = "volumesnapshot"
)

func (p *Provider) selector(filter sandbox.ListFilter) string {
	set := labels.Set{sandbox.LabelManagedBy: sandbox.ManagedByValue, sandbox.LabelInstance: p.coding.InstanceID}
	if filter.Kind != "" {
		set[sandbox.LabelKind] = string(filter.Kind)
	}
	if filter.SandboxID != "" {
		set[sandbox.LabelSandboxID] = filter.SandboxID
	}
	if filter.SessionID != "" {
		set[sandbox.LabelSessionID] = filter.SessionID
	}
	return labels.SelectorFromSet(set).String()
}

// ListManaged lists every Pod, Secret, PVC and VolumeSnapshot of this Flow
// instance (labels flow.dev/managed-by=flow, flow.dev/instance=<id>). The
// namespace NetworkPolicies are not listed: they are not per sandbox.
func (p *Provider) ListManaged(ctx context.Context, filter sandbox.ListFilter) ([]sandbox.ManagedObject, error) {
	options := metav1.ListOptions{LabelSelector: p.selector(filter)}
	result := []sandbox.ManagedObject{}
	pods, err := p.client.CoreV1().Pods(p.k8s.Namespace).List(ctx, options)
	if err != nil {
		return nil, wrap("list pods", err)
	}
	for _, item := range pods.Items {
		result = append(result, sandbox.ManagedObject{Type: TypePod, Name: item.Name, Labels: maps.Clone(item.Labels), CreatedAt: item.CreationTimestamp.Time})
	}
	secrets, err := p.client.CoreV1().Secrets(p.k8s.Namespace).List(ctx, options)
	if err != nil {
		return nil, wrap("list secrets", err)
	}
	for _, item := range secrets.Items {
		result = append(result, sandbox.ManagedObject{Type: TypeSecret, Name: item.Name, Labels: maps.Clone(item.Labels), CreatedAt: item.CreationTimestamp.Time})
	}
	claims, err := p.client.CoreV1().PersistentVolumeClaims(p.k8s.Namespace).List(ctx, options)
	if err != nil {
		return nil, wrap("list pvcs", err)
	}
	for _, item := range claims.Items {
		result = append(result, sandbox.ManagedObject{Type: TypePVC, Name: item.Name, Labels: maps.Clone(item.Labels), CreatedAt: item.CreationTimestamp.Time})
	}
	if p.Capabilities().VolumeSnapshots && p.dynamic != nil {
		snapshots, err := p.dynamic.Resource(volumeSnapshotGVR).Namespace(p.k8s.Namespace).List(ctx, options)
		switch {
		case err == nil:
			for _, item := range snapshots.Items {
				result = append(result, sandbox.ManagedObject{Type: TypeVolumeSnapshot, Name: item.GetName(), Labels: item.GetLabels(), CreatedAt: item.GetCreationTimestamp().Time})
			}
		case meta.IsNoMatchError(err) || apierrors.IsNotFound(err):
			// No snapshot CRD: nothing to list.
		default:
			return nil, wrapSnapshot("list volumesnapshots", err)
		}
	}
	return result, nil
}

// DeleteManaged deletes one listed object; objects not labelled as this
// instance's are refused so a bad listing can never delete foreign objects.
func (p *Provider) DeleteManaged(ctx context.Context, object sandbox.ManagedObject) error {
	if object.Labels[sandbox.LabelManagedBy] != sandbox.ManagedByValue || object.Labels[sandbox.LabelInstance] != p.coding.InstanceID {
		return nil
	}
	var err error
	switch object.Type {
	case TypePod:
		zero := int64(0)
		err = p.client.CoreV1().Pods(p.k8s.Namespace).Delete(ctx, object.Name, metav1.DeleteOptions{GracePeriodSeconds: &zero})
	case TypeSecret:
		err = p.client.CoreV1().Secrets(p.k8s.Namespace).Delete(ctx, object.Name, metav1.DeleteOptions{})
	case TypePVC:
		err = p.client.CoreV1().PersistentVolumeClaims(p.k8s.Namespace).Delete(ctx, object.Name, metav1.DeleteOptions{})
	case TypeVolumeSnapshot:
		if p.dynamic != nil {
			err = p.dynamic.Resource(volumeSnapshotGVR).Namespace(p.k8s.Namespace).Delete(ctx, object.Name, metav1.DeleteOptions{})
		}
	default:
		return nil
	}
	if err != nil && !apierrors.IsNotFound(err) && !meta.IsNoMatchError(err) {
		return wrap("delete "+object.Type+" "+object.Name, err)
	}
	return nil
}

package kubernetes

import (
	"context"
	"fmt"
	"slices"
	"strings"

	authorizationv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"flow/api/internal/sandbox"
)

// accessCheck is one permission Flow needs in the sandbox namespace (design
// §9, the Helm Role).
type accessCheck struct {
	group, resource, subresource, verb string
}

func (p *Provider) requiredAccess() []accessCheck {
	checks := []accessCheck{}
	add := func(group, resource, subresource string, verbs ...string) {
		for _, verb := range verbs {
			checks = append(checks, accessCheck{group, resource, subresource, verb})
		}
	}
	add("", "pods", "", "create", "get", "list", "delete")
	add("", "pods", "log", "get")
	add("", "pods", "exec", "create", "get")
	add("", "secrets", "", "create", "get", "list", "delete", "patch")
	add("", "persistentvolumeclaims", "", "create", "get", "list", "delete")
	if p.k8s.ManageNetworkPolicy {
		add("networking.k8s.io", "networkpolicies", "", "get", "create", "patch")
	}
	if p.coding.Snapshot.Strategy == sandbox.StrategyVolumeSnapshot {
		add(snapshotGroup, "volumesnapshots", "", "create", "get", "list", "delete")
	}
	return checks
}

// Preflight verifies namespace access, RBAC (SelfSubjectAccessReview),
// RuntimeClass, StorageClass and the VolumeSnapshot CRD. Read failures of
// cluster-scoped objects the Role cannot see are warnings, not failures.
func (p *Provider) Preflight(ctx context.Context) []sandbox.PreflightResult {
	results := []sandbox.PreflightResult{}
	ns := p.k8s.Namespace

	if _, err := p.client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{Limit: 1}); err != nil {
		results = append(results, sandbox.PreflightResult{Name: "namespace", OK: false, Message: fmt.Sprintf("cannot list pods in namespace %s: %v", ns, err)})
	} else {
		results = append(results, sandbox.PreflightResult{Name: "namespace", OK: true, Message: "namespace " + ns + " is reachable"})
	}
	if namespace, err := p.client.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{}); err == nil {
		if level := namespace.Labels["pod-security.kubernetes.io/enforce"]; level != "restricted" {
			results = append(results, sandbox.PreflightResult{Name: "pod-security", OK: true, Warning: true, Message: fmt.Sprintf("namespace %s enforces Pod Security %q; label it pod-security.kubernetes.io/enforce=restricted", ns, level)})
		} else {
			results = append(results, sandbox.PreflightResult{Name: "pod-security", OK: true, Message: "namespace enforces the restricted Pod Security Standard"})
		}
	}

	missing := []string{}
	for _, check := range p.requiredAccess() {
		review := &authorizationv1.SelfSubjectAccessReview{Spec: authorizationv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{Namespace: ns, Verb: check.verb, Group: check.group, Resource: check.resource, Subresource: check.subresource},
		}}
		response, err := p.client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
		if err != nil {
			results = append(results, sandbox.PreflightResult{Name: "rbac", OK: true, Warning: true, Message: "cannot run SelfSubjectAccessReview: " + err.Error()})
			missing = nil
			break
		}
		if !response.Status.Allowed {
			name := check.resource
			if check.subresource != "" {
				name += "/" + check.subresource
			}
			if check.group != "" {
				name += "." + check.group
			}
			missing = append(missing, check.verb+" "+name)
		}
	}
	if missing != nil {
		if len(missing) > 0 {
			results = append(results, sandbox.PreflightResult{Name: "rbac", OK: false, Message: "missing permissions in " + ns + ": " + strings.Join(missing, ", ")})
		} else {
			results = append(results, sandbox.PreflightResult{Name: "rbac", OK: true, Message: "all required permissions are granted"})
		}
	}

	if name := p.k8s.RuntimeClassName; name != "" {
		_, err := p.client.NodeV1().RuntimeClasses().Get(ctx, name, metav1.GetOptions{})
		switch {
		case err == nil:
			results = append(results, sandbox.PreflightResult{Name: "runtime-class", OK: true, Message: "RuntimeClass " + name + " exists"})
		case apierrors.IsNotFound(err):
			results = append(results, sandbox.PreflightResult{Name: "runtime-class", OK: false, Message: "RuntimeClass " + name + " does not exist"})
		default:
			results = append(results, sandbox.PreflightResult{Name: "runtime-class", OK: true, Warning: true, Message: "cannot read RuntimeClass " + name + ": " + err.Error()})
		}
	} else {
		results = append(results, sandbox.PreflightResult{Name: "runtime-class", OK: true, Warning: true, Message: "sandboxes run on runc; set FLOW_CODING_K8S_RUNTIME_CLASS to gVisor or Kata for stronger isolation"})
	}
	if !p.k8s.HostUsers {
		results = append(results, sandbox.PreflightResult{Name: "user-namespaces", OK: true, Message: "hostUsers=false: needs Kubernetes ≥1.33 (or the UserNamespacesSupport gate) and a runtime with idmapped mounts"})
	}

	if p.usePVC || p.coding.Snapshot.Strategy == sandbox.StrategyVolumeSnapshot {
		results = append(results, p.storageClassCheck(ctx))
	}
	if p.coding.Snapshot.Strategy == sandbox.StrategyVolumeSnapshot {
		results = append(results, p.snapshotChecks(ctx)...)
	}
	if p.k8s.ManageNetworkPolicy {
		results = append(results, sandbox.PreflightResult{Name: "network-policy", OK: true, Warning: true, Message: "Flow manages the sandbox NetworkPolicies; enforcement depends on the CNI and cannot be verified here"})
	} else {
		results = append(results, sandbox.PreflightResult{Name: "network-policy", OK: true, Warning: true, Message: "FLOW_CODING_K8S_NETWORK_POLICY=false: the sandbox NetworkPolicies must be managed outside Flow"})
	}
	return results
}

func (p *Provider) storageClassCheck(ctx context.Context) sandbox.PreflightResult {
	if name := p.k8s.StorageClass; name != "" {
		_, err := p.client.StorageV1().StorageClasses().Get(ctx, name, metav1.GetOptions{})
		switch {
		case err == nil:
			return sandbox.PreflightResult{Name: "storage-class", OK: true, Message: "StorageClass " + name + " exists"}
		case apierrors.IsNotFound(err):
			return sandbox.PreflightResult{Name: "storage-class", OK: false, Message: "StorageClass " + name + " does not exist"}
		default:
			return sandbox.PreflightResult{Name: "storage-class", OK: true, Warning: true, Message: "cannot read StorageClass " + name + ": " + err.Error()}
		}
	}
	classes, err := p.client.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return sandbox.PreflightResult{Name: "storage-class", OK: true, Warning: true, Message: "cannot list StorageClasses: " + err.Error()}
	}
	for _, class := range classes.Items {
		if class.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
			return sandbox.PreflightResult{Name: "storage-class", OK: true, Message: "default StorageClass " + class.Name + " is used"}
		}
	}
	return sandbox.PreflightResult{Name: "storage-class", OK: false, Message: "no default StorageClass; set FLOW_CODING_K8S_STORAGE_CLASS"}
}

func (p *Provider) snapshotChecks(ctx context.Context) []sandbox.PreflightResult {
	results := []sandbox.PreflightResult{}
	resources, err := p.client.Discovery().ServerResourcesForGroupVersion(snapshotGroup + "/v1")
	present := err == nil && resources != nil && slices.ContainsFunc(resources.APIResources, func(resource metav1.APIResource) bool { return resource.Name == "volumesnapshots" })
	if !present {
		message := "the snapshot.storage.k8s.io/v1 VolumeSnapshot CRD is not installed; use FLOW_CODING_SNAPSHOT_STRATEGY=archive"
		if err != nil && !apierrors.IsNotFound(err) {
			message += " (" + err.Error() + ")"
		}
		return append(results, sandbox.PreflightResult{Name: "volume-snapshots", OK: false, Message: message})
	}
	results = append(results, sandbox.PreflightResult{Name: "volume-snapshots", OK: true, Message: "VolumeSnapshot CRD is installed"})
	if class := p.coding.Snapshot.VolumeSnapshotClass; class != "" && p.dynamic != nil {
		_, err := p.dynamic.Resource(volumeSnapshotClassGVR).Get(ctx, class, metav1.GetOptions{})
		switch {
		case err == nil:
			results = append(results, sandbox.PreflightResult{Name: "volume-snapshot-class", OK: true, Message: "VolumeSnapshotClass " + class + " exists"})
		case apierrors.IsNotFound(err):
			results = append(results, sandbox.PreflightResult{Name: "volume-snapshot-class", OK: false, Message: "VolumeSnapshotClass " + class + " does not exist"})
		default:
			results = append(results, sandbox.PreflightResult{Name: "volume-snapshot-class", OK: true, Warning: true, Message: "cannot read VolumeSnapshotClass " + class + ": " + err.Error()})
		}
	}
	return results
}

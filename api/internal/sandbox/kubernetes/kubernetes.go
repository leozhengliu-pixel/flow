// Package kubernetes is the Kubernetes sandbox provider (track A): plain Pods
// via client-go, GA APIs only (core/v1, networking.k8s.io/v1, and the
// snapshot.storage.k8s.io/v1 CRD through the dynamic client when
// FLOW_CODING_SNAPSHOT_STRATEGY=volumesnapshot). See
// docs/coding-sessions-design.md §9 for the objects it creates.
//
// Every method is idempotent: object names derive from ids, "already exists"
// is success and "not found" on delete is success. Retryable API failures are
// wrapped in sandbox.ErrTransient.
package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"flow/api/internal/config"
	"flow/api/internal/sandbox"
)

func init() { sandbox.Register(config.CodingProviderKubernetes, New) }

// Provider drives sandboxes as Pods in one namespace.
type Provider struct {
	settings
	client     k8s.Interface
	dynamic    dynamic.Interface
	restConfig *rest.Config
	now        func() time.Time
	// newExecutor builds the exec stream (SPDY/WebSocket); tests replace it.
	newExecutor executorFactory

	policyMu      sync.Mutex
	policyEnsured bool
}

// New builds the provider from FLOW_CODING_K8S_*. It uses the kubeconfig at
// FLOW_CODING_K8S_KUBECONFIG when set and the in-cluster service account
// otherwise; it never falls back to the user's default kubeconfig.
func New(deps sandbox.Dependencies) (sandbox.Provider, error) {
	k8sConfig := deps.Config.Kubernetes
	var restConfig *rest.Config
	var err error
	if k8sConfig.Kubeconfig != "" {
		restConfig, err = clientcmd.BuildConfigFromFlags("", k8sConfig.Kubeconfig)
	} else {
		restConfig, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("kubernetes sandbox provider: client config: %w", err)
	}
	if k8sConfig.QPS > 0 {
		restConfig.QPS = float32(k8sConfig.QPS)
	}
	if k8sConfig.Burst > 0 {
		restConfig.Burst = k8sConfig.Burst
	}
	restConfig.UserAgent = "flow-coding-sessions"
	client, err := k8s.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("kubernetes sandbox provider: %w", err)
	}
	dyn, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("kubernetes sandbox provider: %w", err)
	}
	return NewWithClients(deps, client, dyn, restConfig)
}

// NewWithClients builds the provider over existing clients (tests use the
// fake clientset and the fake dynamic client; restConfig may be nil, which
// disables Exec).
func NewWithClients(deps sandbox.Dependencies, client k8s.Interface, dyn dynamic.Interface, restConfig *rest.Config) (*Provider, error) {
	s, err := parseSettings(deps.Config)
	if err != nil {
		return nil, fmt.Errorf("kubernetes sandbox provider: %w", err)
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	p := &Provider{settings: s, client: client, dynamic: dyn, restConfig: restConfig, now: now}
	p.newExecutor = defaultExecutor(restConfig)
	return p, nil
}

func (p *Provider) Name() string { return config.CodingProviderKubernetes }

func (p *Provider) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{
		VolumeSnapshots:   p.coding.Snapshot.Strategy == sandbox.StrategyVolumeSnapshot,
		PersistentVolumes: true,
		Exec:              p.restConfig != nil,
		TTY:               p.restConfig != nil,
		RuntimeClass:      p.k8s.RuntimeClassName != "",
		UserNamespaces:    !p.k8s.HostUsers,
		NetworkPolicy:     true,
		Regions:           len(p.coding.Regions) > 0,
	}
}

// wrap maps API errors to the sandbox contract.
func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	switch {
	case apierrors.IsNotFound(err):
		return fmt.Errorf("%s: %w: %v", op, sandbox.ErrNotFound, err)
	case isQuota(err):
		return fmt.Errorf("%s: %w: %v", op, sandbox.ErrQuota, err)
	case apierrors.IsTimeout(err), apierrors.IsServerTimeout(err), apierrors.IsTooManyRequests(err),
		apierrors.IsInternalError(err), apierrors.IsServiceUnavailable(err), apierrors.IsConflict(err),
		apierrors.IsUnexpectedServerError(err), errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%s: %w: %v", op, sandbox.ErrTransient, err)
	}
	var status apierrors.APIStatus
	if !errors.As(err, &status) {
		// Not an API status: a network or transport failure.
		return fmt.Errorf("%s: %w: %v", op, sandbox.ErrTransient, err)
	}
	return fmt.Errorf("%s: %w", op, err)
}

// isQuota reports a ResourceQuota admission rejection: 403 "exceeded quota",
// or "status unknown for quota" while the quota controller has not yet
// computed the usage of a new or changed quota (also capacity: waiting
// helps, unlike a broken sandbox spec).
func isQuota(err error) bool {
	if !apierrors.IsForbidden(err) {
		return false
	}
	var status apierrors.APIStatus
	if errors.As(err, &status) {
		message := status.Status().Message
		return strings.Contains(message, "exceeded quota") || strings.Contains(message, "status unknown for quota")
	}
	return false
}

// ensureNetworkPolicies creates or patches the namespace sandbox policies
// once per process (and again after a failure).
func (p *Provider) ensureNetworkPolicies(ctx context.Context) error {
	if !p.k8s.ManageNetworkPolicy {
		return nil
	}
	p.policyMu.Lock()
	defer p.policyMu.Unlock()
	if p.policyEnsured {
		return nil
	}
	if err := p.EnsureNetworkPolicies(ctx); err != nil {
		return err
	}
	p.policyEnsured = true
	return nil
}

// EnsureNetworkPolicies creates the sandbox NetworkPolicies, or merge-patches
// their spec when it drifted (RBAC: get, create, patch).
func (p *Provider) EnsureNetworkPolicies(ctx context.Context) error {
	api := p.client.NetworkingV1().NetworkPolicies(p.k8s.Namespace)
	for _, policy := range p.BuildNetworkPolicies() {
		existing, err := api.Get(ctx, policy.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			if _, err := api.Create(ctx, policy, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
				return wrap("create networkpolicy "+policy.Name, err)
			}
			continue
		}
		if err != nil {
			return wrap("get networkpolicy "+policy.Name, err)
		}
		if equalJSON(existing.Spec, policy.Spec) && maps.Equal(existing.Labels, policy.Labels) {
			continue
		}
		patch, err := mergePatch(map[string]any{"metadata": map[string]any{"labels": policy.Labels}, "spec": policy.Spec})
		if err != nil {
			return err
		}
		if _, err := api.Patch(ctx, policy.Name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			return wrap("patch networkpolicy "+policy.Name, err)
		}
	}
	return nil
}

// StartSandbox creates the token Secret, the workspace PVC when needed, and
// the Pod. Starting an existing sandbox returns its status (adoption after a
// restart).
func (p *Provider) StartSandbox(ctx context.Context, spec sandbox.SandboxSpec) (sandbox.SandboxStatus, error) {
	if !sandbox.ValidID(spec.ID) {
		return sandbox.SandboxStatus{}, fmt.Errorf("kubernetes: invalid sandbox id %q", spec.ID)
	}
	spec = sandbox.CompleteSpec(p.coding, spec)
	pods := p.client.CoreV1().Pods(p.k8s.Namespace)
	if existing, err := pods.Get(ctx, PodName(spec.ID), metav1.GetOptions{}); err == nil {
		p.ownSecret(ctx, existing)
		return p.podStatus(existing), nil
	} else if !apierrors.IsNotFound(err) {
		return sandbox.SandboxStatus{}, wrap("get pod", err)
	}
	if err := p.ensureNetworkPolicies(ctx); err != nil {
		return sandbox.SandboxStatus{}, err
	}
	if volumeID := p.workspaceVolumeID(spec); volumeID != "" {
		if spec.Workspace.VolumeID != "" {
			pvc, err := p.client.CoreV1().PersistentVolumeClaims(p.k8s.Namespace).Get(ctx, PVCName(volumeID), metav1.GetOptions{})
			if err != nil {
				return sandbox.SandboxStatus{}, wrap("workspace volume "+volumeID, err)
			}
			if err := usablePVC(pvc); err != nil {
				return sandbox.SandboxStatus{}, err
			}
		} else if _, err := p.ensurePVC(ctx, volumeID, "", "", p.labels(spec), ""); err != nil {
			return sandbox.SandboxStatus{}, err
		}
	}
	if err := p.ensureTokenSecret(ctx, spec); err != nil {
		return sandbox.SandboxStatus{}, err
	}
	pod := p.BuildPod(spec)
	created, err := pods.Create(ctx, pod, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		created, err = pods.Get(ctx, pod.Name, metav1.GetOptions{})
	}
	if err != nil {
		return sandbox.SandboxStatus{}, wrap("create pod", err)
	}
	p.ownSecret(ctx, created)
	return p.podStatus(created), nil
}

// ensureTokenSecret creates the token Secret, or rewrites its token when the
// Secret survived a Pod that is gone (the reconciler minted a fresh token).
func (p *Provider) ensureTokenSecret(ctx context.Context, spec sandbox.SandboxSpec) error {
	if spec.Token == "" {
		return fmt.Errorf("kubernetes: sandbox %s has no token", spec.ID)
	}
	secrets := p.client.CoreV1().Secrets(p.k8s.Namespace)
	secret := p.BuildTokenSecret(spec)
	_, err := secrets.Create(ctx, secret, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return wrap("create token secret", err)
	}
	patch, err := mergePatch(map[string]any{"data": map[string][]byte{tokenKey: []byte(spec.Token)}})
	if err != nil {
		return err
	}
	if _, err := secrets.Patch(ctx, secret.Name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		return wrap("patch token secret", err)
	}
	return nil
}

// ownSecret makes the Pod own its token Secret so the Secret is garbage
// collected with the Pod. Best effort: GC and StopSandbox delete it anyway.
func (p *Provider) ownSecret(ctx context.Context, pod *corev1.Pod) {
	if pod == nil || pod.UID == "" {
		return
	}
	secrets := p.client.CoreV1().Secrets(p.k8s.Namespace)
	name := TokenSecretName(pod.Labels[sandbox.LabelSandboxID])
	secret, err := secrets.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return
	}
	for _, ref := range secret.OwnerReferences {
		if ref.UID == pod.UID {
			return
		}
	}
	patch, err := mergePatch(map[string]any{"metadata": map[string]any{"ownerReferences": []metav1.OwnerReference{{
		APIVersion: "v1", Kind: "Pod", Name: pod.Name, UID: pod.UID,
	}}}})
	if err == nil {
		_, _ = secrets.Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	}
}

// Status reports the observed sandbox. A missing Pod is PhaseMissing, not an
// error.
func (p *Provider) Status(ctx context.Context, sandboxID string) (sandbox.SandboxStatus, error) {
	pod, err := p.client.CoreV1().Pods(p.k8s.Namespace).Get(ctx, PodName(sandboxID), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return sandbox.SandboxStatus{ID: sandboxID, Phase: sandbox.PhaseMissing}, nil
	}
	if err != nil {
		return sandbox.SandboxStatus{}, wrap("get pod", err)
	}
	return p.podStatus(pod), nil
}

// StopSandbox deletes the Pod (SIGTERM, then SIGKILL after Grace), its token
// Secret and, unless KeepVolume, the sandbox's own workspace PVC. A Pod stuck
// terminating past twice the grace (lost node) is force-deleted.
func (p *Provider) StopSandbox(ctx context.Context, sandboxID string, options sandbox.StopOptions) error {
	pods := p.client.CoreV1().Pods(p.k8s.Namespace)
	grace := options.Grace
	if grace <= 0 {
		grace = p.coding.Timeouts.StopGrace
	}
	grace = sandbox.TerminationGrace(grace)
	volumeID := ""
	pod, err := pods.Get(ctx, PodName(sandboxID), metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
	case err != nil:
		return wrap("get pod", err)
	default:
		volumeID = pod.Annotations[AnnotationVolumeID]
		seconds := int64(grace / time.Second)
		if pod.DeletionTimestamp != nil && p.now().Sub(pod.DeletionTimestamp.Time) > 2*grace {
			seconds = 0
		}
		if pod.DeletionTimestamp == nil || seconds == 0 {
			policy := metav1.DeletePropagationBackground
			if err := pods.Delete(ctx, pod.Name, metav1.DeleteOptions{GracePeriodSeconds: &seconds, PropagationPolicy: &policy}); err != nil && !apierrors.IsNotFound(err) {
				return wrap("delete pod", err)
			}
		}
	}
	if err := p.client.CoreV1().Secrets(p.k8s.Namespace).Delete(ctx, TokenSecretName(sandboxID), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return wrap("delete token secret", err)
	}
	if options.KeepVolume {
		return nil
	}
	// Only the sandbox's own volume (auto-created, or restored for it under
	// its id) is deleted; build volumes keyed by a snapshot id belong to the
	// snapshot strategy.
	if volumeID == "" || volumeID == sandboxID {
		if err := p.DeleteVolume(ctx, sandboxID); err != nil {
			return err
		}
	}
	return nil
}

// Logs streams the runner (or restore) container's log.
func (p *Provider) Logs(ctx context.Context, sandboxID string, options sandbox.LogOptions) (io.ReadCloser, error) {
	container := options.Container
	if container == "" {
		container = runnerContainer
	}
	logOptions := &corev1.PodLogOptions{Container: container, Follow: options.Follow, Previous: options.Previous}
	if options.TailLines > 0 {
		logOptions.TailLines = new(options.TailLines)
	}
	if options.SinceTime != nil {
		logOptions.SinceTime = new(metav1.NewTime(*options.SinceTime))
	}
	stream, err := p.client.CoreV1().Pods(p.k8s.Namespace).GetLogs(PodName(sandboxID), logOptions).Stream(ctx)
	if err != nil {
		return nil, wrap("pod logs", err)
	}
	return stream, nil
}

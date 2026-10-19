//go:build envtest

package kubernetes

// The Kubernetes provider against a real kube-apiserver + etcd (see
// envtest_cluster_test.go for how to run it). Every namespace enforces the
// restricted Pod Security Standard, so the API server's own validation and
// admission (PodSecurity, RuntimeClass, Priority, LimitRanger, ResourceQuota)
// judge every object the provider builds.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"flow/api/internal/config"
	"flow/api/internal/sandbox"
)

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

// ensureClusterObjects creates the cluster-scoped objects the tests share:
// RuntimeClasses runc, gvisor (with overhead and scheduling, like GKE
// Sandbox) and kata, a PriorityClass and StorageClasses.
func (c *cluster) ensureClusterObjects(t *testing.T) {
	t.Helper()
	ctx := ctxT(t)
	for _, rc := range []*nodev1.RuntimeClass{
		{ObjectMeta: metav1.ObjectMeta{Name: "runc"}, Handler: "runc"},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "gvisor"}, Handler: "runsc",
			Overhead: &nodev1.Overhead{PodFixed: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m"), corev1.ResourceMemory: resource.MustParse("160Mi")}},
			Scheduling: &nodev1.Scheduling{
				NodeSelector: map[string]string{"sandbox.gke.io/runtime": "gvisor"},
				Tolerations:  []corev1.Toleration{{Key: "sandbox.gke.io/runtime", Operator: corev1.TolerationOpEqual, Value: "gvisor", Effect: corev1.TaintEffectNoSchedule}},
			},
		},
		{ObjectMeta: metav1.ObjectMeta{Name: "kata"}, Handler: "kata"},
	} {
		if _, err := c.client.NodeV1().RuntimeClasses().Create(ctx, rc, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatal(err)
		}
	}
	if _, err := c.client.SchedulingV1().PriorityClasses().Create(ctx, &schedulingv1.PriorityClass{ObjectMeta: metav1.ObjectMeta{Name: "flow-sandbox"}, Value: 1000}, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	binding := storagev1.VolumeBindingWaitForFirstConsumer
	for _, name := range []string{"fast", "csi-rbd"} {
		class := &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: name}, Provisioner: "example.com/csi", VolumeBindingMode: &binding}
		if _, err := c.client.StorageV1().StorageClasses().Create(ctx, class, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatal(err)
		}
	}
}

func envSpec(id, size string) sandbox.SandboxSpec {
	spec := sessionSpec(id)
	spec.Resources = sandbox.Resources{Size: size}
	spec.Env = map[string]string{"NODE_ENV": "test", "GOFLAGS": "-mod=mod"}
	spec.ProxyURL = "http://flow-egress.flow-sandboxes.svc:3128"
	spec.NoProxy = []string{"internal.example.com"}
	return spec
}

// TestEnvtestPodSpecMatrix creates the Pod of every configuration
// combination with a server-side dry run: runtime classes, user namespaces,
// sizes, workspace volumes, archive restore, scheduling and metadata.
func TestEnvtestPodSpecMatrix(t *testing.T) {
	c := envtestCluster(t)
	c.ensureClusterObjects(t)
	ns := c.namespace(t, "matrix")
	ctx := ctxT(t)
	n := 0
	for _, runtimeClass := range []string{"", "runc", "gvisor", "kata"} {
		for _, hostUsers := range []bool{true, false} {
			for _, workspace := range []string{"emptyDir", "pvc"} {
				for _, restore := range []bool{false, true} {
					for _, size := range []string{"small", "medium", "large"} {
						n++
						name := fmt.Sprintf("rc=%s/hostUsers=%v/%s/restore=%v/%s", runtimeClass, hostUsers, workspace, restore, size)
						coding := envConfig(ns)
						coding.Kubernetes.RuntimeClassName = runtimeClass
						coding.Kubernetes.HostUsers = hostUsers
						coding.Kubernetes.WorkspaceVolume = workspace
						coding.Kubernetes.StorageClass = "fast"
						coding.Kubernetes.NodeSelector = map[string]string{"flow.dev/pool": "sandbox"}
						coding.Kubernetes.Tolerations = []string{"flow.dev/sandbox=true:NoSchedule", "flow.dev/spot:NoExecute"}
						coding.Kubernetes.PriorityClassName = "flow-sandbox"
						coding.Kubernetes.ExtraLabels = map[string]string{"team": "platform"}
						coding.Kubernetes.ExtraAnnotations = map[string]string{"owner": "flow"}
						coding.ImagePullSecrets = []string{"regcred"}
						coding.CPURequestRatio = 0.5
						coding.Egress.NoProxy = []string{".corp.example.com"}
						provider := c.provider(t, coding, nil, nil)
						spec := envSpec(fmt.Sprintf("m%03d", n), size)
						spec.Region = "eu-west"
						spec.WorkspaceKey = "workspace-1"
						if restore {
							spec.Workspace.ArchiveURL = "http://flow.flow.svc:8090/internal/coding-runtime/snapshots/snap1/archive"
						}
						pod := provider.BuildPod(spec)
						created, err := c.client.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
						if err != nil {
							t.Fatalf("%s: the API server rejected the Pod: %v", name, err)
						}
						checkCreatedPod(t, name, coding, spec, created)
					}
				}
			}
		}
	}
}

func checkCreatedPod(t *testing.T, name string, coding config.CodingConfig, spec sandbox.SandboxSpec, pod *corev1.Pod) {
	t.Helper()
	fail := func(format string, args ...any) { t.Fatalf(name+": "+format, args...) }
	podSpec := pod.Spec
	if rc := coding.Kubernetes.RuntimeClassName; rc != "" {
		if podSpec.RuntimeClassName == nil || *podSpec.RuntimeClassName != rc {
			fail("runtimeClassName = %v", podSpec.RuntimeClassName)
		}
	} else if podSpec.RuntimeClassName != nil {
		fail("runtimeClassName must be unset")
	}
	if rc := coding.Kubernetes.RuntimeClassName; rc == "gvisor" {
		// RuntimeClass admission merged the class's overhead and scheduling.
		if podSpec.Overhead.Cpu().MilliValue() != 250 || podSpec.NodeSelector["sandbox.gke.io/runtime"] != "gvisor" || podSpec.NodeSelector["flow.dev/pool"] != "sandbox" {
			fail("gvisor overhead/scheduling not merged: overhead=%v nodeSelector=%v", podSpec.Overhead, podSpec.NodeSelector)
		}
	}
	if coding.Kubernetes.HostUsers != (podSpec.HostUsers == nil || *podSpec.HostUsers) {
		fail("hostUsers = %v", podSpec.HostUsers)
	}
	if podSpec.PriorityClassName != "flow-sandbox" || podSpec.Priority == nil || *podSpec.Priority != 1000 {
		fail("priority = %q %v", podSpec.PriorityClassName, podSpec.Priority)
	}
	if len(podSpec.Tolerations) < 2 || podSpec.ImagePullSecrets[0].Name != "regcred" || podSpec.Affinity == nil {
		fail("scheduling = %+v %+v %+v", podSpec.Tolerations, podSpec.ImagePullSecrets, podSpec.Affinity)
	}
	if pod.Labels["team"] != "platform" || pod.Labels[sandbox.LabelSandboxID] != spec.ID || pod.Labels[sandbox.LabelRegion] != "eu-west" || pod.Annotations["owner"] != "flow" {
		fail("metadata = %v %v", pod.Labels, pod.Annotations)
	}
	size := coding.Sizes[spec.Resources.Size]
	runner := podSpec.Containers[0]
	limits, requests := runner.Resources.Limits, runner.Resources.Requests
	if limits.Cpu().String() != size.CPU || limits.Memory().String() != size.Memory || limits.StorageEphemeral().String() != size.EphemeralStorage {
		fail("limits = %v, size %+v", limits, size)
	}
	if requests.Cpu().MilliValue()*2 != limits.Cpu().MilliValue() || !requests.Memory().Equal(*limits.Memory()) {
		fail("requests = %v (ratio 0.5)", requests)
	}
	sc := runner.SecurityContext
	if sc == nil || *sc.AllowPrivilegeEscalation || !*sc.ReadOnlyRootFilesystem || !*sc.RunAsNonRoot || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault || len(sc.Capabilities.Drop) != 1 {
		fail("securityContext = %+v", sc)
	}
	if *podSpec.AutomountServiceAccountToken || *podSpec.EnableServiceLinks || podSpec.RestartPolicy != corev1.RestartPolicyNever || *podSpec.ActiveDeadlineSeconds <= 0 {
		fail("pod settings = %+v", podSpec)
	}
	env := envMap(runner.Env)
	if env["HTTPS_PROXY"] != spec.ProxyURL || !strings.Contains(env["NO_PROXY"], "flow.flow.svc") || !strings.Contains(env["NO_PROXY"], "internal.example.com") || env["NODE_ENV"] != "test" {
		fail("egress env = %v", env)
	}
	usesPVC := coding.Kubernetes.WorkspaceVolume == "pvc"
	if workspace := volumeNamed(pod, "workspace"); workspace == nil || (workspace.PersistentVolumeClaim != nil) != usesPVC {
		fail("workspace volume = %+v", workspace)
	}
	if token := volumeNamed(pod, "token"); token == nil || token.Secret == nil || token.Secret.SecretName != TokenSecretName(spec.ID) {
		fail("token volume = %+v", token)
	}
	if restore := spec.Workspace.ArchiveURL != ""; restore != (len(podSpec.InitContainers) == 1) {
		fail("init containers = %d", len(podSpec.InitContainers))
	}
}

// TestEnvtestSandboxLifecycle starts sandboxes for real, then drives their
// status like a kubelet would and stops them.
func TestEnvtestSandboxLifecycle(t *testing.T) {
	c := envtestCluster(t)
	c.ensureClusterObjects(t)
	ns := c.namespace(t, "lifecycle")
	ctx := ctxT(t)

	for _, tc := range []struct {
		name   string
		mutate func(*config.CodingConfig)
		spec   func() sandbox.SandboxSpec
	}{
		{"archive restore on emptyDir", nil, func() sandbox.SandboxSpec {
			spec := envSpec("l1", "small")
			spec.Workspace.ArchiveURL = "http://flow.flow.svc:8090/internal/coding-runtime/snapshots/snap1/archive"
			return spec
		}},
		{"pvc workspace, gvisor, user namespaces, region", func(c *config.CodingConfig) {
			c.Kubernetes.WorkspaceVolume = "pvc"
			c.Kubernetes.StorageClass = "fast"
			c.Kubernetes.RuntimeClassName = "gvisor"
			c.Kubernetes.HostUsers = false
			c.Kubernetes.Tolerations = []string{"flow.dev/sandbox=true:NoSchedule"}
			c.Kubernetes.NodeSelector = map[string]string{"flow.dev/pool": "sandbox"}
		}, func() sandbox.SandboxSpec {
			spec := envSpec("l2", "large")
			spec.Region = "eu-west"
			return spec
		}},
		{"snapshot build kind", nil, func() sandbox.SandboxSpec {
			spec := envSpec("l3", "medium")
			spec.Kind = sandbox.KindSnapshot
			spec.SessionID = ""
			spec.SnapshotID = "snapl3"
			return spec
		}},
		{"debug kind", func(c *config.CodingConfig) { c.Kubernetes.RuntimeClassName = "kata" }, func() sandbox.SandboxSpec {
			spec := envSpec("l4", "small")
			spec.Kind = sandbox.KindDebug
			return spec
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			coding := envConfig(ns)
			if tc.mutate != nil {
				tc.mutate(&coding)
			}
			provider := c.provider(t, coding, nil, nil)
			spec := tc.spec()
			status, err := provider.StartSandbox(ctx, spec)
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			if status.Phase != sandbox.PhasePending || status.ProviderRef != PodName(spec.ID) || status.ID != spec.ID || status.Kind != spec.Kind {
				t.Fatalf("status = %+v", status)
			}
			if again, err := provider.StartSandbox(ctx, spec); err != nil || again.ProviderRef != status.ProviderRef {
				t.Fatalf("idempotent start = %+v %v", again, err)
			}
			pod, err := c.client.CoreV1().Pods(ns).Get(ctx, PodName(spec.ID), metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			secret, err := c.client.CoreV1().Secrets(ns).Get(ctx, TokenSecretName(spec.ID), metav1.GetOptions{})
			if err != nil || len(secret.OwnerReferences) != 1 || secret.OwnerReferences[0].UID != pod.UID || string(secret.Data[tokenKey]) != spec.Token {
				t.Fatalf("token secret = %+v %v", secret, err)
			}
			if secret.Labels[sandbox.LabelSandboxID] != spec.ID || secret.Labels[sandbox.LabelManagedBy] != sandbox.ManagedByValue {
				t.Fatalf("secret labels = %v", secret.Labels)
			}
			if provider.usePVC {
				pvc, err := c.client.CoreV1().PersistentVolumeClaims(ns).Get(ctx, PVCName(spec.ID), metav1.GetOptions{})
				if err != nil || *pvc.Spec.StorageClassName != "fast" || pod.Annotations[AnnotationVolumeID] != spec.ID {
					t.Fatalf("pvc = %+v %v", pvc, err)
				}
			}
			objects, err := provider.ListManaged(ctx, sandbox.ListFilter{SandboxID: spec.ID})
			if err != nil {
				t.Fatal(err)
			}
			types := map[string]int{}
			for _, object := range objects {
				types[object.Type]++
			}
			if types[TypePod] != 1 || types[TypeSecret] != 1 || (provider.usePVC && types[TypePVC] != 1) {
				t.Fatalf("managed = %v", types)
			}
			if filtered, err := provider.ListManaged(ctx, sandbox.ListFilter{Kind: spec.Kind, SandboxID: spec.ID}); err != nil || len(filtered) != len(objects) {
				t.Fatalf("kind filter = %d %v", len(filtered), err)
			}

			// Logs and exec reach the API server (authorized) but there is no kubelet.
			// (An unscheduled Pod has an empty log.)
			if stream, err := provider.Logs(ctx, spec.ID, sandbox.LogOptions{TailLines: 10}); err != nil {
				t.Fatalf("logs of an unscheduled pod = %v", err)
			} else {
				_, _ = io.Copy(io.Discard, stream)
				stream.Close()
			}
			if _, err := provider.Exec(ctx, spec.ID, sandbox.ExecRequest{Command: []string{"true"}, Stdout: io.Discard}); err == nil || !strings.Contains(err.Error(), "not running") {
				t.Fatalf("exec on a pending pod = %v", err)
			}

			// Kubelet-like status transitions.
			c.bindPod(t, ns, pod.Name, "node-a")
			c.setPodStatus(t, ns, pod.Name, func(s *corev1.PodStatus) {
				s.Phase = corev1.PodPending
				s.Conditions = []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionTrue}}
			})
			if got, _ := provider.Status(ctx, spec.ID); got.Phase != sandbox.PhaseStarting || got.Node != "node-a" {
				t.Fatalf("scheduled status = %+v", got)
			}
			started := metav1.NewTime(time.Now().Truncate(time.Second))
			c.setPodStatus(t, ns, pod.Name, func(s *corev1.PodStatus) {
				s.Phase = corev1.PodRunning
				s.ContainerStatuses = []corev1.ContainerStatus{{Name: runnerContainer, Image: spec.Image, ImageID: "sha256:x", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: started}}}}
			})
			if got, _ := provider.Status(ctx, spec.ID); got.Phase != sandbox.PhaseRunning || got.StartedAt == nil || !got.StartedAt.Equal(started.Time) {
				t.Fatalf("running status = %+v", got)
			}

			// A graceful delete of a bound Pod leaves it terminating (no
			// kubelet confirms); a later stop past twice the grace forces it.
			if err := provider.StopSandbox(ctx, spec.ID, sandbox.StopOptions{Grace: 5 * time.Second, KeepVolume: true}); err != nil {
				t.Fatal(err)
			}
			terminating, err := c.client.CoreV1().Pods(ns).Get(ctx, pod.Name, metav1.GetOptions{})
			if err != nil || terminating.DeletionTimestamp == nil {
				t.Fatalf("pod must be terminating: %+v %v", terminating, err)
			}
			if _, err := c.client.CoreV1().Secrets(ns).Get(ctx, TokenSecretName(spec.ID), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
				t.Fatalf("the token secret must be deleted: %v", err)
			}
			if err := provider.StopSandbox(ctx, spec.ID, sandbox.StopOptions{Grace: 5 * time.Second, KeepVolume: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := c.client.CoreV1().Pods(ns).Get(ctx, pod.Name, metav1.GetOptions{}); err != nil {
				t.Fatalf("a second stop within the grace must not force the delete: %v", err)
			}
			late := c.provider(t, coding, nil, func() time.Time { return time.Now().Add(10 * time.Minute) })
			if err := late.StopSandbox(ctx, spec.ID, sandbox.StopOptions{Grace: 5 * time.Second}); err != nil {
				t.Fatal(err)
			}
			if _, err := c.client.CoreV1().Pods(ns).Get(ctx, pod.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
				t.Fatalf("the stuck pod must be force-deleted: %v", err)
			}
			if got, err := provider.Status(ctx, spec.ID); err != nil || got.Phase != sandbox.PhaseMissing {
				t.Fatalf("status after stop = %+v %v", got, err)
			}
			if provider.usePVC {
				// pvc-protection keeps the claim until the PVC controller
				// (not running here) removes the finalizer.
				pvc, err := c.client.CoreV1().PersistentVolumeClaims(ns).Get(ctx, PVCName(spec.ID), metav1.GetOptions{})
				if err == nil && pvc.DeletionTimestamp == nil {
					t.Fatalf("the sandbox pvc must be deleted: %+v", pvc)
				}
			}
			if err := late.StopSandbox(ctx, spec.ID, sandbox.StopOptions{}); err != nil {
				t.Fatalf("stopping a stopped sandbox must succeed: %v", err)
			}
		})
	}

	// A privileged variant is refused: the namespace really enforces restricted.
	provider := c.provider(t, envConfig(ns), nil, nil)
	pod := provider.BuildPod(envSpec("l9", "small"))
	pod.Spec.Containers[0].SecurityContext.AllowPrivilegeEscalation = new(true)
	if _, err := c.client.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}}); err == nil {
		t.Fatal("PodSecurity admission must reject a non-restricted pod")
	}
}

// TestEnvtestPodStatusFromKubelet maps kubelet-written statuses to sandbox
// phases, reasons and the design's error codes.
func TestEnvtestPodStatusFromKubelet(t *testing.T) {
	c := envtestCluster(t)
	ns := c.namespace(t, "status")
	ctx := ctxT(t)
	provider := c.provider(t, envConfig(ns), nil, nil)
	waiting := func(name, reason string) corev1.ContainerStatus {
		return corev1.ContainerStatus{Name: name, Image: "x", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason, Message: reason + " message"}}}
	}
	terminated := func(name, reason string, code int32) corev1.ContainerStatus {
		now := metav1.Now()
		return corev1.ContainerStatus{Name: name, Image: "x", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: reason, ExitCode: code, StartedAt: now, FinishedAt: now}}}
	}
	for i, tc := range []struct {
		name    string
		kind    sandbox.Kind
		restore bool
		status  corev1.PodStatus
		phase   sandbox.Phase
		reason  sandbox.Reason
		code    string
	}{
		{"unschedulable", sandbox.KindSession, false, corev1.PodStatus{Phase: corev1.PodPending, Conditions: []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: corev1.PodReasonUnschedulable, Message: "0/3 nodes are available"}}}, sandbox.PhasePending, sandbox.ReasonUnschedulable, ""},
		{"image pull", sandbox.KindSession, false, corev1.PodStatus{Phase: corev1.PodPending, ContainerStatuses: []corev1.ContainerStatus{waiting(runnerContainer, "ImagePullBackOff")}}, sandbox.PhasePending, sandbox.ReasonImagePull, "imagePullFailed"},
		{"restore image pull", sandbox.KindSession, true, corev1.PodStatus{Phase: corev1.PodPending, InitContainerStatuses: []corev1.ContainerStatus{waiting(restoreContainer, "ErrImagePull")}}, sandbox.PhasePending, sandbox.ReasonImagePull, "imagePullFailed"},
		{"oom", sandbox.KindSession, false, corev1.PodStatus{Phase: corev1.PodFailed, ContainerStatuses: []corev1.ContainerStatus{terminated(runnerContainer, "OOMKilled", 137)}}, sandbox.PhaseFailed, sandbox.ReasonOOMKilled, "sandboxLost"},
		{"evicted", sandbox.KindSession, false, corev1.PodStatus{Phase: corev1.PodFailed, Reason: "Evicted", Message: "low on ephemeral-storage"}, sandbox.PhaseFailed, sandbox.ReasonEvicted, "sandboxLost"},
		{"deadline", sandbox.KindSession, false, corev1.PodStatus{Phase: corev1.PodFailed, Reason: "DeadlineExceeded"}, sandbox.PhaseFailed, sandbox.ReasonDeadlineExceeded, "sessionTimeout"},
		{"snapshot deadline", sandbox.KindSnapshot, false, corev1.PodStatus{Phase: corev1.PodFailed, Reason: "DeadlineExceeded"}, sandbox.PhaseFailed, sandbox.ReasonDeadlineExceeded, "snapshotFailed"},
		{"restore failed", sandbox.KindSession, true, corev1.PodStatus{Phase: corev1.PodFailed, InitContainerStatuses: []corev1.ContainerStatus{terminated(restoreContainer, "Error", 2)}}, sandbox.PhaseFailed, sandbox.ReasonInitFailed, "snapshotFailed"},
		{"exit non-zero", sandbox.KindSession, false, corev1.PodStatus{Phase: corev1.PodFailed, ContainerStatuses: []corev1.ContainerStatus{terminated(runnerContainer, "Error", 1)}}, sandbox.PhaseFailed, sandbox.ReasonExitNonZero, "sandboxLost"},
		{"succeeded", sandbox.KindSnapshot, false, corev1.PodStatus{Phase: corev1.PodSucceeded, ContainerStatuses: []corev1.ContainerStatus{terminated(runnerContainer, "Completed", 0)}}, sandbox.PhaseSucceeded, sandbox.ReasonNone, ""},
		{"node lost", sandbox.KindSession, false, corev1.PodStatus{Phase: corev1.PodUnknown}, sandbox.PhaseFailed, sandbox.ReasonUnknown, "sandboxLost"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := envSpec(fmt.Sprintf("st%02d", i), "small")
			spec.Kind = tc.kind
			if tc.restore {
				spec.Workspace.ArchiveURL = "http://flow.flow.svc:8090/archive"
			}
			if _, err := provider.StartSandbox(ctx, spec); err != nil {
				t.Fatal(err)
			}
			c.setPodStatus(t, ns, PodName(spec.ID), func(s *corev1.PodStatus) { *s = tc.status })
			got, err := provider.Status(ctx, spec.ID)
			if err != nil || got.Phase != tc.phase || got.Reason != tc.reason {
				t.Fatalf("status = %+v %v, want %s/%s", got, err, tc.phase, tc.reason)
			}
			if code := sandbox.ErrorCode(tc.kind, got.Phase, got.Reason); string(code) != tc.code {
				t.Fatalf("error code = %q, want %q", code, tc.code)
			}
		})
	}
}

// TestEnvtestConcurrentStartAndAdoption races several provider instances
// (replicas) starting the same sandbox, then rotates the token of a sandbox
// whose Pod vanished.
func TestEnvtestConcurrentStartAndAdoption(t *testing.T) {
	c := envtestCluster(t)
	c.ensureClusterObjects(t)
	ns := c.namespace(t, "race")
	ctx := ctxT(t)
	coding := envConfig(ns)
	coding.Kubernetes.WorkspaceVolume = "pvc"
	coding.Kubernetes.StorageClass = "fast"
	spec := envSpec("race1", "medium")
	spec.Kind = sandbox.KindWarm
	spec.SessionID = ""

	var wg sync.WaitGroup
	results := make([]sandbox.SandboxStatus, 6)
	errs := make([]error, 6)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each replica has its own provider: its own NetworkPolicy
			// cache, so the policy creates race too.
			results[i], errs[i] = c.provider(t, coding, nil, nil).StartSandbox(ctx, spec)
		}()
	}
	wg.Wait()
	for i := range results {
		if errs[i] != nil || results[i].ProviderRef != PodName(spec.ID) {
			t.Fatalf("replica %d: %+v %v", i, results[i], errs[i])
		}
	}
	pods, _ := c.client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	policies, _ := c.client.NetworkingV1().NetworkPolicies(ns).List(ctx, metav1.ListOptions{})
	claims, _ := c.client.CoreV1().PersistentVolumeClaims(ns).List(ctx, metav1.ListOptions{})
	if len(pods.Items) != 1 || len(policies.Items) != 2 || len(claims.Items) != 1 {
		t.Fatalf("objects: %d pods, %d policies, %d claims", len(pods.Items), len(policies.Items), len(claims.Items))
	}
	if pods.Items[0].Labels[sandbox.LabelKind] != string(sandbox.KindWarm) {
		t.Fatalf("warm labels = %v", pods.Items[0].Labels)
	}

	// After a restart a new provider adopts the Pod without creating anything.
	rec := &recorder{}
	adopter := c.provider(t, coding, rec.wrap(c.admin), nil)
	if status, err := adopter.StartSandbox(ctx, spec); err != nil || status.ProviderRef != PodName(spec.ID) {
		t.Fatalf("adopt = %+v %v", status, err)
	}
	if n := rec.count("POST", "/pods"); n != 0 {
		t.Fatalf("adoption created %d pods", n)
	}

	// The Pod vanished (force-deleted) but its Secret and PVC survived: a
	// new start with a fresh token rewrites the token and re-owns the Secret.
	zero := int64(0)
	if err := c.client.CoreV1().Pods(ns).Delete(ctx, PodName(spec.ID), metav1.DeleteOptions{GracePeriodSeconds: &zero}); err != nil {
		t.Fatal(err)
	}
	old := pods.Items[0].UID
	spec.Token = "fcs_rotated"
	if _, err := adopter.StartSandbox(ctx, spec); err != nil {
		t.Fatal(err)
	}
	pod, _ := c.client.CoreV1().Pods(ns).Get(ctx, PodName(spec.ID), metav1.GetOptions{})
	secret, _ := c.client.CoreV1().Secrets(ns).Get(ctx, TokenSecretName(spec.ID), metav1.GetOptions{})
	if string(secret.Data[tokenKey]) != "fcs_rotated" || len(secret.OwnerReferences) != 1 || secret.OwnerReferences[0].UID != pod.UID || pod.UID == old {
		t.Fatalf("rotated secret = %+v (pod %s)", secret, pod.UID)
	}
}

// TestEnvtestVolumes covers PVC workspaces with a storage class.
func TestEnvtestVolumes(t *testing.T) {
	c := envtestCluster(t)
	c.ensureClusterObjects(t)
	ns := c.namespace(t, "volumes")
	ctx := ctxT(t)
	coding := envConfig(ns)
	coding.Kubernetes.StorageClass = "fast"
	coding.Kubernetes.ExtraLabels = map[string]string{"team": "platform"}
	provider := c.provider(t, coding, nil, nil)

	spec := sandbox.VolumeSpec{ID: "vol1", WorkspaceKey: "ws-1", Size: "30Gi", Labels: map[string]string{"flow.dev/purpose": "test"}}
	first, err := provider.EnsureVolume(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	second, err := provider.EnsureVolume(ctx, spec)
	if err != nil || second.ProviderRef != first.ProviderRef {
		t.Fatalf("idempotent ensure = %+v %v", second, err)
	}
	if first.Ready || first.Phase != string(corev1.ClaimPending) || first.Labels[sandbox.LabelWorkspace] != sandbox.WorkspaceLabel("ws-1") || first.Labels["team"] != "platform" {
		t.Fatalf("volume = %+v", first)
	}
	pvc, _ := c.client.CoreV1().PersistentVolumeClaims(ns).Get(ctx, PVCName("vol1"), metav1.GetOptions{})
	if *pvc.Spec.StorageClassName != "fast" || pvc.Spec.Resources.Requests.Storage().String() != "30Gi" || pvc.Spec.AccessModes[0] != corev1.ReadWriteOnce {
		t.Fatalf("pvc spec = %+v", pvc.Spec)
	}
	// An explicit class wins over the configured one.
	if _, err := provider.EnsureVolume(ctx, sandbox.VolumeSpec{ID: "vol2", StorageClass: "csi-rbd"}); err != nil {
		t.Fatal(err)
	}
	if pvc, _ := c.client.CoreV1().PersistentVolumeClaims(ns).Get(ctx, PVCName("vol2"), metav1.GetOptions{}); *pvc.Spec.StorageClassName != "csi-rbd" || pvc.Spec.Resources.Requests.Storage().String() != "20Gi" {
		t.Fatalf("explicit class pvc = %+v", pvc.Spec)
	}
	// Bound (as the PV controller would report).
	pvc.Status.Phase = corev1.ClaimBound
	if _, err := c.client.CoreV1().PersistentVolumeClaims(ns).UpdateStatus(ctx, pvc, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if status, err := provider.VolumeStatus(ctx, "vol1"); err != nil || !status.Ready {
		t.Fatalf("bound status = %+v %v", status, err)
	}
	// A size the API server cannot parse is a validation error, not transient.
	if _, err := provider.EnsureVolume(ctx, sandbox.VolumeSpec{ID: "vol3", Size: "lots"}); err == nil || sandbox.IsTransient(err) {
		t.Fatalf("bad size = %v", err)
	}
	if _, err := provider.EnsureVolume(ctx, sandbox.VolumeSpec{ID: "Bad_ID"}); err == nil {
		t.Fatal("an invalid volume id must be refused before any API call")
	}
	// StorageObjectInUseProtection admission adds the
	// kubernetes.io/pvc-protection finalizer to every claim; it keeps a
	// deleted claim until the PVC controller (not running here) sees no Pod
	// using it, which in a cluster takes as long as the Pod's grace period.
	pvc, _ = c.client.CoreV1().PersistentVolumeClaims(ns).Get(ctx, PVCName("vol1"), metav1.GetOptions{})
	if !slices.Contains(pvc.Finalizers, "kubernetes.io/pvc-protection") {
		t.Fatalf("finalizers = %v", pvc.Finalizers)
	}
	if err := provider.DeleteVolume(ctx, "vol1"); err != nil {
		t.Fatal(err)
	}
	if err := provider.DeleteVolume(ctx, "vol1"); err != nil {
		t.Fatalf("delete must be idempotent: %v", err)
	}
	if status, err := provider.VolumeStatus(ctx, "vol1"); err != nil || status.Ready || status.Phase != "Terminating" {
		t.Fatalf("terminating status = %+v %v", status, err)
	}
	if _, err := provider.EnsureVolume(ctx, spec); !errors.Is(err, sandbox.ErrTransient) {
		t.Fatalf("a terminating claim must not be reused: %v", err)
	}
	session := envSpec("volsess", "small")
	session.Workspace.VolumeID = "vol1"
	if _, err := provider.StartSandbox(ctx, session); !errors.Is(err, sandbox.ErrTransient) {
		t.Fatalf("a sandbox must not mount a terminating claim: %v", err)
	}
	if objects, _ := provider.ListManaged(ctx, sandbox.ListFilter{}); slices.ContainsFunc(objects, func(o sandbox.ManagedObject) bool { return o.Name == PVCName("vol1") }) {
		t.Fatal("a terminating claim is not a GC candidate")
	}
	// The PVC controller releases it; then the id can be used again.
	pvc, _ = c.client.CoreV1().PersistentVolumeClaims(ns).Get(ctx, PVCName("vol1"), metav1.GetOptions{})
	pvc.Finalizers = nil
	if _, err := c.client.CoreV1().PersistentVolumeClaims(ns).Update(ctx, pvc, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.EnsureVolume(ctx, spec); err != nil {
		t.Fatalf("recreate after release = %v", err)
	}
	if err := provider.DeleteVolume(ctx, "never"); err != nil {
		t.Fatalf("deleting a missing volume must succeed: %v", err)
	}
	if _, err := provider.VolumeStatus(ctx, "never"); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("missing volume status = %v", err)
	}
}

// TestEnvtestNetworkPolicyIdempotency checks that ensuring the policies
// against the API server's defaulted objects patches only on real drift.
func TestEnvtestNetworkPolicyIdempotency(t *testing.T) {
	c := envtestCluster(t)
	ns := c.namespace(t, "netpol")
	ctx := ctxT(t)
	rec := &recorder{}
	coding := envConfig(ns)
	coding.RuntimeAddr = ":8090"
	provider := c.provider(t, coding, rec.wrap(c.admin), nil)
	if err := provider.EnsureNetworkPolicies(ctx); err != nil {
		t.Fatal(err)
	}
	if n := rec.count("POST", "/networkpolicies"); n != 2 {
		t.Fatalf("creates = %d", n)
	}
	rec.reset()
	for range 3 {
		if err := provider.EnsureNetworkPolicies(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := rec.count("PATCH", "/networkpolicies"); n != 0 {
		t.Fatalf("the API server's defaults must not look like drift: %d patches", n)
	}

	// Drift: someone removed the egress rules and a label.
	patch := []byte(`{"metadata":{"labels":{"flow.dev/instance":null}},"spec":{"egress":null}}`)
	if _, err := c.client.NetworkingV1().NetworkPolicies(ns).Patch(ctx, NetworkPolicyEgress, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	rec.reset()
	if err := provider.EnsureNetworkPolicies(ctx); err != nil {
		t.Fatal(err)
	}
	if n := rec.count("PATCH", "/networkpolicies"); n != 1 {
		t.Fatalf("drift patches = %d", n)
	}
	policy, _ := c.client.NetworkingV1().NetworkPolicies(ns).Get(ctx, NetworkPolicyEgress, metav1.GetOptions{})
	want := provider.BuildNetworkPolicies()[1]
	if !equalJSON(policy.Spec, want.Spec) || !maps.Equal(policy.Labels, want.Labels) {
		t.Fatalf("repaired policy = %+v", policy)
	}
	rec.reset()
	if err := provider.EnsureNetworkPolicies(ctx); err != nil {
		t.Fatal(err)
	}
	if n := rec.count("PATCH", "/networkpolicies"); n != 0 {
		t.Fatalf("patches after repair = %d", n)
	}

	// A configuration change (another proxy port, no runtime port) is applied.
	coding.Egress.ProxyURL = "http://proxy.example:8888"
	coding.RuntimeAddr = ""
	changed := c.provider(t, coding, nil, nil)
	if err := changed.EnsureNetworkPolicies(ctx); err != nil {
		t.Fatal(err)
	}
	policy, _ = c.client.NetworkingV1().NetworkPolicies(ns).Get(ctx, NetworkPolicyEgress, metav1.GetOptions{})
	if got := policy.Spec.Egress[2].Ports[0].Port.IntValue(); got != 8888 || policy.Spec.Egress[1].Ports != nil {
		t.Fatalf("reconfigured egress = %+v", policy.Spec.Egress)
	}
	// Disabled management touches nothing.
	coding.Kubernetes.ManageNetworkPolicy = false
	off := c.provider(t, coding, nil, nil)
	if _, err := off.StartSandbox(ctx, envSpec("np1", "small")); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(off.Preflight(ctx), func(r sandbox.PreflightResult) bool {
		return r.Name == "network-policy" && strings.Contains(r.Message, "outside Flow")
	}) {
		t.Fatal("preflight must warn that policies are managed outside Flow")
	}
}

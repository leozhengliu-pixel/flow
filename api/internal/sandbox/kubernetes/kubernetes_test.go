package kubernetes

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	nodev1 "k8s.io/api/node/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"

	"flow/api/internal/config"
	"flow/api/internal/sandbox"
)

var _ sandbox.Provider = (*Provider)(nil)

const testNamespace = "flow-sandboxes"

func testConfig() config.CodingConfig {
	return config.CodingConfig{
		Enabled:         true,
		Provider:        config.CodingProviderKubernetes,
		RuntimeURL:      "http://flow.flow.svc:8090",
		InstanceID:      "flow",
		Image:           "ghcr.io/example/flow-sandbox:1",
		ImagePullPolicy: "IfNotPresent",
		Sizes: map[string]config.SandboxSize{
			"small":  {CPU: "1", Memory: "8Gi", EphemeralStorage: "20Gi", PIDs: 4096},
			"medium": {CPU: "2", Memory: "16Gi", EphemeralStorage: "40Gi", PIDs: 8192},
		},
		WorkspaceSize:   "20Gi",
		CPURequestRatio: 1,
		RegionLabel:     "topology.kubernetes.io/region",
		WarmPoolMaxAge:  time.Hour,
		Timeouts:        config.CodingTimeouts{StopGrace: time.Minute, MaxSession: 4 * time.Hour, DebugTerminal: 20 * time.Minute},
		Snapshot:        config.CodingSnapshotConfig{Strategy: sandbox.StrategyArchive, Timeout: 30 * time.Minute},
		Kubernetes: config.KubernetesSandboxConfig{
			Namespace:           testNamespace,
			HostUsers:           true,
			WorkspaceVolume:     "auto",
			ServiceAccount:      "flow-sandbox",
			ManageNetworkPolicy: true,
			FlowNamespace:       "flow",
			FlowPodSelector:     map[string]string{"app.kubernetes.io/name": "flow"},
			ProxyNamespace:      testNamespace,
			ProxyPodSelector:    map[string]string{"app.kubernetes.io/name": "flow-egress"},
		},
		Egress: config.CodingEgressConfig{ProxyURL: "http://flow-egress.flow-sandboxes.svc:3128"},
	}
}

type harness struct {
	provider  *Provider
	clientset *fake.Clientset
	dynamic   *dynamicfake.FakeDynamicClient
	now       time.Time
}

func newHarness(t *testing.T, mutate func(*config.CodingConfig), objects ...runtime.Object) *harness {
	t.Helper()
	c := testConfig()
	if mutate != nil {
		mutate(&c)
	}
	clientset := fake.NewClientset(objects...)
	// The fake tracker does not assign UIDs; the API server does.
	clientset.PrependReactor("create", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if create, ok := action.(k8stesting.CreateAction); ok {
			if object, ok := create.GetObject().(metav1.Object); ok && object.GetUID() == "" {
				object.SetUID(types.UID("uid-" + object.GetName()))
			}
		}
		return false, nil, nil
	})
	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		volumeSnapshotGVR:      "VolumeSnapshotList",
		volumeSnapshotClassGVR: "VolumeSnapshotClassList",
	})
	h := &harness{clientset: clientset, dynamic: dyn, now: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)}
	provider, err := NewWithClients(sandbox.Dependencies{Config: c, Now: func() time.Time { return h.now }}, clientset, dyn, &rest.Config{Host: "https://10.0.0.1:6443"})
	if err != nil {
		t.Fatal(err)
	}
	h.provider = provider
	return h
}

func sessionSpec(id string) sandbox.SandboxSpec {
	return sandbox.SandboxSpec{
		ID: id, Kind: sandbox.KindSession, WorkspaceKey: "acme", SessionID: "sess1",
		Resources: sandbox.Resources{Size: "medium"}, Token: "fcs_secret", Env: map[string]string{"NODE_ENV": "test", "FLOW_TOKEN_FILE": "/evil", "HOME": "/root"},
		ProxyURL: "http://flow-egress.flow-sandboxes.svc:3128",
	}
}

func envMap(env []corev1.EnvVar) map[string]string {
	out := map[string]string{}
	for _, item := range env {
		out[item.Name] = item.Value
	}
	return out
}

// TestPodSatisfiesRestrictedPodSecurity checks every field the restricted
// Pod Security Standard (and design §19) requires.
func TestPodSatisfiesRestrictedPodSecurity(t *testing.T) {
	h := newHarness(t, nil)
	spec := sessionSpec("s1")
	spec.Workspace.ArchiveURL = "http://flow.flow.svc:8090/internal/coding-runtime/snapshots/snap1/archive"
	pod := h.provider.BuildPod(spec)
	ps := pod.Spec
	if ps.AutomountServiceAccountToken == nil || *ps.AutomountServiceAccountToken {
		t.Fatal("automountServiceAccountToken must be false")
	}
	if ps.EnableServiceLinks == nil || *ps.EnableServiceLinks {
		t.Fatal("enableServiceLinks must be false")
	}
	if ps.HostNetwork || ps.HostPID || ps.HostIPC {
		t.Fatal("host namespaces must not be shared")
	}
	if ps.RestartPolicy != corev1.RestartPolicyNever {
		t.Fatalf("restartPolicy = %s", ps.RestartPolicy)
	}
	if ps.ServiceAccountName != "flow-sandbox" {
		t.Fatalf("serviceAccountName = %s", ps.ServiceAccountName)
	}
	sc := ps.SecurityContext
	if sc == nil || !*sc.RunAsNonRoot || *sc.RunAsUser != 1000 || *sc.RunAsGroup != 1000 || *sc.FSGroup != 1000 || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("pod security context = %+v", sc)
	}
	for _, volume := range ps.Volumes {
		if volume.HostPath != nil {
			t.Fatalf("volume %s uses hostPath", volume.Name)
		}
	}
	containers := append(append([]corev1.Container{}, ps.InitContainers...), ps.Containers...)
	if len(containers) != 2 {
		t.Fatalf("want restore init container plus runner, got %d", len(containers))
	}
	for _, container := range containers {
		csc := container.SecurityContext
		if csc == nil || csc.AllowPrivilegeEscalation == nil || *csc.AllowPrivilegeEscalation {
			t.Fatalf("%s: allowPrivilegeEscalation must be false", container.Name)
		}
		if csc.Privileged == nil || *csc.Privileged {
			t.Fatalf("%s: must not be privileged", container.Name)
		}
		if csc.ReadOnlyRootFilesystem == nil || !*csc.ReadOnlyRootFilesystem {
			t.Fatalf("%s: root filesystem must be read-only", container.Name)
		}
		if csc.Capabilities == nil || !slices.Equal(csc.Capabilities.Drop, []corev1.Capability{"ALL"}) || len(csc.Capabilities.Add) != 0 {
			t.Fatalf("%s: capabilities = %+v", container.Name, csc.Capabilities)
		}
		if csc.SeccompProfile == nil || csc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
			t.Fatalf("%s: seccomp must be RuntimeDefault", container.Name)
		}
		if len(container.Ports) != 0 {
			t.Fatalf("%s: no ports may be exposed", container.Name)
		}
		for _, env := range container.Env {
			if strings.Contains(env.Value, "fcs_secret") || env.ValueFrom != nil {
				t.Fatalf("%s: the token must never be an env var (%s)", container.Name, env.Name)
			}
		}
	}
	runner := ps.Containers[0]
	env := envMap(runner.Env)
	if env["FLOW_TOKEN_FILE"] != sandbox.TokenMountPath || env["HOME"] != sandbox.HomePath {
		t.Fatalf("platform env must win over environment variables: %v", env)
	}
	if env["NODE_ENV"] != "test" || env["FLOW_SANDBOX"] != "1" || env["LINEAR_SANDBOX"] != "1" || env["FLOW_RUNNER_MODE"] != "session" {
		t.Fatalf("runner env = %v", env)
	}
	if env["HTTPS_PROXY"] != spec.ProxyURL || !strings.Contains(env["NO_PROXY"], "flow.flow.svc") {
		t.Fatalf("proxy env = %v", env)
	}
	if env["FLOW_PIDS_LIMIT"] != "8192" {
		t.Fatalf("PID limit env = %q", env["FLOW_PIDS_LIMIT"])
	}
	if _, ok := env["FLOW_SNAPSHOT"]; ok {
		t.Fatal("FLOW_SNAPSHOT is only set while building a snapshot")
	}
	restoreEnv := envMap(ps.InitContainers[0].Env)
	if restoreEnv["FLOW_RUNNER_MODE"] != "restore" || restoreEnv["FLOW_ARCHIVE_URL"] != spec.Workspace.ArchiveURL {
		t.Fatalf("restore env = %v", restoreEnv)
	}
	if _, ok := restoreEnv["HTTPS_PROXY"]; ok {
		t.Fatal("the restore step talks to Flow directly, never through the proxy")
	}
	if !slices.Equal(ps.InitContainers[0].Command, []string{"flow-runner", "restore"}) || !slices.Equal(runner.Command, []string{"flow-runner", "session"}) {
		t.Fatalf("commands = %v %v", ps.InitContainers[0].Command, runner.Command)
	}
	var token *corev1.Volume
	for i := range ps.Volumes {
		if ps.Volumes[i].Name == "token" {
			token = &ps.Volumes[i]
		}
	}
	if token == nil || token.Secret == nil || token.Secret.SecretName != "flow-cs-s1-token" || *token.Secret.DefaultMode != 0o440 {
		t.Fatalf("token volume = %+v", token)
	}
	if *ps.ActiveDeadlineSeconds != int64((4*time.Hour).Seconds()) || *ps.TerminationGracePeriodSeconds != 90 { // StopGrace 60s + RunnerWrapUp
		t.Fatalf("deadline %d grace %d", *ps.ActiveDeadlineSeconds, *ps.TerminationGracePeriodSeconds)
	}
}

func TestRuntimeClassAndHostUsers(t *testing.T) {
	cases := []struct {
		name         string
		runtimeClass string
		hostUsers    bool
	}{
		{"runc with host users", "", true},
		{"gvisor", "gvisor", true},
		{"kata without host users", "kata", false},
		{"runc with user namespaces", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, func(c *config.CodingConfig) {
				c.Kubernetes.RuntimeClassName = tc.runtimeClass
				c.Kubernetes.HostUsers = tc.hostUsers
			})
			pod := h.provider.BuildPod(sessionSpec("s1"))
			if tc.runtimeClass == "" {
				if pod.Spec.RuntimeClassName != nil {
					t.Fatalf("runtimeClassName must be omitted for runc, got %q", *pod.Spec.RuntimeClassName)
				}
			} else if pod.Spec.RuntimeClassName == nil || *pod.Spec.RuntimeClassName != tc.runtimeClass {
				t.Fatalf("runtimeClassName = %v", pod.Spec.RuntimeClassName)
			}
			if tc.hostUsers {
				if pod.Spec.HostUsers != nil {
					t.Fatal("hostUsers must be omitted when true")
				}
			} else if pod.Spec.HostUsers == nil || *pod.Spec.HostUsers {
				t.Fatal("hostUsers must be false")
			}
			caps := h.provider.Capabilities()
			if caps.RuntimeClass != (tc.runtimeClass != "") || caps.UserNamespaces != !tc.hostUsers {
				t.Fatalf("capabilities = %+v", caps)
			}
		})
	}
}

func TestSchedulingAndMetadataSettings(t *testing.T) {
	h := newHarness(t, func(c *config.CodingConfig) {
		c.Kubernetes.NodeSelector = map[string]string{"flow.dev/pool": "sandbox"}
		c.Kubernetes.Tolerations = []string{"flow.dev/sandbox=true:NoSchedule", "dedicated:NoExecute", "gpu"}
		c.Kubernetes.PriorityClassName = "flow-sandbox"
		c.Kubernetes.ExtraLabels = map[string]string{"team": "platform", sandbox.LabelManagedBy: "someone-else"}
		c.Kubernetes.ExtraAnnotations = map[string]string{"cost-center": "42"}
		c.ImagePullSecrets = []string{"registry", " "}
		c.CPURequestRatio = 0.5
		c.Kubernetes.ServiceAccount = "custom-sa"
	})
	spec := sessionSpec("s1")
	spec.Region = "eu-west"
	pod := h.provider.BuildPod(spec)
	if pod.Spec.NodeSelector["flow.dev/pool"] != "sandbox" || pod.Spec.PriorityClassName != "flow-sandbox" || pod.Spec.ServiceAccountName != "custom-sa" {
		t.Fatalf("scheduling = %+v", pod.Spec)
	}
	wantTolerations := []corev1.Toleration{
		{Key: "flow.dev/sandbox", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule},
		{Key: "dedicated", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
		{Key: "gpu", Operator: corev1.TolerationOpExists},
	}
	if !slices.Equal(pod.Spec.Tolerations, wantTolerations) {
		t.Fatalf("tolerations = %+v", pod.Spec.Tolerations)
	}
	if len(pod.Spec.ImagePullSecrets) != 1 || pod.Spec.ImagePullSecrets[0].Name != "registry" {
		t.Fatalf("image pull secrets = %+v", pod.Spec.ImagePullSecrets)
	}
	terms := pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	if len(terms) != 1 || terms[0].MatchExpressions[0].Key != "topology.kubernetes.io/region" || terms[0].MatchExpressions[0].Values[0] != "eu-west" {
		t.Fatalf("region affinity = %+v", terms)
	}
	if pod.Labels["team"] != "platform" || pod.Labels[sandbox.LabelManagedBy] != "flow" || pod.Labels[sandbox.LabelInstance] != "flow" ||
		pod.Labels[sandbox.LabelSandboxID] != "s1" || pod.Labels[sandbox.LabelSessionID] != "sess1" || pod.Labels[sandbox.LabelRegion] != "eu-west" ||
		pod.Labels[sandbox.LabelSize] != "medium" || pod.Labels[sandbox.LabelWorkspace] != sandbox.WorkspaceLabel("acme") {
		t.Fatalf("labels = %v", pod.Labels)
	}
	if pod.Annotations["cost-center"] != "42" {
		t.Fatalf("annotations = %v", pod.Annotations)
	}
	resources := pod.Spec.Containers[0].Resources
	if resources.Limits.Cpu().String() != "2" || resources.Requests.Cpu().String() != "1" {
		t.Fatalf("cpu limits %s requests %s", resources.Limits.Cpu(), resources.Requests.Cpu())
	}
	if resources.Limits.Memory().String() != "16Gi" || resources.Requests.Memory().String() != "16Gi" {
		t.Fatalf("memory = %+v", resources)
	}
	if resources.Limits.StorageEphemeral().String() != "40Gi" {
		t.Fatalf("ephemeral storage = %s", resources.Limits.StorageEphemeral())
	}
	// Without regions configured there is no affinity.
	spec.Region = ""
	if pod := h.provider.BuildPod(spec); pod.Spec.Affinity != nil {
		t.Fatal("no affinity without a region")
	}
}

func TestParseTolerationsRejectsBadInput(t *testing.T) {
	for _, raw := range []string{"=v:NoSchedule", "key:Sometimes"} {
		if _, err := ParseTolerations([]string{raw}); err == nil {
			t.Errorf("%q must be rejected", raw)
		}
	}
	if _, err := NewWithClients(sandbox.Dependencies{Config: func() config.CodingConfig {
		c := testConfig()
		c.Sizes["small"] = config.SandboxSize{CPU: "lots", Memory: "1Gi"}
		return c
	}()}, fake.NewClientset(), nil, nil); err == nil {
		t.Fatal("invalid size quantities must fail at construction")
	}
}

func TestSnapshotStrategiesShapeTheWorkspace(t *testing.T) {
	t.Run("archive uses emptyDir and a restore init container", func(t *testing.T) {
		h := newHarness(t, nil)
		spec := sessionSpec("s1")
		pod := h.provider.BuildPod(spec)
		if len(pod.Spec.InitContainers) != 0 {
			t.Fatal("no restore step without an archive")
		}
		workspace := volumeNamed(pod, "workspace")
		if workspace.EmptyDir == nil || workspace.EmptyDir.SizeLimit.String() != "20Gi" {
			t.Fatalf("workspace = %+v", workspace)
		}
		if home := volumeNamed(pod, "home"); home.EmptyDir == nil || home.EmptyDir.SizeLimit.String() != "10Gi" {
			t.Fatalf("home = %+v", home)
		}
		spec.Workspace.ArchiveURL = "http://flow/archive"
		if pod := h.provider.BuildPod(spec); len(pod.Spec.InitContainers) != 1 || pod.Spec.InitContainers[0].Name != "restore" {
			t.Fatal("an archive restore needs the restore init container")
		}
		if h.provider.Capabilities().VolumeSnapshots {
			t.Fatal("archive strategy has no native snapshots")
		}
		if _, err := h.provider.CreateSnapshot(context.Background(), sandbox.SnapshotSpec{ID: "snap1", VolumeID: "v1"}); !errors.Is(err, sandbox.ErrUnsupported) {
			t.Fatalf("CreateSnapshot under archive = %v", err)
		}
	})
	t.Run("volumesnapshot uses a PVC for workspace and home", func(t *testing.T) {
		h := newHarness(t, func(c *config.CodingConfig) {
			c.Snapshot.Strategy = sandbox.StrategyVolumeSnapshot
			c.Snapshot.VolumeSnapshotClass = "csi-snapclass"
			c.Kubernetes.StorageClass = "fast"
		})
		ctx := context.Background()
		spec := sessionSpec("s2")
		pod := h.provider.BuildPod(spec)
		workspace := volumeNamed(pod, "workspace")
		if workspace.PersistentVolumeClaim == nil || workspace.PersistentVolumeClaim.ClaimName != "flow-ws-s2" {
			t.Fatalf("workspace = %+v", workspace)
		}
		if volumeNamed(pod, "home") != nil {
			t.Fatal("home lives on the PVC (subPath home)")
		}
		mounts := pod.Spec.Containers[0].VolumeMounts
		if mounts[0].SubPath != "workspace" || mounts[1].SubPath != "home" || mounts[1].Name != "workspace" {
			t.Fatalf("mounts = %+v", mounts)
		}
		if pod.Annotations[AnnotationVolumeID] != "s2" {
			t.Fatalf("volume annotation = %v", pod.Annotations)
		}

		// Build: a volume keyed by the snapshot id, then a VolumeSnapshot of it.
		if _, err := h.provider.EnsureVolume(ctx, sandbox.VolumeSpec{ID: "snap1", WorkspaceKey: "acme", Labels: map[string]string{sandbox.LabelSnapshotID: "snap1"}}); err != nil {
			t.Fatal(err)
		}
		pvc, err := h.clientset.CoreV1().PersistentVolumeClaims(testNamespace).Get(ctx, "flow-ws-snap1", metav1.GetOptions{})
		if err != nil || *pvc.Spec.StorageClassName != "fast" || pvc.Spec.Resources.Requests.Storage().String() != "20Gi" {
			t.Fatalf("build pvc = %+v %v", pvc, err)
		}
		status, err := h.provider.CreateSnapshot(ctx, sandbox.SnapshotSpec{ID: "snap1", VolumeID: "snap1"})
		if err != nil || status.Ready || status.ProviderRef != "flow-snap-snap1" {
			t.Fatalf("create snapshot = %+v %v", status, err)
		}
		object, err := h.dynamic.Resource(volumeSnapshotGVR).Namespace(testNamespace).Get(ctx, "flow-snap-snap1", metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if class, _, _ := unstructured.NestedString(object.Object, "spec", "volumeSnapshotClassName"); class != "csi-snapclass" {
			t.Fatalf("class = %q", class)
		}
		if source, _, _ := unstructured.NestedString(object.Object, "spec", "source", "persistentVolumeClaimName"); source != "flow-ws-snap1" {
			t.Fatalf("source = %q", source)
		}
		if object.GetLabels()[sandbox.LabelSnapshotID] != "snap1" || object.GetLabels()[sandbox.LabelManagedBy] != "flow" {
			t.Fatalf("snapshot labels = %v", object.GetLabels())
		}
		// Idempotent.
		if again, err := h.provider.CreateSnapshot(ctx, sandbox.SnapshotSpec{ID: "snap1", VolumeID: "snap1"}); err != nil || again.ProviderRef != status.ProviderRef {
			t.Fatalf("second create = %+v %v", again, err)
		}
		// Restore refuses a snapshot that is not ready yet.
		if _, err := h.provider.RestoreWorkspace(ctx, sandbox.RestoreSpec{VolumeID: "s3", SnapshotID: "snap1"}); !errors.Is(err, sandbox.ErrPending) {
			t.Fatalf("restore of an unready snapshot = %v", err)
		}
		_ = unstructured.SetNestedField(object.Object, true, "status", "readyToUse")
		_ = unstructured.SetNestedField(object.Object, "30Gi", "status", "restoreSize")
		if _, err := h.dynamic.Resource(volumeSnapshotGVR).Namespace(testNamespace).Update(ctx, object, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
		ready, err := h.provider.SnapshotStatus(ctx, "snap1")
		if err != nil || !ready.Ready || ready.SizeBytes != 30<<30 {
			t.Fatalf("snapshot status = %+v %v", ready, err)
		}
		if _, err := h.provider.RestoreWorkspace(ctx, sandbox.RestoreSpec{VolumeID: "s3", SnapshotID: "snap1", WorkspaceKey: "acme"}); err != nil {
			t.Fatal(err)
		}
		restored, err := h.clientset.CoreV1().PersistentVolumeClaims(testNamespace).Get(ctx, "flow-ws-s3", metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if ds := restored.Spec.DataSource; ds == nil || ds.Kind != "VolumeSnapshot" || ds.Name != "flow-snap-snap1" || *ds.APIGroup != "snapshot.storage.k8s.io" {
			t.Fatalf("dataSource = %+v", ds)
		}
		if restored.Spec.Resources.Requests.Storage().String() != "30Gi" {
			t.Fatalf("restored size must cover restoreSize, got %s", restored.Spec.Resources.Requests.Storage())
		}
		if err := h.provider.DeleteSnapshot(ctx, "snap1"); err != nil {
			t.Fatal(err)
		}
		if err := h.provider.DeleteSnapshot(ctx, "snap1"); err != nil {
			t.Fatalf("delete is idempotent: %v", err)
		}
		if _, err := h.provider.SnapshotStatus(ctx, "snap1"); !errors.Is(err, sandbox.ErrNotFound) {
			t.Fatalf("deleted snapshot = %v", err)
		}
	})
}

func volumeNamed(pod *corev1.Pod, name string) *corev1.Volume {
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].Name == name {
			return &pod.Spec.Volumes[i]
		}
	}
	return nil
}

func TestNetworkPolicies(t *testing.T) {
	h := newHarness(t, func(c *config.CodingConfig) { c.RuntimeAddr = ":8090" })
	policies := h.provider.BuildNetworkPolicies()
	if len(policies) != 2 || policies[0].Name != NetworkPolicyDefaultDeny || policies[1].Name != NetworkPolicyEgress {
		t.Fatalf("policies = %v", policies)
	}
	deny := policies[0].Spec
	if !slices.Equal(deny.PolicyTypes, []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress}) || len(deny.Ingress) != 0 || len(deny.Egress) != 0 {
		t.Fatalf("default deny = %+v", deny)
	}
	if deny.PodSelector.MatchLabels[sandbox.LabelManagedBy] != "flow" {
		t.Fatalf("selector = %+v", deny.PodSelector)
	}
	egress := policies[1].Spec
	if len(egress.Ingress) != 0 || len(egress.Egress) != 3 {
		t.Fatalf("egress policy = %+v", egress)
	}
	for _, rule := range egress.Egress {
		for _, peer := range rule.To {
			if peer.IPBlock != nil {
				t.Fatalf("no ipBlock may be allowed (169.254.169.254 must stay unreachable): %+v", peer.IPBlock)
			}
			if peer.NamespaceSelector == nil || peer.PodSelector == nil {
				t.Fatalf("every peer is a namespace+pod selector: %+v", peer)
			}
		}
	}
	dns, flow, proxy := egress.Egress[0], egress.Egress[1], egress.Egress[2]
	if dns.To[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "kube-system" || dns.To[0].PodSelector.MatchLabels["k8s-app"] != "kube-dns" || len(dns.Ports) != 2 || dns.Ports[0].Port.IntValue() != 53 {
		t.Fatalf("dns rule = %+v", dns)
	}
	if flow.To[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "flow" || flow.To[0].PodSelector.MatchLabels["app.kubernetes.io/name"] != "flow" || len(flow.Ports) != 1 || flow.Ports[0].Port.IntValue() != 8090 {
		t.Fatalf("flow rule = %+v", flow)
	}
	if proxy.To[0].PodSelector.MatchLabels["app.kubernetes.io/name"] != "flow-egress" || proxy.Ports[0].Port.IntValue() != 3128 {
		t.Fatalf("proxy rule = %+v", proxy)
	}

	ctx := context.Background()
	if _, err := h.provider.StartSandbox(ctx, sessionSpec("s1")); err != nil {
		t.Fatal(err)
	}
	list, err := h.clientset.NetworkingV1().NetworkPolicies(testNamespace).List(ctx, metav1.ListOptions{})
	if err != nil || len(list.Items) != 2 {
		t.Fatalf("ensured policies = %v %v", list, err)
	}
	// Drift is patched back.
	drifted := list.Items[1].DeepCopy()
	drifted.Spec.Egress = nil
	if _, err := h.clientset.NetworkingV1().NetworkPolicies(testNamespace).Update(ctx, drifted, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := h.provider.EnsureNetworkPolicies(ctx); err != nil {
		t.Fatal(err)
	}
	fixed, _ := h.clientset.NetworkingV1().NetworkPolicies(testNamespace).Get(ctx, drifted.Name, metav1.GetOptions{})
	if len(fixed.Spec.Egress) != 3 {
		t.Fatalf("drift not repaired: %+v", fixed.Spec)
	}

	off := newHarness(t, func(c *config.CodingConfig) { c.Kubernetes.ManageNetworkPolicy = false })
	if _, err := off.provider.StartSandbox(ctx, sessionSpec("s1")); err != nil {
		t.Fatal(err)
	}
	if list, _ := off.clientset.NetworkingV1().NetworkPolicies(testNamespace).List(ctx, metav1.ListOptions{}); len(list.Items) != 0 {
		t.Fatal("policies must not be created when FLOW_CODING_K8S_NETWORK_POLICY=false")
	}
	// Without a dedicated runtime listener any Flow Pod port is allowed.
	if rule := newHarness(t, nil).provider.BuildNetworkPolicies()[1].Spec.Egress[1]; len(rule.Ports) != 0 {
		t.Fatalf("flow ports = %+v", rule.Ports)
	}
}

func TestStartSandboxIsIdempotentAndAdoptsExistingPods(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	spec := sessionSpec("s1")
	first, err := h.provider.StartSandbox(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if first.ProviderRef != "flow-cs-s1" || first.Phase != sandbox.PhasePending || first.ID != "s1" || first.Kind != sandbox.KindSession {
		t.Fatalf("status = %+v", first)
	}
	secret, err := h.clientset.CoreV1().Secrets(testNamespace).Get(ctx, "flow-cs-s1-token", metav1.GetOptions{})
	if err != nil || string(secret.Data["token"]) != "fcs_secret" || secret.Type != corev1.SecretTypeOpaque {
		t.Fatalf("secret = %+v %v", secret, err)
	}
	if len(secret.OwnerReferences) != 1 || secret.OwnerReferences[0].Kind != "Pod" || secret.OwnerReferences[0].Name != "flow-cs-s1" {
		t.Fatalf("secret must be owned by the pod: %+v", secret.OwnerReferences)
	}
	creates := countActions(h.clientset, "create", "pods")
	if _, err := h.provider.StartSandbox(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if countActions(h.clientset, "create", "pods") != creates {
		t.Fatal("a second start must not create another pod")
	}

	// The Pod is gone but its Secret survived: a fresh token replaces it.
	if err := h.clientset.CoreV1().Pods(testNamespace).Delete(ctx, "flow-cs-s1", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	spec.Token = "fcs_fresh"
	if _, err := h.provider.StartSandbox(ctx, spec); err != nil {
		t.Fatal(err)
	}
	secret, _ = h.clientset.CoreV1().Secrets(testNamespace).Get(ctx, "flow-cs-s1-token", metav1.GetOptions{})
	if string(secret.Data["token"]) != "fcs_fresh" {
		t.Fatalf("token = %q", secret.Data["token"])
	}

	if _, err := h.provider.StartSandbox(ctx, sandbox.SandboxSpec{ID: "Bad_ID", Token: "x"}); err == nil {
		t.Fatal("invalid ids must be rejected")
	}
	spec2 := sessionSpec("s9")
	spec2.Workspace.VolumeID = "missing"
	if _, err := h.provider.StartSandbox(ctx, spec2); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("a missing workspace volume = %v", err)
	}
}

func TestStartSandboxMapsQuotaAndTransientErrors(t *testing.T) {
	h := newHarness(t, nil)
	h.clientset.PrependReactor("create", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "flow-cs-s1", errors.New("exceeded quota: sandbox-quota, requested: cpu=2"))
	})
	if _, err := h.provider.StartSandbox(context.Background(), sessionSpec("s1")); !errors.Is(err, sandbox.ErrQuota) {
		t.Fatalf("quota = %v", err)
	}
	// Before the quota controller computed usage (envtest: the API server's
	// exact message) the rejection is capacity too, not a broken spec.
	unknown := apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "flow-cs-s1", errors.New("status unknown for quota: q, resources: pods"))
	if err := wrap("create pod", unknown); !errors.Is(err, sandbox.ErrQuota) {
		t.Fatalf("status unknown for quota = %v", err)
	}
	cases := []struct {
		err       error
		transient bool
		notFound  bool
	}{
		{apierrors.NewInternalError(errors.New("boom")), true, false},
		{apierrors.NewTooManyRequests("slow down", 1), true, false},
		{apierrors.NewServiceUnavailable("down"), true, false},
		{apierrors.NewConflict(schema.GroupResource{Resource: "pods"}, "x", errors.New("conflict")), true, false},
		{errors.New("dial tcp: connection refused"), true, false},
		{apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, "x"), false, true},
		{apierrors.NewBadRequest("invalid"), false, false},
		{apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "x", errors.New("rbac")), false, false},
	}
	for _, tc := range cases {
		err := wrap("op", tc.err)
		if errors.Is(err, sandbox.ErrTransient) != tc.transient || errors.Is(err, sandbox.ErrNotFound) != tc.notFound {
			t.Errorf("wrap(%v) = %v", tc.err, err)
		}
	}
}

func countActions(clientset *fake.Clientset, verb, resource string) int {
	n := 0
	for _, action := range clientset.Actions() {
		if action.GetVerb() == verb && action.GetResource().Resource == resource {
			n++
		}
	}
	return n
}

func TestPodStatusMapping(t *testing.T) {
	created := metav1.NewTime(time.Date(2026, 10, 18, 11, 0, 0, 0, time.UTC))
	started := metav1.NewTime(created.Add(time.Minute))
	base := func(phase corev1.PodPhase) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "flow-cs-s1", CreationTimestamp: created, Labels: map[string]string{sandbox.LabelSandboxID: "s1", sandbox.LabelKind: "session", sandbox.LabelRegion: "eu"}},
			Spec:       corev1.PodSpec{NodeName: "node-1"},
			Status:     corev1.PodStatus{Phase: phase},
		}
	}
	terminated := func(name string, code int32, reason string) corev1.ContainerStatus {
		return corev1.ContainerStatus{Name: name, State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: code, Reason: reason, StartedAt: started, FinishedAt: metav1.NewTime(started.Add(time.Minute))}}}
	}
	cases := []struct {
		name   string
		pod    func() *corev1.Pod
		phase  sandbox.Phase
		reason sandbox.Reason
	}{
		{"unscheduled", func() *corev1.Pod { return base(corev1.PodPending) }, sandbox.PhasePending, sandbox.ReasonNone},
		{"unschedulable", func() *corev1.Pod {
			pod := base(corev1.PodPending)
			pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: corev1.PodReasonUnschedulable, Message: "0/3 nodes"}}
			return pod
		}, sandbox.PhasePending, sandbox.ReasonUnschedulable},
		{"scheduled, pulling", func() *corev1.Pod {
			pod := base(corev1.PodPending)
			pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionTrue}}
			return pod
		}, sandbox.PhaseStarting, sandbox.ReasonNone},
		{"image pull backoff", func() *corev1.Pod {
			pod := base(corev1.PodPending)
			pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionTrue}}
			pod.Status.InitContainerStatuses = []corev1.ContainerStatus{{Name: "restore", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}}}}
			return pod
		}, sandbox.PhaseStarting, sandbox.ReasonImagePull},
		{"running", func() *corev1.Pod {
			pod := base(corev1.PodRunning)
			pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "runner", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: started}}}}
			return pod
		}, sandbox.PhaseRunning, sandbox.ReasonNone},
		{"succeeded", func() *corev1.Pod {
			pod := base(corev1.PodSucceeded)
			pod.Status.ContainerStatuses = []corev1.ContainerStatus{terminated("runner", 0, "Completed")}
			return pod
		}, sandbox.PhaseSucceeded, sandbox.ReasonNone},
		{"oom killed", func() *corev1.Pod {
			pod := base(corev1.PodFailed)
			pod.Status.ContainerStatuses = []corev1.ContainerStatus{terminated("runner", 137, "OOMKilled")}
			return pod
		}, sandbox.PhaseFailed, sandbox.ReasonOOMKilled},
		{"exit non-zero", func() *corev1.Pod {
			pod := base(corev1.PodFailed)
			pod.Status.ContainerStatuses = []corev1.ContainerStatus{terminated("runner", 2, "Error")}
			return pod
		}, sandbox.PhaseFailed, sandbox.ReasonExitNonZero},
		{"evicted", func() *corev1.Pod {
			pod := base(corev1.PodFailed)
			pod.Status.Reason = "Evicted"
			return pod
		}, sandbox.PhaseFailed, sandbox.ReasonEvicted},
		{"deadline exceeded", func() *corev1.Pod {
			pod := base(corev1.PodFailed)
			pod.Status.Reason = "DeadlineExceeded"
			return pod
		}, sandbox.PhaseFailed, sandbox.ReasonDeadlineExceeded},
		{"restore failed", func() *corev1.Pod {
			pod := base(corev1.PodFailed)
			pod.Status.InitContainerStatuses = []corev1.ContainerStatus{terminated("restore", 1, "Error")}
			return pod
		}, sandbox.PhaseFailed, sandbox.ReasonInitFailed},
		{"node lost", func() *corev1.Pod { return base(corev1.PodUnknown) }, sandbox.PhaseFailed, sandbox.ReasonUnknown},
	}
	h := newHarness(t, nil)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status := h.provider.podStatus(tc.pod())
			if status.Phase != tc.phase || status.Reason != tc.reason {
				t.Fatalf("got %s/%s, want %s/%s (%s)", status.Phase, status.Reason, tc.phase, tc.reason, status.Message)
			}
			if status.ID != "s1" || status.Node != "node-1" || status.Region != "eu" || !status.CreatedAt.Equal(created.Time) {
				t.Fatalf("status = %+v", status)
			}
		})
	}
	ctx := context.Background()
	if missing, err := h.provider.Status(ctx, "nope"); err != nil || missing.Phase != sandbox.PhaseMissing {
		t.Fatalf("missing = %+v %v", missing, err)
	}
}

func TestStopSandboxDeletesOwnObjectsOnly(t *testing.T) {
	h := newHarness(t, func(c *config.CodingConfig) { c.Snapshot.Strategy = sandbox.StrategyVolumeSnapshot })
	ctx := context.Background()
	if _, err := h.provider.StartSandbox(ctx, sessionSpec("s1")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.provider.EnsureVolume(ctx, sandbox.VolumeSpec{ID: "snap1"}); err != nil {
		t.Fatal(err)
	}
	build := sessionSpec("s2")
	build.Kind = sandbox.KindSnapshot
	build.Workspace.VolumeID = "snap1"
	if _, err := h.provider.StartSandbox(ctx, build); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"s1", "s2"} {
		if err := h.provider.StopSandbox(ctx, id, sandbox.StopOptions{Grace: 30 * time.Second}); err != nil {
			t.Fatal(err)
		}
		if err := h.provider.StopSandbox(ctx, id, sandbox.StopOptions{}); err != nil {
			t.Fatalf("stop is idempotent: %v", err)
		}
	}
	pods, _ := h.clientset.CoreV1().Pods(testNamespace).List(ctx, metav1.ListOptions{})
	secrets, _ := h.clientset.CoreV1().Secrets(testNamespace).List(ctx, metav1.ListOptions{})
	if len(pods.Items) != 0 || len(secrets.Items) != 0 {
		t.Fatalf("pods %d secrets %d left", len(pods.Items), len(secrets.Items))
	}
	if _, err := h.provider.VolumeStatus(ctx, "s1"); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("the sandbox's own PVC must be deleted: %v", err)
	}
	if _, err := h.provider.VolumeStatus(ctx, "snap1"); err != nil {
		t.Fatalf("the snapshot build PVC belongs to the strategy and must survive: %v", err)
	}
	var grace int64 = -1
	for _, action := range h.clientset.Actions() {
		if del, ok := action.(k8stesting.DeleteAction); ok && action.GetResource().Resource == "pods" && grace < 0 {
			if options := del.GetDeleteOptions(); options.GracePeriodSeconds != nil {
				grace = *options.GracePeriodSeconds
			}
		}
	}
	if grace != 60 { // 30s stop grace + RunnerWrapUp
		t.Fatalf("pod deletion grace = %d", grace)
	}

	// KeepVolume keeps the sandbox's own PVC.
	if _, err := h.provider.StartSandbox(ctx, sessionSpec("s3")); err != nil {
		t.Fatal(err)
	}
	if err := h.provider.StopSandbox(ctx, "s3", sandbox.StopOptions{KeepVolume: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.provider.VolumeStatus(ctx, "s3"); err != nil {
		t.Fatalf("KeepVolume: %v", err)
	}
}

func TestStopSandboxForceDeletesStuckPods(t *testing.T) {
	deleted := metav1.NewTime(time.Date(2026, 10, 18, 11, 50, 0, 0, time.UTC))
	stuck := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "flow-cs-s1", Namespace: testNamespace, DeletionTimestamp: &deleted, Finalizers: []string{"x"}, Labels: map[string]string{sandbox.LabelSandboxID: "s1"}}}
	h := newHarness(t, nil, stuck)
	if err := h.provider.StopSandbox(context.Background(), "s1", sandbox.StopOptions{Grace: time.Minute}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, action := range h.clientset.Actions() {
		if del, ok := action.(k8stesting.DeleteAction); ok && action.GetResource().Resource == "pods" {
			if options := del.GetDeleteOptions(); options.GracePeriodSeconds != nil && *options.GracePeriodSeconds == 0 {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("a pod terminating for more than twice the grace must be force-deleted")
	}
}

func TestListAndDeleteManaged(t *testing.T) {
	foreign := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: testNamespace, Labels: map[string]string{sandbox.LabelManagedBy: "flow", sandbox.LabelInstance: "staging", sandbox.LabelSandboxID: "x"}}}
	unrelated := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: testNamespace}}
	h := newHarness(t, func(c *config.CodingConfig) { c.Snapshot.Strategy = sandbox.StrategyVolumeSnapshot }, foreign, unrelated)
	ctx := context.Background()
	if _, err := h.provider.StartSandbox(ctx, sessionSpec("s1")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.provider.EnsureVolume(ctx, sandbox.VolumeSpec{ID: "snap1", Labels: map[string]string{sandbox.LabelSnapshotID: "snap1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.provider.CreateSnapshot(ctx, sandbox.SnapshotSpec{ID: "snap1", VolumeID: "snap1"}); err != nil {
		t.Fatal(err)
	}
	objects, err := h.provider.ListManaged(ctx, sandbox.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]int{}
	for _, object := range objects {
		types[object.Type]++
		if object.Labels[sandbox.LabelInstance] != "flow" {
			t.Fatalf("foreign object listed: %+v", object)
		}
	}
	if types[TypePod] != 1 || types[TypeSecret] != 1 || types[TypePVC] != 2 || types[TypeVolumeSnapshot] != 1 {
		t.Fatalf("listed = %v", types)
	}
	filtered, _ := h.provider.ListManaged(ctx, sandbox.ListFilter{SandboxID: "s1"})
	if len(filtered) != 3 {
		t.Fatalf("filtered = %+v", filtered)
	}
	for _, object := range objects {
		if err := h.provider.DeleteManaged(ctx, object); err != nil {
			t.Fatal(err)
		}
		if err := h.provider.DeleteManaged(ctx, object); err != nil {
			t.Fatalf("delete is idempotent: %v", err)
		}
	}
	if left, _ := h.provider.ListManaged(ctx, sandbox.ListFilter{}); len(left) != 0 {
		t.Fatalf("left = %+v", left)
	}
	// An object of another instance is never deleted, even if asked.
	if err := h.provider.DeleteManaged(ctx, sandbox.ManagedObject{Type: TypePod, Name: "other", Labels: foreign.Labels}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.clientset.CoreV1().Pods(testNamespace).Get(ctx, "other", metav1.GetOptions{}); err != nil {
		t.Fatal("a foreign instance's pod was deleted")
	}
}

func TestPreflight(t *testing.T) {
	results := func(h *harness) map[string]sandbox.PreflightResult {
		out := map[string]sandbox.PreflightResult{}
		for _, result := range h.provider.Preflight(context.Background()) {
			out[result.Name] = result
		}
		return out
	}
	allow := func(h *harness, denied ...string) {
		h.clientset.PrependReactor("create", "selfsubjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
			review := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectAccessReview)
			attributes := review.Spec.ResourceAttributes
			review.Status.Allowed = !slices.Contains(denied, attributes.Verb+" "+attributes.Resource)
			return true, review, nil
		})
	}
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNamespace, Labels: map[string]string{"pod-security.kubernetes.io/enforce": "restricted"}}}

	h := newHarness(t, func(c *config.CodingConfig) { c.Kubernetes.RuntimeClassName = "gvisor" }, namespace, &nodev1.RuntimeClass{ObjectMeta: metav1.ObjectMeta{Name: "gvisor"}})
	allow(h)
	got := results(h)
	for _, name := range []string{"namespace", "pod-security", "rbac", "runtime-class"} {
		if !got[name].OK || got[name].Warning {
			t.Errorf("%s = %+v", name, got[name])
		}
	}
	if _, ok := got["storage-class"]; ok {
		t.Error("archive with emptyDir needs no storage class check")
	}

	h = newHarness(t, func(c *config.CodingConfig) {
		c.Kubernetes.RuntimeClassName = "kata"
		c.Snapshot.Strategy = sandbox.StrategyVolumeSnapshot
		c.Kubernetes.StorageClass = "fast"
	})
	allow(h, "delete pods", "create volumesnapshots")
	got = results(h)
	if got["rbac"].OK || !strings.Contains(got["rbac"].Message, "delete pods") || !strings.Contains(got["rbac"].Message, "create volumesnapshots.snapshot.storage.k8s.io") {
		t.Errorf("rbac = %+v", got["rbac"])
	}
	if got["runtime-class"].OK {
		t.Errorf("a missing RuntimeClass must fail: %+v", got["runtime-class"])
	}
	if got["storage-class"].OK {
		t.Errorf("a missing StorageClass must fail: %+v", got["storage-class"])
	}
	if got["volume-snapshots"].OK {
		t.Errorf("a missing VolumeSnapshot CRD must fail: %+v", got["volume-snapshots"])
	}

	h = newHarness(t, func(c *config.CodingConfig) {
		c.Snapshot.Strategy = sandbox.StrategyVolumeSnapshot
	}, &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "standard", Annotations: map[string]string{"storageclass.kubernetes.io/is-default-class": "true"}}})
	allow(h)
	h.clientset.Discovery().(*fakediscovery.FakeDiscovery).Resources = []*metav1.APIResourceList{{GroupVersion: "snapshot.storage.k8s.io/v1", APIResources: []metav1.APIResource{{Name: "volumesnapshots"}}}}
	got = results(h)
	if !got["volume-snapshots"].OK || !got["storage-class"].OK || !got["runtime-class"].Warning {
		t.Errorf("results = %+v", got)
	}
}

type fakeExecutor struct {
	options remotecommand.StreamOptions
	err     error
}

func (f *fakeExecutor) Stream(options remotecommand.StreamOptions) error {
	return f.StreamWithContext(context.Background(), options)
}

func (f *fakeExecutor) StreamWithContext(_ context.Context, options remotecommand.StreamOptions) error {
	f.options = options
	if options.Stdout != nil {
		_, _ = io.WriteString(options.Stdout, "hello\n")
	}
	if options.TerminalSizeQueue != nil {
		if size := options.TerminalSizeQueue.Next(); size == nil || size.Width != 120 {
			return errors.New("resize not delivered")
		}
	}
	return f.err
}

func TestExecAndLogs(t *testing.T) {
	running := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "flow-cs-s1", Namespace: testNamespace, Labels: map[string]string{sandbox.LabelSandboxID: "s1"}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	h := newHarness(t, nil, running)
	executor := &fakeExecutor{}
	var gotURL *url.URL
	h.provider.newExecutor = func(execURL *url.URL) (remotecommand.Executor, error) {
		gotURL = execURL
		return executor, nil
	}
	ctx := context.Background()
	var out bytes.Buffer
	resizes := make(chan sandbox.TerminalSize, 1)
	resizes <- sandbox.TerminalSize{Width: 120, Height: 40}
	result, err := h.provider.Exec(ctx, "s1", sandbox.ExecRequest{Command: []string{"bash", "-l"}, TTY: true, Stdin: strings.NewReader(""), Stdout: &out, Resize: resizes})
	if err != nil || result.ExitCode != 0 || out.String() != "hello\n" {
		t.Fatalf("exec = %+v %v %q", result, err, out.String())
	}
	if gotURL.Host != "10.0.0.1:6443" || gotURL.Path != "/api/v1/namespaces/flow-sandboxes/pods/flow-cs-s1/exec" {
		t.Fatalf("exec url = %s", gotURL)
	}
	query := gotURL.Query()
	if !slices.Equal(query["command"], []string{"bash", "-l"}) || query.Get("container") != "runner" || query.Get("tty") != "true" || query.Get("stderr") != "" {
		t.Fatalf("exec query = %v", query)
	}
	executor.err = utilexec.CodeExitError{Err: errors.New("exit"), Code: 3}
	if result, err := h.provider.Exec(ctx, "s1", sandbox.ExecRequest{Command: []string{"false"}, Stdout: &out}); err != nil || result.ExitCode != 3 {
		t.Fatalf("exit code = %+v %v", result, err)
	}
	if _, err := h.provider.Exec(ctx, "nope", sandbox.ExecRequest{Command: []string{"true"}}); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("missing sandbox = %v", err)
	}
	stream, err := h.provider.Logs(ctx, "s1", sandbox.LogOptions{TailLines: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if data, _ := io.ReadAll(stream); len(data) == 0 {
		t.Fatal("logs are empty")
	}
}

// TestTerminatingObjects: objects held by finalizers (pvc-protection, the
// snapshot controller) are not GC candidates again and are never reused.
func TestTerminatingObjects(t *testing.T) {
	ctx := context.Background()
	deleting := func(at time.Time) metav1.ObjectMeta {
		return metav1.ObjectMeta{Namespace: testNamespace, DeletionTimestamp: &metav1.Time{Time: at}, Finalizers: []string{"kubernetes.io/pvc-protection"},
			Labels: map[string]string{sandbox.LabelManagedBy: "flow", sandbox.LabelInstance: "flow", sandbox.LabelSandboxID: "old"}}
	}
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: deleting(now.Add(-time.Minute))}
	pvc.Name = PVCName("old")
	secret := &corev1.Secret{ObjectMeta: deleting(now.Add(-time.Minute))}
	secret.Name = TokenSecretName("old")
	graceful := &corev1.Pod{ObjectMeta: deleting(now.Add(time.Minute))}
	graceful.Name = PodName("old")
	stuck := &corev1.Pod{ObjectMeta: deleting(now.Add(-time.Minute))}
	stuck.Name, stuck.Labels = PodName("stuck"), map[string]string{sandbox.LabelManagedBy: "flow", sandbox.LabelInstance: "flow", sandbox.LabelSandboxID: "stuck"}
	h := newHarness(t, nil, pvc, secret, graceful, stuck)

	objects, err := h.provider.ListManaged(ctx, sandbox.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 1 || objects[0].Name != PodName("stuck") {
		t.Fatalf("only the pod past its grace may be listed: %+v", objects)
	}
	if _, err := h.provider.EnsureVolume(ctx, sandbox.VolumeSpec{ID: "old"}); !errors.Is(err, sandbox.ErrTransient) {
		t.Fatalf("ensuring a terminating volume = %v", err)
	}
	if status, err := h.provider.VolumeStatus(ctx, "old"); err != nil || status.Ready || status.Phase != "Terminating" {
		t.Fatalf("terminating volume status = %+v %v", status, err)
	}
	spec := sessionSpec("s2")
	spec.Workspace.VolumeID = "old"
	if _, err := h.provider.StartSandbox(ctx, spec); !errors.Is(err, sandbox.ErrTransient) {
		t.Fatalf("starting on a terminating volume = %v", err)
	}
	if countActions(h.clientset, "create", "pods") != 0 {
		t.Fatal("no pod may mount a terminating volume")
	}
}

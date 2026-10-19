package kubernetes

import (
	"fmt"
	"maps"
	"math"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"flow/api/internal/config"
	"flow/api/internal/sandbox"
)

// Object names and fixed values (design §5, §9).
const (
	runnerContainer  = "runner"
	restoreContainer = "restore"
	tokenKey         = "token"
	tokenDir         = "/var/run/flow"
	// AnnotationVolumeID records which workspace volume a Pod mounts.
	AnnotationVolumeID = "flow.dev/volume-id"
	// NetworkPolicyDefaultDeny and NetworkPolicyEgress are the namespace-wide
	// sandbox policies Flow ensures when FLOW_CODING_K8S_NETWORK_POLICY=true.
	NetworkPolicyDefaultDeny = "flow-sandbox-default-deny"
	NetworkPolicyEgress      = "flow-sandbox-egress"
	homeSizeLimit            = "10Gi"
	tmpSizeLimit             = "4Gi"
	defaultProxyPort         = 3128
	// tokenFileMode is 0440, not 0400: with fsGroup the kubelet makes secret
	// files root-owned and group fsGroup, so the owner-only 0400 of the
	// design would be unreadable for uid 1000. Group 1000 is the runner only.
	tokenFileMode int32 = 0o440
)

// PodName is the Pod of a sandbox.
func PodName(sandboxID string) string { return sandbox.ObjectName("cs", sandboxID) }

// TokenSecretName is the Secret holding a sandbox's runtime token.
func TokenSecretName(sandboxID string) string { return PodName(sandboxID) + "-token" }

// PVCName is the PVC of a workspace volume.
func PVCName(volumeID string) string { return sandbox.ObjectName("ws", volumeID) }

// SnapshotName is the VolumeSnapshot of a snapshot id.
func SnapshotName(snapshotID string) string { return sandbox.ObjectName("snap", snapshotID) }

// settings are the parsed, validated provider settings.
type settings struct {
	coding      config.CodingConfig
	k8s         config.KubernetesSandboxConfig
	tolerations []corev1.Toleration
	sizes       map[string]sizeQuantities
	workspace   resource.Quantity
	home        resource.Quantity
	tmp         resource.Quantity
	usePVC      bool // workspace volumes default to PVCs
	pullSecrets []corev1.LocalObjectReference
}

type sizeQuantities struct {
	cpu, memory, ephemeral *resource.Quantity
}

// parseSettings validates everything once, so object building never fails
// on configuration.
func parseSettings(c config.CodingConfig) (settings, error) {
	s := settings{coding: c, k8s: c.Kubernetes, sizes: map[string]sizeQuantities{}}
	if s.k8s.Namespace == "" {
		return s, fmt.Errorf("FLOW_CODING_K8S_NAMESPACE is required")
	}
	if s.coding.InstanceID == "" {
		s.coding.InstanceID = "flow"
	}
	tolerations, err := ParseTolerations(s.k8s.Tolerations)
	if err != nil {
		return s, err
	}
	s.tolerations = tolerations
	for name, size := range c.Sizes {
		var q sizeQuantities
		for _, item := range []struct {
			raw, field string
			dst        **resource.Quantity
		}{{size.CPU, "cpu", &q.cpu}, {size.Memory, "memory", &q.memory}, {size.EphemeralStorage, "ephemeral", &q.ephemeral}} {
			if item.raw == "" {
				continue
			}
			parsed, err := resource.ParseQuantity(item.raw)
			if err != nil {
				return s, fmt.Errorf("FLOW_CODING_SIZE_%s: %s=%q: %w", strings.ToUpper(name), item.field, item.raw, err)
			}
			*item.dst = &parsed
		}
		s.sizes[name] = q
	}
	workspace := c.WorkspaceSize
	if workspace == "" {
		workspace = "20Gi"
	}
	if s.workspace, err = resource.ParseQuantity(workspace); err != nil {
		return s, fmt.Errorf("FLOW_CODING_WORKSPACE_SIZE: %w", err)
	}
	s.home = resource.MustParse(homeSizeLimit)
	s.tmp = resource.MustParse(tmpSizeLimit)
	switch s.k8s.WorkspaceVolume {
	case "pvc":
		s.usePVC = true
	case "emptyDir":
		s.usePVC = false
	default: // auto
		s.usePVC = c.Snapshot.Strategy == sandbox.StrategyVolumeSnapshot
	}
	for _, name := range c.ImagePullSecrets {
		if name = strings.TrimSpace(name); name != "" {
			s.pullSecrets = append(s.pullSecrets, corev1.LocalObjectReference{Name: name})
		}
	}
	if s.k8s.ServiceAccount == "" {
		s.k8s.ServiceAccount = "flow-sandbox"
	}
	return s, nil
}

// ParseTolerations reads "key[=value]:Effect" entries (kubectl taint syntax).
// "key:Effect" tolerates the key with any value; an empty effect tolerates
// every effect.
func ParseTolerations(entries []string) ([]corev1.Toleration, error) {
	result := []corev1.Toleration{}
	for _, raw := range entries {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		keyValue, effect, _ := strings.Cut(raw, ":")
		key, value, hasValue := strings.Cut(keyValue, "=")
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("FLOW_CODING_K8S_TOLERATIONS: %q has no key", raw)
		}
		toleration := corev1.Toleration{Key: key, Operator: corev1.TolerationOpExists}
		if hasValue {
			toleration.Operator, toleration.Value = corev1.TolerationOpEqual, strings.TrimSpace(value)
		}
		switch corev1.TaintEffect(strings.TrimSpace(effect)) {
		case "":
		case corev1.TaintEffectNoSchedule, corev1.TaintEffectPreferNoSchedule, corev1.TaintEffectNoExecute:
			toleration.Effect = corev1.TaintEffect(strings.TrimSpace(effect))
		default:
			return nil, fmt.Errorf("FLOW_CODING_K8S_TOLERATIONS: %q has an unknown effect (NoSchedule, PreferNoSchedule or NoExecute)", raw)
		}
		result = append(result, toleration)
	}
	return result, nil
}

// labels are a sandbox object's labels: configured extras first, the
// platform labels on top (they cannot be overridden).
func (s settings) labels(spec sandbox.SandboxSpec) map[string]string {
	out := maps.Clone(s.k8s.ExtraLabels)
	if out == nil {
		out = map[string]string{}
	}
	maps.Copy(out, sandbox.ObjectLabels(s.coding, spec))
	return out
}

func (s settings) managedLabels(extra map[string]string) map[string]string {
	out := maps.Clone(s.k8s.ExtraLabels)
	if out == nil {
		out = map[string]string{}
	}
	maps.Copy(out, sandbox.ManagedLabels(s.coding, extra))
	return out
}

func (s settings) annotations(extra map[string]string) map[string]string {
	if len(s.k8s.ExtraAnnotations) == 0 && len(extra) == 0 {
		return nil
	}
	out := maps.Clone(s.k8s.ExtraAnnotations)
	if out == nil {
		out = map[string]string{}
	}
	maps.Copy(out, extra)
	return out
}

// resources are limits from the size and requests = limits × ratio for CPU
// (memory and ephemeral storage requests equal their limits, so ratio 1 is
// Guaranteed QoS).
func (s settings) resources(spec sandbox.SandboxSpec) corev1.ResourceRequirements {
	limits, requests := corev1.ResourceList{}, corev1.ResourceList{}
	q := s.sizes[spec.Resources.Size]
	parse := func(raw string, fallback *resource.Quantity) *resource.Quantity {
		if raw != "" {
			if parsed, err := resource.ParseQuantity(raw); err == nil {
				return &parsed
			}
		}
		return fallback
	}
	cpu := parse(spec.Resources.CPU, q.cpu)
	memory := parse(spec.Resources.Memory, q.memory)
	ephemeral := parse(spec.Resources.EphemeralStorage, q.ephemeral)
	if cpu != nil {
		limits[corev1.ResourceCPU] = cpu.DeepCopy()
		ratio := s.coding.CPURequestRatio
		if ratio <= 0 || ratio > 1 {
			ratio = 1
		}
		milli := int64(math.Ceil(float64(cpu.MilliValue()) * ratio))
		requests[corev1.ResourceCPU] = *resource.NewMilliQuantity(max(milli, 1), resource.DecimalSI)
	}
	if memory != nil {
		limits[corev1.ResourceMemory] = memory.DeepCopy()
		requests[corev1.ResourceMemory] = memory.DeepCopy()
	}
	if ephemeral != nil {
		limits[corev1.ResourceEphemeralStorage] = ephemeral.DeepCopy()
		requests[corev1.ResourceEphemeralStorage] = ephemeral.DeepCopy()
	}
	return corev1.ResourceRequirements{Limits: limits, Requests: requests}
}

// containerSecurityContext satisfies the restricted Pod Security Standard.
func containerSecurityContext() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: new(false),
		ReadOnlyRootFilesystem:   new(true),
		RunAsNonRoot:             new(true),
		RunAsUser:                new(int64(sandbox.RunnerUID)),
		RunAsGroup:               new(int64(sandbox.RunnerUID)),
		Privileged:               new(false),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
}

func envVars(env []sandbox.EnvVar) []corev1.EnvVar {
	out := make([]corev1.EnvVar, 0, len(env))
	for _, item := range env {
		out = append(out, corev1.EnvVar{Name: item.Name, Value: item.Value})
	}
	return out
}

// workspaceVolumeID is the PVC a sandbox mounts, or "" for an emptyDir: an
// explicit volume (restored or a snapshot build), else a per-sandbox PVC
// when PVC workspaces are configured.
func (s settings) workspaceVolumeID(spec sandbox.SandboxSpec) string {
	if spec.Workspace.VolumeID != "" {
		return spec.Workspace.VolumeID
	}
	if s.usePVC {
		return spec.ID
	}
	return ""
}

// BuildPod is the Pod of a sandbox (design §9). It is a pure function of the
// settings and the spec, so a recreated Pod is identical.
func (s settings) BuildPod(spec sandbox.SandboxSpec) *corev1.Pod {
	spec = sandbox.CompleteSpec(s.coding, spec)
	mode := sandbox.Mode(spec.Kind)
	labels := s.labels(spec)
	volumeID := s.workspaceVolumeID(spec)
	annotations := map[string]string{}
	if volumeID != "" {
		annotations[AnnotationVolumeID] = volumeID
	}
	deadline := spec.Deadline
	grace := int64(sandbox.TerminationGrace(s.coding.Timeouts.StopGrace) / time.Second)
	if grace <= 0 {
		grace = 90
	}
	resources := s.resources(spec)

	workspaceMount := corev1.VolumeMount{Name: "workspace", MountPath: sandbox.WorkspacePath}
	homeMount := corev1.VolumeMount{Name: "home", MountPath: sandbox.HomePath}
	volumes := []corev1.Volume{}
	if volumeID != "" {
		// /workspace and /home/flow both live on the PVC so a VolumeSnapshot
		// captures mise installs too (design §10).
		workspaceMount.SubPath = "workspace"
		homeMount = corev1.VolumeMount{Name: "workspace", MountPath: sandbox.HomePath, SubPath: "home"}
		volumes = append(volumes, corev1.Volume{Name: "workspace", VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: PVCName(volumeID)},
		}})
	} else {
		volumes = append(volumes,
			corev1.Volume{Name: "workspace", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: new(s.workspace.DeepCopy())}}},
			corev1.Volume{Name: "home", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: new(s.home.DeepCopy())}}},
		)
	}
	volumes = append(volumes,
		corev1.Volume{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: new(s.tmp.DeepCopy())}}},
		corev1.Volume{Name: "token", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
			SecretName:  TokenSecretName(spec.ID),
			DefaultMode: new(tokenFileMode),
			Items:       []corev1.KeyToPath{{Key: tokenKey, Path: tokenKey}},
		}}},
	)
	mounts := []corev1.VolumeMount{
		workspaceMount,
		homeMount,
		{Name: "tmp", MountPath: "/tmp"},
		{Name: "token", MountPath: tokenDir, ReadOnly: true},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        PodName(spec.ID),
			Namespace:   s.k8s.Namespace,
			Labels:      labels,
			Annotations: s.annotations(annotations),
		},
		Spec: corev1.PodSpec{
			AutomountServiceAccountToken:  new(false),
			ServiceAccountName:            s.k8s.ServiceAccount,
			EnableServiceLinks:            new(false),
			RestartPolicy:                 corev1.RestartPolicyNever,
			ActiveDeadlineSeconds:         new(int64(math.Ceil(deadline.Seconds()))),
			TerminationGracePeriodSeconds: new(grace),
			PriorityClassName:             s.k8s.PriorityClassName,
			NodeSelector:                  maps.Clone(s.k8s.NodeSelector),
			Tolerations:                   append([]corev1.Toleration(nil), s.tolerations...),
			ImagePullSecrets:              append([]corev1.LocalObjectReference(nil), s.pullSecrets...),
			HostNetwork:                   false,
			HostPID:                       false,
			HostIPC:                       false,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:        new(true),
				RunAsUser:           new(int64(sandbox.RunnerUID)),
				RunAsGroup:          new(int64(sandbox.RunnerUID)),
				FSGroup:             new(int64(sandbox.RunnerUID)),
				FSGroupChangePolicy: new(corev1.FSGroupChangeOnRootMismatch),
				SeccompProfile:      &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{{
				Name:                     runnerContainer,
				Image:                    spec.Image,
				ImagePullPolicy:          corev1.PullPolicy(s.coding.ImagePullPolicy),
				Command:                  []string{"flow-runner", mode},
				Env:                      envVars(sandbox.RunnerEnv(s.coding, spec, mode)),
				Resources:                resources,
				SecurityContext:          containerSecurityContext(),
				VolumeMounts:             mounts,
				TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
			}},
			Volumes: volumes,
		},
	}
	if len(pod.Spec.NodeSelector) == 0 {
		pod.Spec.NodeSelector = nil
	}
	if len(pod.Spec.Tolerations) == 0 {
		pod.Spec.Tolerations = nil
	}
	if len(pod.Spec.ImagePullSecrets) == 0 {
		pod.Spec.ImagePullSecrets = nil
	}
	if s.k8s.RuntimeClassName != "" {
		pod.Spec.RuntimeClassName = new(s.k8s.RuntimeClassName)
	}
	if !s.k8s.HostUsers {
		pod.Spec.HostUsers = new(false)
	}
	if spec.Region != "" && s.coding.RegionLabel != "" {
		pod.Spec.Affinity = &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
				MatchExpressions: []corev1.NodeSelectorRequirement{{Key: s.coding.RegionLabel, Operator: corev1.NodeSelectorOpIn, Values: []string{spec.Region}}},
			}}},
		}}
	}
	if spec.Workspace.ArchiveURL != "" {
		pod.Spec.InitContainers = []corev1.Container{{
			Name:                     restoreContainer,
			Image:                    spec.Image,
			ImagePullPolicy:          corev1.PullPolicy(s.coding.ImagePullPolicy),
			Command:                  []string{"flow-runner", "restore"},
			Env:                      envVars(sandbox.RestoreEnv(s.coding, spec)),
			Resources:                resources,
			SecurityContext:          containerSecurityContext(),
			VolumeMounts:             mounts,
			TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
		}}
	}
	return pod
}

// BuildTokenSecret is the Secret holding the sandbox token (mounted as a
// file, never an env var).
func (s settings) BuildTokenSecret(spec sandbox.SandboxSpec) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: TokenSecretName(spec.ID), Namespace: s.k8s.Namespace, Labels: s.labels(spec), Annotations: s.annotations(nil)},
		Type:       corev1.SecretTypeOpaque,
		Immutable:  nil,
		Data:       map[string][]byte{tokenKey: []byte(spec.Token)},
	}
}

// BuildPVC is a workspace volume claim, optionally restored from a
// VolumeSnapshot.
func (s settings) BuildPVC(volumeID, size, storageClass string, labels map[string]string, fromSnapshot string) (*corev1.PersistentVolumeClaim, error) {
	quantity := s.workspace.DeepCopy()
	if size != "" {
		parsed, err := resource.ParseQuantity(size)
		if err != nil {
			return nil, fmt.Errorf("workspace volume size %q: %w", size, err)
		}
		quantity = parsed
	}
	if storageClass == "" {
		storageClass = s.k8s.StorageClass
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: PVCName(volumeID), Namespace: s.k8s.Namespace, Labels: s.managedLabels(labels), Annotations: s.annotations(nil)},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: quantity}},
		},
	}
	if storageClass != "" {
		pvc.Spec.StorageClassName = new(storageClass)
	}
	if fromSnapshot != "" {
		pvc.Spec.DataSource = &corev1.TypedLocalObjectReference{APIGroup: new(snapshotGroup), Kind: "VolumeSnapshot", Name: SnapshotName(fromSnapshot)}
	}
	return pvc, nil
}

// BuildNetworkPolicies are the namespace-wide sandbox policies: default deny
// for ingress and egress, plus an egress allow-list to DNS, the Flow runtime
// and the egress proxy. Nothing else is reachable, including the cloud
// metadata endpoint 169.254.169.254: no ipBlock rule exists, so link-local and
// every other address stay denied.
func (s settings) BuildNetworkPolicies() []*networkingv1.NetworkPolicy {
	selector := metav1.LabelSelector{MatchLabels: map[string]string{sandbox.LabelManagedBy: sandbox.ManagedByValue, sandbox.LabelInstance: s.coding.InstanceID}}
	labels := s.managedLabels(nil)
	udp, tcp := corev1.ProtocolUDP, corev1.ProtocolTCP
	port := func(n int, protocol corev1.Protocol) networkingv1.NetworkPolicyPort {
		value := intstr.FromInt32(int32(n))
		return networkingv1.NetworkPolicyPort{Protocol: &protocol, Port: &value}
	}
	peer := func(namespace string, pods map[string]string) networkingv1.NetworkPolicyPeer {
		return networkingv1.NetworkPolicyPeer{
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": namespace}},
			PodSelector:       &metav1.LabelSelector{MatchLabels: maps.Clone(pods)},
		}
	}
	flowRule := networkingv1.NetworkPolicyEgressRule{To: []networkingv1.NetworkPolicyPeer{peer(s.k8s.FlowNamespace, s.k8s.FlowPodSelector)}}
	// NetworkPolicy ports are Pod ports. The dedicated runtime listener's
	// port is known; otherwise any port of the Flow Pods is allowed (the
	// Service port in FLOW_CODING_RUNTIME_URL may differ from the Pod port).
	if runtimePort := sandbox.AddrPort(s.coding.RuntimeAddr); runtimePort > 0 {
		flowRule.Ports = []networkingv1.NetworkPolicyPort{port(runtimePort, tcp)}
	}
	proxyPort := defaultProxyPort
	if p := sandbox.URLPort(s.coding.Egress.ProxyURL); p > 0 && p != 80 && p != 443 {
		proxyPort = p
	}
	proxyRule := networkingv1.NetworkPolicyEgressRule{
		To:    []networkingv1.NetworkPolicyPeer{peer(s.k8s.ProxyNamespace, s.k8s.ProxyPodSelector)},
		Ports: []networkingv1.NetworkPolicyPort{port(proxyPort, tcp)},
	}
	dnsRule := networkingv1.NetworkPolicyEgressRule{
		To:    []networkingv1.NetworkPolicyPeer{peer("kube-system", map[string]string{"k8s-app": "kube-dns"})},
		Ports: []networkingv1.NetworkPolicyPort{port(53, udp), port(53, tcp)},
	}
	return []*networkingv1.NetworkPolicy{
		{
			ObjectMeta: metav1.ObjectMeta{Name: NetworkPolicyDefaultDeny, Namespace: s.k8s.Namespace, Labels: labels},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: selector,
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: NetworkPolicyEgress, Namespace: s.k8s.Namespace, Labels: labels},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: selector,
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
				Egress:      []networkingv1.NetworkPolicyEgressRule{dnsRule, flowRule, proxyRule},
			},
		},
	}
}

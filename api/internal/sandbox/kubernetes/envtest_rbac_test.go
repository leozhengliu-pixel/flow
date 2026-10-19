//go:build envtest

package kubernetes

// The Helm chart's RBAC against the provider: render the chart (helm
// template, KUBECONFIG=/dev/null), apply its namespace, ServiceAccounts,
// Role, RoleBinding, ClusterRole, ClusterRoleBinding, LimitRange and
// ResourceQuota, load the provider configuration from its ConfigMap, and run
// every provider operation as Flow's ServiceAccount (impersonation). Every
// call must be allowed, and every permission the Role grants must be used.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"

	"flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/sandbox"
)

// renderChart runs helm template for a scenario of the chart's own tests.
func renderChart(t *testing.T, scenario, releaseNamespace string, extra ...string) []*unstructured.Unstructured {
	t.Helper()
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is not installed")
	}
	chart, _ := filepath.Abs(filepath.Join("..", "..", "..", "..", "deploy", "helm", "flow"))
	home := t.TempDir()
	args := append([]string{"template", "flow", chart, "-f", filepath.Join(chart, "tests", "scenarios", scenario), "--namespace", releaseNamespace}, extra...)
	cmd := exec.Command(helm, args...)
	// Isolated from any kubeconfig and helm repository cache.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "KUBECONFIG=" + os.DevNull,
		"HELM_CACHE_HOME=" + filepath.Join(home, "cache"), "HELM_CONFIG_HOME=" + filepath.Join(home, "config"), "HELM_DATA_HOME=" + filepath.Join(home, "data")}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("helm template: %v\n%s", err, stderr.String())
	}
	objects, err := decodeYAMLStream(&stdout)
	if err != nil {
		t.Fatal(err)
	}
	return objects
}

// resourceFor maps the chart's kinds this test applies.
var resourceFor = map[string]schema.GroupVersionResource{
	"Namespace":          {Version: "v1", Resource: "namespaces"},
	"ServiceAccount":     {Version: "v1", Resource: "serviceaccounts"},
	"LimitRange":         {Version: "v1", Resource: "limitranges"},
	"ResourceQuota":      {Version: "v1", Resource: "resourcequotas"},
	"Role":               {Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"},
	"RoleBinding":        {Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"},
	"ClusterRole":        {Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"},
	"ClusterRoleBinding": {Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"},
	"RuntimeClass":       {Group: "node.k8s.io", Version: "v1", Resource: "runtimeclasses"},
	"NetworkPolicy":      {Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"},
}

// permission is one (group, resource[/subresource], verb) the API server
// authorizes; name is set for rules restricted by resourceNames.
type permission struct {
	group, resource, verb, name string
}

func (p permission) String() string {
	out := p.verb + " " + p.resource
	if p.group != "" {
		out += "." + p.group
	}
	if p.name != "" {
		out += " (" + p.name + ")"
	}
	return out
}

// rulePermissions expands a Role's or ClusterRole's rules.
func rulePermissions(t *testing.T, object *unstructured.Unstructured) []permission {
	t.Helper()
	rules, _, _ := unstructured.NestedSlice(object.Object, "rules")
	out := []permission{}
	for _, raw := range rules {
		rule := raw.(map[string]any)
		strings := func(key string) []string {
			values, _, _ := unstructured.NestedStringSlice(rule, key)
			return values
		}
		names := strings("resourceNames")
		if len(names) == 0 {
			names = []string{""}
		}
		for _, group := range strings("apiGroups") {
			for _, resource := range strings("resources") {
				for _, verb := range strings("verbs") {
					for _, name := range names {
						out = append(out, permission{group, resource, verb, name})
					}
				}
			}
		}
	}
	return out
}

// requestPermission is the permission an API request needs (ok=false for
// discovery and other non-resource requests).
func requestPermission(req request) (permission, bool) {
	parts := strings.Split(strings.Trim(req.path, "/"), "/")
	var group string
	var rest []string
	switch {
	case len(parts) >= 3 && parts[0] == "api":
		rest = parts[2:]
	case len(parts) >= 4 && parts[0] == "apis":
		group, rest = parts[1], parts[3:]
	default:
		return permission{}, false
	}
	if len(rest) >= 3 && rest[0] == "namespaces" {
		rest = rest[2:] // namespaced
	}
	resource, name, sub := rest[0], "", ""
	if len(rest) > 1 {
		name = rest[1]
	}
	if len(rest) > 2 {
		sub = rest[2]
		resource += "/" + sub
	}
	verb := map[string]string{"POST": "create", "PUT": "update", "PATCH": "patch", "DELETE": "delete", "GET": "get"}[req.method]
	if req.method == "GET" && name == "" {
		verb = "list"
		if strings.Contains(req.query, "watch=true") || strings.Contains(req.query, "watch=1") {
			verb = "watch"
		}
	}
	if req.method == "DELETE" && name == "" {
		verb = "deletecollection"
	}
	if group == "" && resource == "namespaces" {
		return permission{group, resource, verb, name}, true
	}
	return permission{group: group, resource: resource, verb: verb}, true
}

func covers(granted []permission, need permission) bool {
	for _, g := range granted {
		if g.group == need.group && g.resource == need.resource && g.verb == need.verb && (g.name == "" || g.name == need.name) {
			return true
		}
	}
	return false
}

// TestEnvtestHelmRBACIsExactlyEnough is the chart's Role/ClusterRole against
// every provider call, made as Flow's ServiceAccount, for the chart's coding
// scenarios: Kata + volumesnapshot + Flow-managed policies; the defaults
// (runc, archive, Helm-managed policies); policies off without the preflight
// ClusterRole; gVisor with user namespaces and every value set.
func TestEnvtestHelmRBACIsExactlyEnough(t *testing.T) {
	c := envtestCluster(t)
	c.ensureClusterObjects(t)
	c.installSnapshotCRDs(t)
	for i, scenario := range []string{"coding-kata-volumesnapshot.yaml", "coding-runc-archive.yaml", "coding-netpol-off.yaml", "coding-gvisor-full.yaml"} {
		t.Run(strings.TrimSuffix(scenario, ".yaml"), func(t *testing.T) {
			helmRBACScenario(t, c, scenario, fmt.Sprintf("flow-r%d-%d", i, time.Now().UnixNano()%1_000_000))
		})
	}
}

func helmRBACScenario(t *testing.T, c *cluster, scenario, releaseNamespace string) {
	ctx := ctxT(t)
	// Each scenario gets its own release and sandbox namespace.
	objects := renderChart(t, scenario, releaseNamespace, "--set", "sandboxes.namespace.name="+releaseNamespace+"-sandboxes")

	// Apply the RBAC and namespace objects (the release namespace first).
	if _, err := c.client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: releaseNamespace}}, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	var role, clusterRole *unstructured.Unstructured
	var configMap map[string]any
	sandboxNamespace, flowSA := "", ""
	sort.SliceStable(objects, func(i, j int) bool { return objects[i].GetKind() == "Namespace" && objects[j].GetKind() != "Namespace" })
	for _, object := range objects {
		kind := object.GetKind()
		if kind == "ConfigMap" && object.GetName() == "flow" {
			configMap, _, _ = unstructured.NestedMap(object.Object, "data")
		}
		gvr, ok := resourceFor[kind]
		if !ok {
			continue
		}
		switch kind {
		case "Role":
			role = object
		case "ClusterRole":
			clusterRole = object
		case "Namespace":
			sandboxNamespace = object.GetName()
		case "RoleBinding":
			subjects, _, _ := unstructured.NestedSlice(object.Object, "subjects")
			subject := subjects[0].(map[string]any)
			flowSA = fmt.Sprintf("system:serviceaccount:%s:%s", subject["namespace"], subject["name"])
		}
		api := c.dynamic.Resource(gvr)
		var err error
		if namespace := object.GetNamespace(); namespace != "" {
			_, err = api.Namespace(namespace).Create(ctx, object, metav1.CreateOptions{})
		} else {
			_, err = api.Create(ctx, object, metav1.CreateOptions{})
		}
		if err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatalf("apply %s %s: %v", kind, object.GetName(), err)
		}
	}
	if role == nil || configMap == nil || sandboxNamespace == "" || flowSA == "" {
		t.Fatalf("chart objects missing: role=%v configMap=%v namespace=%q sa=%q", role != nil, configMap != nil, sandboxNamespace, flowSA)
	}
	// Make the chart's ResourceQuota enforcing (its controller is not
	// running): status.hard = spec.hard, nothing used yet. Admission then
	// charges every create, so every container needs requests and limits.
	quotas, err := c.client.CoreV1().ResourceQuotas(sandboxNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, quota := range quotas.Items {
		quota.Status.Hard = quota.Spec.Hard
		quota.Status.Used = corev1.ResourceList{}
		for name, hard := range quota.Spec.Hard {
			zero := hard.DeepCopy()
			zero.Set(0)
			quota.Status.Used[name] = zero
		}
		if _, err := c.client.CoreV1().ResourceQuotas(sandboxNamespace).UpdateStatus(ctx, &quota, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	class := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": snapshotGroup + "/v1", "kind": "VolumeSnapshotClass",
		"metadata": map[string]any{"name": "csi-snapclass"}, "driver": "example.com/csi", "deletionPolicy": "Delete",
	}}
	if _, err := c.dynamic.Resource(volumeSnapshotClassGVR).Create(ctx, class, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}

	// The provider configuration is the chart's ConfigMap.
	t.Setenv("FLOW_APP_URL", "https://flow.example.com")
	// The chart puts the key in a Secret; any 32-byte test key will do.
	t.Setenv("FLOW_CODING_SECRET_KEY", "dGVzdC1rZXktdGVzdC1rZXktdGVzdC1rZXktdGVzdC0=")
	for key, value := range configMap {
		if strings.HasPrefix(key, "FLOW_CODING_") {
			t.Setenv(key, fmt.Sprint(value))
		}
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatalf("load the chart's configuration: %v", err)
	}
	coding := loaded.Coding
	if coding.Kubernetes.Namespace != sandboxNamespace || coding.Kubernetes.FlowNamespace != releaseNamespace {
		t.Fatalf("chart coding config = %+v", coding.Kubernetes)
	}
	volumeSnapshots := coding.Snapshot.Strategy == sandbox.StrategyVolumeSnapshot

	// Flow's ServiceAccount, recorded.
	rec := &recorder{}
	impersonated := rest.CopyConfig(c.admin)
	impersonated.Impersonate = rest.ImpersonationConfig{UserName: flowSA, Groups: []string{"system:serviceaccounts", "system:serviceaccounts:" + releaseNamespace, "system:authenticated"}}
	asFlow := rec.wrap(impersonated)
	provider := c.provider(t, coding, asFlow, nil)

	// Preflight: every check passes with the chart's RBAC (without the
	// preflight ClusterRole the cluster-scoped checks are warnings).
	for _, result := range provider.Preflight(ctx) {
		if !result.OK {
			t.Errorf("preflight %s failed: %s", result.Name, result.Message)
		}
		if result.Warning && (result.Name == "rbac" || (clusterRole != nil && (strings.Contains(result.Message, "cannot read") || strings.Contains(result.Message, "cannot list")))) {
			t.Errorf("preflight %s: %s", result.Name, result.Message)
		}
	}
	// Without a configured StorageClass preflight lists them.
	noClass := coding
	noClass.Kubernetes.StorageClass = ""
	_ = c.provider(t, noClass, asFlow, nil).Preflight(ctx)

	// A session sandbox (volumesnapshot ⇒ PVC workspace).
	session := envSpec("rbac1", "small")
	if _, err := provider.StartSandbox(ctx, session); err != nil {
		t.Fatalf("start as Flow: %v", err)
	}
	if _, err := provider.StartSandbox(ctx, session); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Status(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if stream, err := provider.Logs(ctx, session.ID, sandbox.LogOptions{TailLines: 5}); err != nil {
		t.Fatalf("logs as Flow: %v", err)
	} else {
		_, _ = io.Copy(io.Discard, stream)
		stream.Close()
	}
	c.setPodStatus(t, sandboxNamespace, PodName(session.ID), func(s *corev1.PodStatus) { s.Phase = corev1.PodRunning })
	// Exec is authorized, then fails: the Pod has no node to stream from.
	if _, err := provider.Exec(ctx, session.ID, sandbox.ExecRequest{Command: []string{"true"}, Stdout: io.Discard}); err == nil || strings.Contains(strings.ToLower(err.Error()), "forbidden") {
		t.Fatalf("exec as Flow = %v", err)
	}
	// Without "create pods/exec" even the WebSocket exec (a GET) is refused:
	// since Kubernetes 1.35 (AuthorizePodWebsocketUpgradeCreatePermission)
	// the upgrade needs create too, so the Role's create is required.
	execCreateNeeded := false
	{
		probe := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "exec-get-only", Namespace: sandboxNamespace}, Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
			{APIGroups: []string{""}, Resources: []string{"pods/exec"}, Verbs: []string{"get"}},
		}}
		binding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "exec-get-only", Namespace: sandboxNamespace},
			RoleRef:  rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: probe.Name},
			Subjects: []rbacv1.Subject{{Kind: "User", APIGroup: "rbac.authorization.k8s.io", Name: "exec-probe"}}}
		if _, err := c.client.RbacV1().Roles(sandboxNamespace).Create(ctx, probe, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.client.RbacV1().RoleBindings(sandboxNamespace).Create(ctx, binding, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		probeConfig := rest.CopyConfig(c.admin)
		probeConfig.Impersonate = rest.ImpersonationConfig{UserName: "exec-probe"}
		_, err := c.provider(t, coding, probeConfig, nil).Exec(ctx, session.ID, sandbox.ExecRequest{Command: []string{"true"}, Stdout: io.Discard})
		execCreateNeeded = err != nil && strings.Contains(strings.ToLower(err.Error()), "forbidden")
		t.Logf("exec with only get pods/exec: %v", err)
	}

	// Snapshot build, VolumeSnapshot, restore, delete.
	var strategy sandbox.SnapshotStrategy
	ref := ""
	if volumeSnapshots {
		if strategy, err = sandbox.NewStrategy(coding, provider, nil); err != nil {
			t.Fatal(err)
		}
		plan, err := strategy.PlanBuild(ctx, "rsnap1", "ws-rbac")
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := strategy.Finish(ctx, "rsnap1", plan); !errors.Is(err, sandbox.ErrPending) {
			t.Fatalf("finish as Flow = %v", err)
		}
		c.markSnapshotReady(t, sandboxNamespace, SnapshotName("rsnap1"), map[string]any{"readyToUse": true, "restoreSize": "1Gi"})
		if ref, _, err = strategy.Finish(ctx, "rsnap1", plan); err != nil {
			t.Fatal(err)
		}
		if _, err := strategy.Restore(ctx, ref, "rbac2", "ws-rbac"); err != nil {
			t.Fatalf("restore as Flow: %v", err)
		}
	}

	// Policy drift is repaired by a fresh process (get + patch).
	if coding.Kubernetes.ManageNetworkPolicy {
		drift := []byte(`{"spec":{"egress":null}}`)
		if _, err := c.client.NetworkingV1().NetworkPolicies(sandboxNamespace).Patch(ctx, NetworkPolicyEgress, types.MergePatchType, drift, metav1.PatchOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := c.provider(t, coding, asFlow, nil).EnsureNetworkPolicies(ctx); err != nil {
			t.Fatalf("repair policies as Flow: %v", err)
		}
	}

	// A Pod gone while its Secret survived: the token is rewritten (patch).
	zero := int64(0)
	if err := c.client.CoreV1().Pods(sandboxNamespace).Delete(ctx, PodName(session.ID), metav1.DeleteOptions{GracePeriodSeconds: &zero}); err != nil {
		t.Fatal(err)
	}
	session.Token = "fcs_rotated"
	if _, err := provider.StartSandbox(ctx, session); err != nil {
		t.Fatal(err)
	}

	// The volume API works in every mode (the Role always grants PVCs).
	if _, err := provider.VolumeStatus(ctx, "rorphan"); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("volume status as Flow = %v", err)
	}

	// Orphan GC lists and deletes as Flow.
	store := newEnvStore(time.Now)
	store.snapshots["rsnap1"] = domain.CodingSnapshotReady
	if _, err := provider.EnsureVolume(ctx, sandbox.VolumeSpec{ID: "rorphan", Labels: map[string]string{sandbox.LabelSnapshotID: "rorphan"}}); err != nil {
		t.Fatal(err)
	}
	gc := &sandbox.GC{Store: store, Provider: provider, Config: coding, Now: func() time.Time { return time.Now().Add(time.Hour) }}
	if deleted, err := gc.RunOnce(ctx); err != nil || deleted == 0 {
		t.Fatalf("gc as Flow: %d %v", deleted, err)
	}
	if strategy != nil {
		if err := strategy.Delete(ctx, ref); err != nil {
			t.Fatal(err)
		}
	}
	if err := provider.StopSandbox(ctx, "rbac2", sandbox.StopOptions{Grace: time.Second}); err != nil {
		t.Fatal(err)
	}

	// Every call was allowed, and needed no permission the chart lacks.
	granted := rulePermissions(t, role)
	if clusterRole != nil {
		granted = append(granted, rulePermissions(t, clusterRole)...)
	}
	// Every authenticated user may create SelfSubjectAccessReviews
	// (system:basic-user), which preflight uses.
	baseline := []permission{{group: "authorization.k8s.io", resource: "selfsubjectaccessreviews", verb: "create"}}
	used := []permission{}
	for _, req := range rec.snapshot() {
		need, ok := requestPermission(req)
		if !ok {
			continue
		}
		// Without the preflight ClusterRole, preflight's cluster-scoped
		// reads are refused by design (they become warnings).
		optional := clusterRole == nil && slices.Contains([]string{"runtimeclasses", "storageclasses", "volumesnapshotclasses"}, need.resource)
		if req.status == 403 && !optional {
			t.Errorf("%s %s?%s was forbidden for Flow's ServiceAccount", req.method, req.path, req.query)
		}
		if !covers(granted, need) && !covers(baseline, need) && !optional {
			t.Errorf("%s %s needs %s, which the chart does not grant", req.method, req.path, need)
		}
		used = append(used, need)
	}
	if execCreateNeeded {
		used = append(used, permission{resource: "pods/exec", verb: "create"})
	}
	// The Role (namespaced, with write access) must be exact. The preflight
	// ClusterRole is a fixed read-only set; a configuration may not need all
	// of it (no RuntimeClass, no PVC workspaces), which is only logged.
	for _, grant := range rulePermissions(t, role) {
		if !slices.ContainsFunc(used, func(need permission) bool { return covers([]permission{grant}, need) }) {
			t.Errorf("the chart's Role grants %s, which no provider call uses", grant)
		}
	}
	if clusterRole != nil {
		for _, grant := range rulePermissions(t, clusterRole) {
			if !slices.ContainsFunc(used, func(need permission) bool { return covers([]permission{grant}, need) }) {
				t.Logf("the preflight ClusterRole grants %s, unused by this configuration", grant)
			}
		}
	}
	if t.Failed() {
		for _, req := range rec.snapshot() {
			t.Logf("%d %s %s?%s", req.status, req.method, req.path, req.query)
		}
	}

	// And preflight's own list of required permissions agrees with the Role.
	for _, check := range provider.requiredAccess() {
		resource := check.resource
		if check.subresource != "" {
			resource += "/" + check.subresource
		}
		if !covers(granted, permission{group: check.group, resource: resource, verb: check.verb}) {
			t.Errorf("preflight requires %s %s, which the chart does not grant", check.verb, resource)
		}
	}
}

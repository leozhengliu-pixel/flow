//go:build envtest

package kubernetes

// TestAgainstRealAPIServer creates sandbox objects against a real
// kube-apiserver + etcd started from envtest binaries (FLOW_ENVTEST_ASSETS,
// a directory with kube-apiserver and etcd, e.g. from
// `setup-envtest use 1.34.x --bin-dir … -p path`). It never reads a
// kubeconfig. The namespace enforces the restricted Pod Security Standard,
// so the API server's own PodSecurity admission validates the Pod.
//
//	FLOW_ENVTEST_ASSETS=/path/to/k8s/1.34.1-linux-amd64 go test -tags envtest ./internal/sandbox/kubernetes/

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"flow/api/internal/config"
	"flow/api/internal/sandbox"
)

// Track A ports (5101-5109).
const (
	envtestEtcdPort      = 5101
	envtestEtcdPeerPort  = 5102
	envtestAPIServerPort = 5103
)

func startProcess(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	log, err := os.Create(filepath.Join(t.TempDir(), filepath.Base(name)+".log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		if t.Failed() {
			data, _ := os.ReadFile(log.Name())
			if len(data) > 4000 {
				data = data[len(data)-4000:]
			}
			t.Logf("%s log tail:\n%s", name, data)
		}
	})
}

func TestAgainstRealAPIServer(t *testing.T) {
	assets := os.Getenv("FLOW_ENVTEST_ASSETS")
	if assets == "" {
		t.Skip("FLOW_ENVTEST_ASSETS is not set")
	}
	dir := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(dir, "sa.key")
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600); err != nil {
		t.Fatal(err)
	}
	token := "envtest-" + fmt.Sprint(time.Now().UnixNano())
	tokenFile := filepath.Join(dir, "tokens.csv")
	if err := os.WriteFile(tokenFile, []byte(token+`,admin,admin,"system:masters"`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	etcdURL := fmt.Sprintf("http://127.0.0.1:%d", envtestEtcdPort)
	startProcess(t, filepath.Join(assets, "etcd"), "--data-dir", filepath.Join(dir, "etcd"), "--listen-client-urls", etcdURL, "--advertise-client-urls", etcdURL,
		"--listen-peer-urls", fmt.Sprintf("http://127.0.0.1:%d", envtestEtcdPeerPort), "--unsafe-no-fsync")
	startProcess(t, filepath.Join(assets, "kube-apiserver"), "--etcd-servers", etcdURL, "--cert-dir", filepath.Join(dir, "certs"),
		"--secure-port", fmt.Sprint(envtestAPIServerPort), "--bind-address", "127.0.0.1", "--service-cluster-ip-range", "10.0.0.0/24",
		"--service-account-key-file", keyFile, "--service-account-signing-key-file", keyFile, "--service-account-issuer", "https://localhost",
		"--token-auth-file", tokenFile, "--authorization-mode", "RBAC", "--allow-privileged=false")

	restConfig := &rest.Config{Host: fmt.Sprintf("https://127.0.0.1:%d", envtestAPIServerPort), BearerToken: token, TLSClientConfig: rest.TLSClientConfig{Insecure: true}}
	httpClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, Timeout: 2 * time.Second}
	deadline := time.Now().Add(60 * time.Second)
	for {
		request, _ := http.NewRequest(http.MethodGet, restConfig.Host+"/readyz", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		if response, err := httpClient.Do(request); err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("kube-apiserver did not become ready")
		}
		time.Sleep(500 * time.Millisecond)
	}
	client, err := k8s.NewForConfig(restConfig)
	if err != nil {
		t.Fatal(err)
	}
	dyn, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNamespace, Labels: map[string]string{"pod-security.kubernetes.io/enforce": "restricted"}}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().ServiceAccounts(testNamespace).Create(ctx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "flow-sandbox"}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*config.CodingConfig)
		spec   func() sandbox.SandboxSpec
	}{
		{"archive with restore", nil, func() sandbox.SandboxSpec {
			spec := sessionSpec("e1")
			spec.Workspace.ArchiveURL = "http://flow.flow.svc:8090/internal/coding-runtime/snapshots/snap1/archive"
			return spec
		}},
		{"pvc workspace, tolerations, region", func(c *config.CodingConfig) {
			c.Kubernetes.WorkspaceVolume = "pvc"
			c.Kubernetes.Tolerations = []string{"flow.dev/sandbox=true:NoSchedule"}
			c.Kubernetes.NodeSelector = map[string]string{"flow.dev/pool": "sandbox"}
		}, func() sandbox.SandboxSpec {
			spec := sessionSpec("e2")
			spec.Region = "eu-west"
			return spec
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testConfig()
			if tc.mutate != nil {
				tc.mutate(&c)
			}
			provider, err := NewWithClients(sandbox.Dependencies{Config: c}, client, dyn, restConfig)
			if err != nil {
				t.Fatal(err)
			}
			spec := tc.spec()
			status, err := provider.StartSandbox(ctx, spec)
			if err != nil {
				t.Fatalf("the API server (with restricted PodSecurity admission) rejected the sandbox: %v", err)
			}
			if status.Phase != sandbox.PhasePending || status.ProviderRef != PodName(spec.ID) {
				t.Fatalf("status = %+v", status)
			}
			if again, err := provider.StartSandbox(ctx, spec); err != nil || again.ProviderRef != status.ProviderRef {
				t.Fatalf("idempotent start = %+v %v", again, err)
			}
			secret, err := client.CoreV1().Secrets(testNamespace).Get(ctx, TokenSecretName(spec.ID), metav1.GetOptions{})
			if err != nil || len(secret.OwnerReferences) != 1 || secret.OwnerReferences[0].Kind != "Pod" {
				t.Fatalf("token secret = %+v %v", secret, err)
			}
			policies, err := client.NetworkingV1().NetworkPolicies(testNamespace).List(ctx, metav1.ListOptions{})
			if err != nil || len(policies.Items) != 2 {
				t.Fatalf("network policies = %v %v", policies, err)
			}
			if err := provider.EnsureNetworkPolicies(ctx); err != nil {
				t.Fatalf("policies must be re-ensurable: %v", err)
			}
			objects, err := provider.ListManaged(ctx, sandbox.ListFilter{SandboxID: spec.ID})
			if err != nil || len(objects) < 2 {
				t.Fatalf("managed = %+v %v", objects, err)
			}
			if err := provider.StopSandbox(ctx, spec.ID, sandbox.StopOptions{Grace: time.Second}); err != nil {
				t.Fatal(err)
			}
			if _, err := client.CoreV1().Secrets(testNamespace).Get(ctx, TokenSecretName(spec.ID), metav1.GetOptions{}); err == nil {
				t.Fatal("the token secret must be deleted")
			}
		})
	}

	// A privileged variant must be refused by the API server: proves the
	// namespace really enforces restricted, so the passes above mean something.
	provider, _ := NewWithClients(sandbox.Dependencies{Config: testConfig()}, client, dyn, restConfig)
	pod := provider.BuildPod(sessionSpec("e3"))
	pod.Spec.Containers[0].SecurityContext.AllowPrivilegeEscalation = new(true)
	if _, err := client.CoreV1().Pods(testNamespace).Create(ctx, pod, metav1.CreateOptions{}); err == nil {
		t.Fatal("PodSecurity admission must reject a non-restricted pod")
	}

	results := map[string]sandbox.PreflightResult{}
	for _, result := range provider.Preflight(ctx) {
		results[result.Name] = result
	}
	if !results["namespace"].OK || !results["rbac"].OK || !results["pod-security"].OK || results["pod-security"].Warning {
		t.Fatalf("preflight = %+v", results)
	}
	snapshotProvider, _ := NewWithClients(sandbox.Dependencies{Config: func() config.CodingConfig {
		c := testConfig()
		c.Snapshot.Strategy = sandbox.StrategyVolumeSnapshot
		return c
	}()}, client, dyn, restConfig)
	for _, result := range snapshotProvider.Preflight(ctx) {
		if result.Name == "volume-snapshots" && result.OK {
			t.Fatal("no VolumeSnapshot CRD is installed in envtest")
		}
	}
}

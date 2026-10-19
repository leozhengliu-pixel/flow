//go:build envtest

package kubernetes

// A real kube-apiserver + etcd for the envtest suite. The binaries come from
// setup-envtest (KUBEBUILDER_ASSETS or FLOW_ENVTEST_ASSETS: a directory with
// kube-apiserver and etcd); nothing ever reads a kubeconfig. There is no
// kubelet, scheduler or controller manager: Pods stay Pending unless a test
// patches their status, and no garbage collector or PVC controller runs.
//
//	KUBECONFIG=/dev/null KUBEBUILDER_ASSETS=$(setup-envtest use -p path) \
//	  go test -tags envtest ./internal/sandbox/kubernetes/

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"flow/api/internal/config"
	"flow/api/internal/sandbox"
)

// cluster is the shared envtest control plane.
type cluster struct {
	host      string
	token     string
	admin     *rest.Config
	client    k8s.Interface
	dynamic   dynamic.Interface
	processes []*exec.Cmd
	logs      []string
	dir       string
}

var (
	sharedCluster *cluster
	clusterErr    error
	clusterOnce   sync.Once
)

func envtestAssets() string {
	if dir := os.Getenv("KUBEBUILDER_ASSETS"); dir != "" {
		return dir
	}
	return os.Getenv("FLOW_ENVTEST_ASSETS")
}

func TestMain(m *testing.M) {
	// Never let anything in this process pick up the user's kubeconfig.
	_ = os.Setenv("KUBECONFIG", os.DevNull)
	code := m.Run()
	if sharedCluster != nil {
		sharedCluster.stop()
	}
	os.Exit(code)
}

// envtestCluster starts the control plane once per test binary, or skips.
func envtestCluster(t *testing.T) *cluster {
	t.Helper()
	assets := envtestAssets()
	if assets == "" {
		t.Skip("KUBEBUILDER_ASSETS (or FLOW_ENVTEST_ASSETS) is not set")
	}
	clusterOnce.Do(func() { sharedCluster, clusterErr = startCluster(assets) })
	if clusterErr != nil {
		t.Fatalf("envtest control plane: %v", clusterErr)
	}
	return sharedCluster
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func (c *cluster) start(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	// No inherited KUBECONFIG or proxy settings.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + c.dir, "KUBECONFIG=" + os.DevNull}
	logPath := filepath.Join(c.dir, filepath.Base(name)+".log")
	log, err := os.Create(logPath)
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		return err
	}
	c.processes = append(c.processes, cmd)
	c.logs = append(c.logs, logPath)
	return nil
}

func (c *cluster) stop() {
	for i := len(c.processes) - 1; i >= 0; i-- {
		_ = c.processes[i].Process.Kill()
		_, _ = c.processes[i].Process.Wait()
	}
	_ = os.RemoveAll(c.dir)
}

func (c *cluster) logTail() string {
	var out strings.Builder
	for _, path := range c.logs {
		data, _ := os.ReadFile(path)
		if len(data) > 3000 {
			data = data[len(data)-3000:]
		}
		fmt.Fprintf(&out, "--- %s\n%s\n", filepath.Base(path), data)
	}
	return out.String()
}

func startCluster(assets string) (*cluster, error) {
	dir, err := os.MkdirTemp("", "flow-envtest-")
	if err != nil {
		return nil, err
	}
	c := &cluster{dir: dir}
	fail := func(err error) (*cluster, error) {
		tail := c.logTail()
		c.stop()
		return nil, fmt.Errorf("%w\n%s", err, tail)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fail(err)
	}
	keyFile := filepath.Join(dir, "sa.key")
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600); err != nil {
		return fail(err)
	}
	c.token = "envtest-" + fmt.Sprint(time.Now().UnixNano())
	tokenFile := filepath.Join(dir, "tokens.csv")
	if err := os.WriteFile(tokenFile, []byte(c.token+`,admin,admin,"system:masters"`+"\n"), 0o600); err != nil {
		return fail(err)
	}
	ports := make([]int, 3)
	for i := range ports {
		if ports[i], err = freePort(); err != nil {
			return fail(err)
		}
	}
	etcdURL := fmt.Sprintf("http://127.0.0.1:%d", ports[0])
	if err := c.start(filepath.Join(assets, "etcd"), "--data-dir", filepath.Join(dir, "etcd"), "--listen-client-urls", etcdURL, "--advertise-client-urls", etcdURL,
		"--listen-peer-urls", fmt.Sprintf("http://127.0.0.1:%d", ports[1]), "--unsafe-no-fsync"); err != nil {
		return fail(err)
	}
	// Default admission plugins (PodSecurity, RuntimeClass, Priority,
	// LimitRanger, ResourceQuota, StorageObjectInUseProtection, …) are on.
	if err := c.start(filepath.Join(assets, "kube-apiserver"), "--etcd-servers", etcdURL, "--cert-dir", filepath.Join(dir, "certs"),
		"--secure-port", fmt.Sprint(ports[2]), "--bind-address", "127.0.0.1", "--service-cluster-ip-range", "10.0.0.0/24",
		"--service-account-key-file", keyFile, "--service-account-signing-key-file", keyFile, "--service-account-issuer", "https://localhost",
		"--token-auth-file", tokenFile, "--authorization-mode", "RBAC", "--allow-privileged=false"); err != nil {
		return fail(err)
	}
	c.host = fmt.Sprintf("https://127.0.0.1:%d", ports[2])
	c.admin = &rest.Config{Host: c.host, BearerToken: c.token, TLSClientConfig: rest.TLSClientConfig{Insecure: true}, QPS: 200, Burst: 400}
	httpClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, Timeout: 2 * time.Second}
	deadline := time.Now().Add(90 * time.Second)
	for {
		request, _ := http.NewRequest(http.MethodGet, c.host+"/readyz", nil)
		request.Header.Set("Authorization", "Bearer "+c.token)
		if response, err := httpClient.Do(request); err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			return fail(fmt.Errorf("kube-apiserver did not become ready"))
		}
		time.Sleep(300 * time.Millisecond)
	}
	if c.client, err = k8s.NewForConfig(c.admin); err != nil {
		return fail(err)
	}
	if c.dynamic, err = dynamic.NewForConfig(c.admin); err != nil {
		return fail(err)
	}
	// The default ServiceAccount controller is not running; namespaces
	// still work without it.
	return c, nil
}

// namespace creates a fresh namespace enforcing the restricted Pod Security
// Standard (the chart's default), with the sandbox ServiceAccount.
func (c *cluster) namespace(t *testing.T, prefix string) string {
	t.Helper()
	ctx := context.Background()
	name := fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()%1_000_000_000)
	labels := map[string]string{
		"pod-security.kubernetes.io/enforce":         "restricted",
		"pod-security.kubernetes.io/enforce-version": "latest",
	}
	if _, err := c.client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.client.CoreV1().ServiceAccounts(name).Create(ctx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "flow-sandbox"}, AutomountServiceAccountToken: new(false)}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	return name
}

// envConfig is testConfig for a namespace, with every size defined.
func envConfig(namespace string) config.CodingConfig {
	c := testConfig()
	c.Kubernetes.Namespace = namespace
	c.Kubernetes.ProxyNamespace = namespace
	c.Sizes["large"] = config.SandboxSize{CPU: "4", Memory: "32Gi", EphemeralStorage: "80Gi", PIDs: 16384}
	return c
}

// request is one API call seen by a recorder.
type request struct {
	method, path, query string
	status              int
}

// recorder records every request a rest.Config makes.
type recorder struct {
	mu       sync.Mutex
	requests []request
}

func (r *recorder) wrap(config *rest.Config) *rest.Config {
	config = rest.CopyConfig(config)
	config.WrapTransport = func(next http.RoundTripper) http.RoundTripper {
		return roundTripper(func(req *http.Request) (*http.Response, error) {
			response, err := next.RoundTrip(req)
			status := 0
			if response != nil {
				status = response.StatusCode
			}
			r.mu.Lock()
			r.requests = append(r.requests, request{method: req.Method, path: req.URL.Path, query: req.URL.RawQuery, status: status})
			r.mu.Unlock()
			return response, err
		})
	}
	return config
}

func (r *recorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = nil
}

func (r *recorder) snapshot() []request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]request(nil), r.requests...)
}

// count reports how many requests used method on a path containing part.
func (r *recorder) count(method, part string) int {
	n := 0
	for _, req := range r.snapshot() {
		if req.method == method && strings.Contains(req.path, part) {
			n++
		}
	}
	return n
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// provider builds a provider over config (the admin config unless one is
// given), optionally recording its requests.
func (c *cluster) provider(t *testing.T, coding config.CodingConfig, restConfig *rest.Config, now func() time.Time) *Provider {
	t.Helper()
	if restConfig == nil {
		restConfig = c.admin
	}
	client, err := k8s.NewForConfig(restConfig)
	if err != nil {
		t.Fatal(err)
	}
	dyn, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewWithClients(sandbox.Dependencies{Config: coding, Now: now}, client, dyn, restConfig)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

// setPodStatus simulates the kubelet: it writes the Pod's status subresource.
func (c *cluster) setPodStatus(t *testing.T, namespace, name string, mutate func(*corev1.PodStatus)) {
	t.Helper()
	ctx := context.Background()
	err := wait.PollUntilContextTimeout(ctx, 50*time.Millisecond, 10*time.Second, true, func(ctx context.Context) (bool, error) {
		pod, err := c.client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		mutate(&pod.Status)
		_, err = c.client.CoreV1().Pods(namespace).UpdateStatus(ctx, pod, metav1.UpdateOptions{})
		if apierrors.IsConflict(err) {
			return false, nil
		}
		return err == nil, err
	})
	if err != nil {
		t.Fatalf("set pod status %s: %v", name, err)
	}
}

// bindPod simulates the scheduler (pods/binding), so deletes become graceful.
func (c *cluster) bindPod(t *testing.T, namespace, name, node string) {
	t.Helper()
	binding := &corev1.Binding{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}, Target: corev1.ObjectReference{Kind: "Node", Name: node}}
	if err := c.client.CoreV1().Pods(namespace).Bind(context.Background(), binding, metav1.CreateOptions{}); err != nil {
		t.Fatalf("bind %s: %v", name, err)
	}
}

var crdGVR = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}

var crdOnce sync.Once

// installSnapshotCRDs installs the vendored external-snapshotter v8.2.0 CRDs
// (testdata/crds) once and waits until they are served.
func (c *cluster) installSnapshotCRDs(t *testing.T) {
	t.Helper()
	var installErr error
	crdOnce.Do(func() { installErr = c.applyCRDs() })
	if installErr != nil {
		t.Fatal(installErr)
	}
}

func (c *cluster) applyCRDs() error {
	ctx := context.Background()
	files, err := filepath.Glob(filepath.Join("testdata", "crds", "*.yaml"))
	if err != nil || len(files) == 0 {
		return fmt.Errorf("no CRDs in testdata/crds: %v", err)
	}
	for _, file := range files {
		objects, err := decodeYAML(file)
		if err != nil {
			return err
		}
		for _, object := range objects {
			if _, err := c.dynamic.Resource(crdGVR).Create(ctx, object, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
				return fmt.Errorf("create CRD %s: %w", object.GetName(), err)
			}
		}
	}
	return wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		resources, err := c.client.Discovery().ServerResourcesForGroupVersion(snapshotGroup + "/v1")
		if err != nil {
			return false, nil
		}
		found := 0
		for _, resource := range resources.APIResources {
			switch resource.Name {
			case "volumesnapshots", "volumesnapshotclasses", "volumesnapshotcontents":
				found++
			}
		}
		return found == 3, nil
	})
}

// decodeYAML reads a multi-document YAML file into objects.
func decodeYAML(path string) ([]*unstructured.Unstructured, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return decodeYAMLStream(file)
}

func decodeYAMLStream(reader io.Reader) ([]*unstructured.Unstructured, error) {
	decoder := utilyaml.NewYAMLOrJSONDecoder(reader, 4096)
	out := []*unstructured.Unstructured{}
	for {
		object := map[string]any{}
		if err := decoder.Decode(&object); err == io.EOF {
			return out, nil
		} else if err != nil {
			return nil, err
		}
		if len(object) == 0 {
			continue
		}
		out = append(out, &unstructured.Unstructured{Object: object})
	}
}

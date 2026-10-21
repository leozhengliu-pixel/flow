package sandbox

import (
	"context"
	"strings"
	"testing"

	"flow/api/internal/config"
)

// envMap flattens RunnerEnv, failing on duplicate names (the platform
// variables must be written exactly once).
func envMap(t *testing.T, env []EnvVar) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, item := range env {
		if _, dup := out[item.Name]; dup {
			t.Fatalf("variable %s set twice", item.Name)
		}
		out[item.Name] = item.Value
	}
	return out
}

// The container environment carries the proxy without any credential: the
// identity is per sandbox, rotates with the token file, and is added by the
// runner at spawn time. The proxy variables are identical in both cases and
// NO_PROXY/no_proxy keep their meaning.
func TestRunnerEnvProxyHasNoCredentials(t *testing.T) {
	const sentinel = "fcs_SENTINEL_never_in_env"
	c := config.CodingConfig{RuntimeURL: "http://flow.flow.svc:8090", Egress: config.CodingEgressConfig{NoProxy: []string{"internal.example.com"}}}
	for _, test := range []struct {
		name, proxy, want string
	}{
		{"plain", "http://flow-egress:3128", "http://flow-egress:3128"},
		{"trailing space", " http://flow-egress:3128 ", "http://flow-egress:3128"},
		{"configured credentials are dropped", "http://shared:operator-secret@flow-egress:3128", "http://flow-egress:3128"},
		{"configured encoded credentials are dropped", "http://a%40b:p%3Aw%2Fd@flow-egress.flow.svc:3128/", "http://flow-egress.flow.svc:3128/"},
		{"user without password is dropped", "http://someone@flow-egress:3128", "http://flow-egress:3128"},
	} {
		for _, kind := range []Kind{KindSession, KindSnapshot, KindDebug, KindWarm} {
			t.Run(test.name+"/"+string(kind), func(t *testing.T) {
				spec := SandboxSpec{ID: "01jabc", Kind: kind, Token: sentinel, ProxyURL: test.proxy}
				env := envMap(t, RunnerEnv(c, spec, Mode(kind)))
				for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
					if env[name] != test.want {
						t.Fatalf("%s = %q, want %q", name, env[name], test.want)
					}
				}
				if env["NO_PROXY"] == "" || env["NO_PROXY"] != env["no_proxy"] {
					t.Fatalf("NO_PROXY %q / no_proxy %q", env["NO_PROXY"], env["no_proxy"])
				}
				for _, host := range []string{"localhost", "127.0.0.1", "::1", "flow.flow.svc", "internal.example.com"} {
					if !strings.Contains(env["NO_PROXY"], host) {
						t.Fatalf("NO_PROXY %q lacks %s", env["NO_PROXY"], host)
					}
				}
				for name, value := range env {
					if strings.Contains(value, sentinel) || strings.Contains(value, "operator-secret") || strings.Contains(value, "@") {
						t.Fatalf("%s carries a credential", name)
					}
				}
			})
		}
	}
}

func TestRunnerEnvWithoutProxy(t *testing.T) {
	c := config.CodingConfig{RuntimeURL: "http://flow:8090"}
	for _, proxy := range []string{"", "   ", "not a url"} {
		env := envMap(t, RunnerEnv(c, SandboxSpec{ID: "01jabc", Kind: KindSession, ProxyURL: proxy}, "session"))
		for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
			if _, ok := env[name]; ok {
				t.Fatalf("proxy %q: %s set without a proxy", proxy, name)
			}
		}
	}
	env := envMap(t, RunnerEnv(c, SandboxSpec{ID: "01jabc", Kind: KindSession}, "session"))
	if _, ok := env["NO_PROXY"]; ok {
		t.Fatal("NO_PROXY without a proxy")
	}
	// The environment's own variables never override the platform's proxy
	// variables, in either case.
	spec := SandboxSpec{ID: "01jabc", Kind: KindSession, ProxyURL: "http://flow-egress:3128", Env: map[string]string{"https_proxy": "http://evil:1", "HTTP_PROXY": "http://evil:1", "no_proxy": "*", "ALL_PROXY": "socks5://evil:1"}}
	env = envMap(t, RunnerEnv(c, spec, "session"))
	if env["https_proxy"] != "http://flow-egress:3128" || env["HTTP_PROXY"] != "http://flow-egress:3128" || env["no_proxy"] == "*" || env["ALL_PROXY"] != "" {
		t.Fatalf("environment overrode the proxy: %v", env)
	}
}

// Recovery and rotation: every start mints a new token, but the rendered
// environment is the same and never contains either token, so a container
// adopted after a token rewrite (docker "created", OpenSandbox adopt) is
// not left with a stale credential in its environment.
func TestReconcilerSpecEnvIsStableAcrossTokenRotation(t *testing.T) {
	w := newWorld()
	r := w.reconciler("flow-a")
	r.Config.Egress.ProxyURL = "http://shared:operator-secret@proxy:3128"
	record := sessionRecord("s1")
	w.store.add(record)
	first, err := w.store.MintSandboxToken(context.Background(), record, r.Config.Timeouts.TokenGrace)
	if err != nil {
		t.Fatal(err)
	}
	second, err := w.store.MintSandboxToken(context.Background(), record, r.Config.Timeouts.TokenGrace)
	if err != nil || first == second {
		t.Fatal("rotation must mint a different token")
	}
	specA, specB := r.Spec(record, w.clock.Now()), r.Spec(record, w.clock.Now())
	specA.Token, specB.Token = first, second
	envA, envB := envMap(t, RunnerEnv(r.Config, specA, "session")), envMap(t, RunnerEnv(r.Config, specB, "session"))
	for name, value := range envA {
		if envB[name] != value {
			t.Fatalf("%s differs across rotation", name)
		}
		if strings.Contains(value, first) || strings.Contains(value, second) || strings.Contains(value, "operator-secret") {
			t.Fatalf("%s carries a credential", name)
		}
	}
	if envA["HTTPS_PROXY"] != "http://proxy:3128" {
		t.Fatalf("HTTPS_PROXY = %q", envA["HTTPS_PROXY"])
	}
}

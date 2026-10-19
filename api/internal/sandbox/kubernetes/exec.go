package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"

	"flow/api/internal/sandbox"
)

// executorFactory builds a remote command stream for a pods/exec URL.
type executorFactory func(execURL *url.URL) (remotecommand.Executor, error)

// defaultExecutor prefers the WebSocket exec protocol (Kubernetes ≥1.30) and
// falls back to SPDY when the API server or a proxy refuses the upgrade.
func defaultExecutor(config *rest.Config) executorFactory {
	if config == nil {
		return nil
	}
	return func(execURL *url.URL) (remotecommand.Executor, error) {
		spdy, err := remotecommand.NewSPDYExecutor(config, "POST", execURL)
		if err != nil {
			return nil, err
		}
		websocket, err := remotecommand.NewWebSocketExecutor(config, "GET", execURL.String())
		if err != nil {
			return spdy, nil
		}
		return remotecommand.NewFallbackExecutor(websocket, spdy, func(err error) bool {
			return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
		})
	}
}

// execURL is the pods/exec subresource URL of a Pod.
func (p *Provider) execURL(podName string, options *corev1.PodExecOptions) (*url.URL, error) {
	host := "https://kubernetes.default.svc"
	if p.restConfig != nil && p.restConfig.Host != "" {
		host = p.restConfig.Host
	}
	if !strings.Contains(host, "://") {
		host = "https://" + host
	}
	base, err := url.Parse(host)
	if err != nil {
		return nil, err
	}
	params, err := scheme.ParameterCodec.EncodeParameters(options, corev1.SchemeGroupVersion)
	if err != nil {
		return nil, err
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/api/v1/namespaces/" + p.k8s.Namespace + "/pods/" + podName + "/exec"
	base.RawQuery = params.Encode()
	return base, nil
}

// sizeQueue adapts the request's resize channel.
type sizeQueue struct {
	ctx     context.Context
	resizes <-chan sandbox.TerminalSize
}

func (q sizeQueue) Next() *remotecommand.TerminalSize {
	select {
	case <-q.ctx.Done():
		return nil
	case size, ok := <-q.resizes:
		if !ok {
			return nil
		}
		return &remotecommand.TerminalSize{Width: size.Width, Height: size.Height}
	}
}

// Exec runs a command in the runner container (debug terminal, probes). A
// non-zero exit is a result, not an error.
func (p *Provider) Exec(ctx context.Context, sandboxID string, request sandbox.ExecRequest) (sandbox.ExecResult, error) {
	if p.newExecutor == nil {
		return sandbox.ExecResult{}, fmt.Errorf("%w: %w", sandbox.ErrUnsupported, errNoRestConfig)
	}
	if len(request.Command) == 0 {
		return sandbox.ExecResult{}, errors.New("kubernetes: exec needs a command")
	}
	pod, err := p.client.CoreV1().Pods(p.k8s.Namespace).Get(ctx, PodName(sandboxID), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return sandbox.ExecResult{}, sandbox.ErrNotFound
	}
	if err != nil {
		return sandbox.ExecResult{}, wrap("get pod", err)
	}
	if pod.Status.Phase != corev1.PodRunning {
		return sandbox.ExecResult{}, fmt.Errorf("kubernetes: sandbox %s is not running (%s)", sandboxID, pod.Status.Phase)
	}
	container := request.Container
	if container == "" {
		container = runnerContainer
	}
	options := &corev1.PodExecOptions{
		Container: container,
		Command:   request.Command,
		Stdin:     request.Stdin != nil,
		Stdout:    request.Stdout != nil,
		Stderr:    request.Stderr != nil && !request.TTY,
		TTY:       request.TTY,
	}
	execURL, err := p.execURL(pod.Name, options)
	if err != nil {
		return sandbox.ExecResult{}, err
	}
	executor, err := p.newExecutor(execURL)
	if err != nil {
		return sandbox.ExecResult{}, fmt.Errorf("kubernetes: exec: %w", err)
	}
	stream := remotecommand.StreamOptions{Stdin: request.Stdin, Stdout: request.Stdout, Tty: request.TTY}
	if !request.TTY {
		stream.Stderr = request.Stderr
	}
	if request.TTY && request.Resize != nil {
		stream.TerminalSizeQueue = sizeQueue{ctx: ctx, resizes: request.Resize}
	}
	err = executor.StreamWithContext(ctx, stream)
	var exitErr utilexec.ExitError
	if errors.As(err, &exitErr) && exitErr.Exited() {
		return sandbox.ExecResult{ExitCode: exitErr.ExitStatus()}, nil
	}
	if err != nil {
		return sandbox.ExecResult{}, fmt.Errorf("kubernetes: exec: %w", err)
	}
	return sandbox.ExecResult{ExitCode: 0}, nil
}

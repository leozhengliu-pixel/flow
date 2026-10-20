package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
)

// Git relay (coding.GitRelay, docs/coding-sessions-security.md "Member
// credentials"): a session that pushes as its creator has origin's push URL
// pointed here. The sandbox authenticates with the opaque placeholder its
// credential helper got (HTTP Basic, as git sends it); Flow checks that the
// request is a git smart-HTTP request for the session's one repository,
// replaces the placeholder with the member's token and forwards it to the
// code host. The token never reaches the sandbox, and nothing but the three
// smart-HTTP endpoints of that repository is ever forwarded.
//
// Owner: track B (session core). Keep the route list in sync with publicapi.InternalRoutes.
func (s *server) registerCodingGitRelayRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /internal/coding-runtime/git/{path...}", s.codingGitRelay)
	mux.HandleFunc("POST /internal/coding-runtime/git/{path...}", s.codingGitRelay)
}

// gitRelayMaxBody caps one relayed request body (a push pack). GitHub
// refuses pushes over 2 GiB.
const gitRelayMaxBody = 2 << 30

// gitRelayUpstreamBase is the code host's base URL for a session's host.
// Repositories are cloned from https://<host>/<repository>.git
// (coding.CloneURL), so the relay forwards to the same place. Tests point it
// at fake hosts.
var gitRelayUpstreamBase = func(hostName string) string { return "https://" + hostName }

// gitRelayForwardHeaders are the request headers git's smart-HTTP client
// sends that the host needs. Authorization, cookies and everything else are
// dropped.
var gitRelayForwardHeaders = []string{"Accept", "Accept-Encoding", "Content-Type", "Content-Encoding", "Git-Protocol", "User-Agent"}

// gitRelayReturnHeaders are the response headers git needs. Notably
// WWW-Authenticate is never passed back, so a refused credential does not
// make git ask the helper again.
var gitRelayReturnHeaders = []string{"Content-Type", "Content-Encoding", "Cache-Control", "Expires", "Pragma"}

func (s *server) codingGitRelay(w http.ResponseWriter, r *http.Request) {
	if !s.codingEnabled() || s.coding.service == nil {
		http.NotFound(w, r)
		return
	}
	path, ok := coding.ParseGitRelayPath(r.Method, r.PathValue("path"), r.URL.Query().Get("service"))
	if !ok {
		// Only smart-HTTP requests for a repository are relayed: no dumb
		// HTTP, no API paths, nothing that could reach the host otherwise.
		http.Error(w, "Not a git smart-HTTP request", http.StatusNotFound)
		return
	}
	_, placeholder, hasAuth := r.BasicAuth()
	if !hasAuth || placeholder == "" {
		// git sends credentials only after a 401 challenge; the helper then
		// answers with the placeholder.
		w.Header().Set("WWW-Authenticate", `Basic realm="Flow git relay"`)
		http.Error(w, "Authentication required", http.StatusUnauthorized)
		return
	}
	target, err := s.coding.service.GitRelay(r.Context(), placeholder, path)
	if err != nil {
		status, _ := codingErrorStatus(err)
		if status == http.StatusInternalServerError {
			log.Printf("coding git relay %s %s: %v", r.Method, path.Repository, err)
		}
		http.Error(w, strings.TrimPrefix(codingRelayMessage(err, status), "coding: "), status)
		return
	}
	base := strings.TrimRight(gitRelayUpstreamBase(target.HostName), "/")
	client, err := s.gitRelayClient(r.Context(), target, base)
	if err != nil {
		http.Error(w, "The code host is not reachable from Flow", http.StatusBadGateway)
		return
	}
	endpoint := base + "/" + target.Repository + ".git/"
	if path.Advertisement {
		endpoint += "info/refs?service=" + path.Service
	} else {
		endpoint += path.Service
	}
	ctx, cancel := context.WithTimeout(r.Context(), coding.GitRelayTimeout)
	defer cancel()
	var body io.Reader
	if r.Method == http.MethodPost {
		body = http.MaxBytesReader(w, r.Body, gitRelayMaxBody)
	}
	upstream, err := http.NewRequestWithContext(ctx, r.Method, endpoint, body)
	if err != nil {
		http.Error(w, "Invalid relay request", http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodPost {
		upstream.ContentLength = r.ContentLength
	}
	for _, name := range gitRelayForwardHeaders {
		if value := r.Header.Get(name); value != "" {
			upstream.Header.Set(name, value)
		}
	}
	upstream.SetBasicAuth(target.Username, target.Password)
	response, err := client.Do(upstream)
	if err != nil {
		log.Printf("coding git relay session=%s %s: %v", target.SessionID, path.Service, err)
		http.Error(w, "Could not reach the code host", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		// The host refused the member's (or workspace's) credential. The
		// body is not passed on: it could echo request details.
		http.Error(w, "The code host refused the push credential", http.StatusForbidden)
		return
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		http.Error(w, "The code host redirected the request", http.StatusBadGateway)
		return
	}
	for _, name := range gitRelayReturnHeaders {
		if value := response.Header.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	flusher, _ := w.(http.Flusher)
	buffer := make([]byte, 32<<10)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			if _, err := w.Write(buffer[:n]); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				log.Printf("coding git relay session=%s %s: reading the host response: %v", target.SessionID, path.Service, readErr)
			}
			return
		}
	}
}

func codingRelayMessage(err error, status int) string {
	if status == http.StatusInternalServerError {
		return "Coding sessions: internal error"
	}
	return err.Error()
}

// gitRelayClient reaches the session's code host: the GitLab integration's
// client for GitLab (admin-configured instances only), the hardened public
// HTTPS client for GitHub. Redirects are never followed (they would carry
// the credential elsewhere), and the request timeout is the relay's.
func (s *server) gitRelayClient(ctx context.Context, target coding.GitRelayTarget, base string) (*http.Client, error) {
	var client *http.Client
	var err error
	if target.CodeHost == domain.CodeHostGitLab {
		client, err = s.gitlabClient(ctx, base)
	} else {
		client, err = s.outboundClient(ctx, base)
	}
	if err != nil {
		return nil, err
	}
	relay := *client
	relay.Timeout = coding.GitRelayTimeout
	relay.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &relay, nil
}

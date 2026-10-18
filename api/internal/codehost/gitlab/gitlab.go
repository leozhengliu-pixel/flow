// Package gitlab is the GitLab code host (track J), built on the existing
// GitLab token/OAuth connections. Sandbox credentials are per-session project
// access tokens minted through the API (FLOW_CODING_GITLAB_CREDENTIAL_MODE),
// MRs are opened by Flow as drafts, and Merge Request / Note / Pipeline hooks
// become session follow-ups. See docs/coding-sessions-design.md §14.
package gitlab

import (
	"flow/api/internal/codehost"
	"flow/api/internal/domain"
)

func init() { codehost.Register(domain.CodeHostGitLab, New) }

// Host implements codehost.Host for GitLab; methods not yet implemented fall
// through to the embedded stub.
type Host struct {
	codehost.Stub
	deps codehost.Dependencies
}

// New builds the GitLab host.
func New(deps codehost.Dependencies) (codehost.Host, error) {
	return &Host{Stub: codehost.Stub{HostKind: domain.CodeHostGitLab}, deps: deps}, nil
}

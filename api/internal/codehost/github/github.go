// Package github is the GitHub code host through the GitHub App (track D):
// App JWT (RS256, 9-minute expiry), installation tokens scoped to one
// repository with contents/pull_requests write and checks/actions read,
// draft PRs, PR comment/review webhooks. Classic OAuth/PAT GitHub
// connections keep serving PR sync unchanged.
package github

import (
	"flow/api/internal/codehost"
	"flow/api/internal/domain"
)

func init() { codehost.Register(domain.CodeHostGitHub, New) }

// Host implements codehost.Host for GitHub; methods not yet implemented
// fall through to the embedded stub.
type Host struct {
	codehost.Stub
	deps codehost.Dependencies
}

// New builds the GitHub host from FLOW_GITHUB_APP_*.
func New(deps codehost.Dependencies) (codehost.Host, error) {
	return &Host{Stub: codehost.Stub{HostKind: domain.CodeHostGitHub}, deps: deps}, nil
}

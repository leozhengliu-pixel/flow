package codehost_test

import (
	"errors"
	"fmt"
	"testing"

	"flow/api/internal/codehost"
	_ "flow/api/internal/codehost/github"
	_ "flow/api/internal/codehost/gitlab"
	"flow/api/internal/domain"
)

func TestHostsRegister(t *testing.T) {
	hosts, err := codehost.OpenAll(codehost.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{domain.CodeHostGitHub, domain.CodeHostGitLab} {
		if hosts[kind] == nil || hosts[kind].Kind() != kind {
			t.Fatalf("%s not registered: %v", kind, hosts)
		}
	}
}

func TestErrorCodes(t *testing.T) {
	cases := []struct {
		kind string
		err  error
		want domain.CodingErrorCode
	}{
		{domain.CodeHostGitHub, codehost.ErrNotConnected, domain.CodingErrNoGitHubConnection},
		{domain.CodeHostGitLab, fmt.Errorf("x: %w", codehost.ErrNotConnected), domain.CodingErrNoGitLabConnection},
		{domain.CodeHostGitHub, codehost.ErrTransient, domain.CodingErrGitHubTransientError},
		{domain.CodeHostGitLab, codehost.ErrTransient, domain.CodingErrGitLabTransientError},
		{domain.CodeHostGitHub, codehost.ErrUserNoAccess, domain.CodingErrPersonalAccessDenied},
		{domain.CodeHostGitHub, codehost.ErrNoAccess, domain.CodingErrNoCodeAccess},
		{domain.CodeHostGitHub, codehost.ErrUserNotLinked, domain.CodingErrRequiresGitHubConnection},
		{domain.CodeHostGitLab, codehost.ErrUserNotLinked, domain.CodingErrRequiresGitLabConnection},
		{domain.CodeHostGitHub, fmt.Errorf("x: %w", codehost.ErrRepoNotInPolicy), domain.CodingErrRepoNotInPolicy},
		{domain.CodeHostGitHub, errors.New("boom"), domain.CodingErrInternal},
	}
	for _, c := range cases {
		if got := codehost.ErrorCode(c.kind, c.err); got != c.want {
			t.Errorf("%s %v: got %s want %s", c.kind, c.err, got, c.want)
		}
	}
}

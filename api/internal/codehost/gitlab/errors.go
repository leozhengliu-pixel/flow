package gitlab

import (
	"errors"
	"fmt"
	"net/http"

	"flow/api/internal/codehost"
	"flow/api/internal/domain"
)

// GitLab-specific errors. Each wraps a codehost sentinel so
// codehost.ErrorCode keeps working; ErrorCode below refines the mapping.
var (
	// ErrProjectTokensUnavailable: GitLab refused to create a project access
	// token (connection token without api scope, below Maintainer, or a
	// gitlab.com project without Premium/Ultimate). Maps to noCodeAccess.
	ErrProjectTokensUnavailable = fmt.Errorf("%w: gitlab project access tokens unavailable", codehost.ErrNoAccess)
	// ErrOAuthRequired: credential mode oauth but the workspace connected
	// GitLab with an access token. Maps to noGitLabConnection.
	ErrOAuthRequired = fmt.Errorf("%w: gitlab oauth connection required", codehost.ErrNotConnected)
	// ErrUserNotLinked: the policy needs the requesting user's GitLab account
	// but the user has not linked one. Maps to requiresGitlabConnection.
	ErrUserNotLinked = fmt.Errorf("%w: gitlab account not linked", codehost.ErrUserNoAccess)
	// ErrReadOnlyConnection: the connection token only has read_api. Maps to
	// noCodeAccess.
	ErrReadOnlyConnection = fmt.Errorf("%w: gitlab connection is read-only", codehost.ErrNoAccess)
)

// Messages shown to users (session error detail and settings copy).
const (
	MessageProjectTokens = "Flow could not create a GitLab project access token. Project access tokens need a connection token with the api scope and the Maintainer role (or higher) on the project. On GitLab.com they also need the Premium or Ultimate tier. Grant those, or set FLOW_CODING_GITLAB_CREDENTIAL_MODE to oauth or connection."
	MessageOAuthRequired = "FLOW_CODING_GITLAB_CREDENTIAL_MODE is oauth, but GitLab is connected with an access token. Connect GitLab with OAuth, or change the credential mode."
	MessageReadOnly      = "The GitLab connection token is read-only (read_api scope). Coding sessions need the api scope to create project access tokens and merge requests."
	MessageUserNotLinked = "This workspace requires your linked GitLab account for coding sessions. Link GitLab in your account settings."
	MessageTokenRevoked  = "The GitLab access token is invalid, expired or revoked. Update the token in Settings › Integrations › GitLab."
)

// Error wraps a codehost sentinel with a user-presentable message.
type Error struct {
	Kind    error
	Message string
	// Status, Request and Body describe the failed API call (never the token).
	Status  int
	Request string
	Body    string
}

func (e *Error) Error() string {
	if e.Request != "" {
		return fmt.Sprintf("gitlab: %s (%s: HTTP %d)", e.Message, e.Request, e.Status)
	}
	return "gitlab: " + e.Message
}

func (e *Error) Unwrap() error { return e.Kind }

func notConnected(message string) error {
	return &Error{Kind: codehost.ErrNotConnected, Message: message}
}

// classify maps an HTTP status to a codehost sentinel.
func classify(status int) error {
	switch {
	case status == http.StatusUnauthorized:
		return codehost.ErrNotConnected
	case status == http.StatusForbidden:
		return codehost.ErrNoAccess
	case status == http.StatusNotFound:
		return codehost.ErrNotFound
	case status == http.StatusTooManyRequests, status == http.StatusRequestTimeout, status >= 500:
		return codehost.ErrTransient
	default:
		return nil
	}
}

// ErrorCode maps a GitLab host error to the session error code (the reference
// codes plus Flow's). It refines codehost.ErrorCode for GitLab-only cases.
func ErrorCode(err error) domain.CodingErrorCode {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrUserNotLinked):
		return domain.CodingErrRequiresGitLabConnection
	case errors.Is(err, codehost.ErrNotFound):
		// A project that disappeared (or is hidden from the token) is an access problem.
		return domain.CodingErrNoCodeAccess
	default:
		return codehost.ErrorCode(domain.CodeHostGitLab, err)
	}
}

// ErrorMessage returns the user-facing message of a host error ("" when the
// error carries none).
func ErrorMessage(err error) string {
	var gitlabErr *Error
	if errors.As(err, &gitlabErr) {
		return gitlabErr.Message
	}
	switch {
	case errors.Is(err, ErrProjectTokensUnavailable):
		return MessageProjectTokens
	case errors.Is(err, ErrOAuthRequired):
		return MessageOAuthRequired
	case errors.Is(err, ErrUserNotLinked):
		return MessageUserNotLinked
	case errors.Is(err, ErrReadOnlyConnection):
		return MessageReadOnly
	}
	return ""
}

// DescribeError implements codehost.ErrorDescriber (ErrorCode and
// ErrorMessage); ErrRejected maps to internal, which the publisher turns into
// pullRequestFailed.
func (h *Host) DescribeError(err error) (domain.CodingErrorCode, string) {
	return ErrorCode(err), ErrorMessage(err)
}

var _ codehost.ErrorDescriber = (*Host)(nil)

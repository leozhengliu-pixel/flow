package codehost

import (
	"context"
	"fmt"
	"net/http"
)

// Stub is a Host whose every method returns ErrNotImplemented. The github and
// gitlab packages embed it until their tracks implement each method.
type Stub struct{ HostKind Kind }

func (s Stub) Kind() Kind                                      { return s.HostKind }
func (s Stub) err() error                                      { return fmt.Errorf("%w: %s", ErrNotImplemented, s.HostKind) }
func (s Stub) Connected(context.Context, string) (bool, error) { return false, nil }
func (s Stub) ListRepositories(context.Context, string, ListOptions) (RepositoryPage, error) {
	return RepositoryPage{}, s.err()
}
func (s Stub) ResolveRepository(context.Context, string, RepoRef) (Repository, error) {
	return Repository{}, s.err()
}
func (s Stub) CheckUserAccess(context.Context, string, RepoRef, UserIdentity) error { return s.err() }
func (s Stub) MintCredential(context.Context, CredentialRequest) (Credential, error) {
	return Credential{}, s.err()
}
func (s Stub) RevokeCredential(context.Context, string, Credential) error { return nil }
func (s Stub) BranchHead(context.Context, string, RepoRef, string) (string, error) {
	return "", s.err()
}
func (s Stub) FindChangeRequest(context.Context, string, RepoRef, string) (*ChangeRequest, error) {
	return nil, s.err()
}
func (s Stub) GetChangeRequest(context.Context, string, RepoRef, int) (ChangeRequest, error) {
	return ChangeRequest{}, s.err()
}
func (s Stub) CreateChangeRequest(context.Context, string, CreateChangeRequestInput) (ChangeRequest, error) {
	return ChangeRequest{}, s.err()
}
func (s Stub) UpdateChangeRequestBody(context.Context, string, RepoRef, int, string) error {
	return s.err()
}
func (s Stub) Comment(context.Context, string, RepoRef, int, string) error { return s.err() }
func (s Stub) CIStatus(context.Context, string, RepoRef, string) (CIStatus, error) {
	return CIStatus{}, s.err()
}
func (s Stub) ParseWebhook(context.Context, http.Header, []byte) (WebhookEvent, error) {
	return WebhookEvent{}, s.err()
}
func (s Stub) EgressHosts(context.Context, string) ([]string, error) { return nil, nil }

package main

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// The response rewriter (X-Flow-File-Urls-Expire-In) runs outside
// authentication. The authenticated handler records who the request is in a
// holder the rewriter put on the context, and the rewriter then signs a file
// only if that principal may read it now.
type fileSigningPrincipalKey struct{}

type fileSigningPrincipal struct {
	mu        sync.Mutex
	set       bool
	user      domain.User
	key       *domain.APIKey
	workspace string
}

func (p *fileSigningPrincipal) record(user domain.User, key *domain.APIKey, workspace string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.set, p.user, p.workspace = true, user, workspace
	p.key = nil
	if key != nil {
		copied := *key
		p.key = &copied
	}
}

func recordFileSigningPrincipal(ctx context.Context, user domain.User, key *domain.APIKey, workspace string) {
	if holder, _ := ctx.Value(fileSigningPrincipalKey{}).(*fileSigningPrincipal); holder != nil {
		holder.record(user, key, workspace)
	}
}

// fileSigningAuthority decides, per distinct file name of one response,
// whether the request's principal may read it. Answers are cached for the
// request. An unauthenticated or failed request signs nothing.
type fileSigningAuthority struct {
	s       *server
	outer   *http.Request
	holder  *fileSigningPrincipal
	mu      sync.Mutex
	request *http.Request
	release func()
	cache   map[string]bool
}

func (s *server) beginFileSigning(r *http.Request) (*http.Request, *fileSigningAuthority) {
	holder := &fileSigningPrincipal{}
	r = r.WithContext(context.WithValue(r.Context(), fileSigningPrincipalKey{}, holder))
	return r, &fileSigningAuthority{s: s, outer: r, holder: holder, cache: map[string]bool{}}
}

func (a *fileSigningAuthority) close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.release != nil {
		a.release()
		a.release = nil
	}
}

// signer is the principal the response's links are signed for.
func (a *fileSigningAuthority) signer() (fileSigner, bool) {
	a.holder.mu.Lock()
	defer a.holder.mu.Unlock()
	if !a.holder.set || a.holder.user.ID == "" {
		return fileSigner{}, false
	}
	signer := fileSigner{Subject: a.holder.user.ID, Workspace: a.holder.workspace}
	if a.holder.key != nil {
		signer.KeyID = a.holder.key.ID
	} else {
		signer.Workspace = workspaceKey(a.outer)
	}
	return signer, signer.valid()
}

func (a *fileSigningAuthority) allow(name string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if allowed, ok := a.cache[name]; ok {
		return allowed
	}
	allowed := a.readable(name)
	if len(a.cache) < 4096 {
		a.cache[name] = allowed
	}
	return allowed
}

func (a *fileSigningAuthority) readable(name string) bool {
	if name == "" || strings.ContainsAny(name, "/\\\x00") {
		return false
	}
	if a.s.authDisabled {
		return true
	}
	if a.request == nil {
		a.holder.mu.Lock()
		set, user, key, workspace := a.holder.set, a.holder.user, a.holder.key, a.holder.workspace
		a.holder.mu.Unlock()
		if !set || user.ID == "" {
			return false
		}
		ctx := context.WithValue(a.outer.Context(), authUserContextKey{}, user)
		if key != nil {
			ctx = context.WithValue(ctx, apiKeyContextKey{}, *key)
			ctx = context.WithValue(ctx, workspaceKeyContextKey{}, workspace)
		}
		ctx = store.ContextWithActor(ctx, user)
		request := a.outer.WithContext(ctx)
		if a.s.store.BoundedMetadataEnabled() {
			request, a.release = beginRequestResourceReads(request)
		}
		a.request = a.s.withSessionPolicy(request)
	}
	return a.s.fileReadable(a.request, name)
}

// codingArtifactLinkLive is the download check of a link written into a
// change-request body: the artifact row must still exist in that session and
// the session must still exist.
func (s *server) codingArtifactLinkLive(ctx context.Context, name, workspace, sessionID string) bool {
	owner, session, _, err := s.store.CodingArtifactByObject(ctx, name)
	recordUploadVisibilityRefusal(ctx, err)
	if err != nil || owner != workspace || session != sessionID {
		return false
	}
	sessions := codingArtifacts.sessions(s)
	if sessions == nil {
		return false
	}
	_, err = sessions.CodingSession(ctx, workspace, sessionID)
	recordUploadVisibilityRefusal(ctx, err)
	return err == nil
}

// codingArtifactReadableBy reports whether the request's principal may read
// the coding session the named artifact belongs to.
func (s *server) codingArtifactReadableBy(r *http.Request, name string) bool {
	workspace, sessionID, _, err := s.store.CodingArtifactByObject(r.Context(), name)
	recordUploadVisibilityRefusal(r.Context(), err)
	if err != nil {
		return false
	}
	sessions := codingArtifacts.sessions(s)
	if sessions == nil {
		return false
	}
	session, err := sessions.CodingSession(r.Context(), workspace, sessionID)
	recordUploadVisibilityRefusal(r.Context(), err)
	if err != nil {
		return false
	}
	ctx := context.WithValue(r.Context(), workspaceKeyContextKey{}, workspace)
	request := r.WithContext(ctx)
	request.Header = http.Header{}
	request.URL = &url.URL{Path: "/api/coding/sessions/" + url.PathEscape(sessionID)}
	err = codingArtifacts.authorize(s, request, session)
	recordUploadVisibilityRefusal(r.Context(), err)
	return err == nil
}

// signing is the rewriter's configuration: links for this request's principal
// and only for files it may read. Without a principal nothing is signed.
func (a *fileSigningAuthority) signing(key []byte, base string, expires time.Time) *hydratedURLSigning {
	signing := &hydratedURLSigning{key: key, base: base, expires: expires, allow: a.allow}
	signer, ok := a.signer()
	if !ok {
		// No authenticated principal: nothing is signed, except where the
		// server has no authentication at all (development) or a pipeline
		// test signs fixture names.
		if a.s.authDisabled || a.s.fileSigningAllowAll {
			signing.allow = nil
		} else {
			signing.allow = func(string) bool { return false }
		}
	}
	if a.s.fileSigningAllowAll {
		signing.allow = nil
	}
	signing.signer = signer
	return signing
}

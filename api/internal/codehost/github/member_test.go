package github

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"flow/api/internal/codehost"
)

// A member's user-to-server token opens the pull request as them; a token
// GitHub rejects falls back to the App installation token.
func TestCreateChangeRequestActsAsTheMember(t *testing.T) {
	fake := newFakeGitHub(t)
	host, _ := newTestHost(t, fake, nil)
	var authorizations []string
	fake.setOverride("POST /repos/acme/app/pulls", func(w http.ResponseWriter, r *http.Request) bool {
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") == "Bearer gho_revoked" {
			writeJSONResponse(w, http.StatusUnauthorized, map[string]string{"message": "Bad credentials"})
			return true
		}
		return false
	})
	input := codehost.CreateChangeRequestInput{Repo: appRepo, Head: "alice/fix-1", Base: "main", Title: "Fix", Draft: true, ActAs: &codehost.ActingUser{FlowUserID: "u1", Login: "alice", Token: "gho_alice", Bearer: true}}
	change, err := host.CreateChangeRequest(t.Context(), "ws", input)
	if err != nil || !change.OpenedByMember || len(authorizations) != 1 || authorizations[0] != "Bearer gho_alice" {
		t.Fatalf("member PR %+v %v %v", change, err, authorizations)
	}
	input.Head, input.ActAs.Token = "alice/fix-2", "gho_revoked"
	change, err = host.CreateChangeRequest(t.Context(), "ws", input)
	if err != nil || change.OpenedByMember || len(authorizations) != 3 || authorizations[2][:9] != "token ghs" {
		t.Fatalf("fallback PR %+v %v %v", change, err, authorizations)
	}
}

// The user-to-server exchange sends the PKCE verifier and keeps the token
// pair; refresh rotates it, and a bad refresh token means "reconnect".
func TestUserTokenExchangeAndRefresh(t *testing.T) {
	fake := newFakeGitHub(t)
	host, _ := newTestHost(t, fake, nil)
	fake.setOverride("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) bool {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case body["grant_type"] == "refresh_token" && body["refresh_token"] == "ghr_1":
			writeJSONResponse(w, http.StatusOK, map[string]any{"access_token": "gho_user2", "refresh_token": "ghr_2", "expires_in": 28800, "refresh_token_expires_in": 15897600})
		case body["grant_type"] == "refresh_token":
			writeJSONResponse(w, http.StatusOK, map[string]string{"error": "bad_refresh_token"})
		case body["code"] == "good" && body["code_verifier"] == "verifier-1":
			writeJSONResponse(w, http.StatusOK, map[string]any{"access_token": "gho_user", "refresh_token": "ghr_1", "expires_in": 28800, "refresh_token_expires_in": 15897600})
		default:
			writeJSONResponse(w, http.StatusOK, map[string]string{"error": "bad_verification_code"})
		}
		return true
	})
	if url := host.AuthorizeURLWithChallenge("st", "https://flow/cb", "chal"); !strings.Contains(url, "code_challenge=chal") || !strings.Contains(url, "code_challenge_method=S256") {
		t.Fatalf("authorize URL %s", url)
	}
	if _, _, err := host.ExchangeUserToken(t.Context(), "good", "", "wrong"); err == nil {
		t.Fatal("a wrong PKCE verifier must fail")
	}
	link, token, err := host.ExchangeUserToken(t.Context(), "good", "", "verifier-1")
	if err != nil || link.Login != "alice" || token.AccessToken != "gho_user" || token.RefreshToken != "ghr_1" || time.Until(token.ExpiresAt) < 7*time.Hour {
		t.Fatalf("exchange %+v %+v %v", link, token, err)
	}
	refreshed, err := host.RefreshUserToken(t.Context(), "ghr_1")
	if err != nil || refreshed.AccessToken != "gho_user2" || refreshed.RefreshToken != "ghr_2" {
		t.Fatalf("refresh %+v %v", refreshed, err)
	}
	if _, err := host.RefreshUserToken(t.Context(), "ghr_old"); err == nil || !isUserNotLinked(err) {
		t.Fatalf("bad refresh token → user not linked, got %v", err)
	}
}

func isUserNotLinked(err error) bool {
	return codehost.ErrorCode("github", err) == "requiresGithubConnection"
}

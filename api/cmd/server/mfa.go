package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

type mfaChallenge struct {
	Session     webauthn.SessionData `json:"session"`
	SessionHash string               `json:"sessionHash"`
}

func authenticationCookieHash(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	hash := sha256.Sum256([]byte(cookie.Value))
	return hex.EncodeToString(hash[:])
}
func (s *server) mfaWebAuthnUser(user domain.User) accountWebAuthnUser {
	result := accountWebAuthnUser{user: user}
	for _, entry := range s.accountPasskeys(user.ID) {
		key := entry.key
		if key.UserID == user.ID {
			var credential webauthn.Credential
			if json.Unmarshal([]byte(key.CredentialJSON), &credential) == nil {
				result.credentials = append(result.credentials, credential)
			}
		}
	}
	return result
}

type accountPasskey struct {
	workspace string
	key       domain.Passkey
}

func (s *server) accountPasskeys(userID string) []accountPasskey {
	result := []accountPasskey{}
	seen := map[string]bool{}
	for _, workspace := range s.store.WorkspaceKeys() {
		data, ok := s.store.WorkspaceMetadata(workspace)
		if !ok {
			continue
		}
		for _, key := range data.Passkeys {
			if key.UserID == userID && !seen[key.ID] {
				seen[key.ID] = true
				result = append(result, accountPasskey{workspace: workspace, key: key})
			}
		}
	}
	return result
}

func (s *server) beginMFA(w http.ResponseWriter, r *http.Request) {
	actor := requestActor(s, r)
	user := s.mfaWebAuthnUser(actor)
	if len(user.credentials) == 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Register a passkey to enable two-factor authentication", "code": "mfa_enrollment_required"})
		return
	}
	instance, origin, err := webAuthnForRequest(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	options, session, err := instance.BeginLogin(user, webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		writeError(w, 400, "could not begin authentication")
		return
	}
	session.Origin = origin
	encoded, _ := json.Marshal(mfaChallenge{Session: *session, SessionHash: authenticationCookieHash(r)})
	id, err := randomSecret("passkey_mfa_")
	if err != nil {
		writeError(w, 500, "could not begin authentication")
		return
	}
	now := time.Now().UTC()
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "passkey.authentication_started", actor.ID, nil, func(next *domain.Bootstrap) error {
		next.PasskeyRegistrationChallenges = slices.DeleteFunc(next.PasskeyRegistrationChallenges, func(item domain.PasskeyRegistrationChallenge) bool {
			return item.ExpiresAt.Before(now) || (item.UserID == actor.ID && strings.HasPrefix(item.ID, "passkey_mfa_"))
		})
		next.PasskeyRegistrationChallenges = append(next.PasskeyRegistrationChallenges, domain.PasskeyRegistrationChallenge{ID: id, UserID: actor.ID, SessionJSON: string(encoded), CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)})
		return nil
	})
	if err != nil {
		respondMutation(w, err, 500, nil)
		return
	}
	writeJSON(w, 200, map[string]any{"challengeId": id, "options": options})
}

func (s *server) finishMFA(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ChallengeID string          `json:"challengeId"`
		Credential  json.RawMessage `json:"credential"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !strings.HasPrefix(input.ChallengeID, "passkey_mfa_") {
		writeError(w, http.StatusBadRequest, "invalid authentication challenge")
		return
	}
	actor := requestActor(s, r)
	data := s.workspaceData(r)
	index := slices.IndexFunc(data.PasskeyRegistrationChallenges, func(item domain.PasskeyRegistrationChallenge) bool {
		return item.ID == input.ChallengeID && item.UserID == actor.ID && item.ExpiresAt.After(time.Now().UTC())
	})
	if index < 0 {
		writeError(w, 400, "authentication challenge expired")
		return
	}
	var challenge mfaChallenge
	if json.Unmarshal([]byte(data.PasskeyRegistrationChallenges[index].SessionJSON), &challenge) != nil || challenge.SessionHash == "" || challenge.SessionHash != authenticationCookieHash(r) {
		writeError(w, 400, "authentication challenge belongs to another session")
		return
	}
	instance, origin, err := webAuthnForRequest(r)
	if err != nil || challenge.Session.Origin != origin {
		writeError(w, 400, "authentication origin changed")
		return
	}
	response, err := protocol.ParseCredentialRequestResponseBytes(input.Credential)
	if err != nil {
		writeError(w, 400, "invalid authentication response")
		return
	}
	credential, err := instance.ValidateLogin(s.mfaWebAuthnUser(actor), challenge.Session, response)
	if err != nil {
		writeError(w, 403, "second factor verification failed")
		return
	}
	encoded, _ := json.Marshal(credential)
	now := time.Now().UTC()
	keyWorkspace := ""
	for _, entry := range s.accountPasskeys(actor.ID) {
		var stored webauthn.Credential
		if json.Unmarshal([]byte(entry.key.CredentialJSON), &stored) == nil && bytes.Equal(stored.ID, credential.ID) {
			keyWorkspace = entry.workspace
			break
		}
	}
	if keyWorkspace == "" {
		writeError(w, 403, "passkey is no longer registered")
		return
	}
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "passkey.authentication_finished", actor.ID, nil, func(next *domain.Bootstrap) error {
		i := slices.IndexFunc(next.PasskeyRegistrationChallenges, func(item domain.PasskeyRegistrationChallenge) bool {
			return item.ID == input.ChallengeID && item.UserID == actor.ID && item.ExpiresAt.After(now)
		})
		if i < 0 {
			return errConflict
		}
		next.PasskeyRegistrationChallenges = slices.Delete(next.PasskeyRegistrationChallenges, i, i+1)
		return nil
	})
	if err != nil {
		respondMutation(w, err, 200, nil)
		return
	}
	err = s.store.MutateWorkspace(r.Context(), keyWorkspace, "passkey.authentication_used", actor.ID, nil, func(next *domain.Bootstrap) error {
		for index := range next.Passkeys {
			key := &next.Passkeys[index]
			if key.UserID != actor.ID {
				continue
			}
			var stored webauthn.Credential
			if json.Unmarshal([]byte(key.CredentialJSON), &stored) == nil && bytes.Equal(stored.ID, credential.ID) {
				key.CredentialJSON = string(encoded)
				key.LastUsedAt = &now
				return nil
			}
		}
		return errNotFound
	})
	if err != nil {
		respondMutation(w, err, 200, nil)
		return
	}
	cookie, _ := r.Cookie(sessionCookieName)
	auth, err := s.store.SessionAuthentication(r.Context(), cookie.Value)
	if err == nil {
		err = s.store.SetSessionAuthentication(r.Context(), cookie.Value, auth.Provider, auth.Issuer, true)
	}
	if err != nil {
		writeError(w, 500, "could not save authentication verification")
		return
	}
	writeJSON(w, 200, map[string]bool{"verified": true})
}

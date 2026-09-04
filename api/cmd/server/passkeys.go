package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"flow/api/internal/domain"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

const maxPasskeyNameLength = 64

// accountWebAuthnUser adapts Flow's account model to the WebAuthn user
// contract. A stable digest gives each account an opaque, fixed-size handle.
type accountWebAuthnUser struct {
	user        domain.User
	credentials []webauthn.Credential
}

func (u accountWebAuthnUser) WebAuthnID() []byte {
	// User IDs are already opaque in the datastore; hashing prevents exposing
	// account identifiers to authenticators while retaining stability.
	digest := sha256.Sum256([]byte(u.user.ID))
	return digest[:]
}
func (u accountWebAuthnUser) WebAuthnName() string { return u.user.Email }
func (u accountWebAuthnUser) WebAuthnDisplayName() string {
	if u.user.DisplayName != "" {
		return u.user.DisplayName
	}
	return u.user.Name
}
func (u accountWebAuthnUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

func passkeyPublic(item domain.Passkey) domain.Passkey {
	item.CredentialJSON = ""
	return item
}

func (s *server) listPasskeys(w http.ResponseWriter, r *http.Request) {
	actor := requestActor(s, r)
	items := []domain.Passkey{}
	for _, item := range s.workspaceData(r).Passkeys {
		if item.UserID == actor.ID {
			items = append(items, passkeyPublic(item))
		}
	}
	writeJSON(w, http.StatusOK, items)
}

func webAuthnForRequest(r *http.Request) (*webauthn.WebAuthn, string, error) {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		scheme := "https"
		if r.TLS == nil {
			scheme = "http"
		}
		origin = scheme + "://" + r.Host
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, "", errors.New("invalid WebAuthn origin")
	}
	config := &webauthn.Config{
		RPID:          parsed.Hostname(),
		RPDisplayName: "Flow",
		RPOrigins:     []string{origin},
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationPreferred,
		},
	}
	instance, err := webauthn.New(config)
	if err != nil {
		return nil, "", err
	}
	return instance, origin, nil
}

func (s *server) beginPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	actor := requestActor(s, r)
	data := s.workspaceData(r)
	if strings.EqualFold(data.ViewerRole, "guest") {
		writeError(w, http.StatusForbidden, "guest users cannot register passkeys")
		return
	}
	credentials := make([]webauthn.Credential, 0)
	for _, item := range data.Passkeys {
		if item.UserID != actor.ID || item.CredentialJSON == "" {
			continue
		}
		var credential webauthn.Credential
		if json.Unmarshal([]byte(item.CredentialJSON), &credential) == nil {
			credentials = append(credentials, credential)
		}
	}
	instance, origin, err := webAuthnForRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	creation, session, err := instance.BeginRegistration(accountWebAuthnUser{user: actor, credentials: credentials})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not begin passkey registration")
		return
	}
	// Keep the complete session server-side; only an opaque registration ID is
	// returned to the browser, preventing challenge or RP tampering.
	session.Origin = origin
	sessionJSON, err := json.Marshal(session)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not save registration session")
		return
	}
	registrationID, err := randomSecret("passkey_reg_")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create registration session")
		return
	}
	now := time.Now().UTC()
	challenge := domain.PasskeyRegistrationChallenge{ID: registrationID, UserID: actor.ID, SessionJSON: string(sessionJSON), CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "passkey.registration_started", registrationID, nil, func(next *domain.Bootstrap) error {
		next.PasskeyRegistrationChallenges = append(next.PasskeyRegistrationChallenges, challenge)
		cutoff := now.Add(-10 * time.Minute)
		kept := next.PasskeyRegistrationChallenges[:0]
		for _, item := range next.PasskeyRegistrationChallenges {
			if item.ExpiresAt.After(cutoff) {
				kept = append(kept, item)
			}
		}
		next.PasskeyRegistrationChallenges = kept
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusInternalServerError, nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"registrationId": registrationID, "options": creation})
}

func (s *server) finishPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RegistrationID string          `json:"registrationId"`
		Name           string          `json:"name"`
		Credential     json.RawMessage `json:"credential"`
	}
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.RegistrationID) == "" || len(input.Credential) == 0 {
		writeError(w, http.StatusBadRequest, "registrationId and credential are required")
		return
	}
	actor := requestActor(s, r)
	data := s.workspaceData(r)
	var pending *domain.PasskeyRegistrationChallenge
	for index := range data.PasskeyRegistrationChallenges {
		item := &data.PasskeyRegistrationChallenges[index]
		if item.ID == input.RegistrationID && item.UserID == actor.ID {
			pending = item
			break
		}
	}
	if pending == nil || pending.ExpiresAt.Before(time.Now().UTC()) {
		writeError(w, http.StatusBadRequest, "registration session expired")
		return
	}
	var session webauthn.SessionData
	if err := json.Unmarshal([]byte(pending.SessionJSON), &session); err != nil {
		writeError(w, http.StatusBadRequest, "invalid registration session")
		return
	}
	instance, origin, err := webAuthnForRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if session.Origin != "" && session.Origin != origin {
		writeError(w, http.StatusBadRequest, "passkey origin changed")
		return
	}
	credentials := make([]webauthn.Credential, 0)
	for _, item := range data.Passkeys {
		if item.UserID != actor.ID || item.CredentialJSON == "" {
			continue
		}
		var credential webauthn.Credential
		if json.Unmarshal([]byte(item.CredentialJSON), &credential) == nil {
			credentials = append(credentials, credential)
		}
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(input.Credential)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid passkey response")
		return
	}
	credential, err := instance.CreateCredential(accountWebAuthnUser{user: actor, credentials: credentials}, session, parsed)
	if err != nil {
		writeError(w, http.StatusBadRequest, "passkey verification failed")
		return
	}
	credentialJSON, err := json.Marshal(credential)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not save passkey")
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = "Passkey"
	}
	if len([]rune(name)) > maxPasskeyNameLength {
		writeError(w, http.StatusBadRequest, "passkey name is too long")
		return
	}
	created := domain.Passkey{ID: fmt.Sprintf("passkey_%d", time.Now().UnixNano()), UserID: actor.ID, Name: name, CredentialJSON: string(credentialJSON), CreatedAt: time.Now().UTC()}
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "passkey.created", created.ID, nil, func(next *domain.Bootstrap) error {
		index := -1
		for i := range next.PasskeyRegistrationChallenges {
			if next.PasskeyRegistrationChallenges[i].ID == input.RegistrationID && next.PasskeyRegistrationChallenges[i].UserID == actor.ID {
				index = i
				break
			}
		}
		if index < 0 || next.PasskeyRegistrationChallenges[index].ExpiresAt.Before(time.Now().UTC()) {
			return errInvalid
		}
		next.PasskeyRegistrationChallenges = append(next.PasskeyRegistrationChallenges[:index], next.PasskeyRegistrationChallenges[index+1:]...)
		next.Passkeys = append(next.Passkeys, created)
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusBadRequest, nil)
		return
	}
	writeJSON(w, http.StatusCreated, passkeyPublic(created))
}

func (s *server) updatePasskey(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len([]rune(name)) > maxPasskeyNameLength {
		writeError(w, http.StatusBadRequest, "passkey name must be between 1 and 64 characters")
		return
	}
	actor := requestActor(s, r)
	var updated domain.Passkey
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "passkey.updated", r.PathValue("id"), input, func(data *domain.Bootstrap) error {
		for index := range data.Passkeys {
			if data.Passkeys[index].ID == r.PathValue("id") && data.Passkeys[index].UserID == actor.ID {
				data.Passkeys[index].Name = name
				updated = data.Passkeys[index]
				return nil
			}
		}
		return errNotFound
	})
	if err != nil {
		respondMutation(w, err, http.StatusNotFound, nil)
		return
	}
	writeJSON(w, http.StatusOK, passkeyPublic(updated))
}

func (s *server) deletePasskey(w http.ResponseWriter, r *http.Request) {
	actor := requestActor(s, r)
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "passkey.deleted", r.PathValue("id"), nil, func(data *domain.Bootstrap) error {
		before := len(data.Passkeys)
		kept := make([]domain.Passkey, 0, before)
		for _, item := range data.Passkeys {
			if item.ID != r.PathValue("id") || item.UserID != actor.ID {
				kept = append(kept, item)
			}
		}
		if len(kept) == before {
			return errNotFound
		}
		data.Passkeys = kept
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusNoContent, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

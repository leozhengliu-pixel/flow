package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

const (
	maxSigningKeyBytes     = 128 * 1024
	maxSigningKeyNameRunes = 255
)

// parseCommitSigningKey validates an uploaded private key and returns only
// display-safe metadata. Private key bytes are never part of the metadata.
func parseCommitSigningKey(raw string) (domain.CommitSigningKey, error) {
	key, _, err := parseCommitSigningKeyMaterial(raw)
	return key, err
}

// parseCommitSigningKeyMaterial also returns the parsed key, whose material
// coding sessions keep encrypted to sign commits (track E).
func parseCommitSigningKeyMaterial(raw string) (domain.CommitSigningKey, coding.SigningKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return domain.CommitSigningKey{}, coding.SigningKey{}, errors.New("private key is required")
	}
	if len(raw) > maxSigningKeyBytes {
		return domain.CommitSigningKey{}, coding.SigningKey{}, fmt.Errorf("private key exceeds %d bytes", maxSigningKeyBytes)
	}
	parsed, err := coding.ParseSigningKey([]byte(raw))
	if err != nil {
		switch {
		case errors.Is(err, coding.ErrSigningKeyEncrypted):
			return domain.CommitSigningKey{}, coding.SigningKey{}, errors.New("encrypted private keys are not supported")
		case strings.Contains(raw, "BEGIN PGP PRIVATE KEY BLOCK"):
			return domain.CommitSigningKey{}, coding.SigningKey{}, errors.New("invalid PGP private key")
		default:
			return domain.CommitSigningKey{}, coding.SigningKey{}, errors.New("invalid SSH private key")
		}
	}
	keyType := "ssh"
	if parsed.Format == coding.SigningFormatOpenPGP {
		keyType = "pgp"
	}
	return domain.CommitSigningKey{Type: keyType, Fingerprint: parsed.Fingerprint}, parsed, nil
}

// commitSigningKeyResponse is the account API's view of the key. NeedsReupload
// (coding sessions, track E) marks a key uploaded before Flow stored private
// keys for signing: the account shows it, but sessions cannot sign with it
// until it is uploaded again.
type commitSigningKeyResponse struct {
	domain.CommitSigningKey
	NeedsReupload bool `json:"needsReupload,omitempty"`
}

// signingKeyNeedsReupload reports whether a key's material is missing while
// signing key storage is configured.
func (s *server) signingKeyNeedsReupload(ctx context.Context, userID string) bool {
	if !store.SigningKeyStorageConfigured() {
		return false
	}
	stored, err := s.store.HasSigningKeyMaterial(ctx, userID)
	return err == nil && !stored
}

func (s *server) getCommitSigningKey(w http.ResponseWriter, r *http.Request) {
	_, settings, ok := s.store.AccountCollections(workspaceKey(r), requestActor(s, r).ID)
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if settings.CommitSigningKey == nil {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, commitSigningKeyResponse{CommitSigningKey: *settings.CommitSigningKey, NeedsReupload: s.signingKeyNeedsReupload(r.Context(), requestActor(s, r).ID)})
}

func (s *server) addCommitSigningKey(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name       string `json:"name"`
		PrivateKey string `json:"privateKey"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len([]rune(input.Name)) > maxSigningKeyNameRunes {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("name must be %d characters or fewer", maxSigningKeyNameRunes))
		return
	}
	key, parsed, err := parseCommitSigningKeyMaterial(input.PrivateKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	key.Name, key.AddedAt = input.Name, time.Now().UTC()
	actor := requestActor(s, r)
	// Coding sessions sign commits with this key (track E): keep the private
	// key encrypted, before the metadata says it exists.
	storedMaterial := false
	if store.SigningKeyStorageConfigured() {
		if err := s.store.PutCodingSigningKey(r.Context(), actor.ID, parsed.Format, parsed.Fingerprint, []byte(strings.TrimSpace(input.PrivateKey))); err != nil {
			slog.ErrorContext(r.Context(), "commit signing key material not stored", "user_id", actor.ID, "error", err.Error())
			writeError(w, http.StatusInternalServerError, "the signing key could not be stored; try again")
			return
		}
		storedMaterial = true
	}
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "commit_signing_key.added", actor.ID, map[string]string{"name": input.Name, "type": key.Type, "fingerprint": key.Fingerprint}, func(data *domain.Bootstrap) error {
		settings := data.UserSettings[actor.ID]
		settings.UserID = actor.ID
		settings.CommitSigningKey = &key
		data.UserSettings[actor.ID] = settings
		return nil
	})
	if err != nil {
		if storedMaterial {
			_ = s.store.DeleteSigningKeyMaterial(context.WithoutCancel(r.Context()), actor.ID)
		}
		respondMutation(w, err, http.StatusBadRequest, nil)
		return
	}
	writeJSON(w, http.StatusCreated, commitSigningKeyResponse{CommitSigningKey: key})
}

func (s *server) removeCommitSigningKey(w http.ResponseWriter, r *http.Request) {
	actor := requestActor(s, r)
	// Coding sessions (track E): the stored private key goes first, so a
	// failure leaves the key listed and the removal can be retried.
	if _, settings, ok := s.store.AccountCollections(workspaceKey(r), actor.ID); ok && settings.CommitSigningKey != nil {
		if err := s.store.DeleteSigningKeyMaterial(r.Context(), actor.ID); err != nil {
			slog.ErrorContext(r.Context(), "commit signing key material not deleted", "user_id", actor.ID, "error", err.Error())
			writeError(w, http.StatusInternalServerError, "the signing key could not be removed; try again")
			return
		}
	}
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "commit_signing_key.removed", actor.ID, nil, func(data *domain.Bootstrap) error {
		settings := data.UserSettings[actor.ID]
		if settings.CommitSigningKey == nil {
			return errNotFound
		}
		settings.CommitSigningKey = nil
		data.UserSettings[actor.ID] = settings
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusNotFound, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

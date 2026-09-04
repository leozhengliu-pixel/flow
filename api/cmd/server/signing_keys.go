package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"flow/api/internal/domain"
	"golang.org/x/crypto/ssh"
)

const (
	maxSigningKeyBytes     = 128 * 1024
	maxSigningKeyNameRunes = 255
)

// parseCommitSigningKey validates an uploaded private key and returns only
// display-safe metadata. Private key bytes are intentionally not retained.
func parseCommitSigningKey(raw string) (domain.CommitSigningKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return domain.CommitSigningKey{}, errors.New("private key is required")
	}
	if len(raw) > maxSigningKeyBytes {
		return domain.CommitSigningKey{}, fmt.Errorf("private key exceeds %d bytes", maxSigningKeyBytes)
	}
	if strings.Contains(raw, "BEGIN PGP PRIVATE KEY BLOCK") {
		if !strings.Contains(raw, "END PGP PRIVATE KEY BLOCK") {
			return domain.CommitSigningKey{}, errors.New("invalid PGP private key")
		}
		sum := sha256.Sum256([]byte(raw))
		return domain.CommitSigningKey{Type: "pgp", Fingerprint: "SHA256:" + hex.EncodeToString(sum[:])}, nil
	}
	key, err := ssh.ParseRawPrivateKey([]byte(raw))
	if err != nil {
		if _, encrypted := err.(*ssh.PassphraseMissingError); encrypted {
			return domain.CommitSigningKey{}, errors.New("encrypted private keys are not supported")
		}
		return domain.CommitSigningKey{}, errors.New("invalid SSH private key")
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		return domain.CommitSigningKey{}, errors.New("invalid SSH private key")
	}
	return domain.CommitSigningKey{
		Type:        "ssh",
		Fingerprint: ssh.FingerprintSHA256(signer.PublicKey()),
	}, nil
}

func (s *server) getCommitSigningKey(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	settings := data.UserSettings[requestActor(s, r).ID]
	if settings.CommitSigningKey == nil {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, settings.CommitSigningKey)
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
	key, err := parseCommitSigningKey(input.PrivateKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	key.Name, key.AddedAt = input.Name, time.Now().UTC()
	actor := requestActor(s, r)
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "commit_signing_key.added", actor.ID, map[string]string{"name": input.Name, "type": key.Type, "fingerprint": key.Fingerprint}, func(data *domain.Bootstrap) error {
		settings := data.UserSettings[actor.ID]
		settings.UserID = actor.ID
		settings.CommitSigningKey = &key
		data.UserSettings[actor.ID] = settings
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusBadRequest, nil)
		return
	}
	writeJSON(w, http.StatusCreated, key)
}

func (s *server) removeCommitSigningKey(w http.ResponseWriter, r *http.Request) {
	actor := requestActor(s, r)
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

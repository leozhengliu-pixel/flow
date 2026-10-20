package store

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

var errApplicationIdentityCurrent = errors.New("application identity is current")

// SyncApplicationIdentity copies an OAuth application's current name and logo
// to every installation of it and to each installation's app user. Issues,
// comments, activity and history resolve users on read, so the agent shows
// the application's new name and logo on existing records as well as new ones.
func (s *SQLiteStore) SyncApplicationIdentity(ctx context.Context, clientID string) error {
	app, ok := s.OAuthApplicationByClientID(clientID)
	name := strings.TrimSpace(app.Name)
	if !ok || name == "" {
		return nil
	}
	installations, err := s.ApplicationInstallationsByClient(ctx, clientID)
	if err != nil {
		return err
	}
	var errs []error
	for _, installation := range installations {
		errs = append(errs, s.syncInstallationIdentity(ctx, installation, name, strings.TrimSpace(app.LogoURL)))
	}
	return errors.Join(errs...)
}

// SyncAllApplicationIdentities brings every installed OAuth application's app
// users in line with the application (e.g. records written before renames
// and logo changes were propagated). It runs at startup.
func (s *SQLiteStore) SyncAllApplicationIdentities(ctx context.Context) error {
	s.mu.RLock()
	clientIDs := []string{}
	for _, data := range s.workspaces {
		for _, app := range data.OAuthApplications {
			clientIDs = append(clientIDs, app.ClientID)
		}
	}
	s.mu.RUnlock()
	slices.Sort(clientIDs)
	var errs []error
	for _, clientID := range slices.Compact(clientIDs) {
		errs = append(errs, s.SyncApplicationIdentity(ctx, clientID))
	}
	return errors.Join(errs...)
}

func (s *SQLiteStore) syncInstallationIdentity(ctx context.Context, ref InstallationRef, name, avatarURL string) error {
	var event domain.DomainEvent
	err := s.oauthMetadataTransaction(ctx, ref.WorkspaceKey, func(tx *sqlTx, lock string, current domain.Bootstrap) (domain.Bootstrap, error) {
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT data FROM application_installations WHERE workspace_key=? AND id=?`+lock, ref.WorkspaceKey, ref.ID).Scan(&raw); err != nil {
			return current, err
		}
		var installation domain.ApplicationInstallation
		if err := json.Unmarshal(raw, &installation); err != nil {
			return current, err
		}
		index := slices.IndexFunc(current.Users, func(user domain.User) bool { return user.ID == installation.UserID })
		userCurrent := index < 0 || current.Users[index].Name == name && current.Users[index].DisplayName == name && current.Users[index].AvatarURL == avatarURL
		if installation.Name == name && installation.AvatarURL == avatarURL && userCurrent {
			return current, errApplicationIdentityCurrent
		}
		now := time.Now().UTC()
		installation.Name, installation.AvatarURL, installation.UpdatedAt = name, avatarURL, now
		raw, err := json.Marshal(installation)
		if err != nil {
			return current, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE application_installations SET data=? WHERE id=?`, raw, installation.ID); err != nil {
			return current, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE auth_users SET name=?,display_name=?,avatar_url=?,updated_at=? WHERE id=?`, name, name, avatarURL, now.Format(time.RFC3339Nano), installation.UserID); err != nil {
			return current, err
		}
		if index >= 0 {
			user := current.Users[index]
			user.Name, user.DisplayName, user.AvatarURL = name, name, avatarURL
			userRaw, err := json.Marshal(user)
			if err != nil {
				return current, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='users' AND record_key=?`, userRaw, ref.WorkspaceKey, user.ID); err != nil {
				return current, err
			}
			current.Users = slices.Clone(current.Users)
			current.Users[index] = user
			if current.Viewer.ID == user.ID {
				current.Viewer = user
			}
		}
		// Open clients reload the workspace and show the new name and logo.
		event, err = oauthRecordEvent(ctx, tx, current.Workspace.ID, "application.updated", installation.ID)
		return current, err
	})
	if errors.Is(err, errApplicationIdentityCurrent) {
		return nil
	}
	if err == nil {
		s.publishOAuthRecordEvent(ref.WorkspaceKey, event)
	}
	return err
}

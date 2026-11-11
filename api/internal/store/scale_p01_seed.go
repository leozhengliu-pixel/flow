//go:build loadtest

package store

import (
	"context"
	"fmt"
	"math"
	"time"

	"flow/api/internal/domain"
)

func scaleP01SyntheticSpec(spec SyntheticMetadata) (SyntheticMetadata, error) {
	if spec.CountScale == 0 {
		spec.CountScale = 1
	}
	if spec.BodyScale == 0 {
		spec.BodyScale = 1
	}
	for _, factor := range []float64{spec.CountScale, spec.BodyScale} {
		if factor <= 0 || math.IsNaN(factor) || math.IsInf(factor, 0) {
			return spec, fmt.Errorf("invalid synthetic metadata scale")
		}
	}
	for _, value := range []struct {
		target *int
		scale  float64
	}{
		{&spec.Projects, spec.CountScale}, {&spec.Trash, spec.CountScale}, {&spec.Documents, spec.CountScale}, {&spec.Conversations, spec.CountScale}, {&spec.Notifications, spec.CountScale},
		{&spec.ProjectBytes, spec.BodyScale}, {&spec.TrashBytes, spec.BodyScale}, {&spec.DocumentBytes, spec.BodyScale}, {&spec.ConversationBytes, spec.BodyScale},
	} {
		if *value.target < 0 || float64(*value.target)*value.scale > 1<<30 {
			return spec, fmt.Errorf("invalid synthetic metadata size")
		}
		*value.target = int(math.Ceil(float64(*value.target) * value.scale))
	}
	if (spec.Projects > 0 || spec.Trash > 0 || spec.Documents > 0) && len(spec.TeamIDs) == 0 {
		return spec, fmt.Errorf("synthetic metadata requires teams")
	}
	return spec, nil
}

func scaleP01SyntheticProject(creator domain.User, spec SyntheticMetadata, i int, now time.Time) domain.Project {
	project := syntheticProject(creator, spec, i, now)
	if spec.Relations {
		if len(spec.UserIDs) > 0 {
			project.MemberIDs = []string{spec.UserIDs[i%len(spec.UserIDs)]}
		}
		if i > 0 {
			project.DependencyIDs = []string{fmt.Sprintf("lt-project-%05d", i-1)}
		}
		project.Milestones = []domain.ProjectMilestone{{ID: fmt.Sprintf("lt-milestone-%05d", i), Name: "Load milestone"}}
	}
	return project
}

func scaleP01SyntheticDocument(creator domain.User, spec SyntheticMetadata, i int, now time.Time) domain.Document {
	document := syntheticDocument(creator, spec, i, now)
	if spec.Relations {
		if spec.Projects > 0 {
			document.ProjectIDs = []string{fmt.Sprintf("lt-project-%05d", i%spec.Projects)}
		}
		document.Revisions = []domain.DocumentRevision{{ID: fmt.Sprintf("lt-doc-revision-%05d", i), DocumentID: document.ID, Title: document.Title, Content: filler("historical document", spec.DocumentBytes/2), Author: creator, AuthorIDs: []string{creator.ID}, CreatedAt: now.Add(-time.Hour)}}
	}
	return document
}

type ScaleP01Principal struct {
	User    domain.User
	Token   string
	Role    string
	TeamIDs []string
}

// SeedScaleP01Principals bypasses registration throttling and password hashing
// per user, not request authentication. Each principal has a real session,
// membership and credential context. It is only available with -tags=loadtest.
// The first two teams are public; any remaining teams are private. Unique
// permissions require eleven private teams to represent 2,000 combinations.
func (s *SQLiteStore) SeedScaleP01Principals(ctx context.Context, workspace string, teams []string, count int, uniquePermissions bool) ([]ScaleP01Principal, error) {
	data, ok := s.WorkspaceSettingsMetadata(workspace)
	if !ok || len(teams) < 2 || count < 1 || count > 10000 || uniquePermissions && len(teams) < 13 {
		return nil, fmt.Errorf("invalid P01 principal fixture")
	}
	hash, err := testFixturePasswordHash()
	if err != nil {
		return nil, err
	}
	people := make([]ScaleP01Principal, 0, count)
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	for start := 0; start < count; start += 128 {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		batchErr := func() error {
			for i := start; i < min(count, start+128); i++ {
				user := domain.User{ID: fmt.Sprintf("p01_user_%05d", i), Email: fmt.Sprintf("p01-user-%05d@example.test", i), Name: fmt.Sprintf("Load person %d", i), DisplayName: fmt.Sprintf("Load person %d", i), Active: true}
				role := "member"
				joined := []string{teams[i%2]}
				if i%4 == 3 {
					role = "guest"
				}
				if uniquePermissions {
					for bit := range 11 {
						if i&(1<<bit) != 0 {
							joined = append(joined, teams[2+bit])
						}
					}
				} else if i%4 < 2 && len(teams) >= 4 {
					joined = append(joined, teams[2+i%2])
				}
				token, err := randomToken()
				if err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO auth_users(id,email,name,display_name,avatar_url,password_hash,email_verified_at,active,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, user.ID, user.Email, user.Name, user.DisplayName, "", string(hash), stamp, 1, stamp, stamp); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at,last_seen_at) VALUES(?,?,?,?,?,?)`, data.Workspace.ID, user.ID, role, "active", stamp, stamp); err != nil {
					return err
				}
				for _, team := range joined {
					if _, err := tx.ExecContext(ctx, `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at,origin) VALUES(?,?,?,?,?,?)`, data.Workspace.ID, team, user.ID, "member", stamp, domain.TeamMemberOriginInvitation); err != nil {
						return err
					}
				}
				expires := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339Nano)
				if _, err := tx.ExecContext(ctx, `INSERT INTO auth_sessions(token_hash,user_id,expires_at,created_at,last_seen_at) VALUES(?,?,?,?,?)`, tokenHash(token), user.ID, expires, stamp, stamp); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO auth_session_security(token_hash,provider,issuer,mfa_verified_at) VALUES(?,?,?,?)`, tokenHash(token), "password", "", nil); err != nil {
					return err
				}
				people = append(people, ScaleP01Principal{User: user, Token: token, Role: role, TeamIDs: joined})
			}
			return tx.Commit()
		}()
		if batchErr != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("P01 principals batch %d: %w", start, batchErr)
		}
	}
	return people, nil
}

package store

import (
	"context"
	"strings"
)

// AuthenticationProviderPolicy reads only the two decisions needed by the
// trusted authentication-policy caller. Both aggregates share one SQL snapshot;
// provider bodies and collection size never enter the application working set.
// A missing workspace returns sql.ErrNoRows.
func (s *SQLiteStore) AuthenticationProviderPolicy(ctx context.Context, workspace string, auth SessionAuthentication) (enforced, matched bool, err error) {
	ctx, release, err := s.admitMetadataWork(ctx, 2<<20)
	if err != nil {
		return false, false, err
	}
	defer release()
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	enabled := s.jsonText("r.data", "enabled") + ` IN ('1','true')`
	required := s.jsonText("r.data", "enforced") + ` IN ('1','true')`
	if s.dialect == "sqlite" {
		enabled = s.jsonText("r.data", "enabled") + `=1`
		required = s.jsonText("r.data", "enforced") + `=1`
	}
	issuer := "COALESCE(" + s.jsonText("r.data", "issuer") + ",'')"
	if s.dialect == "mysql" {
		issuer = "TRIM(TRAILING '/' FROM " + issuer + ")"
	} else {
		issuer = "RTRIM(" + issuer + ",'/')"
	}
	providerID := "COALESCE(" + s.jsonText("r.data", "id") + ",'')"
	match := "(?=1 AND " + s.metadataRecordKeyEquals(providerID, "?") + ") OR (?=1 AND " + s.metadataRecordKeyEquals(issuer, "?") + ")"
	policy := "(" + enabled + " AND " + required + ")"
	var anyEnforced, anyMatched int
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(CASE WHEN `+policy+` THEN 1 ELSE 0 END),0),COALESCE(MAX(CASE WHEN `+policy+` AND (`+match+`) THEN 1 ELSE 0 END),0) FROM workspace_states w LEFT JOIN workspace_metadata_records r ON r.workspace_key=w.workspace_key AND r.field='identityProviders' WHERE w.workspace_key=? GROUP BY w.workspace_key`,
		boolInt(strings.HasPrefix(auth.Provider, "enterprise:")), strings.TrimPrefix(auth.Provider, "enterprise:"),
		boolInt(auth.Provider == "oidc" || auth.Provider == "saml"), strings.TrimRight(auth.Issuer, "/"), workspace).Scan(&anyEnforced, &anyMatched)
	return anyEnforced != 0, anyMatched != 0, err
}

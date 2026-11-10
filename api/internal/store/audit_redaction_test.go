package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// auditTableMentions counts the audit rows of a chain whose stored text
// contains text (any case).
func auditTableMentions(t *testing.T, repo *SQLiteStore, workspaceID, text string) int {
	t.Helper()
	var count int
	pattern := labelReferencePattern(strings.ToLower(text))
	if err := repo.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM audit_events WHERE workspace_id=? AND (LOWER(actor) LIKE ? ESCAPE '!' OR LOWER(metadata) LIKE ? ESCAPE '!' OR LOWER(ip) LIKE ? ESCAPE '!' OR LOWER(user_agent) LIKE ? ESCAPE '!')`, workspaceID, pattern, pattern, pattern, pattern).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// Erasing a person rewrites their entries to the pseudonymous identity and
// the chain still verifies; entries keep their ids, actions and resources.
func TestRedactAuditPersonKeepsTheChainVerifiable(t *testing.T) {
	ctx := context.Background()
	repo, _ := openAuditTestStore(t)
	workspaceID := auditTestWorkspaceID(t, repo)
	const personID = "usr_erased_person"
	person := domain.User{ID: personID, Name: "ada", DisplayName: "Ada Lovelace", Email: "Ada@Example.com", AvatarURL: "https://cdn.example.com/ada.png"}
	admin := domain.User{ID: "usr_admin", Name: "grace", DisplayName: "Grace Hopper", Email: "grace@example.com"}
	now := time.Now().UTC()
	entries := []domain.AuditLogEntry{
		{Action: "signed_in", ResourceType: "account", ResourceID: personID, Actor: person, IP: "203.0.113.0", UserAgent: "Firefox on Linux", CreatedAt: now},
		{Action: "role_changed", ResourceType: "workspace_member", ResourceID: personID, Actor: admin, IP: "198.51.100.0", Metadata: map[string]any{"from": "member", "to": "admin", "userId": personID, "userName": "Ada Lovelace", "userEmail": "ada@example.com"}, CreatedAt: now},
		{Action: "invited", ResourceType: "invitation", ResourceID: "inv_1", Actor: admin, Metadata: map[string]any{"email": "ADA@example.com", "note": "invited ada@example.com today"}, CreatedAt: now},
		{Action: "created", ResourceType: "issue", ResourceID: "iss_1", Actor: admin, Metadata: map[string]any{"title": "Unrelated"}, CreatedAt: now},
	}
	if err := repo.AppendAuditEntries(ctx, workspaceID, entries...); err != nil {
		t.Fatal(err)
	}
	other := "ws_other_chain"
	if err := repo.AppendAuditEntries(ctx, other, domain.AuditLogEntry{Action: "created", ResourceType: "issue", ResourceID: "iss_2", Actor: person, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if report, err := repo.VerifyAuditChain(ctx, workspaceID); err != nil || !report.Valid || report.Entries != 4 {
		t.Fatalf("verify before = %+v, %v", report, err)
	}

	// One workspace: only its chain changes.
	redacted, err := repo.RedactAuditPerson(ctx, []string{workspaceID}, personID, person.Email)
	if err != nil || redacted != 3 {
		t.Fatalf("redacted %d, %v", redacted, err)
	}
	report, err := repo.VerifyAuditChain(ctx, workspaceID)
	if err != nil || !report.Valid || report.Entries != 5 {
		t.Fatalf("verify after the erasure = %+v, %v", report, err)
	}
	for _, text := range []string{"Ada Lovelace", "ada@example.com", "ada.png", "203.0.113.0", "Firefox"} {
		if n := auditTableMentions(t, repo, workspaceID, text); n != 0 {
			t.Errorf("%d audit rows still contain %q", n, text)
		}
	}
	if n := auditTableMentions(t, repo, other, "Ada Lovelace"); n != 1 {
		t.Fatalf("the other chain was changed: %d", n)
	}
	list, err := repo.ListAuditEntries(ctx, workspaceID, AuditQuery{})
	if err != nil {
		t.Fatal(err)
	}
	byAction := map[string]domain.AuditLogEntry{}
	for _, entry := range list {
		byAction[entry.Action] = entry
	}
	signIn := byAction["signed_in"]
	if signIn.Actor.ID != personID || signIn.Actor.DisplayName != domain.DeletedUserDisplayName || signIn.Actor.Email != "" || signIn.IP != "" || signIn.UserAgent != "" || signIn.ResourceID != personID {
		t.Fatalf("person's own entry = %+v", signIn)
	}
	role := byAction["role_changed"]
	if role.Actor.DisplayName != "Grace Hopper" || role.IP != "198.51.100.0" || role.Metadata["to"] != "admin" || role.Metadata["userEmail"] != "" {
		t.Fatalf("entry about the person = %+v", role)
	}
	if byAction["created"].Metadata["title"] != "Unrelated" {
		t.Fatalf("unrelated entry = %+v", byAction["created"])
	}
	record := byAction[AuditActionPersonalDataRedacted]
	if record.ActorType != AuditActorSystem || record.ResourceID != personID {
		t.Fatalf("redaction record = %+v", record)
	}

	// Repeating it changes nothing; the whole account erasure reaches the
	// other chain too.
	if again, err := repo.RedactAuditPerson(ctx, []string{workspaceID}, personID, person.Email); err != nil || again != 0 {
		t.Fatalf("second redaction %d, %v", again, err)
	}
	if redacted, err := repo.RedactAuditPerson(ctx, nil, personID, person.Email); err != nil || redacted != 1 {
		t.Fatalf("account-wide redaction %d, %v", redacted, err)
	}
	for _, chain := range []string{workspaceID, other} {
		if report, err := repo.VerifyAuditChain(ctx, chain); err != nil || !report.Valid {
			t.Fatalf("verify %s = %+v, %v", chain, report, err)
		}
	}

	// Entries appended and pruned afterwards keep the chain verifiable.
	appendTestAudit(t, repo, workspaceID, now, "after")
	if deleted, err := repo.PruneAuditEvents(ctx, workspaceID, now.Add(time.Second)); err != nil || deleted != 6 {
		t.Fatalf("pruned %d, %v", deleted, err)
	}
	if report, err := repo.VerifyAuditChain(ctx, workspaceID); err != nil || !report.Valid {
		t.Fatalf("verify after prune = %+v, %v", report, err)
	}
}

// Removing the salt of an entry no redaction record lists, or editing an
// erased entry's chained columns, still breaks verification.
func TestRedactedAuditEntriesStayTamperEvident(t *testing.T) {
	ctx := context.Background()
	repo, _ := openAuditTestStore(t)
	workspaceID := auditTestWorkspaceID(t, repo)
	const personID = "usr_erased_person"
	if err := repo.AppendAuditEntries(ctx, workspaceID,
		domain.AuditLogEntry{Action: "signed_in", ResourceType: "account", ResourceID: personID, Actor: domain.User{ID: personID, DisplayName: "Ada Lovelace", Email: "ada@example.com"}},
		domain.AuditLogEntry{Action: "created", ResourceType: "issue", ResourceID: "iss_1", Actor: domain.User{ID: "usr_admin", DisplayName: "Grace Hopper"}},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RedactAuditPerson(ctx, []string{workspaceID}, personID, "ada@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE audit_events SET metadata='{"forged":true}',personal_salt='' WHERE workspace_id=? AND seq=2`, workspaceID); err != nil {
		t.Fatal(err)
	}
	if report, _ := repo.VerifyAuditChain(ctx, workspaceID); report.Valid || report.BrokenAt != 2 {
		t.Fatalf("unlisted erasure: %+v", report)
	}

	other, _ := openAuditTestStore(t)
	otherID := auditTestWorkspaceID(t, other)
	if err := other.AppendAuditEntries(ctx, otherID, domain.AuditLogEntry{Action: "signed_in", ResourceType: "account", ResourceID: personID, Actor: domain.User{ID: personID, DisplayName: "Ada Lovelace"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := other.RedactAuditPerson(ctx, []string{otherID}, personID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := other.db.ExecContext(ctx, `UPDATE audit_events SET action='signed_out' WHERE workspace_id=? AND seq=1`, otherID); err != nil {
		t.Fatal(err)
	}
	if report, _ := other.VerifyAuditChain(ctx, otherID); report.Valid || report.BrokenAt != 1 {
		t.Fatalf("edited erased entry: %+v", report)
	}
	// A personal column of an entry that keeps its salt is still covered.
	third, _ := openAuditTestStore(t)
	thirdID := auditTestWorkspaceID(t, third)
	appendTestAudit(t, third, thirdID, time.Now().UTC(), "a")
	if _, err := third.db.ExecContext(ctx, `UPDATE audit_events SET ip='192.0.2.1' WHERE workspace_id=? AND seq=1`, thirdID); err != nil {
		t.Fatal(err)
	}
	if report, _ := third.VerifyAuditChain(ctx, thirdID); report.Valid || report.BrokenAt != 1 {
		t.Fatalf("edited address: %+v", report)
	}
}

// The redaction on PostgreSQL or MySQL: the person's rows are rewritten,
// matched by id and by email in any case, and the chain verifies.
func TestExternalDatabaseAuditRedaction(t *testing.T) {
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || databaseURL == "" {
		t.Skip("external database configuration is not set")
	}
	ctx := context.Background()
	repo, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	workspaceID := "audit_redact_it_" + idgen.New("")
	personID := "usr_redact_" + idgen.New("")
	defer func() {
		_, _ = repo.db.ExecContext(ctx, `DELETE FROM audit_events WHERE workspace_id=?`, workspaceID)
		_, _ = repo.db.ExecContext(ctx, `DELETE FROM audit_chain_heads WHERE workspace_id=?`, workspaceID)
	}()
	if err := repo.AppendAuditEntries(ctx, workspaceID,
		domain.AuditLogEntry{Action: "signed_in", ResourceType: "account", ResourceID: personID, IP: "203.0.113.0", Actor: domain.User{ID: personID, DisplayName: "Zoë Erased", Email: "zoe.erased@example.com"}},
		domain.AuditLogEntry{Action: "invited", ResourceType: "invitation", ResourceID: "inv", Actor: domain.User{ID: "usr_admin", DisplayName: "Admin"}, Metadata: map[string]any{"email": "ZOE.erased@Example.com"}},
		domain.AuditLogEntry{Action: "created", ResourceType: "issue", ResourceID: "iss", Actor: domain.User{ID: "usr_admin", DisplayName: "Admin"}},
	); err != nil {
		t.Fatal(err)
	}
	redacted, err := repo.RedactAuditPerson(ctx, nil, personID, "zoe.erased@example.com")
	if err != nil || redacted != 2 {
		t.Fatalf("redacted %d, %v", redacted, err)
	}
	if report, err := repo.VerifyAuditChain(ctx, workspaceID); err != nil || !report.Valid || report.Entries != 4 {
		t.Fatalf("verify = %+v, %v", report, err)
	}
	for _, text := range []string{"Zoë Erased", "zoe.erased@example.com", "203.0.113.0"} {
		if n := auditTableMentions(t, repo, workspaceID, text); n != 0 {
			t.Errorf("%d rows still contain %q", n, text)
		}
	}
	if deleted, err := repo.PruneAuditEvents(ctx, workspaceID, time.Now().UTC().Add(time.Minute)); err != nil || deleted != 4 {
		t.Fatalf("pruned %d, %v", deleted, err)
	}
}

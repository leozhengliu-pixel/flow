package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// This is an inventory of canonical metadata, ACL, key, membership and account
// writers, not a list of event names. Trigger capture also covers statements
// built dynamically by these writers. A new writer must be reviewed and added.
const scaleP09ReviewedWriters = `
api_key_authentication.go:writeAPIKeyUse
application_agents.go:revokeApplicationCredentialsTx
application_agents.go:writeApplicationInstallationTx
application_identity.go:syncInstallationIdentity
application_uninstall.go:UninstallApplication
auth.go:AcceptInvitationWithChanges
auth.go:ChangePassword
auth.go:DeleteTeamMemberships
auth.go:EnsureWorkspaceMembership
auth.go:LoginExternal
auth.go:LoginExternalIdentity
auth.go:LoginWithAuthToken
auth.go:Register
auth.go:ResetPassword
auth.go:UpdateMemberIdentity
auth.go:UpdateMemberRole
auth.go:UpdateProfile
auth.go:VerifyEmail
auth.go:changeMemberStatus
auth.go:ensureAuthTestFixture
auth.go:ensureSeedWorkspaceOwners
auth.go:provisionExternalMemberships
content_offload.go:migrateOffloadedRecord
favorite_mutation.go:mutateStandaloneFavorite
feature_flags_mutation.go:mutateFeatureFlags
issue_records.go:replaceIssueRecords
label_deletion.go:MutateLabelDeletion
oauth_application_credentials.go:IssueApplicationCredential
oauth_authorization_records.go:CreateOAuthAuthorizationGrant
oauth_authorization_records.go:RequestOAuthApplicationApproval
oauth_authorization_records.go:RevokeOAuthAccessToken
oauth_authorization_records.go:RevokeOAuthAuthorizationRecords
oauth_refresh_replay.go:revokeOAuthAccessFamily
oauth_token_exchange.go:ExchangeOAuthGrant
personal_data.go:EraseAccount
personal_data.go:ErasePersonFromWorkspace
scale_p01_seed.go:SeedScaleP01Principals
scim.go:CreateSCIMUser
scim.go:DeprovisionSCIMUser
scim.go:SyncSCIMGroupTeamChanges
scim.go:UpdateSCIMUser
scim.go:recomputeSCIMUserRole
scim.go:writeSCIMUser
sqlite.go:createWorkspace
sqlite.go:updateWorkspace
team_hierarchy.go:syncTeamAncestorMembers
team_membership_origin.go:ChangeTeamMembership
team_membership_origin.go:RemoveMemberWithChanges
team_membership_origin.go:addTeamAncestorMemberships
team_mutation.go:persistTeamMetadata
team_mutation.go:writeMetadataRecordChanges
team_mutation.go:writeMetadataRecordUpserts
user_settings_mutation.go:mutateUserSettingsRecord
workflow_state_deletion.go:DeleteWorkflowStateRecords
workspace_metadata_records.go:applyMetadataRecords
workspace_metadata_records.go:deleteMetadataRecordRow
workspace_metadata_records.go:writeMetadataRecordRow
`

const scaleP09ReviewedDynamicWriters = `
chat_integrations.go:DeleteChatIntegration
chat_integrations.go:PurgeChatHistory
chat_threads.go:purgeChatThreads
chat_workspace_lifecycle.go:deleteChatWorkspace
chat_workspace_lifecycle.go:renameChatPayloadWorkspace
chat_workspace_lifecycle.go:renameChatWorkspace
database.go:rewriteSQL
issue_index_delta.go:syncIssueIndexRows
issue_records.go:replaceIssueRecords
issue_scope_mutation.go:mutateIssueScope
metadata_catalog_backfill.go:BackfillMetadataCatalog
oauth_token_exchange.go:ExchangeOAuthGrant
personal_data.go:DeleteAgentSessionRuns
record_journal.go:maintainRecordSummary
scale_p09_journal_schema.go:recordJournalTriggerSpecs
scale_p09_journal_repair.go:pruneOrphanRecordCatalog
secret_reseal.go:resealTextSecrets
team_deletion.go:deleteRowsByIDs
workspace_purge.go:PurgeWorkspaceRows
workspace_purge.go:deleteWorkspaceRows
workspace_purge.go:removeWorkspaceFromCatalog
workspace_rename.go:conflictingKeyRowsStatement
workspace_rename.go:moveWorkspaceKeyRows
workspace_rename.go:rewriteWorkspaceKeyJSON
`

func TestScaleP09WriterCoverage(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	canonicalMutation := regexp.MustCompile(`(?is)\b(?:INSERT\s+(?:OR\s+\w+\s+|IGNORE\s+)?INTO|UPDATE|DELETE\s+FROM)\s+(workspace_metadata_records|workspace_memberships|team_memberships|auth_users|issue_permission_records)\b`)
	actual := map[string]bool{}
	dynamic := map[string]bool{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if path == "record_journal.go" && fn.Name.Name == "journalWriteStatement" {
				continue // SQL classification strings, not an executable writer
			}
			name := path + ":" + fn.Name.Name
			ast.Inspect(fn, func(node ast.Node) bool {
				literal, ok := node.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				text, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatal(err)
				}
				if match := canonicalMutation.FindStringSubmatch(text); match != nil {
					actual[name] = true
					if !recordJournalTables[strings.ToLower(match[1])] || !journalWriteStatement(text) {
						t.Errorf("writer has no transaction/trigger capture: %s: %s", name, text)
					}
				}
				if strings.Contains(text, "DELETE FROM ") || strings.Contains(text, "UPDATE ") || strings.Contains(text, "INSERT INTO ") {
					if text == "DELETE FROM " || text == "UPDATE " || text == "INSERT INTO " {
						dynamic[name] = true
					}
				}
				return true
			})
		}
	}
	want := strings.Fields(scaleP09ReviewedWriters)
	slices.Sort(want)
	got := make([]string, 0, len(actual))
	for name := range actual {
		got = append(got, name)
	}
	slices.Sort(got)
	if !reflect.DeepEqual(got, want) {
		for _, name := range got {
			if !slices.Contains(want, name) {
				t.Errorf("unreviewed writer: %s", name)
			}
		}
		for _, name := range want {
			if !actual[name] {
				t.Errorf("writer inventory is stale: %s", name)
			}
		}
	}
	dynamicNames := make([]string, 0, len(dynamic))
	for name := range dynamic {
		dynamicNames = append(dynamicNames, name)
	}
	dynamicWant := strings.Fields(scaleP09ReviewedDynamicWriters)
	slices.Sort(dynamicNames)
	slices.Sort(dynamicWant)
	if !reflect.DeepEqual(dynamicNames, dynamicWant) {
		t.Errorf("dynamic SQL writer inventory changed; review trigger coverage:\ngot %v\nwant %v", dynamicNames, dynamicWant)
	}
	// The dynamic source registries are independently exhaustive in their
	// existing schema coverage tests. Verify their canonical tables here too.
	for _, table := range []string{"workspace_metadata_records", "workspace_memberships", "team_memberships", "issue_records", "workspace_content_records", "issue_permission_records", "auth_users"} {
		for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
			if table == "auth_users" && event == "INSERT" {
				continue // no account has a membership before insertion
			}
			for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
				found := false
				for _, spec := range recordJournalTriggerSpecs(dialect) {
					if spec.table == table && spec.event == event && spec.body != "" {
						found = true
					}
				}
				if !found {
					t.Errorf("missing %s capture for %s %s", dialect, event, table)
				}
			}
		}
	}
	// These dynamic writers must continue through the transaction wrapper.
	for _, path := range []string{"personal_data.go", "workspace_purge.go", "workspace_rename.go", "issue_index_delta.go", "issue_scope_mutation.go", "team_deletion.go"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), ".Tx.Exec") || strings.Contains(string(raw), ".DB.Exec") {
			t.Errorf("dynamic writer bypasses journal capture: %s", path)
		}
	}
	t.Logf("reviewed canonical/ACL/key/membership writers=%d dynamic SQL families=%d", len(got), len(dynamic))
}

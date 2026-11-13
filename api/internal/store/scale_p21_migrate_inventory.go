package store

// Reviews apply to both representations of the installed workspace. Keep the
// reason beside the entry; a policy-only copy must never certify body readers.
func scaleP21MigrateReviewedConsumers() map[string]string {
	return map[string]string{
		"store/auth.go:SetLastWorkspace":                            "Bounded mode reads only the installed workspace identity before the unchanged live membership check and account-state write; no body collection is copied.",
		"store/feature_flags_mutation.go:WorkspaceSettingsMetadata": "Copies only the workspace identity and workspace settings, both retained and charged to the process policy budget.",
		"store/issue_access_metadata.go:IssueAccessMetadata":        "Copies only resident workspace settings and the team policy graph; request snapshots retain their existing precedence and authorization remains with the caller.",
		"store/oauth_workspace_reads.go:ResolveTeamID":              "Matches IDs and case-insensitive team keys within the resident team graph; it reads no body collection.",
		"store/oauth_workspace_reads.go:TeamSettingsFor":            "Looks up one team settings record in the resident policy map, preserving the trusted internal reader contract.",
		"store/peer_freshness.go:HasWorkspace":                      "Checks only whether the workspace core is installed; it does not inspect or certify record completeness.",
		"store/scim.go:MustWorkspaceID":                             "Bounded mode reads only the installed workspace identity; off mode retains the legacy metadata reader and empty-key behavior.",
	}
}

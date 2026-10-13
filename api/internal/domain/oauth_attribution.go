package domain

// ActorBot attributes a record to an OAuth application ("<User> via <App>").
//
// It is stored on issues, comments, attachments, documents and activity when
// the request was authenticated with an OAuth token:
//   - actor=user token: the record's user is the person who authorized the
//     application; ActorBot names the application.
//   - actor=app token with createAsUser: the record's user is the application
//     user; UserDisplayName / UserAvatarURL name the external person the
//     application acted for.
type ActorBot struct {
	ID string `json:"id,omitempty"`
	// Type is "oauthApplication" for OAuth clients.
	Type            string `json:"type"`
	Name            string `json:"name,omitempty"`
	AvatarURL       string `json:"avatarUrl,omitempty"`
	UserDisplayName string `json:"userDisplayName,omitempty"`
	UserAvatarURL   string `json:"userAvatarUrl,omitempty"`
}

// ActorBotTypeOAuthApplication marks attribution to an OAuth client.
const ActorBotTypeOAuthApplication = "oauthApplication"

// UserOnlyOAuthScope reports scopes that only a user-actor token can hold.
// "admin" is never granted to actor=app tokens; "timeSchedule:write" is
// accepted for compatibility with the reference app integrations and grants nothing.
func UserOnlyOAuthScope(scope string) bool {
	return scope == "admin" || scope == "timeSchedule:write"
}

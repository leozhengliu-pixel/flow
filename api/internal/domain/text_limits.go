package domain

// Sizes of the VARCHAR columns that hold user text, in characters (MySQL
// utf8mb4 and Postgres both count characters, not bytes). Handlers check
// input against them so an over-long value is a 400, not a database error.
const (
	// IssueTitleMaxLength is issue_records.title.
	IssueTitleMaxLength = 2048
	// SCIMValueMaxLength is auth_identities.username/subject, auth_users.name
	// and display_name, and scim_groups.display_name/external_id.
	SCIMValueMaxLength = 320
	// UploadTitleMaxLength is file_uploads.title and subtitle.
	UploadTitleMaxLength = 512
	// SearchQueryMaxLength is search_history.query (VARCHAR on MySQL).
	SearchQueryMaxLength = 300
	// UserNameMaxLength is auth_users.name and display_name.
	UserNameMaxLength = 320
	// EmailMaxLength is the longest address SMTP allows (RFC 5321); the
	// email columns are VARCHAR(320).
	EmailMaxLength = 254
	// AvatarURLMaxLength is auth_users.avatar_url.
	AvatarURLMaxLength = 2048
)

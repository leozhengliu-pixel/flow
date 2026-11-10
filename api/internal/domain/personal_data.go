package domain

import "time"

// DeletedUserDisplayName is the name every deleted or erased person shows
// with. The web app translates it.
const DeletedUserDisplayName = "Deleted user"

// DeletedUserIdentity is the pseudonymous identity that replaces a person
// whose account was deleted, or whose personal data a workspace admin erased:
// the same stable id (an opaque random account id, which never carried
// personal data), a neutral handle derived from it, and no name, email or
// avatar.
func DeletedUserIdentity(id string) User {
	handle := id
	if len(handle) > 8 {
		handle = handle[len(handle)-8:]
	}
	return User{ID: id, Name: "deleted-user-" + handle, DisplayName: DeletedUserDisplayName, Deleted: true}
}

// PersonalDataExport is one "Download your data" request: a zip of the
// signed-in person's own data, prepared in the background and downloadable
// by them until it expires.
type PersonalDataExport struct {
	ID          string     `json:"id"`
	Status      string     `json:"status" enum:"queued,running,completed,failed,expired"`
	Size        int64      `json:"size,omitempty"`
	Error       string     `json:"error,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
	// DownloadURL is set on completed exports that have not expired.
	DownloadURL string `json:"downloadUrl,omitempty"`
}

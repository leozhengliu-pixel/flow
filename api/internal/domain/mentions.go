package domain

// IsUserMention reports whether a rich-text `mention` node's attrs name a
// person. Mention nodes reference every kind of workspace resource
// (attrs.mentionType issue, project, document, ...); only "user" mentions, and
// older nodes written before mentionType existed, carry a user id.
func IsUserMention(attrs map[string]any) bool {
	kind, _ := attrs["mentionType"].(string)
	return kind == "" || kind == "user"
}

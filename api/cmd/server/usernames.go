package main

import (
	"regexp"
	"strconv"
	"strings"

	"flow/api/internal/domain"
)

// Usernames follow Linear's handle rules: lowercase letters, digits, dots,
// underscores and hyphens. They are a workspace-scoped handle distinct from
// the user's full name (User.Name/User.DisplayName keep their meaning).
const maxUsernameLength = 40

var usernamePattern = regexp.MustCompile(`^[a-z0-9._-]+$`)

// normalizeUsernameInput lowercases and trims a requested username.
func normalizeUsernameInput(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// validUsername reports whether value is an acceptable, already-normalized
// username: 1-40 allowed characters containing at least one letter or digit.
func validUsername(value string) bool {
	if value == "" || len(value) > maxUsernameLength || !usernamePattern.MatchString(value) {
		return false
	}
	return strings.IndexFunc(value, func(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') }) >= 0
}

// sanitizeUsername turns arbitrary text (an email local part, a legacy
// profile value) into a username candidate, or "" when nothing usable remains.
func sanitizeUsername(value string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			builder.WriteRune(r)
		}
	}
	result := strings.Trim(builder.String(), "._-")
	if len(result) > maxUsernameLength {
		result = strings.Trim(result[:maxUsernameLength], "._-")
	}
	if !validUsername(result) {
		return ""
	}
	return result
}

// usernameCandidate is the preferred username for a user before uniqueness is
// applied, and whether it was explicitly chosen in account settings.
func usernameCandidate(user domain.User, settings domain.UserSettings) (string, bool) {
	if explicit := normalizeUsernameInput(settings.Username); validUsername(explicit) {
		return explicit, true
	}
	if at := strings.LastIndex(user.Email, "@"); at > 0 {
		if value := sanitizeUsername(user.Email[:at]); value != "" {
			return value, false
		}
	}
	if value := sanitizeUsername(settings.Username); value != "" {
		return value, false
	}
	if value := sanitizeUsername(user.Name); value != "" {
		return value, false
	}
	if value := sanitizeUsername(user.DisplayName); value != "" {
		return value, false
	}
	return "user", false
}

// workspaceUsernames derives a unique username for every user. Explicit
// usernames (validated as unique when saved) are claimed first; derived
// usernames that would collide get the lowest free numeric suffix, in the
// order the users are supplied, so the result is stable across requests.
func workspaceUsernames(users []domain.User, settings map[string]domain.UserSettings) map[string]string {
	result := make(map[string]string, len(users))
	taken := make(map[string]bool, len(users))
	type pending struct{ id, base string }
	derived := make([]pending, 0, len(users))
	for _, user := range users {
		if user.ID == "" {
			continue
		}
		if _, seen := result[user.ID]; seen {
			continue
		}
		base, explicit := usernameCandidate(user, settings[user.ID])
		if explicit && !taken[base] {
			result[user.ID], taken[base] = base, true
			continue
		}
		result[user.ID] = ""
		derived = append(derived, pending{user.ID, base})
	}
	for _, item := range derived {
		name := item.base
		for suffix := 2; taken[name]; suffix++ {
			tail := strconv.Itoa(suffix)
			head := item.base
			if len(head)+len(tail) > maxUsernameLength {
				head = head[:maxUsernameLength-len(tail)]
			}
			name = head + tail
		}
		result[item.id], taken[name] = name, true
	}
	return result
}

// bootstrapUsers lists every user visible in a bootstrap, directory users
// first so derived usernames stay stable regardless of membership decoration.
func bootstrapUsers(data *domain.Bootstrap) []domain.User {
	users := make([]domain.User, 0, len(data.Users)+len(data.Members)+1)
	users = append(users, data.Users...)
	for _, member := range data.Members {
		users = append(users, member.User)
	}
	if data.Viewer.ID != "" {
		users = append(users, data.Viewer)
	}
	return users
}

// applyUsernames fills User.Username on every user in the bootstrap. It must
// run before per-viewer trimming of UserSettings.
func applyUsernames(data *domain.Bootstrap) {
	usernames := workspaceUsernames(bootstrapUsers(data), data.UserSettings)
	for index := range data.Users {
		data.Users[index].Username = usernames[data.Users[index].ID]
	}
	for index := range data.Members {
		data.Members[index].User.Username = usernames[data.Members[index].User.ID]
	}
	data.Viewer.Username = usernames[data.Viewer.ID]
}

// usernameTaken reports whether another user in the workspace already holds
// (explicitly or by derivation) the requested username.
func usernameTaken(data domain.Bootstrap, userID, username string) bool {
	users := bootstrapUsers(&data)
	usernames := workspaceUsernames(users, data.UserSettings)
	for id, value := range usernames {
		if id != userID && value == username {
			return true
		}
	}
	return false
}

// errUsernameTaken maps to 409 through respondMutation's errConflict branch
// while keeping a readable message.
var errUsernameTaken error = usernameTakenError{}

type usernameTakenError struct{}

func (usernameTakenError) Error() string { return "That username is already taken" }
func (usernameTakenError) Unwrap() error { return errConflict }

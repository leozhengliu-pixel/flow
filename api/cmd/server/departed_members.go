package main

import (
	"net/url"
	"os"
	"strings"

	"flow/api/internal/domain"
)

// presentDepartedMembers fills the derived fields of Bootstrap.DepartedMembers
// (Settings › Members › Inactive, "Left workspace"): a username, and for the
// app user of an uninstalled application the generated address the reference app shows
// for such users (<id>@oauthapp.<host>), since app users have no email.
func presentDepartedMembers(data *domain.Bootstrap) {
	for index := range data.DepartedMembers {
		user := &data.DepartedMembers[index].User
		if strings.HasPrefix(user.ID, "app_") && user.Email == "" {
			// The handle comes from the app's name, as the reference app's ("qaoauthinstallflow").
			user.Username, _ = usernameCandidate(domain.User{Name: user.Name, DisplayName: user.DisplayName}, domain.UserSettings{})
			user.Email = appUserEmail(user.ID)
			continue
		}
		user.Username, _ = usernameCandidate(*user, data.UserSettings[user.ID])
	}
}

// appUserEmail is an app user's generated address: its id, formatted like a
// UUID, at oauthapp.<the app's host>.
func appUserEmail(userID string) string {
	id := strings.TrimPrefix(userID, "app_")
	if len(id) == 32 {
		id = id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
	}
	host := "localhost"
	if parsed, err := url.Parse(strings.TrimSpace(os.Getenv("FLOW_APP_URL"))); err == nil && parsed.Hostname() != "" {
		host = parsed.Hostname()
	}
	return id + "@oauthapp." + host
}

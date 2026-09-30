package main

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestSanitizeAndValidateUsername(t *testing.T) {
	cases := map[string]string{
		"bcgroupdev":            "bcgroupdev",
		"Skyler.Anderson":       "skyler.anderson",
		"first+tag":             "firsttag",
		"  -._odd_.-  ":         "odd",
		"Dev User":              "devuser",
		"___":                   "",
		"名字":                    "",
		strings.Repeat("a", 60): strings.Repeat("a", 40),
	}
	for input, want := range cases {
		if got := sanitizeUsername(input); got != want {
			t.Errorf("sanitizeUsername(%q) = %q, want %q", input, got, want)
		}
	}
	for _, value := range []string{"bcgroupdev", "a.b_c-d", "x1"} {
		if !validUsername(value) {
			t.Errorf("validUsername(%q) = false", value)
		}
	}
	for _, value := range []string{"", "Upper", "has space", "._-", "emoji😀", strings.Repeat("a", 41)} {
		if validUsername(value) {
			t.Errorf("validUsername(%q) = true", value)
		}
	}
}

func TestWorkspaceUsernamesDerivationAndUniqueness(t *testing.T) {
	users := []domain.User{
		{ID: "u1", Name: "Dev User", DisplayName: "Dev User", Email: "bcgroupdev@gmail.com"},
		{ID: "u2", Name: "Other", DisplayName: "Other", Email: "bcgroupdev@example.com"},
		{ID: "u3", Name: "Third", DisplayName: "Third", Email: "BCGroupDev@corp.test"},
		{ID: "u4", Name: "Picked", DisplayName: "Picked", Email: "picked@example.com"},
		{ID: "u5", Name: "No Email", DisplayName: "No Email"},
	}
	settings := map[string]domain.UserSettings{
		// An explicit username wins even when another user derives the same one.
		"u4": {Username: "bcgroupdev"},
	}
	got := workspaceUsernames(users, settings)
	want := map[string]string{"u1": "bcgroupdev2", "u2": "bcgroupdev3", "u3": "bcgroupdev4", "u4": "bcgroupdev", "u5": "noemail"}
	for id, value := range want {
		if got[id] != value {
			t.Fatalf("username for %s = %q, want %q (all: %#v)", id, got[id], value, got)
		}
	}
	if !usernameTaken(domain.Bootstrap{Users: users, UserSettings: settings}, "u1", "bcgroupdev") {
		t.Fatal("explicit username should be reported as taken")
	}
	if usernameTaken(domain.Bootstrap{Users: users, UserSettings: settings}, "u4", "bcgroupdev") {
		t.Fatal("a user's own username is never taken")
	}
	if !usernameTaken(domain.Bootstrap{Users: users}, "u4", "bcgroupdev") {
		t.Fatal("derived usernames of other users should be reported as taken")
	}
}

func TestBootstrapExposesUsernamesAndProfileValidatesThem(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	seen := map[string]string{}
	for _, user := range bootstrap.Users {
		if !validUsername(user.Username) {
			t.Fatalf("user %s has invalid username %q", user.ID, user.Username)
		}
		if other, ok := seen[user.Username]; ok {
			t.Fatalf("username %q shared by %s and %s", user.Username, other, user.ID)
		}
		seen[user.Username] = user.ID
		if at := strings.Index(user.Email, "@"); at > 0 && user.Username != sanitizeUsername(user.Email[:at]) && !strings.HasPrefix(user.Username, sanitizeUsername(user.Email[:at])) {
			t.Fatalf("username %q not derived from email %q", user.Username, user.Email)
		}
	}
	if bootstrap.Viewer.Username == "" || len(bootstrap.Members) == 0 || bootstrap.Members[0].User.Username == "" {
		t.Fatalf("viewer/member usernames missing: viewer=%q", bootstrap.Viewer.Username)
	}
	if len(bootstrap.Users) < 2 {
		t.Skip("fixture needs two users")
	}
	var other domain.User
	for _, user := range bootstrap.Users {
		if user.ID != bootstrap.Viewer.ID {
			other = user
			break
		}
	}
	profile := func(username string, status int) {
		t.Helper()
		requestJSON[any](t, handler, http.MethodPatch, "/api/account/profile", map[string]any{"displayName": bootstrap.Viewer.DisplayName, "username": username}, status)
	}
	profile("Has Space", http.StatusBadRequest)
	profile("bad!chars", http.StatusBadRequest)
	profile(other.Username, http.StatusConflict)
	profile("Fresh.Handle", http.StatusOK)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if bootstrap.Viewer.Username != "fresh.handle" {
		t.Fatalf("viewer username = %q, want fresh.handle", bootstrap.Viewer.Username)
	}
	if viewerName := bootstrap.Viewer.Name; viewerName == "fresh.handle" {
		t.Fatalf("saving a username overwrote the account name (profile URL key)")
	}
	// An admin renaming a member's username gets the same rules and leaves the name alone.
	member := func(username string, status int) {
		t.Helper()
		requestJSON[any](t, handler, http.MethodPatch, "/api/workspaces/"+bootstrap.Workspace.URLKey+"/members/"+other.ID, map[string]any{"username": username}, status)
	}
	member("bad handle", http.StatusBadRequest)
	member("fresh.handle", http.StatusConflict)
	member("Other.Handle", http.StatusOK)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	for _, user := range bootstrap.Users {
		if user.ID == other.ID && (user.Username != "other.handle" || user.Name != other.Name) {
			t.Fatalf("member after username update: username=%q name=%q (was %q)", user.Username, user.Name, other.Name)
		}
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/account/settings", map[string]any{"username": "other.handle"}, http.StatusConflict)
	requestJSON[any](t, handler, http.MethodPatch, "/api/account/settings", map[string]any{"username": "no spaces allowed"}, http.StatusBadRequest)
}

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP20ThousandMemberTransport(t *testing.T) {
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", 32))))
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	db := newScaleP04Database(t)
	rows := db.query(t, "SELECT id FROM auth_users WHERE id=?", db.users[0].ID)
	rows.Close()
	prefix := idgen.New("p20-directory-")
	bind := func(query string) string {
		if db.config.Driver != "postgres" && db.config.Driver != "postgresql" {
			return query
		}
		index := 0
		var result strings.Builder
		for _, character := range query {
			if character == '?' {
				index++
				fmt.Fprintf(&result, "$%d", index)
			} else {
				result.WriteRune(character)
			}
		}
		return result.String()
	}
	t.Cleanup(func() {
		if _, err := db.reader.ExecContext(context.Background(), bind(`DELETE FROM auth_users WHERE id LIKE ?`), prefix+"-%"); err != nil {
			t.Error(err)
		}
	})
	tx, err := db.reader.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for index := range 1000 {
		id := fmt.Sprintf("%s-%04d", prefix, index)
		if _, err := tx.ExecContext(t.Context(), bind(`INSERT INTO auth_users(id,email,name,display_name,password_hash,email_verified_at,created_at,updated_at) SELECT ?,?,?,?,password_hash,email_verified_at,created_at,updated_at FROM auth_users WHERE id=?`), id, id+"@example.test", fmt.Sprintf("P20 user %04d", index), fmt.Sprintf("P20 user %04d", index), db.users[0].ID); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(t.Context(), bind(`INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at) SELECT workspace_id,?,'member',?,joined_at FROM workspace_memberships WHERE workspace_id=? AND user_id=?`), id, map[bool]string{true: "suspended", false: "active"}[index == 999], db.workspaceID, db.users[0].ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	handler := db.handler(t, true)
	read := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %.200s", path, w.Code, w.Body.String())
		}
		return w
	}
	bootstrap := read("/api/bootstrap")
	var legacy domain.Bootstrap
	if err := json.Unmarshal(bootstrap.Body.Bytes(), &legacy); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	cursor, pages, maximum, total := "", 0, 0, 0
	for {
		w := read("/api/resource-windows/members?limit=50&cursor=" + cursor)
		var page store.RecordPage
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Items) > 50 || w.Body.Len() > 256<<10 {
			t.Fatal("member transport exceeded its row/byte limit")
		}
		pages++
		maximum = max(maximum, w.Body.Len())
		total += w.Body.Len()
		for _, item := range page.Items {
			var member domain.WorkspaceMember
			if err := json.Unmarshal(item.Data, &member); err != nil {
				t.Fatal(err)
			}
			if seen[member.User.ID] {
				t.Fatal("member duplicated across windows")
			}
			seen[member.User.ID] = true
		}
		if !page.HasMore {
			break
		}
		if page.Next == "" || page.Next == cursor {
			t.Fatal("member cursor did not advance")
		}
		cursor = page.Next
	}
	if len(seen) != len(legacy.Members) || len(seen) < 1000 {
		t.Fatalf("partial directory: %d of %d", len(seen), len(legacy.Members))
	}
	t.Logf("fixture=1000-extra-members legacyBootstrapBytes=%d memberPages=%d maxMemberPageBytes=%d totalMemberPageBytes=%d completeMembers=%d", bootstrap.Body.Len(), pages, maximum, total, len(seen))
}

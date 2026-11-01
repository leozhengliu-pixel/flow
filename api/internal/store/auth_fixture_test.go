package store

import (
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestAuthTestFixtureKeepsPasswordCostAndIsolation(t *testing.T) {
	for _, password := range []string{"test-password", "test-password", "custom-fixture-password"} {
		t.Run(password, func(t *testing.T) {
			t.Parallel()
			repository, err := OpenDatabase(DatabaseConfig{
				Driver: "sqlite", Path: filepath.Join(t.TempDir(), "flow.db"),
				FixtureProfile: "test", FixturePassword: password, MaxOpenConns: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repository.Close() })
			viewer := repository.Bootstrap().Viewer
			var hash string
			if err := repository.db.QueryRow(`SELECT password_hash FROM auth_users WHERE id=?`, viewer.ID).Scan(&hash); err != nil {
				t.Fatal(err)
			}
			if cost, err := bcrypt.Cost([]byte(hash)); err != nil || cost != bcrypt.DefaultCost {
				t.Fatalf("password cost = %d, %v", cost, err)
			}
			if _, _, err := repository.Login(t.Context(), viewer.Email, password); err != nil {
				t.Fatalf("fixture login: %v", err)
			}
			if _, _, err := repository.Login(t.Context(), viewer.Email, "wrong-password"); err == nil {
				t.Fatal("fixture accepted the wrong password")
			}
			if _, err := repository.db.Exec(`UPDATE auth_users SET password_hash='' WHERE id=?`, viewer.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

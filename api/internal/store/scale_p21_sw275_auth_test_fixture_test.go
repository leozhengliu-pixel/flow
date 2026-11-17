package store

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func scaleP21SW275Rows(t *testing.T, repo *SQLiteStore) []string {
	t.Helper()
	var rows []string
	for _, query := range []string{
		`SELECT id,email,name,display_name,avatar_url,password_hash,email_verified_at,active,created_at,updated_at FROM auth_users ORDER BY id`,
		`SELECT workspace_id,user_id,role,status,joined_at,last_seen_at FROM workspace_memberships ORDER BY workspace_id,user_id`,
		`SELECT workspace_id,team_id,user_id,role,joined_at FROM team_memberships ORDER BY workspace_id,team_id,user_id`,
	} {
		result, err := repo.db.QueryContext(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		columns, _ := result.Columns()
		for result.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := result.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			parts := make([]string, len(values))
			for i, value := range values {
				if raw, ok := value.([]byte); ok {
					value = string(raw)
				}
				parts[i] = fmt.Sprint(value)
				// The fixture stamps every row with one clock reading.
				if strings.HasSuffix(columns[i], "_at") && value != nil {
					parts[i] = "stamp"
				}
			}
			rows = append(rows, strings.Join(parts, "|"))
		}
		if err := result.Err(); err != nil {
			t.Fatal(err)
		}
		result.Close()
	}
	return rows
}

func scaleP21SW275Clear(t *testing.T, repo *SQLiteStore) {
	t.Helper()
	for _, statement := range []string{`DELETE FROM team_memberships`, `DELETE FROM workspace_memberships`, `DELETE FROM auth_users`} {
		if _, err := repo.db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
}

// TestScaleP21SW275AuthTestFixtureDifferential compares the test-profile
// account seed of a legacy store with a bounded store whose resident copy is
// policy-only: the same auth users, workspace memberships and team roles,
// including inline (duplicate-ID) user collections where the first entry
// wins, inactive and mixed-case users, several teams, the populated no-op and
// the missing-password refusal. The bounded seed decodes no workspace.
func TestScaleP21SW275AuthTestFixtureDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, inline := range []bool{false, true} {
		t.Run(fmt.Sprintf("inline=%t", inline), func(t *testing.T) {
			repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			workspace := "test-workspace"
			stored, _ := repo.storedWorkspace(workspace)
			if err := repo.MutateWorkspace(WithMetadataFields(t.Context(), "users", "teams"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
				data.Users = append(data.Users,
					domain.User{ID: "sw275-mixed", Email: " Mixed.Case@Example.TEST ", Name: "Mixed", DisplayName: "mixed", AvatarURL: "https://example.test/a.png", Active: true},
					domain.User{ID: "sw275-inactive", Email: "inactive@example.test", Name: "Inactive"},
				)
				if inline {
					// A duplicate ID keeps the collection inline in the root;
					// the first entry is the one the fixture inserts.
					data.Users = append(data.Users, domain.User{ID: "sw275-mixed", Email: "second@example.test", Name: "Second", Active: true})
				}
				data.Teams = append(data.Teams, domain.Team{ID: "sw275-team", Key: "SWT", Name: "SW275 team"})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			var records int
			if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field='users'`, workspace).Scan(&records); err != nil {
				t.Fatal(err)
			}
			if inline != (records == 0) {
				t.Fatalf("inline=%t but %d user records", inline, records)
			}
			var results [][]string
			for _, bounded := range []bool{false, true} {
				scaleP21SW275Clear(t, repo)
				target := repo
				if bounded {
					view, err := repo.BoundedMetadataViewForTesting(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					defer view.releaseMetadataLogicalSources()
					if data, _ := view.storedWorkspace(workspace); len(data.Users) != 0 || len(data.Teams) == 0 {
						t.Fatalf("bounded resident copy is not policy-only: users=%d teams=%d", len(data.Users), len(data.Teams))
					}
					target = view
				}
				target.fixturePassword = ""
				if err := target.ensureAuthTestFixture(t.Context()); err == nil || err.Error() != "test fixture password is required" {
					t.Fatalf("bounded=%t missing password: %v", bounded, err)
				}
				target.fixturePassword = "test-password"
				if err := target.ensureAuthTestFixture(t.Context()); err != nil {
					t.Fatalf("bounded=%t: %v", bounded, err)
				}
				rows := scaleP21SW275Rows(t, repo)
				// A populated table is left alone.
				if _, err := repo.db.ExecContext(t.Context(), `DELETE FROM team_memberships`); err != nil {
					t.Fatal(err)
				}
				if err := target.ensureAuthTestFixture(t.Context()); err != nil {
					t.Fatal(err)
				}
				if after := scaleP21SW275Rows(t, repo); len(after) >= len(rows) {
					t.Fatalf("bounded=%t populated fixture was reseeded", bounded)
				}
				if bounded && (target.WorkspaceMetadataDecodes() != 0 || target.metadataResidency.work.used != 0) {
					t.Fatal("bounded fixture decoded the workspace or leaked work")
				}
				results = append(results, rows)
			}
			if !reflect.DeepEqual(results[0], results[1]) {
				t.Fatalf("fixture rows differ:\nlegacy=%q\nbounded=%q", results[0], results[1])
			}
			joined := strings.Join(results[0], "\n")
			for _, want := range []string{"sw275-mixed|mixed.case@example.test|Mixed|", "sw275-inactive|inactive@example.test|Inactive||", "|sw275-team|" + stored.Viewer.ID + "|owner|"} {
				if !strings.Contains(joined, want) {
					t.Fatalf("fixture rows lack %q:\n%s", want, joined)
				}
			}
		})
	}
}

// TestScaleP21SW275AuthTestFixtureCallset proves the fixture is a
// constructor-only seed: its single reference is OpenDatabase's
// seedWorkspaceOwners stage under the "test" fixture profile, and OpenDatabase
// is called by no request handler or ticker (only main and test helpers).
func TestScaleP21SW275AuthTestFixtureCallset(t *testing.T) {
	references := scaleP21SW275References(t, ".", "ensureAuthTestFixture")
	if !reflect.DeepEqual(references, []string{"database.go:OpenDatabase"}) {
		t.Fatalf("ensureAuthTestFixture references=%v", references)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "database.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	guarded := 0
	ast.Inspect(file, func(node ast.Node) bool {
		branch, ok := node.(*ast.IfStmt)
		if !ok || !scaleP21SW278CallNamed(branch.Cond, "EqualFold") {
			return true
		}
		ast.Inspect(branch.Body, func(node ast.Node) bool {
			if selector, ok := node.(*ast.SelectorExpr); ok && selector.Sel.Name == "ensureAuthTestFixture" {
				var condition strings.Builder
				ast.Inspect(branch.Cond, func(node ast.Node) bool {
					if literal, ok := node.(*ast.BasicLit); ok {
						condition.WriteString(literal.Value)
					}
					if selector, ok := node.(*ast.SelectorExpr); ok {
						condition.WriteString("." + selector.Sel.Name)
					}
					return true
				})
				if condition.String() != `.EqualFold.TrimSpace.FixtureProfile"test"` {
					t.Errorf("fixture guard is %q", condition.String())
				}
				guarded++
			}
			return true
		})
		return true
	})
	if guarded != 1 {
		t.Fatalf("ensureAuthTestFixture test-profile guards=%d", guarded)
	}
	// Constructors close transitively over wrappers: every caller is a
	// constructor wrapper, the process entry point or a CLI subcommand that
	// only main dispatches, never a handler or ticker.
	constructors := map[string]bool{"OpenDatabase": true}
	allowed := map[string]bool{"main.go:main": true}
	for changed := true; changed; {
		changed = false
		names := []string{}
		for name := range constructors {
			names = append(names, name)
		}
		callers := append(scaleP21SW275References(t, ".", names...), scaleP21SW275References(t, "../../cmd/server", names...)...)
		for _, caller := range callers {
			switch caller {
			case "sqlite.go:OpenSQLite", "sqlite.go:OpenSQLiteTestFixture", "audit_log.go:runAuditVerifyCommand":
				function := caller[strings.Index(caller, ":")+1:]
				if !constructors[function] {
					constructors[function], changed = true, true
				}
			case "main.go:main":
			default:
				t.Errorf("store constructor %v referenced from %s", names, caller)
			}
			allowed[caller] = true
		}
	}
	if !allowed["main.go:main"] || !constructors["runAuditVerifyCommand"] {
		t.Fatalf("store constructor closure=%v", constructors)
	}
}

func slicesContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// scaleP21SW275References lists file:function for every non-test selector or
// identifier naming one of names (calls and function values alike), skipping
// the declarations themselves.
func scaleP21SW275References(t *testing.T, dir string, names ...string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var references []string
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			owner := "var"
			var body ast.Node = declaration
			if function, ok := declaration.(*ast.FuncDecl); ok {
				owner = function.Name.Name
				if function.Body == nil {
					continue
				}
				body = function.Body
			}
			ast.Inspect(body, func(node ast.Node) bool {
				name := ""
				switch value := node.(type) {
				case *ast.SelectorExpr:
					name = value.Sel.Name
				case *ast.Ident:
					name = value.Name
				}
				for _, want := range names {
					if name == want {
						references = append(references, filepath.Base(path)+":"+owner)
					}
				}
				return true
			})
		}
	}
	return scaleP21SW275Unique(references)
}

func scaleP21SW275Unique(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

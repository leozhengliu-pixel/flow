package store

import (
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// This is one closed production-source audit, not a list of convenient test
// constructors. Fingerprints deliberately require re-audit even for harmless
// changes to a carrier: metadata can escape through an existing captured value.
func TestBorrowedSourceOriginGuard(t *testing.T) {
	contracts := map[string]string{
		// SQL-reader borrower only: it never binds or edits snapshot metadata.
		"workflow_run_write.go": "f157c0fc7ac57b4167be8349bb308712498b09524b87579218fa2f129fff4d9e",
		"issue_records.go":      "475e2001455c652c9d3546d89acdc76361ca8d65081b93b72a2677de0e5b0b2a",
		"issue_query.go":        "1e7cfc29bebf6e58999abc0cb7bf5924e563558b48d346f03865ee1be5c3a621",
		// Bootstrap bodies reuse the outline's snapshot only for the same
		// database and workspace; the IDs come from the authorized outline.
		"issue_scan.go": "dd147c46e1bad9c304a87f9968b4944171159f97463b741d523d74dc9940d7ba",
		// The result cache only checks for a borrowed snapshot to bypass Redis.
		"shared_result_cache.go":                 "a8b3776efb34fe28b10957fe5e9e29f064bf2965809262760d29bfa07351afb3",
		"auth.go":                                "f2ec6e4263b8a860678bc995f9324515c3ed5dcdab9f485689091c7f1d9f65ce",
		"team_default_favorites.go":              "160beec6f9080397a9addddd55d7c0399ad2a755c2b9331ea7b1699811e9083e",
		"snapshot_buffers.go":                    "023c93b51f6e9eaa6c0ba0b29a9f9e49d92ce006406801fb07d45cc8609e6bb2",
		"snapshot_clone.go":                      "4a6d61a5291c12c71aa737f952c137a51d5dbd5ccbdf72fbce5af5fed755b166",
		"../../cmd/server/advanced_features.go":  "0d76b3449e39b23060f85d31817d4ff186210e1f0c5099f6e7d82442f118c27b",
		"../../cmd/server/loop_settings.go":      "0ba113d212c8ec1821641ec5c8f2b9aa2d70cc209c011f6c8735f808ec8558c7",
		"../../cmd/server/main.go":               "dd6abff1eb5807ce791f040d57d3d5939249a34527e8815f03ab2e9863cdc336",
		"../../cmd/server/notification_state.go": "c5e058084e8016be49818e55180bdb3859f9222a1821184f4b81d22ea22eba02",
		"../../cmd/server/personal_data.go":      "b5d62e57389a5eb2f880dbcad11ad17aea5d9a9fda7d57a014b587cbd3792d9c",
		"../../cmd/server/cold_dispatch.go":      "de00d735cbaf6591548901dcd880ffc7c1a0b41de10ea5103fe532675220fb8a",
		// Background comment-action generator: borrows the issue bootstrap
		// projection for its own run; never reaches request authorization.
		"../../cmd/server/comment_action_writes.go":           "5765c0ab53a9346be3631aa48ab3d55c96204256e4ce63bdc5eb31db83983c39",
		"../../cmd/server/general_bootstrap.go":               "8fd49d508ec7e273efff59626b595a0a6cbc54ca52ef8d29d2f56b4103ea29b0",
		"../../cmd/server/issue_bootstrap_stream.go":          "ebed940c8ebed5de341c827652b768ae5c534d850e8bb10b37da929e59731a1b",
		"../../cmd/server/issue_create_targets.go":            "4797fc2981774f90cbd94a0f00a835e5391e323bc28d5e067692b0b426172523",
		"../../cmd/server/issue_record_authorization_read.go": "26389243d1ca95c867f3653a204d841ecc83bfa48a0d477f18895e262b5fe19b",
		"../../cmd/server/issue_update_targets.go":            "0aba87367470607dfcbc0ae7e48ec7c0e8402c7c5020991d85acc2c6af9594ee",
		"../../cmd/server/triage_suggestions.go":              "ce4dc87da131244726aadae31d61b8a7fe9cd5760d05b895013aa8ac88fc2002",
		"../../cmd/server/settings_features.go":               "0e938b5814112b572cba1c0caf54a89df3aed99f61677d45b69194b3a2eb49ae",
		"authz_epoch.go":                                      "6de890ae6a645e416fbbfb479dfcb165c68b78ccd384851c433f03c5a285e583",
		"content_records.go":                                  "b128e7aa3a74c4dcf0167ccbef6f316757c3ddc02645a89a12becee369fcdff2",
		"database.go":                                         "0f8031be40273399b3e770ff44726abdb107483b5d282feaa5d675277c4be0cb",
		"issue_access_metadata.go":                            "e94330023938805faa4db03459377c9807c5bcb467182e4919a756924fd5ae74",
		"metadata_decode.go":                                  "2df3c831f43724f14fbe66f470de959c92d6044a87aa42ff4f2771d8928f8b2e",
		"metadata_delta.go":                                   "366ef37cf6bb72eba0add53e6b73262c9d3c0c25e9cc7694038eb0941ad2c7c7",
		"metadata_copy_revision.go":                           "92d7334ac527c66c79c288b629f9c1d2c16fb4643be0ffb16319a0585d8610fb",
		"metadata_mutation.go":                                "b7589f199a137fbec8cf28d63b4558bc9712e148cd3c67eae2a9984174c21293",
		"metadata_record_scope.go":                            "25885e1106e0b8ee8dbc2b9f2f24d94c0d9764c99dfd644fe5a32165e21647b8",
		"read_snapshot.go":                                    "5485d56062abc1d044da880b2ef6351828732ce1fc99b201fec20905a71fa12a",
		"resource_access.go":                                  "460e374d1cf4a0915d8887aa408d68a6bb79f6ae267c3ebfff2d1d298977de64",
		"record_peer_state.go":                                "aabfdf46597e6f84410c9eb70367cc7d3bde01ac0dcd3510b761d8c7feaa173e",
		"epoch_access.go":                                     "3a90a16ace7b653f6269cef736dd758162ea758d5e754508dc3d3804f771903a",
		"query_window_metadata.go":                            "135ee7327543f7c5ce3400e115d24c570e7abe197670f2b5a58bd6f1495e9763",
		"record_selection.go":                                 "e6f3312be20c5716b6dcf65ec96b540e579261f2cf43469b83f43dc57cde36ee",
		"issue_authorization_read.go":                         "416b8c51c15ebd18a60a4ecf8f0a00975884e304b55e17f08068a8aee56980f8",
		"issue_create_read.go":                                "2f3b94c3036cb5e4f0c138dcef6393eb112a3c3b9a7deb0fbc6c722617ada196",
		"issue_record_authorization_release.go":               "9f812ba7e907bca285aeefa89ff5cbb08825046d045e469336ebb0bd81c2d64d",
		"issue_update_targets.go":                             "6f8158f72b939dadc5a948dbc759a5c59e6243977ec7457ef2b0a21c37e13814",
		"account_read.go":                                     "a3778caaf862c41e4211c7e0685d0a2797219e96227dfc828b769ae110699512",
		"application_access.go":                               "8fe55ce6e2620b35ec763cdd750900a8dba92129941520d9d814d073aee12eab",
		"application_task_context_read.go":                    "1a9a4dfc48a5b207610073519983e3dc24bb1386e78a86a14ccf68c32f96681d",
		"bootstrap_content_walk.go":                           "d35211614268eca617bc2a9c31d69aabe631478532235c20d46045c3ada99e18",
		"bootstrap_issue_walk.go":                             "d3e058f3228cbb9c62d9e3844c25c5b07b29435415c95902c8e44a885ced6c92",
		"bootstrap_projection_read.go":                        "2f3a7f0955fa03c089526e1ad3a04b47c4557ddb762039e93a5fb9fe9fe07687",
		"collaboration_document_read.go":                      "c88b6c53d52611b52141631e0a62c325c7d0154bf1c5165f6bfc4aaff857cd08",
		"collection_snapshot.go":                              "4c56cbee0ff4e8e6cfb40b1b38044d05c907ee1604f71e877b775c46b1cac137",
		"cycle_schedule_write.go":                             "9f0dac8dfa14cd1d9c4607ed155f9fc32fc837b0768198012ac6fb9d60dc54af",
		"delivery_context.go":                                 "bc62a21c7a6819988b4714be44873eed41c31d50f9b93e93668e3ac03f2ae75e",
		"desktop_ack_write.go":                                "caca6a6974572bf3205f7dcf5280b54dd7ad502f75b761967281665259b90682",
		"document_auxiliary_read.go":                          "509970bec9f7e70a18b5054485374e46e441f5a03c31dfb491497ba16b570a6d",
		"document_directory_read.go":                          "8e2485e4fd78d48db3e37412a32ea9d4f54289c790f530786b37be9adcc9573a",
		"document_read.go":                                    "b49acf3d6ec91d63c4f4d4f0200f17fec708289fc55b246594e4d18c3ec53d4b",
		"general_bootstrap_read.go":                           "92b6a27db6eae76b79cd58caddae317009b2384449b310aec03bd52824157570",
		"installed_resource_point_read.go":                    "9ea5e51dc1e426ffe0a3fda73a001acfef726c1b07798498f79fee5ab3b16e9e",
		"issue_authorization_policy.go":                       "54ee99fb352b668bf1449348dc05b8681a6f7b60da63c87eb84dacf103b98b1d",
		"issue_authorization_rows.go":                         "c705222b1316d5919f8df6cb6b9c519fea7fa4224fe855112e940ebf8595d74a",
		"issue_facet_read.go":                                 "de6294df06d5c3554bc70f2475ea0f118f3c59bc54737b6b56786d8bd68a915b",
		"legacy_discussion_write.go":                          "fb168bbffcffd0d3c616ed2aa0a42c50c036bfe58fd272a1d53758aecdd22f1b",
		"legacy_event_policy_read.go":                         "052fd25c7f6a7d55643703c2313fc3d04c79d7dc82ccde0d25f3a3d1e2bc623a",
		"legacy_import_inputs.go":                             "0448187a80aeeb2866c106f8a1cb80dc24def6e660e5a7af4bb2006fc0fbb3d6",
		"native_reads.go":                                     "de2af9f882ae7a7f0aa81e45da6233bcbdf42b7ab475786318576e9e18191c66",
		"notification_visibility.go":                          "23cc2669e291b3a0edf7c8673b0106c559e93b58482ed6c094facd6e9c916ce3",
		"snapshot_read.go":                                    "cb171d1d1921793a1fddbd9a648e9ffd53cb622d8d1194ac63f8316ad9e9bdb1",
		"project_directory_query.go":                          "0e7707a6b304cc5ee654c4fd86a99149a9495dd3b810e4b62b66b31307d8149c",
		"read_leaf_rows.go":                                   "45c2d9bd63bd19145b8c48771aad80470efa4d75d051449d4b4644a09b50e05f",
		"resource_authorization_read.go":                      "6a82512ebc0e8087b1de53b815cfeaf05e1c2fe4b1a98e26c67f198eaab07ef4",
		"resource_directory_read.go":                          "e4b167be4130adfb9bd0e6a022afc3fc1eb0ba49d343a1307545aee31bdf5032",
		"resource_policy_memo.go":                             "5f9a39859cf52e0dce38653aed5edbb556df6ec5c3dce282b2c391df2328a67c",
		"resource_read.go":                                    "e1a69d63861b5852e3ea335c82cf31feab6f3f72e24a1277f3c295bd17534c49",
		"former_members.go":                                   "793dc57c8beb3c01ebde0a809de973ecfa90e6ddec75ac8a2dc4a633b64534d8",
		"sync_requests.go":                                    "9457a75a494d3015cb9366cc7de07e492e59173210d372c056bd98b9dbb6b201",
		"team_resource_read.go":                               "73b9f8987cbfc1df8db7613cdffa7f828fb087b4e16dd6a11af2615086514d08",
		"team_member_request.go":                              "131cbc4c25f0dc1a5c3d222f9427e21fb350efcf62880559a6310af2e11fc7ac",
		"triage_suggestion_read.go":                           "83681e09dfbf29ea6c4888607bac7d7e97a37605bd1f7ef904f1f6941e65477f",
		// Issue deletion borrows its own write transaction for policy and issue headers.
		"bounded_issue_lifecycle.go": "a61882da0ccd093f8e0ddc17ffc395bd845e1663a273bb28991385349d4a33b3",
		// Attachment visibility binds its owned raw projection only inside its
		// own Project call; the context never reaches request authorization.
		"attachment_visibility_read.go": "d6ae0a5f2e25635ae7a834a18e27eccb2b23b1ef31848d7cb47c376418729221",
		"issue_read_source.go":          "434b6412f3e6546786feba97a97e5dd5c9c1a726f579949b03bf9d78bf0f9286",
		// Search binds its local SQL reader for bounded candidates and catalog coverage.
		"search_metadata.go": "8e47c2d662d818e441cdf74f780affd7313aaa757c5790aaabb622c4f8a84025",
		// Cold-request route: the cold store stays inside its callback.
		"../../cmd/server/oauth_authorized_applications.go": "8a0e9bec2c8ea317de40e3e4872b8587a6930369391d9e70fa7b895c38c94e9a",
		// The issue read owns a SQL transaction and the policy it uses.
		"issue_read_snapshot.go": "13cae7fbe50f86fd6f36302adfeb909a0277ea3af6a0b03c3330b974d27c3414",
		// Borrows policy from the same database and workspace, then returns
		// an owned copy of the viewer and team settings required by the route.
		"legacy_comment_action_read.go": "894774ef15c193373601031cd2b5aa670334b39d0bbec90754a9c91a3c722372",
		// Borrows one issue write's policy inputs from the same database and
		// workspace and returns an owned copy of only the named records.
		"legacy_issue_write_authorization.go": "d41b19518097270d1a0e4dd7adcbcd23afb033ebbffc203843d2860147f88223",
	}
	want := map[string]int{
		"authz_epoch.go:AuthzEpoch:clear":                                   1,
		"bounded_issue_lifecycle.go:checkAccess:metadata":                   1,
		"issue_read_snapshot.go:BeginIssueReadSnapshot:metadata":            1,
		"issue_read_snapshot.go:BeginIssueReadSnapshot:sql":                 1,
		"read_snapshot.go:BeginWorkspaceRead:metadata":                      2,
		"resource_access.go:ResolveAccess:sql":                              1,
		"epoch_access.go:LiveEventContext:clear":                            1,
		"query_window_metadata.go:scaleMetadataAt:metadata":                 1,
		"account_read.go:BeginAccountRead:clear":                            1,
		"application_access.go:BeginApplicationListRead:sql":                1,
		"attachment_visibility_read.go:Project:metadata":                    1,
		"bootstrap_projection_read.go:BeginIssueBootstrapRead:metadata":     2,
		"collaboration_document_read.go:beginCollaborationDocumentRead:sql": 1,
		"collection_snapshot.go:metadataCollectionsInSnapshot:sql":          1,
		"document_auxiliary_read.go:BeginDocumentAuxiliaryRead:sql":         1,
		"document_directory_read.go:BeginDocumentDirectoryRead:sql":         1,
		"document_read.go:BeginDocumentRead:sql":                            1,
		"general_bootstrap_read.go:Context:clear":                           1,
		"general_bootstrap_read.go:Context:metadata":                        1,
		"issue_authorization_policy.go:loadPolicy:sql":                      1,
		"issue_authorization_rows.go:rowSnapshot:sql":                       1,
		"issue_facet_read.go:BeginIssueFacetRead:clear":                     1,
		"legacy_event_policy_read.go:readLegacyEventPolicyRecords:sql":      1,
		"native_reads.go:readResidentResourcePolicy:sql":                    1,
		"notification_visibility.go:BeginNotificationVisibilityRead:clear":  1,
		"notification_visibility.go:BeginNotificationVisibilityRead:sql":    1,
		"snapshot_read.go:snapshotAt:metadata":                              1,
		"read_leaf_rows.go:readLeafBegin:clear":                             1,
		"resource_authorization_read.go:BeginResourceAuthorizationRead:sql": 1,
		"resource_directory_read.go:BeginResourceDirectoryRead:sql":         1,
		"resource_read.go:BeginResourceRead:sql":                            1,
		"sync_requests.go:visitRequest:sql":                                 1,
		"triage_suggestion_read.go:ViewerProjectionScope:metadata":          1,
		"triage_suggestion_read.go:projectedWorkspace:metadata":             1,
		"search_metadata.go:beginMetadataSearch:sql":                        1,
	}
	var files []string
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir("../..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			filename, err := filepath.Rel(root, filepath.Join(root, path))
			if err != nil {
				return err
			}
			files = append(files, filename)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	seen := map[string]bool{}
	closedNames := map[string]bool{
		"workspaceRead": true, "workspaceReadKey": true,
		"BeginWorkspaceRead": true, "BeginGeneralBootstrapRead": true,
		"GeneralBootstrapRead": true, "BeginIssueBootstrapRead": true,
		"ViewerProjectionScope": true, "HydrateWorkspaceMetadata": true,
		"withColdMetadataRequest": true, "projectedWorkspace": true,
		"scaleMetadataAt": true, "snapshotAt": true,
		"snapshotFields": true, "projectedWorkspaceCollections": true,
	}
	for _, filename := range files {
		if strings.HasSuffix(filename, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), filename, raw, 0)
		if err != nil {
			t.Fatal(err)
		}
		usesCarrier := false
		for _, declaration := range parsed.Decls {
			owner := "<package>"
			if function, ok := declaration.(*ast.FuncDecl); ok {
				owner = function.Name.Name
			}
			methods := map[*ast.SelectorExpr]bool{}
			ast.Inspect(declaration, func(node ast.Node) bool {
				if call, ok := node.(*ast.CallExpr); ok {
					if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
						methods[selector] = true
					}
				}
				return true
			})
			ast.Inspect(declaration, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.Ident:
					if closedNames[node.Name] {
						usesCarrier = true
					}
				case *ast.SelectorExpr:
					if filepath.Dir(filename) == "." && node.Sel.Name == "references" && !methods[node] {
						usesCarrier = true
					}
				case *ast.CompositeLit:
					name, ok := node.Type.(*ast.Ident)
					if !ok || name.Name != "workspaceRead" {
						break
					}
					class := "sql"
					if len(node.Elts) == 0 {
						class = "clear"
					} else if _, keyed := node.Elts[0].(*ast.KeyValueExpr); !keyed {
						if len(node.Elts) != 4 {
							t.Fatal("unclassified positional workspaceRead", filename, owner)
						}
						class = "metadata"
					} else {
						for _, element := range node.Elts {
							key, ok := element.(*ast.KeyValueExpr)
							if !ok {
								t.Fatal("mixed workspaceRead literal", filename, owner)
							}
							if name, ok := key.Key.(*ast.Ident); ok && name.Name == "metadata" {
								class = "metadata"
							}
						}
					}
					got[filename+":"+owner+":"+class]++
				}
				return true
			})
		}
		expected, closed := contracts[filename]
		if usesCarrier && !closed {
			t.Fatal("new unclassified snapshot use, alias, capture or references write", filename)
		}
		if closed {
			seen[filename] = true
			if actual := fmt.Sprintf("%x", sha256.Sum256(raw)); actual != expected {
				t.Fatal("production source contract changed; re-audit origins, scope filters and carriers", filename, actual)
			}
		}
	}
	if len(seen) != len(contracts) || !reflect.DeepEqual(got, want) {
		t.Fatal("closed source inventory changed", seen, got, want)
	}
}

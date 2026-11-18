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
func TestScaleP21SW318ProductionBorrowedIssueSources(t *testing.T) {
	contracts := map[string]string{
		// SQL-reader borrower only: it never binds or edits snapshot metadata.
		"scale_p21_workflow_run_write.go":             "d596f61a88cadfb708ac4ad416fc0c148a661e8489505ec6d4e45494b074954c",
		"issue_records.go":                            "67a5f108eda6922d5a9f025ca348592de92001689c7df1894c29fed20ef4bdd6",
		"issue_query.go":                              "654052be1f830fad113a7182cca9fa4bbbcb5d8959acbd0b6946bb9ad5366dd1",
		"auth.go":                                     "0f21e50043e919de924eee8d5eb636fc5fe554717e0e86da2c79183e2c887a59",
		"team_default_favorites.go":                   "8ba62b09173955d3f412bba9820ba6292e3ee06fec852571c691e5a62e49e657",
		"scale_p15_snapshot_buffers.go":               "850c692283182c9cfbde136b622dd27e068dc329a764939faeb0acadb15ee1b8",
		"snapshot_clone.go":                           "eed939fd6798a8ade89c9f80a12040f1f5e285dc212e521ad2c38e04fcbc6063",
		"../../cmd/server/advanced_features.go":       "7fd3ec05679971fb868cd9afc7b87653a08c0e593be23ac5f94c8c66d3a5dd4b",
		"../../cmd/server/loop_settings.go":           "740fef6080e4e84f17b8c69a0bd5167ce357ce7287485aa1cf0cba4f7e4751a0",
		"../../cmd/server/main.go":                    "0f1d8581d69aac275dbc1cbbde4cf29c6059b5df63286b94c06a3e97efab858a",
		"../../cmd/server/notification_state.go":      "5827fa9e2184d4c4726e3724a2a4e6d7ce54a956877d58694249a6c2f1cb3f98",
		"../../cmd/server/personal_data.go":           "5536c0e2c75b43c48a35ad3fde7f3d906552296c3f309001a8dd5d806fc1ab22",
		"../../cmd/server/scale_p21_cold_dispatch.go": "5f2d22233b9e5cbfa070487488056b9da19168580b00f3f488dfa6df5c30f36b",
		// Background comment-action generator: borrows the issue bootstrap
		// projection for its own run; never reaches request authorization.
		"../../cmd/server/scale_p21_comment_action_writes.go":           "5765c0ab53a9346be3631aa48ab3d55c96204256e4ce63bdc5eb31db83983c39",
		"../../cmd/server/scale_p21_general_bootstrap.go":               "0ef57c05d1a3a3bae01fd62c081248fd6d4426250693d0a54819ed1b60575227",
		"../../cmd/server/scale_p21_issue_bootstrap_stream.go":          "11ea89bcfbd5c2de9cc7192f37de3b5950a90483be4c97a92f19966c5747c8a2",
		"../../cmd/server/scale_p21_issue_create_targets.go":            "4797fc2981774f90cbd94a0f00a835e5391e323bc28d5e067692b0b426172523",
		"../../cmd/server/scale_p21_issue_record_authorization_read.go": "6026fba608d49f31c47ac07de86b8ea1f000f94b93a7f0758e0ee964303025d4",
		"../../cmd/server/scale_p21_issue_update_targets.go":            "58665b35304f51e087403d1abba92894e319e4f5900039ee17de0a86902745c3",
		"../../cmd/server/scale_p21_triage_suggestions.go":              "48c34cdd8e37d8c4be1395a7b3a2bc8aa4dbfa3dcd827149fa262cccc8257eca",
		"../../cmd/server/settings_features.go":                         "096969fb834370820a32521e53e4bd6460c18807ab48adee5294f618d493d8eb",
		"authz_epoch.go":                                                "ce59afa33d8daca93d0901d13d86a0f7bb5f140cc479efc38395ab568173e9c4",
		"content_records.go":                                            "711cc4e036099177c87ed23e50f56c81703b4aa7af2c3123053cc3873cfaccba",
		"database.go":                                                   "6a388269b4db65f6be0ee1a1f3f7f9aee8f1fe8cadc95d4419ac8cb5778f9912",
		"issue_access_metadata.go":                                      "2463acc37c961816b884c1f4f47dbf70a989912b58550985af97cef38bc21814",
		"metadata_decode.go":                                            "ce3352e177e4b1ed19c6822a04ddc9d1b715fa81d83d4905d1041c1a4ce2fc45",
		"metadata_delta.go":                                             "3040505526a7cf5ff20168733974ce927d98dfe8183498f708ddc46935b237c5",
		"metadata_copy_revision.go":                                     "95075b606ae15685919d8b9ee5630607366082c7d93c85d2412bbaf12b5593b9",
		"metadata_mutation.go":                                          "b7589f199a137fbec8cf28d63b4558bc9712e148cd3c67eae2a9984174c21293",
		"metadata_record_scope.go":                                      "0f02418f3ec0efe4cccc28a7eaf3cf768629493d407c6c2f7b8c80331ce163c1",
		"read_snapshot.go":                                              "c33d3115d40dfd81a31e17c09c540b18ec08c6c47a6c9119f9fd8563232f7044",
		"resource_access.go":                                            "0141d03e3fe562de4bba76926669e55e9f1c3f1bec180f0fd383ba8038c69fa4",
		"scale_p11_peer_state.go":                                       "b22b22ea1fe4fb06bf959b16e81cc6ab4547e6aa04223158ffb5799225be0b02",
		"scale_p12_epoch_access.go":                                     "3a90a16ace7b653f6269cef736dd758162ea758d5e754508dc3d3804f771903a",
		"scale_p19_metadata.go":                                         "26428cdb6628c0e12fcbc9ef507519b6013d10720b1d920f660e71b63ac0ca73",
		"scale_p17_record_selection.go":                                 "6569e3416267eaebeee483c6418859b303ea3a7c6928eed5ffa0de871f6ad287",
		"scale_p21_issue_authorization_read.go":                         "e97d79bd6680ccf399ffd0a01037c7031924599ba24b906f0e6371799199c23f",
		"scale_p21_issue_create_read.go":                                "12f0c7e7be8840acdbd5f840c6376819f7ab8de722d07072b0c9f1efbca8b3a7",
		"scale_p21_issue_record_authorization_release.go":               "9f812ba7e907bca285aeefa89ff5cbb08825046d045e469336ebb0bd81c2d64d",
		"scale_p21_issue_update_targets.go":                             "6f8158f72b939dadc5a948dbc759a5c59e6243977ec7457ef2b0a21c37e13814",
		"scale_p21_account_read.go":                                     "f41c38c683238abbf29aecc854d04d58bb1ed6f1fccd590f01050d5306609cbf",
		"scale_p21_application_access.go":                               "4a8ebfed2bf92d5a8ec543ccb8f9bc68538666ee1f8eea4aebd9099087f1cfbe",
		"scale_p21_application_task_context_read.go":                    "71df6cdfc0fe46f2d149fb90d125eea56515b10c711633e4dab1f464f555f8da",
		"scale_p21_bootstrap_content_walk.go":                           "b795207a7c8db00fb91479dcb795a6112f6678a5b9213216eac1254936f5ccd0",
		"scale_p21_bootstrap_issue_walk.go":                             "12f6b78490e7b638a4f9190763fc8988c2202a2f733b025fd31863e489a93a3c",
		"scale_p21_bootstrap_projection_read.go":                        "855a7297d42689b586b5ce71cede55451ff25bc00d1efeb3f5504839c8c1c2ff",
		"scale_p21_collaboration_document_read.go":                      "87244ad17d8c28ed1329f7ec4963ef0c61cb018941cd838060a45a49e823d5dc",
		"scale_p21_collection_snapshot.go":                              "a24d9e3045ae541a50d7f4b4e7de86eb3e4f2b09b30b98704e19c731b619394f",
		"scale_p21_cycle_schedule_write.go":                             "2620140f60327e0f06beaaaa280ff89b887109e4fb925ff455a529e16eb9b407",
		"scale_p21_delivery_context.go":                                 "53b0754c98b8e3473f4658777aaa0594f9b5caa07ca28f7c3599879332f9c609",
		"scale_p21_desktop_ack_write.go":                                "caca6a6974572bf3205f7dcf5280b54dd7ad502f75b761967281665259b90682",
		"scale_p21_document_auxiliary_read.go":                          "509970bec9f7e70a18b5054485374e46e441f5a03c31dfb491497ba16b570a6d",
		"scale_p21_document_directory_read.go":                          "8f82ff264a997bfdd6dc71f5c3992856f5df4ece322ea248ff29f60f2e2c3247",
		"scale_p21_document_read.go":                                    "4748e956ffcde0adee04ea8af5b077a60d47769953ebe8a7f9ac17f904ad54d1",
		"scale_p21_general_bootstrap_read.go":                           "d7a45918ea4ec4fdfad5bab2614ea5135cd2dbdc50db14db27c47eb873adb32d",
		"scale_p21_installed_resource_point_read.go":                    "9ea5e51dc1e426ffe0a3fda73a001acfef726c1b07798498f79fee5ab3b16e9e",
		"scale_p21_issue_authorization_policy.go":                       "3b1db13512c208deb9b7c7c650e50cb15e926835c5151fc2e841d85313416d21",
		"scale_p21_issue_authorization_rows.go":                         "0b5289e704257da9833d2e335efe53dacafc5cf2ee0b071cb65ed994b3b1d564",
		"scale_p21_issue_facet_read.go":                                 "61147a9c92d8306b9d4dcdcb6c32272d0bb202c1b64b71a0c8de71792b302069",
		"scale_p21_legacy_discussion_write.go":                          "8b73c270fcbbca08db33629ab8d9788ea6afc801c586f5efe2d593e4529a2c92",
		"scale_p21_legacy_event_policy_read.go":                         "974033b2bc4b12a943f916bbf087a927191a26c694c2edb61b17fc7fa1a86c7b",
		"scale_p21_legacy_import_inputs.go":                             "f5435bd286f1ba1547f54e791fe3b5204b1f28740dd29ec2fad585881c7d10e6",
		"scale_p21_native_reads.go":                                     "f9f002b8b707a39498b93a2032dcd3833e13e3822bd89acdf080cc1142d40b25",
		"scale_p21_notification_visibility.go":                          "eec82c274b62f83de7935d73474b72e72e32e6c0c383747bea2049d91de9e35b",
		"scale_p21_p19_snapshot_read.go":                                "2f9c6dc3381c9110759e0f356fedc23e39e3ed1e41d91cfb1f5d1a3bcc3a5d0a",
		"scale_p21_project_directory_query.go":                          "a7d33c1d062035abe237b4180d0edd386bde44d25dc364f874f96714e676f079",
		"scale_p21_read_leaf_rows.go":                                   "45c2d9bd63bd19145b8c48771aad80470efa4d75d051449d4b4644a09b50e05f",
		"scale_p21_resource_authorization_read.go":                      "bc2c4d2660da4d31c412c83bf0b36aacb958cc517b25115af22fe73ed125564a",
		"scale_p21_resource_directory_read.go":                          "525958c0a677927a6045fad929752239bfe97d092aa538d5e0f4f172e21a93d6",
		"scale_p21_resource_policy_memo.go":                             "f4134522db037aadc7cc519e1ceb6f64eb65db899b616681eaeb4257886601e3",
		"scale_p21_resource_read.go":                                    "adde7390708efe7ef7a3dd0b8bc32c653edec8f65c6532d5ae9e7551cea93b4d",
		"scale_p21_sw16_former_members.go":                              "b9cc69d0ce8e48f062ab38f6bf9d617baaa2c47aa9ff89dc1f9bc78f72f5e716",
		"scale_p21_sw21_sync_requests.go":                               "36c3712102a31950ece9e7cf9f5c4a5b76b39ee0ba5a82b7e61d67d7779223a8",
		"scale_p21_sw307_team_resource_read.go":                         "e91a2ec9e65996dc2e2ca07c7ebfb8e618bf3274c29679cd0fda488f6bb675c8",
		"scale_p21_team_member_request.go":                              "06ccd498dd995b497acc1c794c972e3974db28dec2e4a8f03ec469d3db3894ba",
		"scale_p21_triage_suggestion_read.go":                           "83681e09dfbf29ea6c4888607bac7d7e97a37605bd1f7ef904f1f6941e65477f",
		// Attachment visibility binds its owned raw projection only inside its
		// own Project call; the context never reaches request authorization.
		"scale_p21_attachment_visibility_read.go": "d6ae0a5f2e25635ae7a834a18e27eccb2b23b1ef31848d7cb47c376418729221",
		"scale_p21_sw318_issue_read_source.go":    "434b6412f3e6546786feba97a97e5dd5c9c1a726f579949b03bf9d78bf0f9286",
	}
	want := map[string]int{
		"authz_epoch.go:AuthzEpoch:clear":                                             1,
		"read_snapshot.go:BeginWorkspaceRead:metadata":                                2,
		"resource_access.go:ResolveAccess:sql":                                        1,
		"scale_p12_epoch_access.go:LiveEventContext:clear":                            1,
		"scale_p19_metadata.go:scaleP19MetadataAt:metadata":                           1,
		"scale_p21_account_read.go:BeginAccountRead:clear":                            1,
		"scale_p21_application_access.go:BeginApplicationListRead:sql":                1,
		"scale_p21_attachment_visibility_read.go:Project:metadata":                    1,
		"scale_p21_bootstrap_projection_read.go:BeginIssueBootstrapRead:metadata":     2,
		"scale_p21_collaboration_document_read.go:BeginCollaborationDocumentRead:sql": 1,
		"scale_p21_collection_snapshot.go:metadataCollectionsInSnapshot:sql":          1,
		"scale_p21_document_auxiliary_read.go:BeginDocumentAuxiliaryRead:sql":         1,
		"scale_p21_document_directory_read.go:BeginDocumentDirectoryRead:sql":         1,
		"scale_p21_document_read.go:BeginDocumentRead:sql":                            1,
		"scale_p21_general_bootstrap_read.go:Context:clear":                           1,
		"scale_p21_general_bootstrap_read.go:Context:metadata":                        1,
		"scale_p21_issue_authorization_policy.go:loadPolicy:sql":                      1,
		"scale_p21_issue_authorization_rows.go:rowSnapshot:sql":                       1,
		"scale_p21_issue_facet_read.go:BeginIssueFacetRead:clear":                     1,
		"scale_p21_legacy_event_policy_read.go:readLegacyEventPolicyRecords:sql":      1,
		"scale_p21_native_reads.go:readResidentResourcePolicy:sql":                    1,
		"scale_p21_notification_visibility.go:BeginNotificationVisibilityRead:clear":  1,
		"scale_p21_notification_visibility.go:BeginNotificationVisibilityRead:sql":    1,
		"scale_p21_p19_snapshot_read.go:p19SnapshotAt:metadata":                       1,
		"scale_p21_read_leaf_rows.go:readLeafBegin:clear":                             1,
		"scale_p21_resource_authorization_read.go:BeginResourceAuthorizationRead:sql": 1,
		"scale_p21_resource_directory_read.go:BeginResourceDirectoryRead:sql":         1,
		"scale_p21_resource_read.go:BeginResourceRead:sql":                            1,
		"scale_p21_sw21_sync_requests.go:scaleP21SW21VisitRequest:sql":                1,
		"scale_p21_triage_suggestion_read.go:ViewerProjectionScope:metadata":          1,
		"scale_p21_triage_suggestion_read.go:projectedWorkspace:metadata":             1,
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
		"scaleP19MetadataAt": true, "p19SnapshotAt": true,
		"p19SnapshotFields": true, "projectedWorkspaceCollections": true,
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

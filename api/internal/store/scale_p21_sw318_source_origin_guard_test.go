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
		"scale_p21_workflow_run_write.go": "82b916096b9c5e7493da777bc8741a7b59d7e7fd94e8d2f21f217f5966519d71",
		"issue_records.go":                "f5e1cae5c796fd09a5bc7ebd4d7c5b541cf064e2a554cbf6a3cf814b684decd2",
		"issue_query.go":                  "fbbe42adaed6c5920253704649a753d16b9db97af707446d74f7f19251ced6b2",
		// The result cache only checks for a borrowed snapshot to bypass Redis.
		"shared_result_cache.go":                      "a8b3776efb34fe28b10957fe5e9e29f064bf2965809262760d29bfa07351afb3",
		"auth.go":                                     "d6c579edbe9c4874f740795cf8b95b106cbf5bba5d34483da2d9eb6a9097b2cc",
		"team_default_favorites.go":                   "8ba62b09173955d3f412bba9820ba6292e3ee06fec852571c691e5a62e49e657",
		"scale_p15_snapshot_buffers.go":               "55e0d8d461ffd547d4d4fd9329cf9fda23259a0fef08da7e6ba42a0d51c88f7f",
		"snapshot_clone.go":                           "eed939fd6798a8ade89c9f80a12040f1f5e285dc212e521ad2c38e04fcbc6063",
		"../../cmd/server/advanced_features.go":       "a59c6771f95ebb29ed6187a194cf472e1e19a6919427ebf06d6744e187e0f8ba",
		"../../cmd/server/loop_settings.go":           "0ba113d212c8ec1821641ec5c8f2b9aa2d70cc209c011f6c8735f808ec8558c7",
		"../../cmd/server/main.go":                    "cfa59b7bd2aff3be036d1ea6095bd618c48e5c2321971f84eb140b0871acc1b7",
		"../../cmd/server/notification_state.go":      "5827fa9e2184d4c4726e3724a2a4e6d7ce54a956877d58694249a6c2f1cb3f98",
		"../../cmd/server/personal_data.go":           "df7f342d4fdd9a5908cfbaf6cceddd89e8be2be0ff5dbe23e94e9643e9cb26a3",
		"../../cmd/server/scale_p21_cold_dispatch.go": "78f6979c7b6492f603c4b1a452786f590b800139ea08d371bc3bc8054ba3e597",
		// Background comment-action generator: borrows the issue bootstrap
		// projection for its own run; never reaches request authorization.
		"../../cmd/server/scale_p21_comment_action_writes.go":           "5765c0ab53a9346be3631aa48ab3d55c96204256e4ce63bdc5eb31db83983c39",
		"../../cmd/server/scale_p21_general_bootstrap.go":               "4a4d2c67bc03934b0ab5306a249648e2101f2e22e5eab4bb48f920ac329bf94d",
		"../../cmd/server/scale_p21_issue_bootstrap_stream.go":          "d51ae67d4011bf1fe2c515c6c852fa15ac54832144895bde9dfffca673bbd2bb",
		"../../cmd/server/scale_p21_issue_create_targets.go":            "4797fc2981774f90cbd94a0f00a835e5391e323bc28d5e067692b0b426172523",
		"../../cmd/server/scale_p21_issue_record_authorization_read.go": "6026fba608d49f31c47ac07de86b8ea1f000f94b93a7f0758e0ee964303025d4",
		"../../cmd/server/scale_p21_issue_update_targets.go":            "58665b35304f51e087403d1abba92894e319e4f5900039ee17de0a86902745c3",
		"../../cmd/server/scale_p21_triage_suggestions.go":              "ce4dc87da131244726aadae31d61b8a7fe9cd5760d05b895013aa8ac88fc2002",
		"../../cmd/server/settings_features.go":                         "b7c9afbccfb1d370622fa4da34e4059e7ab36519aed716f5cb301fa96c73fe42",
		"authz_epoch.go":                                                "ce59afa33d8daca93d0901d13d86a0f7bb5f140cc479efc38395ab568173e9c4",
		"content_records.go":                                            "724515af9a4bda3f391d89a05154220b5236aca1c8a7a1b4f21cb4d686e8cb0a",
		"database.go":                                                   "5e2c1e00725c8be012939fb6df932d23f47cc4597924e96de80f50714636c181",
		"issue_access_metadata.go":                                      "e94330023938805faa4db03459377c9807c5bcb467182e4919a756924fd5ae74",
		"metadata_decode.go":                                            "ce3352e177e4b1ed19c6822a04ddc9d1b715fa81d83d4905d1041c1a4ce2fc45",
		"metadata_delta.go":                                             "a79e58b9c65fd49c020acf27493596a9ce1546378d3a4ffa5548ccc9e81b7b0b",
		"metadata_copy_revision.go":                                     "6c2e070724a62ec02b6f9779171a30ae880d22e35de6073fce4bdb7f1dd1dd7b",
		"metadata_mutation.go":                                          "b7589f199a137fbec8cf28d63b4558bc9712e148cd3c67eae2a9984174c21293",
		"metadata_record_scope.go":                                      "0f02418f3ec0efe4cccc28a7eaf3cf768629493d407c6c2f7b8c80331ce163c1",
		"read_snapshot.go":                                              "cc14903369aeacba0b8d30fe19573a6f5924790d2a235ed2c71b57bc654756b6",
		"resource_access.go":                                            "0141d03e3fe562de4bba76926669e55e9f1c3f1bec180f0fd383ba8038c69fa4",
		"scale_p11_peer_state.go":                                       "1b30e92c1d14d8fc973f527a2bb97ce4b574626614403dfbe06da8174be0bcd9",
		"scale_p12_epoch_access.go":                                     "3a90a16ace7b653f6269cef736dd758162ea758d5e754508dc3d3804f771903a",
		"scale_p19_metadata.go":                                         "0d847695e8a6dae32226bd778b04736af3e5759416b9ca6e43f2b9cd847aa411",
		"scale_p17_record_selection.go":                                 "6569e3416267eaebeee483c6418859b303ea3a7c6928eed5ffa0de871f6ad287",
		"scale_p21_issue_authorization_read.go":                         "e97d79bd6680ccf399ffd0a01037c7031924599ba24b906f0e6371799199c23f",
		"scale_p21_issue_create_read.go":                                "2f3b94c3036cb5e4f0c138dcef6393eb112a3c3b9a7deb0fbc6c722617ada196",
		"scale_p21_issue_record_authorization_release.go":               "9f812ba7e907bca285aeefa89ff5cbb08825046d045e469336ebb0bd81c2d64d",
		"scale_p21_issue_update_targets.go":                             "6f8158f72b939dadc5a948dbc759a5c59e6243977ec7457ef2b0a21c37e13814",
		"scale_p21_account_read.go":                                     "764509ac1287b4f3c340e6f0a3cb0082d6cb8b66422b14582217c52324d26c59",
		"scale_p21_application_access.go":                               "373a4bb17dd58d702b0d52482e8cc3896efd379d3875df8aa5bcc13d639b9ab8",
		"scale_p21_application_task_context_read.go":                    "71df6cdfc0fe46f2d149fb90d125eea56515b10c711633e4dab1f464f555f8da",
		"scale_p21_bootstrap_content_walk.go":                           "b795207a7c8db00fb91479dcb795a6112f6678a5b9213216eac1254936f5ccd0",
		"scale_p21_bootstrap_issue_walk.go":                             "12f6b78490e7b638a4f9190763fc8988c2202a2f733b025fd31863e489a93a3c",
		"scale_p21_bootstrap_projection_read.go":                        "27a82fd14f7ba9244cbe70fe286bfd1fc5b4de3f2d6d711cbb56df078912ee5b",
		"scale_p21_collaboration_document_read.go":                      "d4839174c25808b899839955839d6cd2d9e4a74b5ac76d1158c8a0fb1fb8fae8",
		"scale_p21_collection_snapshot.go":                              "e8d843ee5626ba45c1aa11009ddf1e6860289895367d581fd8552309dbaec1fa",
		"scale_p21_cycle_schedule_write.go":                             "a4759060eb139f2920cef48525526f84b6753833fc57d1720908cedf0c0b9c08",
		"scale_p21_delivery_context.go":                                 "89590bfc452c82e405c4ddd0727946bf6274794ce0c819576ed8da1d0271792a",
		"scale_p21_desktop_ack_write.go":                                "caca6a6974572bf3205f7dcf5280b54dd7ad502f75b761967281665259b90682",
		"scale_p21_document_auxiliary_read.go":                          "509970bec9f7e70a18b5054485374e46e441f5a03c31dfb491497ba16b570a6d",
		"scale_p21_document_directory_read.go":                          "8f82ff264a997bfdd6dc71f5c3992856f5df4ece322ea248ff29f60f2e2c3247",
		"scale_p21_document_read.go":                                    "dbb5f22aa06b5d6cb2d90ca0646fb4e4dceea1df2e96acf54724cdf9038d5a9f",
		"scale_p21_general_bootstrap_read.go":                           "d7a45918ea4ec4fdfad5bab2614ea5135cd2dbdc50db14db27c47eb873adb32d",
		"scale_p21_installed_resource_point_read.go":                    "9ea5e51dc1e426ffe0a3fda73a001acfef726c1b07798498f79fee5ab3b16e9e",
		"scale_p21_issue_authorization_policy.go":                       "3b1db13512c208deb9b7c7c650e50cb15e926835c5151fc2e841d85313416d21",
		"scale_p21_issue_authorization_rows.go":                         "0b5289e704257da9833d2e335efe53dacafc5cf2ee0b071cb65ed994b3b1d564",
		"scale_p21_issue_facet_read.go":                                 "61147a9c92d8306b9d4dcdcb6c32272d0bb202c1b64b71a0c8de71792b302069",
		"scale_p21_legacy_discussion_write.go":                          "8b73c270fcbbca08db33629ab8d9788ea6afc801c586f5efe2d593e4529a2c92",
		"scale_p21_legacy_event_policy_read.go":                         "974033b2bc4b12a943f916bbf087a927191a26c694c2edb61b17fc7fa1a86c7b",
		"scale_p21_legacy_import_inputs.go":                             "f5435bd286f1ba1547f54e791fe3b5204b1f28740dd29ec2fad585881c7d10e6",
		"scale_p21_native_reads.go":                                     "f9f002b8b707a39498b93a2032dcd3833e13e3822bd89acdf080cc1142d40b25",
		"scale_p21_notification_visibility.go":                          "e77a0497022fd22973aeb14e63dd9f85cbe3d42d534ee5602bb3174e6a7d820c",
		"scale_p21_p19_snapshot_read.go":                                "d8d326e1fa687b9d3adfe5de6e63351a399e6ee58172b45fe775ed58e15a9160",
		"scale_p21_project_directory_query.go":                          "a7d33c1d062035abe237b4180d0edd386bde44d25dc364f874f96714e676f079",
		"scale_p21_read_leaf_rows.go":                                   "45c2d9bd63bd19145b8c48771aad80470efa4d75d051449d4b4644a09b50e05f",
		"scale_p21_resource_authorization_read.go":                      "21963760ddcf2bebadff8433da6b321b14e2771f959100eda1ddb7ddedf2d699",
		"scale_p21_resource_directory_read.go":                          "5401a9c8b2a1f342f0c9d75c03b1f67cc1e0b6a5c5c9f84e67ae714aa8f059be",
		"scale_p21_resource_policy_memo.go":                             "f4134522db037aadc7cc519e1ceb6f64eb65db899b616681eaeb4257886601e3",
		"scale_p21_resource_read.go":                                    "5ca09910f1c137dfd89587667b24c63996fb802783b8e5e6a6f50a0b6a7e9d5c",
		"scale_p21_sw16_former_members.go":                              "b9cc69d0ce8e48f062ab38f6bf9d617baaa2c47aa9ff89dc1f9bc78f72f5e716",
		"scale_p21_sw21_sync_requests.go":                               "813f93f5de089ba2a7ebdc0985ee9d434adf1494fd49068afda17fad6d1c8295",
		"scale_p21_sw307_team_resource_read.go":                         "e91a2ec9e65996dc2e2ca07c7ebfb8e618bf3274c29679cd0fda488f6bb675c8",
		"scale_p21_team_member_request.go":                              "0360800099462666455efdb0d7b09147467ad20fa5d2dce88a6d20aee0e25c5d",
		"scale_p21_triage_suggestion_read.go":                           "83681e09dfbf29ea6c4888607bac7d7e97a37605bd1f7ef904f1f6941e65477f",
		// Attachment visibility binds its owned raw projection only inside its
		// own Project call; the context never reaches request authorization.
		"scale_p21_attachment_visibility_read.go": "d6ae0a5f2e25635ae7a834a18e27eccb2b23b1ef31848d7cb47c376418729221",
		"scale_p21_sw318_issue_read_source.go":    "434b6412f3e6546786feba97a97e5dd5c9c1a726f579949b03bf9d78bf0f9286",
		// Search binds its local SQL reader for bounded candidates and catalog coverage.
		"search_metadata.go": "2035dcdbf551533763079ee658e905e1e44a58bfb8b3dd3e0e6e1f72faf22f17",
		// Cold-request route: the cold store stays inside its callback.
		"../../cmd/server/oauth_authorized_applications.go": "8a0e9bec2c8ea317de40e3e4872b8587a6930369391d9e70fa7b895c38c94e9a",
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
		"scale_p21_collaboration_document_read.go:beginCollaborationDocumentRead:sql": 1,
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
		"search_metadata.go:beginMetadataSearch:sql":                                  1,
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

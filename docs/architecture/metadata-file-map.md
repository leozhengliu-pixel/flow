# Metadata file map

This is the wave 2 rename proposal for the scale-phase Go files. Schedule it
after the parallel performance and rollout branches have merged. The proposed
domains are filename and ownership groups within the existing Go packages.
Keeping package boundaries in this pass preserves access to package-private
storage types and lets the file moves be reviewed independently.

## Guard updates

| Guard or data | Update alongside the file moves |
| --- | --- |
| `api/internal/store/scale_p21_inventory/{store,server}/*.json` | Rename each affected JSON basename and replace its `source` to match the destination Go file. Update source paths and line anchors in `callers`, including entries in JSON files whose own source stays put. The loader validates the `source` correspondence, and the coverage test compares the inventory with `<package>/<file>:<function>` keys it discovers from the AST. Keep the classifications, reasons and owners. |
| `scale_p21_migrate_inventory.go`, `scale_p21_reviewed_routes_test.go` | Update source-qualified migration consumer keys and reviewed MCP bindings. HTTP route patterns and handler names stay the same. |
| `scale_p21_sw318_source_origin_guard_test.go` | Update paths in the SHA-256 tables and file-qualified source-origin allowance keys. Carry over the existing hash for byte-identical moves. Recompute only for edited pinned files. |
| SW270, SW282, SW286, SW292, SW305, SW504, SW559 and writer/revision coverage guards | Update explicit filenames, path prefixes and source locations in the AST checks. Inspect other source-reading tests too. Run `rg` for both the old basename and any reused filename prefix across the repository. |
| SW550 generated route temperature table | Change `scaleP21SW550TableFile` and source-file references. Run `TestScaleP21SW550DefaultColdRouteTable` to verify the renamed generated output matches the reviewed route and temperature data. |
| Activation and differential tests | Keep `Test*` identifiers and build constraints. Keep the assertions for both flag values. Update filename-based fixture paths and diagnostic source expectations where a guard names a moved file. |
| `docs/bounded-metadata*.md`, `docs/openapi.json` and CI timing manifests | Update documented Go source paths and any filename fields in manifests. Keep test-name-based timing keys when test names carry over. |

Keep the embedded inventory directory, package declarations, build tags and
GOOS suffixes in place. Check package initialization order when a rename changes
file order. Verify default, `loadtest` and `integration` builds and vets, the
source and route guards, and the SQLite document, project update and issue
bootstrap differentials after the moves.

## File Map

The map covers all 477 production files and 819 test files with phase-prefixed Go filenames in this checkout, including `scale_p15b_`.
Each section lists the destination package directory. Entries in its table are basenames in that directory.
Same-stem tests follow their implementation's name. Some names retain a phase or workstream suffix to distinguish overlapping coverage.

### Residency

Installed workspace images, resident generations, startup admission, memory budgets and route temperature.

**`api/cmd/server` (`main`)**

| Current | Proposed |
| --- | --- |
| `scale_p15_heap_test.go` | `bootstrap_heap_budget_test.go` |
| `scale_p15_heap.go` | `bootstrap_heap_budget.go` |
| `scale_p21_metadata_reload_source_test.go` | `metadata_reload_source_test.go` |
| `scale_p21_route_temperature.go` | `route_temperature.go` |
| `scale_p21_sw24_installed_identity_remigration_test.go` | `installed_identity_remigration_test.go` |

**`api/internal/store` (`store`)**

| Current | Proposed |
| --- | --- |
| `scale_p15_heap_test.go` | `heap_test.go` |
| `scale_p15_snapshot_reservation.go` | `snapshot_reservation.go` |
| `scale_p21_collection_snapshot.go` | `collection_snapshot.go` |
| `scale_p21_installed_teams_wire.go` | `installed_teams_wire.go` |
| `scale_p21_residency_inventory.go` | `residency_inventory.go` |
| `scale_p21_residency_test.go` | `residency_test.go` |
| `scale_p21_resident_generation.go` | `resident_generation.go` |
| `scale_p21_sw0_collection_snapshot_test.go` | `collection_snapshot_test.go` |
| `scale_p21_sw0_installed_teams_wire_test.go` | `installed_teams_wire_test.go` |
| `scale_p21_sw0_installed_workspace_test.go` | `installed_workspace_test.go` |
| `scale_p21_sw0_installed_workspace.go` | `installed_workspace.go` |
| `scale_p21_sw557_bounded_startup_test.go` | `bounded_startup_test.go` |
| `scale_p21_temperature_test.go` | `temperature_test.go` |

### Cold Reads

Request projection, SQL snapshot pools, bounded COLD reads and the legacy read lane.

**`api/cmd/server` (`main`)**

| Current | Proposed |
| --- | --- |
| `scale_p12_projection_proof_test.go` | `projection_proof_test.go` |
| `scale_p15_snapshot_buffers_test.go` | `snapshot_buffers_test.go` |
| `scale_p19_metadata.go` | `query_window_metadata.go` |
| `scale_p21_cold_authorization.go` | `cold_authorization.go` |
| `scale_p21_cold_dispatch_api_keys.go` | `cold_dispatch_api_keys.go` |
| `scale_p21_cold_dispatch_drafts.go` | `cold_dispatch_drafts.go` |
| `scale_p21_cold_dispatch_oauth.go` | `cold_dispatch_oauth.go` |
| `scale_p21_cold_dispatch_settings.go` | `cold_dispatch_settings.go` |
| `scale_p21_cold_dispatch.go` | `cold_dispatch.go` |
| `scale_p21_hot_member_directory_test.go` | `hot_member_directory_test.go` |
| `scale_p21_legacy_view_busy.go` | `legacy_view_busy.go` |
| `scale_p21_m3_point_test.go` | `point_test.go` |
| `scale_p21_read_leaves.go` | `read_leaves.go` |
| `scale_p21_sw108_thread_subscriptions_test.go` | `cold_read_thread_subscriptions_test.go` |
| `scale_p21_sw174_workflow_headers_test.go` | `workflow_headers_test.go` |
| `scale_p21_sw18_readers_test.go` | `readers_test.go` |
| `scale_p21_sw191_cold_drafts_test.go` | `cold_drafts_test.go` |
| `scale_p21_sw191_cold_routes_test.go` | `cold_routes_test.go` |
| `scale_p21_sw207_cold_settings_test.go` | `cold_settings_test.go` |
| `scale_p21_sw215_cold_authorization_test.go` | `cold_authorization_test.go` |
| `scale_p21_sw23_reads_test.go` | `reads_test.go` |
| `scale_p21_sw24_native_readers_test.go` | `native_readers_test.go` |
| `scale_p21_sw240_read_leaf_routes_test.go` | `read_leaf_routes_test.go` |
| `scale_p21_sw244_cold_template_large_test.go` | `cold_template_large_test.go` |
| `scale_p21_sw4_point_test.go` | `cold_read_point_test.go` |
| `scale_p21_sw41_message_point_test.go` | `message_point_test.go` |
| `scale_p21_sw43_card_point_test.go` | `card_point_test.go` |
| `scale_p21_sw550_default_cold_test.go` | `route_temperature_guard_test.go` |
| `scale_p21_sw551_cold_limit_test.go` | `cold_limit_test.go` |
| `scale_p21_sw558_legacy_view_lane_test.go` | `legacy_view_lane_test.go` |

**`api/internal/store` (`store`)**

| Current | Proposed |
| --- | --- |
| `scale_p10_directory_reads.go` | `directory_reads.go` |
| `scale_p10_directory_test.go` | `directory_test.go` |
| `scale_p10_record_reads_test.go` | `record_reads_test.go` |
| `scale_p10_record_reads.go` | `record_reads.go` |
| `scale_p15_snapshot_buffers_test.go` | `snapshot_buffers_test.go` |
| `scale_p15_snapshot_buffers.go` | `snapshot_buffers.go` |
| `scale_p17_record_selection_test.go` | `record_selection_test.go` |
| `scale_p17_record_selection.go` | `record_selection.go` |
| `scale_p19_metadata_test.go` | `query_window_metadata_test.go` |
| `scale_p19_metadata.go` | `query_window_metadata.go` |
| `scale_p21_ask_status_read_test.go` | `ask_status_read_test.go` |
| `scale_p21_ask_status_read.go` | `ask_status_read.go` |
| `scale_p21_cold_decode_cache_test.go` | `cold_decode_cache_test.go` |
| `scale_p21_cold_decode_cache.go` | `cold_decode_cache.go` |
| `scale_p21_cold_dispatch.go` | `cold_dispatch.go` |
| `scale_p21_cold_facade_revision_test.go` | `cold_facade_revision_test.go` |
| `scale_p21_cold_generic.go` | `cold_generic.go` |
| `scale_p21_cold_incremental_test.go` | `cold_incremental_test.go` |
| `scale_p21_cold_incremental.go` | `cold_incremental.go` |
| `scale_p21_cold_loop_contract_test.go` | `cold_loop_contract_test.go` |
| `scale_p21_cold_preferences_test.go` | `cold_preferences_test.go` |
| `scale_p21_cold_read_owner.go` | `cold_read_owner.go` |
| `scale_p21_cold_read_test.go` | `cold_read_test.go` |
| `scale_p21_cold_read.go` | `cold_read.go` |
| `scale_p21_cold_start_test.go` | `cold_start_test.go` |
| `scale_p21_cold_team_test.go` | `cold_team_test.go` |
| `scale_p21_default_cold.go` | `default_cold.go` |
| `scale_p21_hot_member_directory_test.go` | `hot_member_directory_test.go` |
| `scale_p21_legacy_delta_read_budget.go` | `legacy_delta_read_budget.go` |
| `scale_p21_legacy_inline_fields.go` | `legacy_inline_fields.go` |
| `scale_p21_legacy_metadata_headers.go` | `legacy_metadata_headers.go` |
| `scale_p21_legacy_metadata_slow.go` | `legacy_metadata_slow.go` |
| `scale_p21_legacy_resource_counts.go` | `legacy_resource_counts.go` |
| `scale_p21_legacy_view_lane.go` | `legacy_view_lane.go` |
| `scale_p21_native_reads.go` | `native_reads.go` |
| `scale_p21_owned_child_read_test.go` | `owned_child_read_test.go` |
| `scale_p21_owned_metadata_read.go` | `owned_metadata_read.go` |
| `scale_p21_p19_snapshot_read.go` | `snapshot_read.go` |
| `scale_p21_read_leaf_rows.go` | `read_leaf_rows.go` |
| `scale_p21_resident_detail_read_test.go` | `resident_detail_read_test.go` |
| `scale_p21_resident_detail_read.go` | `resident_detail_read.go` |
| `scale_p21_snapshot_work.go` | `snapshot_work.go` |
| `scale_p21_sqlite_snapshot_pool.go` | `sqlite_snapshot_pool.go` |
| `scale_p21_sw0_metadata_headers_test.go` | `metadata_headers_test.go` |
| `scale_p21_sw0_snapshot_work_test.go` | `snapshot_work_test.go` |
| `scale_p21_sw109_legacy_read_boundaries_test.go` | `legacy_read_boundaries_test.go` |
| `scale_p21_sw112_p19_snapshot_read_test.go` | `p19_snapshot_read_test.go` |
| `scale_p21_sw18_readers_test.go` | `readers_test.go` |
| `scale_p21_sw191_cold_dispatch_test.go` | `cold_dispatch_test.go` |
| `scale_p21_sw196_legacy_headers_test.go` | `legacy_headers_test.go` |
| `scale_p21_sw215_cold_read_owner_test.go` | `cold_read_owner_test.go` |
| `scale_p21_sw24_native_reads_test.go` | `cold_read_native_reads_test.go` |
| `scale_p21_sw24_native_reads.go` | `cold_read_native_reads.go` |
| `scale_p21_sw240_read_leaves_test.go` | `read_leaves_test.go` |
| `scale_p21_sw250_projection_scalar_test.go` | `projection_scalar_test.go` |
| `scale_p21_sw28_projection_test.go` | `projection_test.go` |
| `scale_p21_sw316_cold_generic_test.go` | `cold_generic_test.go` |
| `scale_p21_sw32_owned_read_test.go` | `owned_read_test.go` |
| `scale_p21_sw325_cold_next_issue_test.go` | `cold_next_issue_test.go` |
| `scale_p21_sw337_canonical_read_page_test.go` | `canonical_read_page_test.go` |
| `scale_p21_sw530_skeletal_read.go` | `skeletal_read.go` |
| `scale_p21_sw531_skeletal_read_test.go` | `skeletal_read_test.go` |
| `scale_p21_sw553_facade_services_test.go` | `facade_services_test.go` |
| `scale_p21_sw559_resident_image_readers_test.go` | `resident_image_readers_test.go` |
| `scale_p21_sw560_read_window_test.go` | `read_window_test.go` |
| `scale_p21_sw576_p19_snapshot_access_retry_test.go` | `p19_snapshot_access_retry_test.go` |
| `scale_p21_sw591_sqlite_snapshot_pool_test.go` | `sqlite_snapshot_pool_test.go` |
| `scale_p21_sw71_legacy_resource_counts_test.go` | `legacy_resource_counts_test.go` |

### Spool

Spill files, byte accounting, file lifetimes and platform-specific directory handling.

**`api/cmd/server` (`main`)**

| Current | Proposed |
| --- | --- |
| `scale_p15b_spool_test.go` | `spool_test.go` |

**`api/internal/store` (`store`)**

| Current | Proposed |
| --- | --- |
| `scale_p15b_spool_test.go` | `spool_test.go` |
| `scale_p15b_spool.go` | `spool.go` |
| `scale_p21_integration_binary_spool.go` | `integration_binary_spool.go` |
| `scale_p21_integration_row_spool.go` | `integration_row_spool.go` |
| `scale_p21_logical_spool_group.go` | `logical_spool_group.go` |
| `scale_p21_logical_spool_io_test.go` | `logical_spool_io_test.go` |
| `scale_p21_logical_spool_io.go` | `logical_spool_io.go` |
| `scale_p21_spool_directory_other.go` | `spool_directory_other.go` |
| `scale_p21_spool_directory_test.go` | `spool_directory_test.go` |
| `scale_p21_spool_directory_unix.go` | `spool_directory_unix.go` |
| `scale_p21_spool_directory.go` | `spool_directory.go` |
| `scale_p21_sw229_inline_root_spool_test.go` | `inline_root_spool_test.go` |
| `scale_p21_sw348_integration_binary_spool_test.go` | `integration_binary_spool_test.go` |
| `scale_p21_sw354_integration_spool_transfer_test.go` | `integration_spool_transfer_test.go` |
| `scale_p21_sw602_spool_group_test.go` | `spool_group_test.go` |

### Logical Metadata

Logical sources, framing and ownership, JSON encoding, install and recovery.

**`api/cmd/server` (`main`)**

| Current | Proposed |
| --- | --- |
| `scale_p12_metadata_source.go` | `metadata_source.go` |
| `scale_p15_json.go` | `bootstrap_json.go` |
| `scale_p15_parallel_json.go` | `bootstrap_parallel_json.go` |
| `scale_p21_normalize_scan_test.go` | `normalize_scan_test.go` |

**`api/internal/store` (`store`)**

| Current | Proposed |
| --- | --- |
| `scale_p15_hydration_budget.go` | `hydration_budget.go` |
| `scale_p15_hydration_test.go` | `hydration_test.go` |
| `scale_p21_generic_json_cost_test.go` | `generic_json_cost_test.go` |
| `scale_p21_json_generic_owned.go` | `json_generic_owned.go` |
| `scale_p21_json_output_read_test.go` | `json_output_read_test.go` |
| `scale_p21_json_output_read.go` | `json_output_read.go` |
| `scale_p21_json_string_chunks.go` | `json_string_chunks.go` |
| `scale_p21_json_string_source.go` | `json_string_source.go` |
| `scale_p21_large_row_first.go` | `large_row_first.go` |
| `scale_p21_large_row_inputs.go` | `large_row_inputs.go` |
| `scale_p21_large_row_persist.go` | `large_row_persist.go` |
| `scale_p21_large_row_rebase.go` | `large_row_rebase.go` |
| `scale_p21_large_row_refresh.go` | `large_row_refresh.go` |
| `scale_p21_large_row.go` | `large_row.go` |
| `scale_p21_logical_borrowed_range_test.go` | `logical_borrowed_range_test.go` |
| `scale_p21_logical_borrowed_range.go` | `logical_borrowed_range.go` |
| `scale_p21_logical_candidate.go` | `logical_candidate.go` |
| `scale_p21_logical_clone.go` | `logical_clone.go` |
| `scale_p21_logical_external.go` | `logical_external.go` |
| `scale_p21_logical_frame_memo.go` | `logical_frame_memo.go` |
| `scale_p21_logical_generic_json.go` | `logical_generic_json.go` |
| `scale_p21_logical_install.go` | `logical_install.go` |
| `scale_p21_logical_issue_refs.go` | `logical_issue_refs.go` |
| `scale_p21_logical_json_stream.go` | `logical_json_stream.go` |
| `scale_p21_logical_large_retry.go` | `logical_large_retry.go` |
| `scale_p21_logical_normalize_work.go` | `logical_normalize_work.go` |
| `scale_p21_logical_normalize.go` | `logical_normalize.go` |
| `scale_p21_logical_raw_message_frame.go` | `logical_raw_message_frame.go` |
| `scale_p21_logical_read_window.go` | `logical_read_window.go` |
| `scale_p21_logical_recovery.go` | `logical_recovery.go` |
| `scale_p21_logical_retry.go` | `logical_retry.go` |
| `scale_p21_logical_root.go` | `logical_root.go` |
| `scale_p21_logical_scratch.go` | `logical_scratch.go` |
| `scale_p21_logical_selection.go` | `logical_selection.go` |
| `scale_p21_logical_source_fork.go` | `logical_source_fork.go` |
| `scale_p21_logical_source_locations.go` | `logical_source_locations.go` |
| `scale_p21_logical_source_read.go` | `logical_source_read.go` |
| `scale_p21_logical_source_transform.go` | `logical_source_transform.go` |
| `scale_p21_logical_source.go` | `logical_source.go` |
| `scale_p21_logical_webhook.go` | `logical_webhook.go` |
| `scale_p21_logical_wire_test.go` | `logical_wire_test.go` |
| `scale_p21_logical_wire.go` | `logical_wire.go` |
| `scale_p21_logical_workspace.go` | `logical_workspace.go` |
| `scale_p21_logical_written_index_test.go` | `logical_written_index_test.go` |
| `scale_p21_logical_written_index.go` | `logical_written_index.go` |
| `scale_p21_metadata_ordinal_profile.go` | `metadata_ordinal_profile.go` |
| `scale_p21_output_owner_test.go` | `output_owner_test.go` |
| `scale_p21_output_owner.go` | `output_owner.go` |
| `scale_p21_sw0_json_string_chunks_test.go` | `json_string_chunks_test.go` |
| `scale_p21_sw144_json_string_source_test.go` | `json_string_source_test.go` |
| `scale_p21_sw202_ordinal_kernel_test.go` | `ordinal_kernel_test.go` |
| `scale_p21_sw223_raw_message_frame_test.go` | `raw_message_frame_test.go` |
| `scale_p21_sw228_generic_owned_test.go` | `generic_owned_test.go` |
| `scale_p21_sw228_json_runs_test.go` | `json_runs_test.go` |
| `scale_p21_sw248_generic_json_test.go` | `generic_json_test.go` |
| `scale_p21_sw27_logical_reload_test.go` | `logical_reload_test.go` |
| `scale_p21_sw27_logical_source_test.go` | `logical_source_test.go` |
| `scale_p21_sw28_content_universe_test.go` | `content_universe_test.go` |
| `scale_p21_sw29_logical_codec_test.go` | `logical_codec_test.go` |
| `scale_p21_sw29_logical_external_test.go` | `logical_external_test.go` |
| `scale_p21_sw29_logical_locations_test.go` | `logical_locations_test.go` |
| `scale_p21_sw29_logical_orphan_test.go` | `logical_orphan_test.go` |
| `scale_p21_sw29_logical_webhook_test.go` | `logical_webhook_test.go` |
| `scale_p21_sw296_large_row_write_test.go` | `large_row_write_test.go` |
| `scale_p21_sw297_large_row_refresh_test.go` | `large_row_refresh_test.go` |
| `scale_p21_sw31_normalize_work_test.go` | `normalize_work_test.go` |
| `scale_p21_sw33_large_root_test.go` | `large_root_test.go` |
| `scale_p21_sw33_logical_authority_test.go` | `logical_authority_test.go` |
| `scale_p21_sw482_large_row_typed_work_test.go` | `large_row_typed_work_test.go` |
| `scale_p21_sw592_logical_read_window_test.go` | `logical_read_window_test.go` |
| `scale_p21_sw96_logical_clone_test.go` | `logical_clone_test.go` |

### Metadata Writes

Resident and COLD write dispatch, postimages, aggregate publication and compatibility mutation paths.

**`api/cmd/server` (`main`)**

| Current | Proposed |
| --- | --- |
| `scale_p01_write_test.go` | `write_test.go` |
| `scale_p04_mutation_inputs_test.go` | `mutation_inputs_test.go` |
| `scale_p16_mutations_test.go` | `mutations_test.go` |
| `scale_p16_record_cascades.go` | `record_cascades.go` |
| `scale_p20_write_history_test.go` | `write_history_test.go` |
| `scale_p21_cold_writers_test.go` | `cold_writers_test.go` |
| `scale_p21_resident_document_write_test.go` | `resident_document_write_test.go` |
| `scale_p21_sw25_aggregate_test.go` | `aggregate_test.go` |
| `scale_p21_sw292_write_chain_boundary_test.go` | `write_chain_boundary_test.go` |
| `scale_p21_sw48_child_route_dispatch_test.go` | `child_route_dispatch_test.go` |

**`api/internal/store` (`store`)**

| Current | Proposed |
| --- | --- |
| `scale_p04_mutation_inputs_test.go` | `mutation_inputs_test.go` |
| `scale_p04_mutation_inputs.go` | `mutation_inputs.go` |
| `scale_p04_mutation_verify_test.go` | `mutation_verify_test.go` |
| `scale_p04_mutation_verify.go` | `mutation_verify.go` |
| `scale_p09_commit_plan_test.go` | `commit_plan_test.go` |
| `scale_p09_prepared_writes.go` | `prepared_writes.go` |
| `scale_p09_writer_coverage_test.go` | `writer_coverage_test.go` |
| `scale_p16_record_cascades.go` | `record_cascades.go` |
| `scale_p16_record_mutation.go` | `scoped_record_mutation.go` |
| `scale_p16_record_verify_test.go` | `record_verify_test.go` |
| `scale_p16_record_verify.go` | `record_verify.go` |
| `scale_p16_verify_comparison_test.go` | `verify_comparison_test.go` |
| `scale_p16_verify_comparison.go` | `verify_comparison.go` |
| `scale_p21_aggregate_closure_inline_write.go` | `aggregate_closure_inline_write.go` |
| `scale_p21_aggregate_closure_overlay.go` | `aggregate_closure_overlay.go` |
| `scale_p21_aggregate_closure_payload_work.go` | `aggregate_closure_payload_work.go` |
| `scale_p21_aggregate_closure_refresh_write.go` | `aggregate_closure_refresh_write.go` |
| `scale_p21_aggregate_closure_refresh.go` | `aggregate_closure_refresh.go` |
| `scale_p21_aggregate_closure_work.go` | `aggregate_closure_work.go` |
| `scale_p21_aggregate_closure.go` | `aggregate_closure.go` |
| `scale_p21_aggregate_publication.go` | `aggregate_publication.go` |
| `scale_p21_aggregate_traversal.go` | `aggregate_traversal.go` |
| `scale_p21_aggregate_user_index_test.go` | `aggregate_user_index_test.go` |
| `scale_p21_aggregate_user_index.go` | `aggregate_user_index.go` |
| `scale_p21_appending.go` | `appending.go` |
| `scale_p21_batch_write_admission.go` | `batch_write_admission.go` |
| `scale_p21_cold_write_test.go` | `cold_write_test.go` |
| `scale_p21_cold_write.go` | `cold_write.go` |
| `scale_p21_commit_stamp.go` | `commit_stamp.go` |
| `scale_p21_inline_resident_root.go` | `inline_resident_root.go` |
| `scale_p21_inline_root_write.go` | `inline_root_write.go` |
| `scale_p21_inline_scoped_write.go` | `inline_scoped_write.go` |
| `scale_p21_legacy_actor_write.go` | `legacy_actor_write.go` |
| `scale_p21_legacy_array_reorder.go` | `legacy_array_reorder.go` |
| `scale_p21_legacy_count_write.go` | `legacy_count_write.go` |
| `scale_p21_legacy_image_counts.go` | `legacy_image_counts.go` |
| `scale_p21_legacy_metadata_write.go` | `legacy_metadata_write.go` |
| `scale_p21_legacy_publication.go` | `legacy_publication.go` |
| `scale_p21_legacy_record_base.go` | `legacy_record_base.go` |
| `scale_p21_logical_publication_test.go` | `logical_publication_test.go` |
| `scale_p21_logical_publication.go` | `logical_publication.go` |
| `scale_p21_metadata_sql_stage_accounting.go` | `metadata_sql_stage_accounting.go` |
| `scale_p21_metadata_sql_stage.go` | `metadata_sql_stage.go` |
| `scale_p21_record_mutation.go` | `bounded_record_mutation.go` |
| `scale_p21_resident_document_write.go` | `resident_document_write.go` |
| `scale_p21_resident_project_write_test.go` | `resident_project_write_test.go` |
| `scale_p21_resident_project_write.go` | `resident_project_write.go` |
| `scale_p21_resident_record_ordinals.go` | `resident_record_ordinals.go` |
| `scale_p21_resident_write_verify.go` | `resident_write_verify.go` |
| `scale_p21_staged_write_measure_test.go` | `staged_write_measure_test.go` |
| `scale_p21_sw0_appending_test.go` | `appending_test.go` |
| `scale_p21_sw103_mutate_workspace_test.go` | `mutate_workspace_test.go` |
| `scale_p21_sw110_thread_writer_test.go` | `thread_writer_test.go` |
| `scale_p21_sw183_publication_admission_test.go` | `publication_admission_test.go` |
| `scale_p21_sw188_inline_resident_test.go` | `inline_resident_test.go` |
| `scale_p21_sw188_inline_write_test.go` | `inline_write_test.go` |
| `scale_p21_sw193_inline_writer_test.go` | `inline_writer_test.go` |
| `scale_p21_sw193_inline_writer.go` | `inline_writer.go` |
| `scale_p21_sw205_aggregate_publication_test.go` | `aggregate_publication_test.go` |
| `scale_p21_sw205_aggregate_stack_test.go` | `aggregate_stack_test.go` |
| `scale_p21_sw236_inline_root_writer_test.go` | `inline_root_writer_test.go` |
| `scale_p21_sw26_migration_stage.go` | `migration_stage.go` |
| `scale_p21_sw27_commit_stamp_test.go` | `commit_stamp_test.go` |
| `scale_p21_sw28_aggregate_test.go` | `aggregate_test.go` |
| `scale_p21_sw28_appending_test.go` | `metadata_write_appending_test.go` |
| `scale_p21_sw28_publication_test.go` | `publication_test.go` |
| `scale_p21_sw29_unavailable_write_test.go` | `unavailable_write_test.go` |
| `scale_p21_sw293_write_chain_test.go` | `write_chain_test.go` |
| `scale_p21_sw31_timestamp_inputs_test.go` | `timestamp_inputs_test.go` |
| `scale_p21_sw352_publication_frame_test.go` | `publication_frame_test.go` |
| `scale_p21_sw541_inline_append_test.go` | `inline_append_test.go` |
| `scale_p21_sw554_cold_write_panic_test.go` | `cold_write_panic_test.go` |
| `scale_p21_sw590_writer_connection_order_test.go` | `writer_connection_order_test.go` |
| `scale_p21_sw76_legacy_actor_write_test.go` | `legacy_actor_write_test.go` |
| `scale_p21_sw78_legacy_count_write_test.go` | `legacy_count_write_test.go` |
| `scale_p21_sw83_legacy_metadata_write_test.go` | `legacy_metadata_write_test.go` |
| `scale_p21_sw92_legacy_image_counts_test.go` | `legacy_image_counts_test.go` |
| `scale_p21_sw93_legacy_record_base_test.go` | `legacy_record_base_test.go` |
| `scale_p21_sw95_legacy_publication_test.go` | `legacy_publication_test.go` |
| `scale_p21_thread_publication.go` | `thread_publication.go` |
| `scale_p21_timestamp_inputs.go` | `timestamp_inputs.go` |
| `scale_p21_work_retry.go` | `work_retry.go` |
| `scale_p21_write_chain.go` | `write_chain.go` |

### Search

Metadata and fulltext migration and reads, query caching and plan checks.

**`api/internal/store` (`store`)**

| Current | Proposed |
| --- | --- |
| `scale_p21_project_query_cache.go` | `project_query_cache.go` |
| `scale_p21_sw17_search_key_budget_test.go` | `search_key_budget_test.go` |
| `scale_p21_sw17_search_migration.go` | `search_migration.go` |
| `scale_p21_sw17_search_test.go` | `search_test.go` |
| `scale_p21_sw18_search_test.go` | `search_search_test.go` |
| `scale_p21_sw466_search_document_upsert_test.go` | `search_document_upsert_test.go` |
| `scale_p21_sw467_metadata_search_rewrite_test.go` | `metadata_search_rewrite_test.go` |
| `scale_p21_sw469_fulltext_restore_test.go` | `fulltext_restore_test.go` |
| `scale_p21_sw470_metadata_search_canonical_test.go` | `metadata_search_canonical_test.go` |
| `scale_p21_sw94_project_query_cache_test.go` | `project_query_cache_test.go` |

### Sync And Bootstrap

Manifests, bootstrap streams, record feeds and cursor handling.

**`api/cmd/server` (`main`)**

| Current | Proposed |
| --- | --- |
| `scale_p01_browser_acks_test.go` | `browser_acks_test.go` |
| `scale_p06_encoding_test.go` | `realtime_encoding_test.go` |
| `scale_p06_encoding.go` | `realtime_encoding.go` |
| `scale_p10_sync_test.go` | `sync_test.go` |
| `scale_p12_sync_stream.go` | `sync_stream.go` |
| `scale_p12_sync_v2_test.go` | `sync_v2_test.go` |
| `scale_p15_legacy_stream.go` | `legacy_stream.go` |
| `scale_p19_manifest.go` | `manifest.go` |
| `scale_p19_updates.go` | `project_update_query.go` |
| `scale_p20_member_transport_test.go` | `member_transport_test.go` |
| `scale_p21_bootstrap_encoding_cache_test.go` | `bootstrap_encoding_cache_test.go` |
| `scale_p21_bootstrap_encoding_cache.go` | `bootstrap_encoding_cache.go` |
| `scale_p21_bootstrap_loop_projection.go` | `bootstrap_loop_projection.go` |
| `scale_p21_bootstrap_loop_text.go` | `bootstrap_loop_text.go` |
| `scale_p21_bootstrap_project_history_test.go` | `bootstrap_project_history_test.go` |
| `scale_p21_general_bootstrap.go` | `general_bootstrap.go` |
| `scale_p21_issue_bootstrap_stream.go` | `issue_bootstrap_stream.go` |
| `scale_p21_sw139_bootstrap_loop_projection_test.go` | `bootstrap_loop_projection_test.go` |
| `scale_p21_sw145_issue_bootstrap_routes_test.go` | `issue_bootstrap_routes_test.go` |
| `scale_p21_sw151_bootstrap_literal_markers_test.go` | `bootstrap_literal_markers_test.go` |
| `scale_p21_sw164_general_bootstrap_test.go` | `general_bootstrap_test.go` |
| `scale_p21_sw225_issue_bootstrap_boundary_test.go` | `issue_bootstrap_boundary_test.go` |
| `scale_p21_sw24_manifest_test.go` | `manifest_test.go` |
| `scale_p21_sw265_bootstrap_leaves_guard_test.go` | `bootstrap_leaves_guard_test.go` |
| `scale_p21_sw266_bootstrap_refusal_test.go` | `bootstrap_refusal_test.go` |
| `scale_p21_sw575_sync_read_access_retry_test.go` | `sync_read_access_retry_test.go` |

**`api/internal/store` (`store`)**

| Current | Proposed |
| --- | --- |
| `scale_p15_record_stream.go` | `record_stream.go` |
| `scale_p19_cursor.go` | `query_window_cursor.go` |
| `scale_p21_bootstrap_body_projection.go` | `bootstrap_body_projection.go` |
| `scale_p21_bootstrap_content_walk.go` | `bootstrap_content_walk.go` |
| `scale_p21_bootstrap_encoding_identity_test.go` | `bootstrap_encoding_identity_test.go` |
| `scale_p21_bootstrap_encoding_identity.go` | `bootstrap_encoding_identity.go` |
| `scale_p21_bootstrap_issue_walk.go` | `bootstrap_issue_walk.go` |
| `scale_p21_bootstrap_json_rewrite.go` | `bootstrap_json_rewrite.go` |
| `scale_p21_bootstrap_projection_read.go` | `bootstrap_projection_read.go` |
| `scale_p21_general_bootstrap_read.go` | `general_bootstrap_read.go` |
| `scale_p21_sw138_bootstrap_body_projection_test.go` | `bootstrap_body_projection_test.go` |
| `scale_p21_sw143_bootstrap_projection_read_test.go` | `bootstrap_projection_read_test.go` |
| `scale_p21_sw146_bootstrap_update_maps_test.go` | `bootstrap_update_maps_test.go` |
| `scale_p21_sw162_bootstrap_issue_walk_test.go` | `bootstrap_issue_walk_test.go` |
| `scale_p21_sw163_bootstrap_content_walk_test.go` | `bootstrap_content_walk_test.go` |
| `scale_p21_sw165_general_bootstrap_read_test.go` | `general_bootstrap_read_test.go` |
| `scale_p21_sw166_bootstrap_session_bodies_test.go` | `bootstrap_session_bodies_test.go` |
| `scale_p21_sw21_sync_requests_test.go` | `sync_requests_test.go` |
| `scale_p21_sw21_sync_requests.go` | `sync_requests.go` |
| `scale_p21_sw88_bootstrap_test.go` | `bootstrap_test.go` |

### Realtime

Replay, event reads, live delivery, presence, epochs and revision coherence.

**`api/cmd/server` (`main`)**

| Current | Proposed |
| --- | --- |
| `scale_p06_presence_test.go` | `presence_test.go` |
| `scale_p06_transport_test.go` | `transport_test.go` |
| `scale_p08_realtime_test.go` | `realtime_test.go` |
| `scale_p09_realtime_integration_test.go` | `realtime_integration_test.go` |
| `scale_p12_epoch_budget_test.go` | `epoch_budget_test.go` |
| `scale_p12_epoch_cache_test.go` | `epoch_cache_test.go` |
| `scale_p12_epoch_cache.go` | `epoch_cache.go` |
| `scale_p12_epoch_measure_test.go` | `epoch_measure_test.go` |
| `scale_p12_epoch_unique_test.go` | `epoch_unique_test.go` |
| `scale_p12_event_source_test.go` | `event_source_test.go` |
| `scale_p12_journal_fallback_test.go` | `journal_fallback_test.go` |
| `scale_p21_sw62_presence_test.go` | `realtime_presence_test.go` |

**`api/internal/boundedcrdt` (`boundedcrdt`)**

| Current | Proposed |
| --- | --- |
| `scale_p21_bounded_crdt_test.go` | `bounded_crdt_test.go` |
| `scale_p21_sw205_json_lifetime_test.go` | `json_lifetime_test.go` |

**`api/internal/boundedcrdt/internal/meter` (`meter`)**

| Current | Proposed |
| --- | --- |
| `scale_p21_meter_test.go` | `meter_test.go` |
| `scale_p21_sw205_json_width_test.go` | `json_width_test.go` |

**`api/internal/store` (`store`)**

| Current | Proposed |
| --- | --- |
| `scale_p06_replay_test.go` | `replay_test.go` |
| `scale_p06_replay.go` | `replay.go` |
| `scale_p12_epoch_access.go` | `epoch_access.go` |
| `scale_p12_epoch_context_test.go` | `epoch_context_test.go` |
| `scale_p12_event_access_test.go` | `event_access_test.go` |
| `scale_p12_event_access.go` | `event_access.go` |
| `scale_p12_source_records.go` | `source_records.go` |
| `scale_p21_event_policy_source.go` | `event_policy_source.go` |
| `scale_p21_legacy_event_policy_read.go` | `legacy_event_policy_read.go` |
| `scale_p21_presence_document_read.go` | `presence_document_read.go` |
| `scale_p21_presence_document_value.go` | `presence_document_value.go` |
| `scale_p21_realtime_record_header.go` | `realtime_record_header.go` |
| `scale_p21_sw105_presence_document_value_test.go` | `presence_document_value_test.go` |
| `scale_p21_sw64_presence_document_read_test.go` | `presence_document_read_test.go` |
| `scale_p21_sw73_event_policy_source_test.go` | `event_policy_source_test.go` |
| `scale_p21_sw85_realtime_record_header_test.go` | `realtime_record_header_test.go` |

### Journal And Peers

Journal persistence and repair, record revisions and peer application.

**`api/cmd/server` (`main`)**

| Current | Proposed |
| --- | --- |
| `scale_p11_peer_test.go` | `peer_test.go` |
| `scale_p17_peers_test.go` | `peers_test.go` |
| `scale_p20_peers_test.go` | `journal_peers_test.go` |

**`api/internal/store` (`store`)**

| Current | Proposed |
| --- | --- |
| `scale_p09_commit_revision_test.go` | `commit_revision_test.go` |
| `scale_p09_content_keys.go` | `content_keys.go` |
| `scale_p09_journal_activation.go` | `journal_activation.go` |
| `scale_p09_journal_flush.go` | `journal_flush.go` |
| `scale_p09_journal_inline.go` | `journal_inline.go` |
| `scale_p09_journal_repair.go` | `journal_repair.go` |
| `scale_p09_journal_retention.go` | `journal_retention.go` |
| `scale_p09_journal_schema.go` | `journal_schema.go` |
| `scale_p09_journal_test.go` | `journal_test.go` |
| `scale_p09_key_migration.go` | `key_migration.go` |
| `scale_p09_legacy_sequencer.go` | `legacy_sequencer.go` |
| `scale_p09_mysql_identity.go` | `mysql_identity.go` |
| `scale_p09_off_revision_test.go` | `off_revision_test.go` |
| `scale_p09_order_cache.go` | `order_cache.go` |
| `scale_p09_primary_keys.go` | `primary_keys.go` |
| `scale_p09_sqlite_commit.go` | `sqlite_commit.go` |
| `scale_p09_sqlite_projection.go` | `sqlite_projection.go` |
| `scale_p11_peer_apply.go` | `record_peer_apply.go` |
| `scale_p11_peer_state.go` | `record_peer_state.go` |
| `scale_p11_peer_test.go` | `peer_test.go` |
| `scale_p21_peer_apply.go` | `bounded_metadata_peer_apply.go` |
| `scale_p21_peer_recovery_test.go` | `peer_recovery_test.go` |

### Coordination

Shared cache limits, Redis coordination and presence primitives.

**`api/cmd/server` (`main`)**

| Current | Proposed |
| --- | --- |
| `scale_p11_redis_test.go` | `redis_test.go` |

**`api/internal/coordination` (`coordination`)**

| Current | Proposed |
| --- | --- |
| `scale_p06_presence_test.go` | `presence_test.go` |
| `scale_p21_cache_bytes.go` | `cache_bytes.go` |
| `scale_p21_sw94_cache_bytes_test.go` | `cache_bytes_test.go` |

### Access And Identity

Authorization, identity, credentials, access scopes and policy projections.

**`api/cmd/server` (`main`)**

| Current | Proposed |
| --- | --- |
| `scale_p02_session_touch_test.go` | `session_touch_test.go` |
| `scale_p09_permission_test.go` | `permission_test.go` |
| `scale_p10_credential_scope_test.go` | `credential_scope_test.go` |
| `scale_p11_permission_test.go` | `access_permission_test.go` |
| `scale_p15_permission_test.go` | `access_permission_p15_test.go` |
| `scale_p15b_signing.go` | `signing.go` |
| `scale_p16_permission_effects_test.go` | `permission_effects_test.go` |
| `scale_p16_permissions_test.go` | `permissions_test.go` |
| `scale_p16_record_authorization.go` | `record_authorization.go` |
| `scale_p17_permissions_test.go` | `access_permissions_test.go` |
| `scale_p17_revocation_test.go` | `revocation_test.go` |
| `scale_p17_scope_boundary_test.go` | `scope_boundary_test.go` |
| `scale_p18_grants_test.go` | `grants_test.go` |
| `scale_p18_permissions_test.go` | `access_permissions_p18_test.go` |
| `scale_p20_permissions_test.go` | `access_permissions_p20_test.go` |
| `scale_p21_attachment_visibility.go` | `attachment_visibility.go` |
| `scale_p21_escaped_route_shape.go` | `escaped_route_shape.go` |
| `scale_p21_hot_oauth_logo_test.go` | `hot_oauth_logo_test.go` |
| `scale_p21_issue_child_authorization.go` | `issue_child_authorization.go` |
| `scale_p21_issue_record_authorization_read.go` | `issue_record_authorization_read.go` |
| `scale_p21_m2_identity_test.go` | `identity_test.go` |
| `scale_p21_m2_oauth_test.go` | `oauth_test.go` |
| `scale_p21_m2_policy_test.go` | `policy_test.go` |
| `scale_p21_m3_credentials_test.go` | `credentials_test.go` |
| `scale_p21_m3_identity_test.go` | `access_identity_test.go` |
| `scale_p21_oauth_applications_list.go` | `oauth_applications_list.go` |
| `scale_p21_project_visibility.go` | `project_visibility.go` |
| `scale_p21_scoped_access_test.go` | `scoped_access_test.go` |
| `scale_p21_scoped_access.go` | `scoped_access.go` |
| `scale_p21_sw0_document_signing_test.go` | `document_signing_test.go` |
| `scale_p21_sw113_issue_permission_read_test.go` | `issue_permission_read_test.go` |
| `scale_p21_sw12_oauth_test.go` | `access_oauth_test.go` |
| `scale_p21_sw120_default_viewer_auth_test.go` | `default_viewer_auth_test.go` |
| `scale_p21_sw16_former_members_test.go` | `access_former_members_test.go` |
| `scale_p21_sw187_issue_record_authorization_test.go` | `issue_record_authorization_test.go` |
| `scale_p21_sw2_auth_policy_test.go` | `auth_policy_test.go` |
| `scale_p21_sw20_team_purge_test.go` | `team_purge_test.go` |
| `scale_p21_sw20_triage_identity_test.go` | `triage_identity_test.go` |
| `scale_p21_sw231_filter_auth_test.go` | `filter_auth_test.go` |
| `scale_p21_sw24_consent_wire_test.go` | `consent_wire_test.go` |
| `scale_p21_sw25_erasure_read.go` | `erasure_read.go` |
| `scale_p21_sw25_erasure_test.go` | `erasure_test.go` |
| `scale_p21_sw253_collection_authorization_test.go` | `collection_authorization_test.go` |
| `scale_p21_sw253_collection_authorization.go` | `collection_authorization.go` |
| `scale_p21_sw255_resource_authorization_test.go` | `resource_authorization_test.go` |
| `scale_p21_sw255_resource_authorization.go` | `resource_authorization.go` |
| `scale_p21_sw256_issue_label_authorization.go` | `issue_label_authorization.go` |
| `scale_p21_sw256_issue_read_authorization_test.go` | `issue_read_authorization_test.go` |
| `scale_p21_sw268_attachment_visibility_test.go` | `attachment_visibility_test.go` |
| `scale_p21_sw282_document_auth_boundary_test.go` | `document_auth_boundary_test.go` |
| `scale_p21_sw284_document_auth_routes_test.go` | `document_auth_routes_test.go` |
| `scale_p21_sw294_oauth_metadata_test.go` | `oauth_metadata_test.go` |
| `scale_p21_sw305_auth_ingress_boundary_test.go` | `auth_ingress_boundary_test.go` |
| `scale_p21_sw306_membership_authorization_test.go` | `membership_authorization_test.go` |
| `scale_p21_sw306_membership_authorization.go` | `membership_authorization.go` |
| `scale_p21_sw307_team_resource_authorization_test.go` | `team_resource_authorization_test.go` |
| `scale_p21_sw307_team_resource_authorization.go` | `team_resource_authorization.go` |
| `scale_p21_sw309_resource_auth_sql_test.go` | `resource_auth_sql_test.go` |
| `scale_p21_sw315_team_membership_authorization_test.go` | `team_membership_authorization_test.go` |
| `scale_p21_sw315_team_membership_authorization.go` | `team_membership_authorization.go` |
| `scale_p21_sw319_trash_authorization_test.go` | `trash_authorization_test.go` |
| `scale_p21_sw323_hot_auth_closure_test.go` | `hot_auth_closure_test.go` |
| `scale_p21_sw327_escaped_resource_authorization_test.go` | `escaped_resource_authorization_test.go` |
| `scale_p21_sw329_escaped_label_authorization_test.go` | `escaped_label_authorization_test.go` |
| `scale_p21_sw330_escaped_team_authorization_test.go` | `escaped_team_authorization_test.go` |
| `scale_p21_sw331_escaped_legacy_issue_authorization_test.go` | `escaped_legacy_issue_authorization_test.go` |
| `scale_p21_sw332_escaped_membership_authorization_test.go` | `escaped_membership_authorization_test.go` |
| `scale_p21_sw334_producer_upload_authorization_test.go` | `producer_upload_authorization_test.go` |
| `scale_p21_sw350_history_authorization_test.go` | `history_authorization_test.go` |
| `scale_p21_sw410_oauth_client_lookup_test.go` | `oauth_client_lookup_test.go` |
| `scale_p21_sw430_oauth_cold_test.go` | `oauth_cold_test.go` |
| `scale_p21_sw450_api_keys_cold_test.go` | `api_keys_cold_test.go` |
| `scale_p21_sw47_issue_child_authorization_test.go` | `issue_child_authorization_test.go` |
| `scale_p21_sw490_auth_fallback_closure_test.go` | `auth_fallback_closure_test.go` |
| `scale_p21_sw490_fallback_authorization.go` | `fallback_authorization.go` |
| `scale_p21_sw491_fallback_authorization_test.go` | `fallback_authorization_test.go` |
| `scale_p21_sw492_cycle_authorization_test.go` | `cycle_authorization_test.go` |
| `scale_p21_sw492_record_authorization.go` | `access_record_authorization.go` |
| `scale_p21_sw493_view_dashboard_authorization_test.go` | `view_dashboard_authorization_test.go` |
| `scale_p21_sw493_view_dashboard_authorization.go` | `view_dashboard_authorization.go` |
| `scale_p21_sw494_record_kind_authorization_test.go` | `record_kind_authorization_test.go` |
| `scale_p21_sw494_record_kind_authorization.go` | `record_kind_authorization.go` |
| `scale_p21_sw495_reference_authorization_test.go` | `reference_authorization_test.go` |
| `scale_p21_sw495_reference_authorization.go` | `reference_authorization.go` |
| `scale_p21_sw496_body_authorization_test.go` | `body_authorization_test.go` |
| `scale_p21_sw496_body_authorization.go` | `body_authorization.go` |
| `scale_p21_sw520_former_member_document_test.go` | `former_member_document_test.go` |
| `scale_p21_sw520_former_member_document.go` | `former_member_document.go` |
| `scale_p21_sw84_project_visibility_test.go` | `project_visibility_test.go` |
| `scale_p21_sw9_identity_test.go` | `access_identity_sw9_test.go` |

**`api/internal/store` (`store`)**

| Current | Proposed |
| --- | --- |
| `scale_p01_permission_test.go` | `permission_test.go` |
| `scale_p02_session_touch_test.go` | `session_touch_test.go` |
| `scale_p12_metadata_scopes.go` | `metadata_scopes.go` |
| `scale_p17_reference_fallback.go` | `reference_fallback.go` |
| `scale_p21_attachment_visibility_read.go` | `attachment_visibility_read.go` |
| `scale_p21_default_viewer.go` | `default_viewer.go` |
| `scale_p21_former_member_logical_read.go` | `former_member_logical_read.go` |
| `scale_p21_issue_authorization_policy.go` | `issue_authorization_policy.go` |
| `scale_p21_issue_authorization_read_test.go` | `issue_authorization_read_test.go` |
| `scale_p21_issue_authorization_read.go` | `issue_authorization_read.go` |
| `scale_p21_issue_authorization_rows.go` | `issue_authorization_rows.go` |
| `scale_p21_issue_record_authorization_release.go` | `issue_record_authorization_release.go` |
| `scale_p21_migrate_oauth_test.go` | `migrate_oauth_test.go` |
| `scale_p21_migrate_oauth.go` | `migrate_oauth.go` |
| `scale_p21_migrate_policy_test.go` | `migrate_policy_test.go` |
| `scale_p21_oauth_application_client_lookup.go` | `oauth_application_client_lookup.go` |
| `scale_p21_oauth_applications_read.go` | `oauth_applications_read.go` |
| `scale_p21_oauth_logo_read_test.go` | `oauth_logo_read_test.go` |
| `scale_p21_oauth_logo_read.go` | `oauth_logo_read.go` |
| `scale_p21_oauth_metadata_write.go` | `oauth_metadata_write.go` |
| `scale_p21_permission_test.go` | `access_permission_test.go` |
| `scale_p21_policy_headers.go` | `policy_headers.go` |
| `scale_p21_policy_work.go` | `policy_work.go` |
| `scale_p21_project_visibility.go` | `project_visibility.go` |
| `scale_p21_record_authorization_read.go` | `record_authorization_read.go` |
| `scale_p21_reference_visibility.go` | `reference_visibility.go` |
| `scale_p21_resolved_access.go` | `resolved_access.go` |
| `scale_p21_resource_authorization_read.go` | `resource_authorization_read.go` |
| `scale_p21_resource_policy_memo_test.go` | `resource_policy_memo_test.go` |
| `scale_p21_resource_policy_memo.go` | `resource_policy_memo.go` |
| `scale_p21_scoped_access_test.go` | `scoped_access_test.go` |
| `scale_p21_scoped_access.go` | `scoped_access.go` |
| `scale_p21_startup_oauth_applications.go` | `startup_oauth_applications.go` |
| `scale_p21_sw0_policy_headers_test.go` | `policy_headers_test.go` |
| `scale_p21_sw0_scoped_identity_test.go` | `scoped_identity_test.go` |
| `scale_p21_sw114_issue_permission_value_test.go` | `issue_permission_value_test.go` |
| `scale_p21_sw118_default_viewer_test.go` | `default_viewer_test.go` |
| `scale_p21_sw137_installed_policy_memo_test.go` | `installed_policy_memo_test.go` |
| `scale_p21_sw15_installed_viewer_wire_test.go` | `installed_viewer_wire_test.go` |
| `scale_p21_sw16_former_members_test.go` | `former_members_test.go` |
| `scale_p21_sw16_former_members.go` | `former_members.go` |
| `scale_p21_sw17_auth_registry_test.go` | `auth_registry_test.go` |
| `scale_p21_sw17_credential_records_test.go` | `credential_records_test.go` |
| `scale_p21_sw17_credential_records.go` | `credential_records.go` |
| `scale_p21_sw17_oauth_test.go` | `oauth_test.go` |
| `scale_p21_sw2_auth_policy_test.go` | `auth_policy_test.go` |
| `scale_p21_sw2_auth_policy.go` | `auth_policy.go` |
| `scale_p21_sw20_installed_viewer_test.go` | `installed_viewer_test.go` |
| `scale_p21_sw20_installed_viewer.go` | `installed_viewer.go` |
| `scale_p21_sw20_team_policy_order_test.go` | `team_policy_order_test.go` |
| `scale_p21_sw20_team_purge_policy_test.go` | `team_purge_policy_test.go` |
| `scale_p21_sw20_team_purge_policy.go` | `team_purge_policy.go` |
| `scale_p21_sw21_personal_identity_test.go` | `personal_identity_test.go` |
| `scale_p21_sw21_personal_scrub_test.go` | `personal_scrub_test.go` |
| `scale_p21_sw21_personal_scrub.go` | `personal_scrub.go` |
| `scale_p21_sw211_former_member_logical_test.go` | `former_member_logical_test.go` |
| `scale_p21_sw22_visibility_keys_test.go` | `visibility_keys_test.go` |
| `scale_p21_sw22_visibility_keys.go` | `visibility_keys.go` |
| `scale_p21_sw23_team_policy_native_test.go` | `team_policy_native_test.go` |
| `scale_p21_sw308_resource_authorization_test.go` | `resource_authorization_test.go` |
| `scale_p21_sw310_team_policy_transport_test.go` | `team_policy_transport_test.go` |
| `scale_p21_sw411_oauth_client_lookup_test.go` | `oauth_client_lookup_test.go` |
| `scale_p21_sw431_oauth_exchange_test.go` | `oauth_exchange_test.go` |
| `scale_p21_sw49_issue_authorization_policy_test.go` | `issue_authorization_policy_test.go` |
| `scale_p21_sw542_oauth_inline_test.go` | `oauth_inline_test.go` |
| `scale_p21_sw82_project_visibility_test.go` | `project_visibility_test.go` |
| `scale_p21_team_policy_physical_keys_test.go` | `team_policy_physical_keys_test.go` |

### Workspace Administration

Account and team directories, workspace settings, erasure and administrative writes.

**`api/cmd/server` (`main`)**

| Current | Proposed |
| --- | --- |
| `scale_p21_sw125_account_routes_test.go` | `account_routes_test.go` |
| `scale_p21_sw127_account_settings_test.go` | `account_settings_test.go` |
| `scale_p21_sw20_team_collision_test.go` | `team_collision_test.go` |
| `scale_p21_sw22_customer_taxonomy_test.go` | `customer_taxonomy_test.go` |
| `scale_p21_sw22_team_handler_test.go` | `team_handler_test.go` |
| `scale_p21_sw22_workspace_purge_test.go` | `workspace_purge_test.go` |
| `scale_p21_sw280_team_create_membership_race_test.go` | `team_create_membership_race_test.go` |
| `scale_p21_sw43_member_directory_test.go` | `member_directory_test.go` |
| `scale_p21_sw555_workspace_rename_test.go` | `workspace_rename_test.go` |
| `scale_p21_sw601_team_settings_read_test.go` | `team_settings_read_test.go` |
| `scale_p21_sw602_account_settings_test.go` | `workspace_account_settings_test.go` |
| `scale_p21_sw602_new_workspace_test.go` | `new_workspace_test.go` |
| `scale_p21_team_create_test.go` | `team_create_test.go` |
| `scale_p21_team_create.go` | `team_create.go` |
| `scale_p21_team_settings_read.go` | `team_settings_read.go` |

**`api/internal/store` (`store`)**

| Current | Proposed |
| --- | --- |
| `scale_p09_account_write.go` | `account_write.go` |
| `scale_p21_account_collection_read.go` | `account_collection_read.go` |
| `scale_p21_account_read.go` | `account_read.go` |
| `scale_p21_legacy_actor_directory.go` | `legacy_actor_directory.go` |
| `scale_p21_legacy_actor_image.go` | `legacy_actor_image.go` |
| `scale_p21_legacy_display_image.go` | `legacy_display_image.go` |
| `scale_p21_member_directory.go` | `workspace_member_directory.go` |
| `scale_p21_sw101_legacy_actor_directory_test.go` | `legacy_actor_directory_test.go` |
| `scale_p21_sw122_account_collection_read_test.go` | `account_collection_read_test.go` |
| `scale_p21_sw123_account_read_test.go` | `account_read_test.go` |
| `scale_p21_sw19_scim_teams_test.go` | `scim_teams_test.go` |
| `scale_p21_sw19_scim_teams.go` | `scim_teams.go` |
| `scale_p21_sw22_team_read_test.go` | `team_read_test.go` |
| `scale_p21_sw22_team_read.go` | `team_read.go` |
| `scale_p21_sw23_team_projection_test.go` | `team_projection_test.go` |
| `scale_p21_sw23_team_projection.go` | `team_projection.go` |
| `scale_p21_sw26_workspace_keys_test.go` | `workspace_keys_test.go` |
| `scale_p21_sw26_workspace_keys.go` | `workspace_keys.go` |
| `scale_p21_sw602_created_workspace_test.go` | `created_workspace_test.go` |
| `scale_p21_sw69_legacy_actor_image_test.go` | `legacy_actor_image_test.go` |
| `scale_p21_team_create_write.go` | `team_create_write.go` |
| `scale_p21_team_maintenance.go` | `team_maintenance.go` |
| `scale_p21_team_member_request.go` | `team_member_request.go` |
| `scale_p21_team_settings_read.go` | `team_settings_read.go` |
| `scale_p21_user_settings_write.go` | `user_settings_write.go` |

### Issue Records

Issue reads, lists and mutation inputs, moves, frames and related resource projections.

**`api/cmd/server` (`main`)**

| Current | Proposed |
| --- | --- |
| `scale_p16_record_suggestions.go` | `record_suggestions.go` |
| `scale_p19_initiatives.go` | `initiatives.go` |
| `scale_p19_query.go` | `project_initiative_query.go` |
| `scale_p21_issue_attachment_context.go` | `issue_attachment_context.go` |
| `scale_p21_issue_batch_test.go` | `issue_batch_test.go` |
| `scale_p21_issue_collaboration_join_test.go` | `issue_collaboration_join_test.go` |
| `scale_p21_issue_create_targets.go` | `issue_create_targets.go` |
| `scale_p21_issue_editor_boundary_test.go` | `issue_editor_boundary_test.go` |
| `scale_p21_issue_query_access.go` | `issue_query_access.go` |
| `scale_p21_issue_update_targets.go` | `issue_update_targets.go` |
| `scale_p21_issue_update_test.go` | `issue_update_test.go` |
| `scale_p21_label_directory.go` | `label_directory.go` |
| `scale_p21_label_scopes.go` | `label_scopes.go` |
| `scale_p21_project_update_test.go` | `project_update_test.go` |
| `scale_p21_public_label_record_test.go` | `public_label_record_test.go` |
| `scale_p21_resource_relation_list_test.go` | `resource_relation_list_test.go` |
| `scale_p21_sw106_discussion_routes_test.go` | `discussion_routes_test.go` |
| `scale_p21_sw117_issue_reaction_routes_test.go` | `issue_reaction_routes_test.go` |
| `scale_p21_sw133_retained_upload_release_test.go` | `retained_upload_release_test.go` |
| `scale_p21_sw150_issue_attachment_routes_test.go` | `issue_attachment_routes_test.go` |
| `scale_p21_sw16_project_template_writes_test.go` | `project_template_writes_test.go` |
| `scale_p21_sw16_project_templates_test.go` | `project_templates_test.go` |
| `scale_p21_sw16_project_templates.go` | `project_templates.go` |
| `scale_p21_sw184_issue_batch_admission_test.go` | `issue_batch_admission_test.go` |
| `scale_p21_sw185_triage_admission_test.go` | `triage_admission_test.go` |
| `scale_p21_sw185_triage_members_test.go` | `triage_members_test.go` |
| `scale_p21_sw186_issue_creation_test.go` | `issue_creation_test.go` |
| `scale_p21_sw20_label_closure_test.go` | `label_closure_test.go` |
| `scale_p21_sw200_issue_releases_test.go` | `issue_releases_test.go` |
| `scale_p21_sw210_issue_relation_routes_test.go` | `issue_relation_routes_test.go` |
| `scale_p21_sw214_issue_update_snapshot_move_test.go` | `issue_update_snapshot_move_test.go` |
| `scale_p21_sw25_release_source_test.go` | `release_source_test.go` |
| `scale_p21_sw25_release_test.go` | `release_test.go` |
| `scale_p21_sw318_legacy_issue_denial_test.go` | `legacy_issue_denial_test.go` |
| `scale_p21_sw318_legacy_issue_denial.go` | `legacy_issue_denial.go` |
| `scale_p21_sw400_triage_batch_test.go` | `triage_batch_test.go` |
| `scale_p21_sw401_triage_member_snapshots_test.go` | `triage_member_snapshots_test.go` |
| `scale_p21_sw422_large_issue_releases_test.go` | `large_issue_releases_test.go` |
| `scale_p21_sw440_triage_writer_budget_test.go` | `triage_writer_budget_test.go` |
| `scale_p21_sw460_triage_leftovers_test.go` | `triage_leftovers_test.go` |
| `scale_p21_sw464_triage_scan_test.go` | `triage_scan_test.go` |
| `scale_p21_sw59_discussion_write_test.go` | `discussion_write_test.go` |
| `scale_p21_sw600_label_routes_test.go` | `label_routes_test.go` |
| `scale_p21_sw87_project_list_test.go` | `project_list_test.go` |
| `scale_p21_triage_suggestions_test.go` | `triage_suggestions_test.go` |
| `scale_p21_triage_suggestions.go` | `triage_suggestions.go` |
| `scale_p21_triage_writer_test.go` | `triage_writer_test.go` |

**`api/internal/store` (`store`)**

| Current | Proposed |
| --- | --- |
| `scale_p18_project_reads.go` | `project_reads.go` |
| `scale_p21_bounded_discussion_write.go` | `bounded_discussion_write.go` |
| `scale_p21_discussion_inputs.go` | `discussion_inputs.go` |
| `scale_p21_discussion_metadata_write.go` | `discussion_metadata_write.go` |
| `scale_p21_discussion_ordinal_scope.go` | `discussion_ordinal_scope.go` |
| `scale_p21_discussion_rows.go` | `discussion_rows.go` |
| `scale_p21_hot_issue_inputs_test.go` | `hot_issue_inputs_test.go` |
| `scale_p21_hot_issue_inputs.go` | `hot_issue_inputs.go` |
| `scale_p21_issue_attachment_write.go` | `issue_attachment_write.go` |
| `scale_p21_issue_batch_write.go` | `issue_batch_write.go` |
| `scale_p21_issue_callback_lookups_test.go` | `issue_callback_lookups_test.go` |
| `scale_p21_issue_callback_lookups.go` | `issue_callback_lookups.go` |
| `scale_p21_issue_candidate_rows.go` | `issue_candidate_rows.go` |
| `scale_p21_issue_child_content_rows.go` | `issue_child_content_rows.go` |
| `scale_p21_issue_create_admission.go` | `issue_create_admission.go` |
| `scale_p21_issue_create_inputs.go` | `issue_create_inputs.go` |
| `scale_p21_issue_create_read.go` | `issue_create_read.go` |
| `scale_p21_issue_create_rows.go` | `issue_create_rows.go` |
| `scale_p21_issue_create_write.go` | `issue_create_write.go` |
| `scale_p21_issue_document_id.go` | `issue_document_id.go` |
| `scale_p21_issue_editor_lookups_test.go` | `issue_editor_lookups_test.go` |
| `scale_p21_issue_editor_lookups.go` | `issue_editor_lookups.go` |
| `scale_p21_issue_facet_read.go` | `issue_facet_read.go` |
| `scale_p21_issue_frame_compare.go` | `issue_frame_compare.go` |
| `scale_p21_issue_frame_values.go` | `issue_frame_values.go` |
| `scale_p21_issue_hot_metadata.go` | `issue_hot_metadata.go` |
| `scale_p21_issue_mutation_rows.go` | `issue_mutation_rows.go` |
| `scale_p21_issue_persistence_test.go` | `issue_persistence_test.go` |
| `scale_p21_issue_progress_work_test.go` | `issue_progress_work_test.go` |
| `scale_p21_issue_progress_work.go` | `issue_progress_work.go` |
| `scale_p21_issue_project_inputs_test.go` | `issue_project_inputs_test.go` |
| `scale_p21_issue_project_inputs.go` | `issue_project_inputs.go` |
| `scale_p21_issue_project_persistence.go` | `issue_project_persistence.go` |
| `scale_p21_issue_project_postimage.go` | `issue_project_postimage.go` |
| `scale_p21_issue_reaction_write.go` | `issue_reaction_write.go` |
| `scale_p21_issue_relation_write.go` | `issue_relation_write.go` |
| `scale_p21_issue_releases_inputs.go` | `issue_releases_inputs.go` |
| `scale_p21_issue_releases_write.go` | `issue_releases_write.go` |
| `scale_p21_issue_results.go` | `issue_results.go` |
| `scale_p21_issue_team_move.go` | `issue_team_move.go` |
| `scale_p21_issue_update_content_rows.go` | `issue_update_content_rows.go` |
| `scale_p21_issue_update_move.go` | `issue_update_move.go` |
| `scale_p21_issue_update_snapshot_move.go` | `issue_update_snapshot_move.go` |
| `scale_p21_issue_update_targets.go` | `issue_update_targets.go` |
| `scale_p21_issue_workflow_states_test.go` | `issue_workflow_states_test.go` |
| `scale_p21_issue_workflow_states.go` | `issue_workflow_states.go` |
| `scale_p21_label_deletion_write.go` | `label_deletion_write.go` |
| `scale_p21_label_issue_counts.go` | `label_issue_counts.go` |
| `scale_p21_legacy_discussion_inputs.go` | `legacy_discussion_inputs.go` |
| `scale_p21_legacy_discussion_write.go` | `legacy_discussion_write.go` |
| `scale_p21_legacy_editor_lookups.go` | `legacy_editor_lookups.go` |
| `scale_p21_legacy_issue_canonical.go` | `legacy_issue_canonical.go` |
| `scale_p21_legacy_issue_frame_history.go` | `legacy_issue_frame_history.go` |
| `scale_p21_legacy_issue_frame_inputs.go` | `legacy_issue_frame_inputs.go` |
| `scale_p21_legacy_issue_frame_persist.go` | `legacy_issue_frame_persist.go` |
| `scale_p21_legacy_issue_frame_scan.go` | `legacy_issue_frame_scan.go` |
| `scale_p21_legacy_issue_history.go` | `legacy_issue_history.go` |
| `scale_p21_legacy_issue_image.go` | `legacy_issue_image.go` |
| `scale_p21_legacy_issue_scan.go` | `legacy_issue_scan.go` |
| `scale_p21_legacy_progress_image.go` | `legacy_progress_image.go` |
| `scale_p21_project_directory_query.go` | `project_directory_query.go` |
| `scale_p21_project_progress_stream.go` | `project_progress_stream.go` |
| `scale_p21_project_update_parent_map.go` | `project_update_parent_map.go` |
| `scale_p21_project_update_schedule_stream.go` | `project_update_schedule_stream.go` |
| `scale_p21_resource_relation_read_test.go` | `resource_relation_read_test.go` |
| `scale_p21_resource_relation_read.go` | `resource_relation_read.go` |
| `scale_p21_shared_issue_read.go` | `shared_issue_read.go` |
| `scale_p21_sw116_issue_reaction_write_test.go` | `issue_reaction_write_test.go` |
| `scale_p21_sw119_legacy_progress_image_test.go` | `legacy_progress_image_test.go` |
| `scale_p21_sw149_issue_attachment_write_test.go` | `issue_attachment_write_test.go` |
| `scale_p21_sw16_project_templates_test.go` | `project_templates_test.go` |
| `scale_p21_sw16_project_templates.go` | `project_templates.go` |
| `scale_p21_sw17_issue_attribute_checkpoint_test.go` | `issue_attribute_checkpoint_test.go` |
| `scale_p21_sw17_issue_attribute_migration_test.go` | `issue_attribute_migration_test.go` |
| `scale_p21_sw17_issue_attribute_migration.go` | `issue_attribute_migration.go` |
| `scale_p21_sw184_issue_batch_admission_test.go` | `issue_batch_admission_test.go` |
| `scale_p21_sw185_triage_materialization_test.go` | `triage_materialization_test.go` |
| `scale_p21_sw19_issue_stats_test.go` | `issue_stats_sw19_test.go` |
| `scale_p21_sw192_issue_candidate_rows_test.go` | `issue_candidate_rows_test.go` |
| `scale_p21_sw196_discussion_scope_test.go` | `discussion_scope_test.go` |
| `scale_p21_sw197_project_progress_stream_test.go` | `project_progress_stream_test.go` |
| `scale_p21_sw202_project_display_stream_test.go` | `project_display_stream_test.go` |
| `scale_p21_sw210_issue_relation_validation_test.go` | `issue_relation_validation_test.go` |
| `scale_p21_sw218_legacy_issue_work_test.go` | `legacy_issue_work_test.go` |
| `scale_p21_sw224_issue_encoding_ownership_test.go` | `issue_encoding_ownership_test.go` |
| `scale_p21_sw234_issue_canonical_depth_test.go` | `issue_canonical_depth_test.go` |
| `scale_p21_sw234_issue_canonical_test.go` | `issue_canonical_test.go` |
| `scale_p21_sw237_issue_frame_scan_test.go` | `issue_frame_scan_test.go` |
| `scale_p21_sw238_issue_frame_values_test.go` | `issue_frame_values_test.go` |
| `scale_p21_sw239_issue_metadata_retry_test.go` | `issue_metadata_retry_test.go` |
| `scale_p21_sw241_issue_frame_compare_test.go` | `issue_frame_compare_test.go` |
| `scale_p21_sw25_release_keys_test.go` | `release_keys_test.go` |
| `scale_p21_sw25_release_keys.go` | `release_keys.go` |
| `scale_p21_sw254_issue_transfer_test.go` | `issue_transfer_test.go` |
| `scale_p21_sw258_issue_metadata_large_retry_test.go` | `issue_metadata_large_retry_test.go` |
| `scale_p21_sw318_issue_read_source_test.go` | `issue_read_source_test.go` |
| `scale_p21_sw318_issue_read_source.go` | `issue_read_source.go` |
| `scale_p21_sw347_issue_row_ownership_test.go` | `issue_row_ownership_test.go` |
| `scale_p21_sw57_issue_mutation_rows_test.go` | `issue_mutation_rows_test.go` |
| `scale_p21_sw58_discussion_metadata_write_test.go` | `discussion_metadata_write_test.go` |
| `scale_p21_sw60_discussion_rows_test.go` | `discussion_rows_test.go` |
| `scale_p21_sw61_discussion_inputs_test.go` | `discussion_inputs_test.go` |
| `scale_p21_sw63_discussion_writer_test.go` | `discussion_writer_test.go` |
| `scale_p21_sw66_issue_update_content_rows_test.go` | `issue_update_content_rows_test.go` |
| `scale_p21_sw67_issue_child_content_rows_test.go` | `issue_child_content_rows_test.go` |
| `scale_p21_sw70_legacy_issue_scan_test.go` | `legacy_issue_scan_test.go` |
| `scale_p21_sw72_legacy_issue_image_test.go` | `legacy_issue_image_test.go` |
| `scale_p21_sw90_project_directory_boundary_test.go` | `project_directory_boundary_test.go` |
| `scale_p21_sw90_project_directory_query_test.go` | `project_directory_query_test.go` |
| `scale_p21_sw91_legacy_discussion_inputs_test.go` | `legacy_discussion_inputs_test.go` |
| `scale_p21_sw98_legacy_discussion_write_test.go` | `legacy_discussion_write_test.go` |
| `scale_p21_sw99_legacy_editor_lookups_test.go` | `legacy_editor_lookups_test.go` |
| `scale_p21_triage_candidates.go` | `triage_candidates.go` |
| `scale_p21_triage_suggestion_history.go` | `triage_suggestion_history.go` |
| `scale_p21_triage_suggestion_read.go` | `triage_suggestion_read.go` |
| `scale_p21_triage_suggestion_write.go` | `triage_suggestion_write.go` |

### Content Records

Documents, attachments, resource listings, collaboration content and their persistence.

**`api/cmd/server` (`main`)**

| Current | Proposed |
| --- | --- |
| `scale_p03_resource_access_test.go` | `resource_access_test.go` |
| `scale_p17_document_list.go` | `document_list.go` |
| `scale_p20_resource_reads.go` | `resource_reads.go` |
| `scale_p21_collaboration_header_cache.go` | `collaboration_header_cache.go` |
| `scale_p21_collaboration_mentions.go` | `collaboration_mentions.go` |
| `scale_p21_doc_routes_test.go` | `doc_routes_test.go` |
| `scale_p21_document_directory_test.go` | `document_directory_test.go` |
| `scale_p21_document_work_budget_test.go` | `document_work_budget_test.go` |
| `scale_p21_hot_document_read_test.go` | `hot_document_read_test.go` |
| `scale_p21_public_resource_records_test.go` | `public_resource_records_test.go` |
| `scale_p21_request_resource_read.go` | `request_resource_read.go` |
| `scale_p21_resource_connections_test.go` | `resource_connections_test.go` |
| `scale_p21_resource_request_test.go` | `resource_request_test.go` |
| `scale_p21_sw0_upload_test.go` | `upload_test.go` |
| `scale_p21_sw104_document_event_source_test.go` | `document_event_source_test.go` |
| `scale_p21_sw124_resource_window_routes_test.go` | `resource_window_routes_test.go` |
| `scale_p21_sw130_document_auxiliary_routes_test.go` | `document_auxiliary_routes_test.go` |
| `scale_p21_sw178_collaboration_document_test.go` | `collaboration_document_test.go` |
| `scale_p21_sw179_collaboration_document_read_test.go` | `collaboration_document_read_test.go` |
| `scale_p21_sw18_team_resources.go` | `team_resources.go` |
| `scale_p21_sw180_collaboration_header_cache_test.go` | `collaboration_header_cache_test.go` |
| `scale_p21_sw211_legacy_collection_boundary_test.go` | `legacy_collection_boundary_test.go` |
| `scale_p21_sw24_export_reads.go` | `export_reads.go` |
| `scale_p21_sw24_import_source_hold_test.go` | `import_source_hold_test.go` |
| `scale_p21_sw346_residual_producer_upload_test.go` | `residual_producer_upload_test.go` |
| `scale_p21_sw480_document_update_test.go` | `document_update_test.go` |

**`api/internal/store` (`store`)**

| Current | Proposed |
| --- | --- |
| `scale_p03_resource_access_test.go` | `resource_access_test.go` |
| `scale_p03_resource_records.go` | `resource_records.go` |
| `scale_p06_collaboration_pages.go` | `collaboration_pages.go` |
| `scale_p12_document_decode.go` | `document_decode.go` |
| `scale_p16_document_cascade.go` | `document_cascade.go` |
| `scale_p17_document_directory.go` | `document_directory.go` |
| `scale_p17_trash_headers.go` | `trash_headers.go` |
| `scale_p20_resource_reads_test.go` | `resource_reads_test.go` |
| `scale_p20_resource_reads.go` | `resource_reads.go` |
| `scale_p21_collaboration_document_read.go` | `collaboration_document_read.go` |
| `scale_p21_collection_read.go` | `collection_read.go` |
| `scale_p21_document_auxiliary_read.go` | `document_auxiliary_read.go` |
| `scale_p21_document_directory_read_test.go` | `document_directory_read_test.go` |
| `scale_p21_document_directory_read.go` | `document_directory_read.go` |
| `scale_p21_document_read.go` | `document_read.go` |
| `scale_p21_document_update_write.go` | `document_update_write.go` |
| `scale_p21_hot_document_read_test.go` | `hot_document_read_test.go` |
| `scale_p21_hot_document_write_test.go` | `hot_document_write_test.go` |
| `scale_p21_import_inputs.go` | `content_import_inputs.go` |
| `scale_p21_installed_resource_point_read_test.go` | `installed_resource_point_read_test.go` |
| `scale_p21_installed_resource_point_read.go` | `installed_resource_point_read.go` |
| `scale_p21_legacy_content_image.go` | `legacy_content_image.go` |
| `scale_p21_legacy_import_inputs.go` | `legacy_import_inputs.go` |
| `scale_p21_resource_adapter_test.go` | `resource_adapter_test.go` |
| `scale_p21_resource_directory_read_test.go` | `resource_directory_read_test.go` |
| `scale_p21_resource_directory_read.go` | `resource_directory_read.go` |
| `scale_p21_resource_inline_source.go` | `resource_inline_source.go` |
| `scale_p21_resource_read_test.go` | `resource_read_test.go` |
| `scale_p21_resource_read.go` | `resource_read.go` |
| `scale_p21_resource_window_read.go` | `resource_window_read.go` |
| `scale_p21_retained_upload_read.go` | `retained_upload_read.go` |
| `scale_p21_retry_document_canonical.go` | `retry_document_canonical.go` |
| `scale_p21_sw0_collection_properties_test.go` | `collection_properties_test.go` |
| `scale_p21_sw0_collection_read_test.go` | `collection_read_test.go` |
| `scale_p21_sw100_legacy_import_inputs_test.go` | `legacy_import_inputs_test.go` |
| `scale_p21_sw121_resource_window_read_test.go` | `resource_window_read_test.go` |
| `scale_p21_sw129_document_auxiliary_failure_test.go` | `document_auxiliary_failure_test.go` |
| `scale_p21_sw129_document_auxiliary_read_test.go` | `document_auxiliary_read_test.go` |
| `scale_p21_sw132_retained_upload_read_test.go` | `retained_upload_read_test.go` |
| `scale_p21_sw16_attachment_migration_test.go` | `attachment_migration_test.go` |
| `scale_p21_sw16_attachment_source_hold_test.go` | `attachment_source_hold_test.go` |
| `scale_p21_sw16_attachment_typed_test.go` | `attachment_typed_test.go` |
| `scale_p21_sw16_attachment_typed.go` | `attachment_typed.go` |
| `scale_p21_sw168_document_alias_header_test.go` | `document_alias_header_test.go` |
| `scale_p21_sw18_team_resources.go` | `team_resources.go` |
| `scale_p21_sw181_collaboration_transient_test.go` | `collaboration_transient_test.go` |
| `scale_p21_sw26_resource_slug_test.go` | `resource_slug_test.go` |
| `scale_p21_sw302_retry_document_codec_test.go` | `retry_document_codec_test.go` |
| `scale_p21_sw307_team_resource_read_test.go` | `team_resource_read_test.go` |
| `scale_p21_sw307_team_resource_read.go` | `team_resource_read.go` |
| `scale_p21_sw328_collection_root_patch_test.go` | `collection_root_patch_test.go` |
| `scale_p21_sw351_content_row_ownership_test.go` | `content_row_ownership_test.go` |
| `scale_p21_sw353_collection_ownership_test.go` | `collection_ownership_test.go` |
| `scale_p21_sw481_document_update_profile_test.go` | `document_update_profile_test.go` |
| `scale_p21_sw51_import_inputs_test.go` | `import_inputs_test.go` |
| `scale_p21_sw540_resource_inline_test.go` | `resource_inline_test.go` |
| `scale_p21_sw74_legacy_content_image_test.go` | `legacy_content_image_test.go` |

### Notifications And Settings

Personal settings, notification state, delivery context and device acknowledgements.

**`api/cmd/server` (`main`)**

| Current | Proposed |
| --- | --- |
| `scale_p14_credential_notification_state_test.go` | `credential_notification_state_test.go` |
| `scale_p14_notification_state_test.go` | `notification_state_test.go` |
| `scale_p21_account_collection_settings.go` | `account_collection_settings.go` |
| `scale_p21_hot_notification_handler_test.go` | `hot_notification_handler_test.go` |
| `scale_p21_hot_preference_review_test.go` | `hot_preference_review_test.go` |
| `scale_p21_hot_pulse_events_test.go` | `hot_pulse_events_test.go` |
| `scale_p21_hot_pulse_group_test.go` | `hot_pulse_group_test.go` |
| `scale_p21_hot_pulse_test.go` | `hot_pulse_test.go` |
| `scale_p21_m3_settings_test.go` | `settings_test.go` |
| `scale_p21_pulse_history_test.go` | `pulse_history_test.go` |
| `scale_p21_pulse_remaining_test.go` | `pulse_remaining_test.go` |
| `scale_p21_pulse_summary_read_test.go` | `pulse_summary_read_test.go` |
| `scale_p21_pulse_summary_write_test.go` | `pulse_summary_write_test.go` |
| `scale_p21_sw135_desktop_ack_routes_test.go` | `desktop_ack_routes_test.go` |
| `scale_p21_sw15_notification_read_test.go` | `notification_read_test.go` |
| `scale_p21_sw3_settings_budget_test.go` | `settings_budget_test.go` |
| `scale_p21_sw42_notification_point_test.go` | `notification_point_test.go` |
| `scale_p21_sw5_settings_test.go` | `settings_settings_test.go` |
| `scale_p21_sw55_notification_card_test.go` | `notification_card_test.go` |

**`api/internal/store` (`store`)**

| Current | Proposed |
| --- | --- |
| `scale_p14_notification_state_test.go` | `notification_state_test.go` |
| `scale_p21_delivery_context.go` | `delivery_context.go` |
| `scale_p21_desktop_ack_write.go` | `desktop_ack_write.go` |
| `scale_p21_hot_delivery_context_test.go` | `hot_delivery_context_test.go` |
| `scale_p21_hot_pulse_group_test.go` | `hot_pulse_group_test.go` |
| `scale_p21_hot_pulse_read.go` | `hot_pulse_read.go` |
| `scale_p21_notification_visibility.go` | `notification_visibility.go` |
| `scale_p21_pulse_legacy_boundary_test.go` | `pulse_legacy_boundary_test.go` |
| `scale_p21_pulse_summary_write_test.go` | `pulse_summary_write_test.go` |
| `scale_p21_pulse_summary_write.go` | `pulse_summary_write.go` |
| `scale_p21_slack_notification_read_test.go` | `slack_notification_read_test.go` |
| `scale_p21_slack_notification_read.go` | `slack_notification_read.go` |
| `scale_p21_sw0_pulse_output_test.go` | `pulse_output_test.go` |
| `scale_p21_sw0_settings_test.go` | `settings_test.go` |
| `scale_p21_sw134_desktop_ack_write_test.go` | `desktop_ack_write_test.go` |
| `scale_p21_sw15_notification_read_test.go` | `notification_read_test.go` |
| `scale_p21_sw15_notification_read.go` | `notification_read.go` |
| `scale_p21_sw23_pulse_prune_test.go` | `pulse_prune_test.go` |
| `scale_p21_sw288_pulse_schedule_source_test.go` | `pulse_schedule_source_test.go` |

### Agent And Workflows

Agent sessions, comment actions, loop runs, schedules and recurrent work.

**`api/cmd/server` (`main`)**

| Current | Proposed |
| --- | --- |
| `scale_p21_agent_reply_write_test.go` | `agent_reply_write_test.go` |
| `scale_p21_agent_session_lifecycle.go` | `agent_session_lifecycle.go` |
| `scale_p21_comment_action_reads.go` | `comment_action_reads.go` |
| `scale_p21_comment_action_setting_start_test.go` | `comment_action_setting_start_test.go` |
| `scale_p21_comment_action_write_test.go` | `comment_action_write_test.go` |
| `scale_p21_comment_action_writes.go` | `comment_action_writes.go` |
| `scale_p21_hot_loop_tick_test.go` | `hot_loop_tick_test.go` |
| `scale_p21_loop_contract_busy_test.go` | `loop_contract_busy_test.go` |
| `scale_p21_loop_run_start.go` | `loop_run_start.go` |
| `scale_p21_m1_loop_finish_test.go` | `loop_finish_test.go` |
| `scale_p21_recurrence_normalization.go` | `recurrence_normalization.go` |
| `scale_p21_recurring_occurrence_tick.go` | `recurring_occurrence_tick.go` |
| `scale_p21_sw136_loop_text_test.go` | `loop_text_test.go` |
| `scale_p21_sw153_comment_action_reads_test.go` | `comment_action_reads_test.go` |
| `scale_p21_sw156_recurrence_normalization_test.go` | `recurrence_normalization_test.go` |
| `scale_p21_sw159_worker_reply_test.go` | `worker_reply_test.go` |
| `scale_p21_sw18_loop_source_test.go` | `loop_source_test.go` |
| `scale_p21_sw182_cycle_schedule_test.go` | `automation_cycle_schedule_test.go` |
| `scale_p21_sw198_agent_session_lifecycle_test.go` | `agent_session_lifecycle_test.go` |
| `scale_p21_sw201_workflow_write_test.go` | `workflow_write_test.go` |
| `scale_p21_sw202_delivery_worker_test.go` | `delivery_worker_test.go` |
| `scale_p21_sw206_recurring_occurrence_test.go` | `recurring_occurrence_test.go` |
| `scale_p21_sw209_advanced_schedule_test.go` | `advanced_schedule_test.go` |
| `scale_p21_sw23_loop_key_test.go` | `loop_key_test.go` |
| `scale_p21_sw24_worker_point_test.go` | `worker_point_test.go` |
| `scale_p21_sw24_worker_point.go` | `worker_point.go` |
| `scale_p21_sw260_loop_run_start_test.go` | `loop_run_start_test.go` |
| `scale_p21_sw291_loop_attachment_visibility_test.go` | `loop_attachment_visibility_test.go` |
| `scale_p21_sw322_workflow_matrix_test.go` | `workflow_matrix_test.go` |
| `scale_p21_sw338_recurring_large_test.go` | `recurring_large_test.go` |
| `scale_p21_sw342_workflow_tick_test.go` | `workflow_tick_test.go` |
| `scale_p21_sw421_loop_next_run_test.go` | `loop_next_run_test.go` |
| `scale_p21_sw500_workflow_close_test.go` | `workflow_close_test.go` |
| `scale_p21_sw501_workflow_tick_close_test.go` | `workflow_tick_close_test.go` |
| `scale_p21_sw552_loop_cold_work_test.go` | `loop_cold_work_test.go` |
| `scale_p21_sw570_loop_runs_read_test.go` | `loop_runs_read_test.go` |
| `scale_p21_sw580_workflow_point_test.go` | `workflow_point_test.go` |
| `scale_p21_worker_chat_sweep_test.go` | `worker_chat_sweep_test.go` |
| `scale_p21_workflow_schedule_tick.go` | `workflow_schedule_tick.go` |

**`api/internal/store` (`store`)**

| Current | Proposed |
| --- | --- |
| `scale_p21_advanced_schedule_fingerprint.go` | `advanced_schedule_fingerprint.go` |
| `scale_p21_advanced_schedule_inputs.go` | `advanced_schedule_inputs.go` |
| `scale_p21_advanced_schedule_scope.go` | `advanced_schedule_scope.go` |
| `scale_p21_advanced_schedule_write.go` | `advanced_schedule_write.go` |
| `scale_p21_agent_message_output.go` | `agent_message_output.go` |
| `scale_p21_agent_reply_write.go` | `agent_reply_write.go` |
| `scale_p21_agent_session_collection.go` | `agent_session_collection.go` |
| `scale_p21_agent_session_offload.go` | `agent_session_offload.go` |
| `scale_p21_agent_session_persist.go` | `agent_session_persist.go` |
| `scale_p21_agent_session_read_test.go` | `agent_session_read_test.go` |
| `scale_p21_agent_session_read.go` | `agent_session_read.go` |
| `scale_p21_agent_session_rebase.go` | `agent_session_rebase.go` |
| `scale_p21_agent_session_retry.go` | `agent_session_retry.go` |
| `scale_p21_agent_session_root.go` | `agent_session_root.go` |
| `scale_p21_agent_session_stream.go` | `agent_session_stream.go` |
| `scale_p21_agent_session_sync.go` | `agent_session_sync.go` |
| `scale_p21_agent_session_write.go` | `agent_session_write.go` |
| `scale_p21_comment_action_generation.go` | `comment_action_generation.go` |
| `scale_p21_comment_action_issue_projection.go` | `comment_action_issue_projection.go` |
| `scale_p21_comment_action_read.go` | `comment_action_read.go` |
| `scale_p21_comment_action_resolve.go` | `comment_action_resolve.go` |
| `scale_p21_comment_action_write.go` | `comment_action_write.go` |
| `scale_p21_cycle_schedule_inputs.go` | `cycle_schedule_inputs.go` |
| `scale_p21_cycle_schedule_native.go` | `cycle_schedule_native.go` |
| `scale_p21_cycle_schedule_write.go` | `cycle_schedule_write.go` |
| `scale_p21_hot_loop_schedule_test.go` | `hot_loop_schedule_test.go` |
| `scale_p21_issue_recurrence_timing.go` | `issue_recurrence_timing.go` |
| `scale_p21_loop_run_start.go` | `loop_run_start.go` |
| `scale_p21_loop_schedule.go` | `loop_schedule.go` |
| `scale_p21_recurrence_normalization_read.go` | `recurrence_normalization_read.go` |
| `scale_p21_recurring_occurrence_inputs.go` | `recurring_occurrence_inputs.go` |
| `scale_p21_recurring_occurrence_write.go` | `recurring_occurrence_write.go` |
| `scale_p21_sw11_agent_owner_test.go` | `agent_owner_test.go` |
| `scale_p21_sw11_agent_owner.go` | `agent_owner.go` |
| `scale_p21_sw152_comment_action_read_test.go` | `comment_action_read_test.go` |
| `scale_p21_sw154_comment_action_issue_headers_test.go` | `comment_action_issue_headers_test.go` |
| `scale_p21_sw155_issue_recurrence_timing_test.go` | `issue_recurrence_timing_test.go` |
| `scale_p21_sw157_recurrence_normalization_read_test.go` | `recurrence_normalization_read_test.go` |
| `scale_p21_sw16_runtime_lookup_test.go` | `agent_session_draft_loop_lookup_test.go` |
| `scale_p21_sw16_runtime_lookup.go` | `agent_session_draft_loop_lookup.go` |
| `scale_p21_sw18_worker_source_test.go` | `worker_source_test.go` |
| `scale_p21_sw198_agent_session_stream_test.go` | `agent_session_stream_test.go` |
| `scale_p21_sw198_agent_session_write_test.go` | `agent_session_write_test.go` |
| `scale_p21_sw219_workflow_stream_test.go` | `workflow_stream_test.go` |
| `scale_p21_sw23_loop_exists_test.go` | `loop_exists_test.go` |
| `scale_p21_sw23_loop_exists.go` | `loop_exists.go` |
| `scale_p21_sw23_loop_team_key_test.go` | `loop_team_key_test.go` |
| `scale_p21_sw232_workflow_action_projection_test.go` | `workflow_action_projection_test.go` |
| `scale_p21_sw261_loop_run_start_test.go` | `loop_run_start_test.go` |
| `scale_p21_sw420_large_loop_run_stamp_test.go` | `large_loop_run_stamp_test.go` |
| `scale_p21_workflow_run_inputs.go` | `workflow_run_inputs.go` |
| `scale_p21_workflow_run_point.go` | `workflow_run_point.go` |
| `scale_p21_workflow_run_stream.go` | `workflow_run_stream.go` |
| `scale_p21_workflow_run_write.go` | `workflow_run_write.go` |
| `scale_p21_workflow_schedule_read.go` | `workflow_schedule_read.go` |

### Integrations

Application access, inbound and outbound delivery, integration records, webhooks and chat bindings.

**`api/cmd/server` (`main`)**

| Current | Proposed |
| --- | --- |
| `scale_p21_application_access_test.go` | `application_access_test.go` |
| `scale_p21_application_access.go` | `application_access.go` |
| `scale_p21_application_task_context_test.go` | `application_task_context_test.go` |
| `scale_p21_application_webhook_sweep_test.go` | `application_webhook_sweep_test.go` |
| `scale_p21_chat_bootstrap_headers.go` | `chat_bootstrap_headers.go` |
| `scale_p21_chat_card_metadata.go` | `chat_card_metadata.go` |
| `scale_p21_chat_event_policy_test.go` | `chat_event_policy_test.go` |
| `scale_p21_chat_point_read_test.go` | `chat_point_read_test.go` |
| `scale_p21_chat_point_read.go` | `chat_point_read.go` |
| `scale_p21_chat_request_result_test.go` | `chat_request_result_test.go` |
| `scale_p21_chat_request_status_test.go` | `chat_request_status_test.go` |
| `scale_p21_chat_unlink_test.go` | `chat_unlink_test.go` |
| `scale_p21_hot_chat_sources_test.go` | `hot_chat_sources_test.go` |
| `scale_p21_hot_delivery_email_test.go` | `hot_delivery_email_test.go` |
| `scale_p21_hot_webhook_delivery_test.go` | `hot_webhook_delivery_test.go` |
| `scale_p21_hot_webhook_disable_test.go` | `hot_webhook_disable_test.go` |
| `scale_p21_hot_webhook_failure_test.go` | `hot_webhook_failure_test.go` |
| `scale_p21_integration_delivery.go` | `integration_delivery.go` |
| `scale_p21_integration_envelope_owned.go` | `integration_envelope_owned.go` |
| `scale_p21_m3_coding_test.go` | `coding_test.go` |
| `scale_p21_m3_integration_test.go` | `integration_test.go` |
| `scale_p21_sw15_email_intake_test.go` | `email_intake_test.go` |
| `scale_p21_sw170_chat_headers_test.go` | `chat_headers_test.go` |
| `scale_p21_sw171_chat_dm_reply_owner_test.go` | `chat_dm_reply_owner_test.go` |
| `scale_p21_sw173_chat_link_headers_test.go` | `chat_link_headers_test.go` |
| `scale_p21_sw175_chat_inbound_headers_test.go` | `chat_inbound_headers_test.go` |
| `scale_p21_sw176_chat_thread_reply_test.go` | `chat_thread_reply_test.go` |
| `scale_p21_sw18_webhook_credentials.go` | `webhook_credentials.go` |
| `scale_p21_sw19_chat_policy_test.go` | `chat_policy_test.go` |
| `scale_p21_sw19_gitlab_rotation_test.go` | `gitlab_rotation_test.go` |
| `scale_p21_sw19_gitlab_source_test.go` | `gitlab_source_test.go` |
| `scale_p21_sw197_chat_mutations_test.go` | `chat_mutations_test.go` |
| `scale_p21_sw20_application_identity_test.go` | `integration_application_identity_test.go` |
| `scale_p21_sw20_jira_source_test.go` | `jira_source_test.go` |
| `scale_p21_sw228_integration_ownership_test.go` | `integration_ownership_test.go` |
| `scale_p21_sw24_application_source_audit_test.go` | `application_source_audit_test.go` |
| `scale_p21_sw24_github_test.go` | `github_test.go` |
| `scale_p21_sw295_application_webhook_reconcile_test.go` | `application_webhook_reconcile_test.go` |
| `scale_p21_sw345_integration_raw_utf8_test.go` | `integration_raw_utf8_test.go` |
| `scale_p21_sw40_dm_point_test.go` | `dm_point_test.go` |
| `scale_p21_sw44_chat_job_scope_test.go` | `chat_job_scope_test.go` |
| `scale_p21_sw45_delivery_audit_test.go` | `delivery_audit_test.go` |
| `scale_p21_sw46_chat_health_test.go` | `chat_health_test.go` |
| `scale_p21_sw5_integration_oauth_test.go` | `integration_oauth_test.go` |
| `scale_p21_sw51_chat_group_bindings_test.go` | `chat_group_bindings_test.go` |
| `scale_p21_sw510_delivery_outcomes_test.go` | `delivery_outcomes_test.go` |
| `scale_p21_sw53_chat_workflow_test.go` | `chat_workflow_test.go` |
| `scale_p21_sw559_gitlab_rotation_test.go` | `integration_gitlab_rotation_test.go` |
| `scale_p21_sw56_dm_inbox_test.go` | `dm_inbox_test.go` |
| `scale_p21_sw65_chat_sync_comments_test.go` | `chat_sync_comments_test.go` |
| `scale_p21_team_slack_notifications_test.go` | `integration_team_slack_notifications_test.go` |
| `scale_p21_webhook_json_routing_test.go` | `webhook_json_routing_test.go` |

**`api/internal/store` (`store`)**

| Current | Proposed |
| --- | --- |
| `scale_p21_application_access_test.go` | `application_access_test.go` |
| `scale_p21_application_access.go` | `application_access.go` |
| `scale_p21_application_task_context_read_invariant_test.go` | `application_task_context_read_invariant_test.go` |
| `scale_p21_application_task_context_read_sql_test.go` | `application_task_context_read_sql_test.go` |
| `scale_p21_application_task_context_read_test.go` | `application_task_context_read_test.go` |
| `scale_p21_application_task_context_read.go` | `application_task_context_read.go` |
| `scale_p21_application_webhook_reconcile_read.go` | `application_webhook_reconcile_read.go` |
| `scale_p21_application_webhook_sweep_read_test.go` | `application_webhook_sweep_read_test.go` |
| `scale_p21_application_webhook_sweep_read.go` | `application_webhook_sweep_read.go` |
| `scale_p21_chat_display_read_test.go` | `chat_display_read_test.go` |
| `scale_p21_chat_display_read.go` | `chat_display_read.go` |
| `scale_p21_chat_event_policy_read_test.go` | `chat_event_policy_read_test.go` |
| `scale_p21_chat_event_policy_read.go` | `chat_event_policy_read.go` |
| `scale_p21_chat_request_ask_write.go` | `chat_request_ask_write.go` |
| `scale_p21_chat_workflow_read.go` | `chat_workflow_read.go` |
| `scale_p21_hot_delivery_boundary_test.go` | `hot_delivery_boundary_test.go` |
| `scale_p21_hot_webhook_delivery_read_test.go` | `hot_webhook_delivery_read_test.go` |
| `scale_p21_hot_webhook_test.go` | `hot_webhook_test.go` |
| `scale_p21_integration_delivery_bindings.go` | `integration_delivery_bindings.go` |
| `scale_p21_integration_delivery_native.go` | `integration_delivery_native.go` |
| `scale_p21_integration_delivery_pages.go` | `integration_delivery_pages.go` |
| `scale_p21_integration_delivery_profile.go` | `integration_delivery_profile.go` |
| `scale_p21_integration_delivery_read.go` | `integration_delivery_read.go` |
| `scale_p21_integration_delivery_stream.go` | `integration_delivery_stream.go` |
| `scale_p21_integration_normalize.go` | `integration_normalize.go` |
| `scale_p21_integration_result_encoding.go` | `integration_result_encoding.go` |
| `scale_p21_integration_test.go` | `integration_test.go` |
| `scale_p21_legacy_webhook_needed.go` | `legacy_webhook_needed.go` |
| `scale_p21_review_reference_test.go` | `review_reference_test.go` |
| `scale_p21_review_reference.go` | `review_reference.go` |
| `scale_p21_sw102_webhook_boundary_test.go` | `webhook_boundary_test.go` |
| `scale_p21_sw169_delivery_embedded_fields_test.go` | `delivery_embedded_fields_test.go` |
| `scale_p21_sw17_coding_migration_test.go` | `coding_migration_test.go` |
| `scale_p21_sw17_coding_migration.go` | `coding_migration.go` |
| `scale_p21_sw18_webhook_credentials.go` | `webhook_credentials.go` |
| `scale_p21_sw19_chat_policy_test.go` | `chat_policy_test.go` |
| `scale_p21_sw19_chat_policy.go` | `chat_policy.go` |
| `scale_p21_sw19_gitlab_configs_test.go` | `gitlab_configs_test.go` |
| `scale_p21_sw19_gitlab_configs.go` | `gitlab_configs.go` |
| `scale_p21_sw197_chat_ask_test.go` | `chat_ask_test.go` |
| `scale_p21_sw202_integration_delivery_test.go` | `integration_delivery_test.go` |
| `scale_p21_sw22_application_hooks_test.go` | `application_hooks_test.go` |
| `scale_p21_sw22_application_hooks.go` | `application_hooks.go` |
| `scale_p21_sw226_integration_shapes_test.go` | `integration_shapes_test.go` |
| `scale_p21_sw226_integration_stream_test.go` | `integration_stream_test.go` |
| `scale_p21_sw228_delivery_ownership_test.go` | `delivery_ownership_test.go` |
| `scale_p21_sw23_agent_web_access_test.go` | `agent_web_access_test.go` |
| `scale_p21_sw23_agent_web_access.go` | `agent_web_access.go` |
| `scale_p21_sw23_web_history_test.go` | `web_history_test.go` |
| `scale_p21_sw23_web_history.go` | `web_history.go` |
| `scale_p21_sw24_application_identity_ids_test.go` | `application_identity_ids_test.go` |
| `scale_p21_sw24_application_identity_ids.go` | `application_identity_ids.go` |
| `scale_p21_sw324_delivery_frame_comparison_test.go` | `delivery_frame_comparison_test.go` |
| `scale_p21_sw326_integration_normalize_test.go` | `integration_normalize_test.go` |
| `scale_p21_sw333_integration_sql_chunks_test.go` | `integration_sql_chunks_test.go` |
| `scale_p21_sw340_integration_catalog_pages_test.go` | `integration_catalog_pages_test.go` |
| `scale_p21_sw343_integration_stage_pages_test.go` | `integration_stage_pages_test.go` |
| `scale_p21_sw344_integration_root_copy_test.go` | `integration_root_copy_test.go` |
| `scale_p21_sw349_integration_root_reuse_test.go` | `integration_root_reuse_test.go` |
| `scale_p21_sw54_chat_workflow_read_test.go` | `chat_workflow_read_test.go` |
| `scale_p21_sw97_legacy_webhook_needed_test.go` | `legacy_webhook_needed_test.go` |
| `scale_p21_webhook_delivery_read.go` | `webhook_delivery_read.go` |
| `scale_p21_webhook_disable_write_test.go` | `webhook_disable_write_test.go` |
| `scale_p21_webhook_disable_write.go` | `webhook_disable_write.go` |
| `scale_p21_webhook_failure_write_test.go` | `webhook_failure_write_test.go` |
| `scale_p21_webhook_failure_write.go` | `webhook_failure_write.go` |
| `scale_p21_webhook_json_read_test.go` | `webhook_json_read_test.go` |
| `scale_p21_webhook_json_read.go` | `webhook_json_read.go` |

### Coverage And Fixtures

Cross-domain callset, activation and source guards, differential harnesses, runtime probes and load fixtures.

**`api/cmd/server` (`main`)**

| Current | Proposed |
| --- | --- |
| `scale_p01_database_test.go` | `database_test.go` |
| `scale_p01_fixture_test.go` | `fixture_test.go` |
| `scale_p01_histogram_test.go` | `histogram_test.go` |
| `scale_p01_late_commit_test.go` | `late_commit_test.go` |
| `scale_p01_measurement_test.go` | `measurement_test.go` |
| `scale_p01_runner_test.go` | `runner_test.go` |
| `scale_p02_database_test.go` | `coverage_database_test.go` |
| `scale_p03_allocations_test.go` | `allocations_test.go` |
| `scale_p03_database_test.go` | `coverage_database_p03_test.go` |
| `scale_p03_request_cost_test.go` | `request_cost_test.go` |
| `scale_p04_database_test.go` | `coverage_database_p04_test.go` |
| `scale_p04_http_alloc_test.go` | `http_alloc_test.go` |
| `scale_p06_load_metrics_test.go` | `load_metrics_test.go` |
| `scale_p06_load_test.go` | `load_test.go` |
| `scale_p06_measure_test.go` | `measure_test.go` |
| `scale_p07_admission_test.go` | `admission_test.go` |
| `scale_p07_fixture_test.go` | `coverage_fixture_test.go` |
| `scale_p11_failure_test.go` | `failure_test.go` |
| `scale_p11_mode_test.go` | `mode_test.go` |
| `scale_p12_measure_test.go` | `coverage_measure_test.go` |
| `scale_p15_bench_test.go` | `bench_test.go` |
| `scale_p15_stream_test.go` | `stream_test.go` |
| `scale_p15b_measure_test.go` | `coverage_measure_p15b_test.go` |
| `scale_p16_alloc_test.go` | `alloc_test.go` |
| `scale_p16_enable_alloc_test.go` | `enable_alloc_test.go` |
| `scale_p16_enable_test.go` | `enable_test.go` |
| `scale_p16_flags_test.go` | `flags_test.go` |
| `scale_p17_business_test.go` | `business_test.go` |
| `scale_p17_measure_test.go` | `coverage_measure_p17_test.go` |
| `scale_p17_side_effects_test.go` | `side_effects_test.go` |
| `scale_p18_compatibility_test.go` | `compatibility_test.go` |
| `scale_p18_conversation_test.go` | `conversation_test.go` |
| `scale_p18_cost_test.go` | `cost_test.go` |
| `scale_p19_enable_measure_test.go` | `enable_measure_test.go` |
| `scale_p19_enable_test.go` | `coverage_enable_test.go` |
| `scale_p20_measure_test.go` | `coverage_measure_p20_test.go` |
| `scale_p20_tenfold_transfer_test.go` | `tenfold_transfer_test.go` |
| `scale_p21_comment_action_harness_test.go` | `comment_action_harness_test.go` |
| `scale_p21_hot_notifications_test.go` | `hot_notifications_test.go` |
| `scale_p21_hot_team_maintenance_test.go` | `hot_team_maintenance_test.go` |
| `scale_p21_hot_team_member_request_test.go` | `hot_team_member_request_test.go` |
| `scale_p21_m1_connectors_test.go` | `connectors_test.go` |
| `scale_p21_m1_core_test.go` | `core_test.go` |
| `scale_p21_m2_integrations_test.go` | `integrations_test.go` |
| `scale_p21_m2_records_test.go` | `records_test.go` |
| `scale_p21_m3_audit_test.go` | `audit_test.go` |
| `scale_p21_m3_rotation_test.go` | `rotation_test.go` |
| `scale_p21_output_boundary_test.go` | `output_boundary_test.go` |
| `scale_p21_request_owners_test.go` | `request_owners_test.go` |
| `scale_p21_resident_bootstrap_test.go` | `resident_bootstrap_test.go` |
| `scale_p21_rollback_roundtrip_test.go` | `rollback_roundtrip_test.go` |
| `scale_p21_sw0_background_output_test.go` | `background_output_test.go` |
| `scale_p21_sw0_guidance_test.go` | `guidance_test.go` |
| `scale_p21_sw1_wip_test.go` | `wip_test.go` |
| `scale_p21_sw10_notifications_test.go` | `notifications_test.go` |
| `scale_p21_sw11_owner_test.go` | `owner_test.go` |
| `scale_p21_sw115_p19_routes_test.go` | `p19_routes_test.go` |
| `scale_p21_sw15_labels_test.go` | `labels_test.go` |
| `scale_p21_sw16_runtime_source_hold_test.go` | `runtime_source_hold_test.go` |
| `scale_p21_sw16_runtime_test.go` | `runtime_test.go` |
| `scale_p21_sw167_public_comments_test.go` | `public_comments_test.go` |
| `scale_p21_sw17_actor_test.go` | `actor_test.go` |
| `scale_p21_sw189_collaboration_bench_test.go` | `collaboration_bench_test.go` |
| `scale_p21_sw189_mentions_test.go` | `mentions_test.go` |
| `scale_p21_sw19_automation_test.go` | `automation_test.go` |
| `scale_p21_sw19_scim_native_test.go` | `scim_native_test.go` |
| `scale_p21_sw19_scim_test.go` | `coverage_scim_test.go` |
| `scale_p21_sw20_helpers_test.go` | `helpers_test.go` |
| `scale_p21_sw20_restore_test.go` | `restore_test.go` |
| `scale_p21_sw22_webhooks_test.go` | `webhooks_test.go` |
| `scale_p21_sw24_loops_test.go` | `coverage_loops_test.go` |
| `scale_p21_sw267_group_tool_request_test.go` | `group_tool_request_test.go` |
| `scale_p21_sw270_compat_wrapper_boundary_test.go` | `compat_wrapper_boundary_test.go` |
| `scale_p21_sw274_compat_wrapper_routes_test.go` | `compat_wrapper_routes_test.go` |
| `scale_p21_sw286_store_wrapper_boundary_test.go` | `store_wrapper_boundary_test.go` |
| `scale_p21_sw3_integration_probe_test.go` | `integration_probe_test.go` |
| `scale_p21_sw31_time_input_http_test.go` | `time_input_http_test.go` |
| `scale_p21_sw4_attachments_test.go` | `attachments_test.go` |
| `scale_p21_sw491_harness_test.go` | `harness_test.go` |
| `scale_p21_sw530_route_sweep_test.go` | `route_sweep_test.go` |
| `scale_p21_sw556_mcp_cold_test.go` | `mcp_cold_test.go` |
| `scale_p21_sw557_background_cold_context_test.go` | `background_cold_context_test.go` |
| `scale_p21_sw559_resident_image_callers_test.go` | `resident_image_callers_test.go` |
| `scale_p21_sw6_activity_test.go` | `activity_test.go` |
| `scale_p21_sw6_creators_test.go` | `creators_test.go` |
| `scale_p21_sw602_source_charge_test.go` | `source_charge_test.go` |
| `scale_p21_sw7_audits_test.go` | `audits_test.go` |
| `scale_p21_sw86_direct_comment_baseline_test.go` | `direct_comment_baseline_test.go` |
| `scale_p21_team_move_test.go` | `team_move_test.go` |

**`api/internal/store` (`store`)**

| Current | Proposed |
| --- | --- |
| `scale_p01_load_test.go` | `load_test.go` |
| `scale_p01_probe.go` | `load_probe.go` |
| `scale_p01_seed.go` | `load_seed.go` |
| `scale_p03_statement_observer.go` | `statement_observer.go` |
| `scale_p04_alloc_test.go` | `alloc_test.go` |
| `scale_p04_database_test.go` | `coverage_database_test.go` |
| `scale_p04_test_main_test.go` | `test_main_test.go` |
| `scale_p05_bench_test.go` | `bench_test.go` |
| `scale_p05_catalog_plans_test.go` | `catalog_plans_test.go` |
| `scale_p05_catalog_test.go` | `catalog_test.go` |
| `scale_p05_pages_test.go` | `pages_test.go` |
| `scale_p06_paging_test.go` | `paging_test.go` |
| `scale_p09_activation_test.go` | `activation_test.go` |
| `scale_p09_modes_test.go` | `modes_test.go` |
| `scale_p09_write_bench_test.go` | `write_bench_test.go` |
| `scale_p15_bench_test.go` | `coverage_bench_test.go` |
| `scale_p18_conversation_test.go` | `conversation_test.go` |
| `scale_p21_activation_guard_test.go` | `activation_guard_test.go` |
| `scale_p21_auth_test_fixture.go` | `auth_test_fixture.go` |
| `scale_p21_catalog_first_record_test.go` | `catalog_first_record_test.go` |
| `scale_p21_conflict_signal_test.go` | `conflict_signal_test.go` |
| `scale_p21_coverage_test.go` | `coverage_test.go` |
| `scale_p21_hot_notifications_test.go` | `hot_notifications_test.go` |
| `scale_p21_hot_team_maintenance_test.go` | `hot_team_maintenance_test.go` |
| `scale_p21_hot_team_member_request_test.go` | `hot_team_member_request_test.go` |
| `scale_p21_inline_unicode_test.go` | `inline_unicode_test.go` |
| `scale_p21_inventory_test.go` | `metadata_consumer_inventory_test.go` |
| `scale_p21_inventory.go` | `metadata_consumer_inventory.go` |
| `scale_p21_legacy_display_keep_test.go` | `legacy_display_keep_test.go` |
| `scale_p21_loadtest_seed.go` | `coverage_loadtest_seed.go` |
| `scale_p21_m1_color_test.go` | `color_test.go` |
| `scale_p21_m1_contract_test.go` | `contract_test.go` |
| `scale_p21_m1_core_test.go` | `core_test.go` |
| `scale_p21_m1_telemetry_test.go` | `telemetry_test.go` |
| `scale_p21_migrate_inventory.go` | `metadata_migration_consumer_inventory.go` |
| `scale_p21_offset_time_test.go` | `offset_time_test.go` |
| `scale_p21_probe_test.go` | `probe_test.go` |
| `scale_p21_resource_testing.go` | `resource_testing.go` |
| `scale_p21_reviewed_routes_test.go` | `reviewed_routes_test.go` |
| `scale_p21_selected_test_view.go` | `selected_test_view.go` |
| `scale_p21_supported_producers_test.go` | `supported_producers_test.go` |
| `scale_p21_sw0_resident_bounds_test.go` | `resident_bounds_test.go` |
| `scale_p21_sw0_resident_scanner_test.go` | `resident_scanner_test.go` |
| `scale_p21_sw0_resolved_test.go` | `resolved_test.go` |
| `scale_p21_sw0_selected_test_view_test.go` | `selected_test_view_test.go` |
| `scale_p21_sw1_actor_test.go` | `actor_test.go` |
| `scale_p21_sw158_transform_timestamp_inputs_test.go` | `transform_timestamp_inputs_test.go` |
| `scale_p21_sw17_purge_test.go` | `purge_test.go` |
| `scale_p21_sw17_startup_source_test.go` | `startup_source_test.go` |
| `scale_p21_sw17_viewer_default_race_test.go` | `viewer_default_race_test.go` |
| `scale_p21_sw18_stored_workspace_test.go` | `stored_workspace_test.go` |
| `scale_p21_sw189_editor_work_test.go` | `editor_work_test.go` |
| `scale_p21_sw19_review_sources_test.go` | `review_sources_test.go` |
| `scale_p21_sw194_cycle_native_test.go` | `cycle_native_test.go` |
| `scale_p21_sw195_string_encoding_test.go` | `string_encoding_test.go` |
| `scale_p21_sw199_sql_stage_test.go` | `sql_stage_test.go` |
| `scale_p21_sw200_sql_stage_accounting_test.go` | `sql_stage_accounting_test.go` |
| `scale_p21_sw201_workflow_inputs_test.go` | `workflow_inputs_test.go` |
| `scale_p21_sw205_native_bytes_test.go` | `native_bytes_test.go` |
| `scale_p21_sw209_fingerprint_test.go` | `fingerprint_test.go` |
| `scale_p21_sw209_update_stream_test.go` | `update_stream_test.go` |
| `scale_p21_sw214_snapshot_move_test.go` | `snapshot_move_test.go` |
| `scale_p21_sw216_reload_admission_test.go` | `reload_admission_test.go` |
| `scale_p21_sw217_inline_fields_test.go` | `inline_fields_test.go` |
| `scale_p21_sw219_sql_stage_test.go` | `coverage_sql_stage_test.go` |
| `scale_p21_sw220_update_parent_dialect_test.go` | `update_parent_dialect_test.go` |
| `scale_p21_sw220_update_parent_test.go` | `update_parent_test.go` |
| `scale_p21_sw23_core_manifest_guard_test.go` | `core_manifest_guard_test.go` |
| `scale_p21_sw23_resident_order_test.go` | `resident_order_test.go` |
| `scale_p21_sw23_resident_prefix_order_test.go` | `resident_prefix_order_test.go` |
| `scale_p21_sw23_workspace_settings_alias_test.go` | `workspace_settings_alias_test.go` |
| `scale_p21_sw275_auth_test_fixture_test.go` | `auth_test_fixture_test.go` |
| `scale_p21_sw276_loadtest_seed_test.go` | `loadtest_seed_test.go` |
| `scale_p21_sw277_loadtest_callset_test.go` | `loadtest_callset_test.go` |
| `scale_p21_sw278_issue_metadata_callset_test.go` | `issue_metadata_callset_test.go` |
| `scale_p21_sw279_loadtest_seed_scale_test.go` | `loadtest_seed_scale_test.go` |
| `scale_p21_sw28_normalize_test.go` | `normalize_test.go` |
| `scale_p21_sw28_reference_refresh_test.go` | `reference_refresh_test.go` |
| `scale_p21_sw30_normalize_test.go` | `coverage_normalize_test.go` |
| `scale_p21_sw301_retry_adversaries_test.go` | `retry_adversaries_test.go` |
| `scale_p21_sw31_audit_test.go` | `audit_test.go` |
| `scale_p21_sw31_catalog_number_test.go` | `catalog_number_test.go` |
| `scale_p21_sw31_catalog_scalar_test.go` | `catalog_scalar_test.go` |
| `scale_p21_sw31_inline_refresh_test.go` | `inline_refresh_test.go` |
| `scale_p21_sw31_payload_work_test.go` | `payload_work_test.go` |
| `scale_p21_sw311_peer_fixture_test.go` | `peer_fixture_test.go` |
| `scale_p21_sw318_source_origin_guard_test.go` | `borrowed_source_origin_guard_test.go` |
| `scale_p21_sw339_inline_root_pages_test.go` | `inline_root_pages_test.go` |
| `scale_p21_sw36_retry_test.go` | `retry_test.go` |
| `scale_p21_sw504_workflow_kernel_guard_test.go` | `workflow_kernel_guard_test.go` |
| `scale_p21_sw559_installed_accessors_test.go` | `installed_accessors_test.go` |
| `scale_p21_sw77_legacy_delta_budget_test.go` | `legacy_delta_budget_test.go` |
| `scale_p21_sw81_legacy_references_test.go` | `legacy_references_test.go` |
| `scale_p21_team_metadata_callset_test.go` | `team_metadata_callset_test.go` |
| `scale_p21_workflow_aggregate_lookup_test.go` | `workflow_aggregate_lookup_test.go` |

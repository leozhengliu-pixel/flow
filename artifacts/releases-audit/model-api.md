# Releases model and API audit

Date: 2026-08-20

Scope: Flow persistence and HTTP contracts needed to reproduce Linear Releases behavior. UI and DOM measurements are outside this document.

## Linear behavior used as the reference

The reference behavior comes from [Linear's Releases documentation](https://linear.app/docs/releases):

- A release pipeline represents a product/environment combination and belongs to one or more teams.
- A pipeline can be scheduled or continuous and contains ordered stages.
- Releases belong to a pipeline and can carry a name, commit SHA, scheduled date, issues, and release notes.
- A started stage can be frozen so new issues are no longer added to it.
- CI integration uses a pipeline access key and optional glob path filters.
- Release notes can use a template and can be generated automatically.
- Continuous CI can create completed releases, and release completion can trigger workflow automations.

## Gap assessment and disposition

| Capability | Flow before audit | Result of this audit |
| --- | --- | --- |
| Pipeline ownership, type, production flag, stages | Persisted; create/update only | Retained, validated, list/detail/delete/reorder added |
| Pipeline ordering | Missing | Persistent `position` and exact-scope reorder added |
| Path filters | Missing | Persistent glob patterns with syntax validation added |
| Release-note configuration | Missing | Template and automatic-generation flag added |
| CI access key | Missing | Cryptographic rotation endpoint, hash persistence, public metadata, one-time secret added |
| Release-to-pipeline association | Missing | Persistent `pipelineId` with active-pipeline validation added |
| Release stage | Missing | Persistent stage with pipeline stage validation added |
| Stage freezing | Missing | Freeze timestamp and rejection of issue additions while frozen added |
| Commit association | Missing | Persistent `commitSha` added |
| Release notes | Missing | Persistent `releaseNotes` added |
| Scheduled date | Existing `targetDate` | Retained as the scheduled date; strict `YYYY-MM-DD` validation added |
| Start/completion facts | Status only | Persistent `startedAt` and `releasedAt` added |
| Release ordering | Missing | Persistent pipeline-scoped `position` and reorder added |
| Release reads/filtering | Bootstrap only | List/detail API plus pipeline, status, and archive filters added |
| Pipeline deletion/restore | Missing | Reference-protected delete, trash payload, and restore added |
| CI event ingestion | Missing | Deferred; requires an authenticated CI webhook/action contract |
| Automatic note generation | Missing | Configuration is expressible; generation worker/agent deferred |
| Completion automation execution | Existing team rule configuration only | Deferred; no release event automation runner added here |
| Changelog assembly/UI | Missing | Deferred to the Releases UI implementation |

## Persistent model contracts

`ReleasePipeline` now persists:

- `position`
- `pathFilters`
- `releaseNotesTemplate`
- `autoGenerateReleaseNotes`
- `accessKeyPrefix`
- `accessKeyHash`
- `accessKeyCreatedAt`

`Release` now persists:

- `pipelineId`
- `stage`
- `commitSha`
- `releaseNotes`
- `position`
- `startedAt`
- `releasedAt`
- `stageFrozenAt`

Existing fields remain backward compatible. A release may still have no pipeline, so existing seed and imported releases continue to load. `targetDate` remains the scheduled date rather than introducing a duplicate date field.

## HTTP contracts

### Releases

| Method | Path | Contract |
| --- | --- | --- |
| `GET` | `/api/releases` | List active releases by default; accepts `pipelineId`, `status`, and `archived=true|false|all` |
| `GET` | `/api/releases/{id}` | Get one release |
| `POST` | `/api/releases` | Create a release, including pipeline, stage, commit, notes, date, and associations |
| `PATCH` | `/api/releases/{id}` | Update release fields; accepts `stageFrozen` to freeze/unfreeze |
| `POST` | `/api/releases/reorder` | Body: `{ "pipelineId": "...", "ids": [...], "archived": false }` |
| `DELETE` | `/api/releases/{id}` | Move release to trash |

### Release pipelines

| Method | Path | Contract |
| --- | --- | --- |
| `GET` | `/api/release-pipelines` | List active pipelines by default; accepts `archived=true|false|all` |
| `GET` | `/api/release-pipelines/{id}` | Get one pipeline without its access-key hash |
| `POST` | `/api/release-pipelines` | Create pipeline and release-note/CI configuration |
| `PATCH` | `/api/release-pipelines/{id}` | Update or archive pipeline |
| `POST` | `/api/release-pipelines/reorder` | Body: `{ "ids": [...], "archived": false }` |
| `POST` | `/api/release-pipelines/{id}/access-key` | Rotate key and return plaintext once |
| `DELETE` | `/api/release-pipelines/{id}` | Delete only when no release references the pipeline |

Reorder requests must contain the exact set of IDs in their requested scope. Missing, duplicate, foreign, or archived-status-mismatched IDs are rejected instead of partially mutating order.

## Validation and invariants

- Pipeline names are non-empty; teams must exist; type is `scheduled` or `continuous`; stages are non-empty.
- Path filters must be syntactically valid Go-compatible glob patterns.
- A stage cannot be removed from a pipeline while any release still references it.
- A release can only attach to an existing, non-archived pipeline.
- A non-empty release stage must exist in its pipeline.
- Release status remains one of `planned`, `inProgress`, `released`, or `canceled`.
- The first transition to `inProgress` records `startedAt`; the first transition to `released` records both missing start and release timestamps. Historical timestamps are retained if status later changes.
- A stage can only be frozen when both pipeline and stage are set.
- Frozen releases reject new issue IDs with HTTP 409 but allow existing issue removal.
- A pipeline delete returns HTTP 409 while any current release references it.
- Archive list behavior is explicit: omitted/`false` means active, `true` means archived, and `all` means both.

## CI access-key security

- Secrets use `crypto/rand` and the `flow_release_` prefix.
- Only the SHA-256 hash is persisted; the plaintext secret is returned only by the rotation response.
- Public responses expose only the short prefix and creation timestamp.
- Bootstrap, pipeline list/detail, create/update, and reorder responses clear `accessKeyHash`.
- Pipeline trash payloads clear the hash before serialization, preventing hash disclosure through bootstrap trash data.
- Rotating a key replaces the old hash immediately; Flow does not retain retrievable plaintext.

## Verification

Focused lifecycle test: `TestReleasePipelineAndReleaseAPILifecycle`

It covers configuration persistence, ordering, exact-scope reorder rejection, two access-key rotations, internal hash persistence, public hash redaction, release field/timestamp persistence, stage validation, filtering, frozen-stage behavior, referenced-stage conflicts, protected deletion, trash payload redaction, restore, and archived filtering.

Final verification commands:

```text
cd api && go test ./...
cd web && npm run build
```

See the task completion report for their final result.

## Deliberately deferred

The data model and API can now express the settings and manual release lifecycle required by the page. The following require separate runtime/product work and must not be represented as functioning UI controls until implemented:

- Authenticated CI event ingestion and continuous release creation
- Automatic release-note generation worker/agent
- Release-completion workflow automation execution
- Changelog assembly, publishing, and public changelog surfaces
- Repository/provider commit verification beyond storing a commit SHA

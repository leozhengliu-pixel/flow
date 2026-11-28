# Bounded JSON Payload Audit

The audit at `3d09565e5` found no remaining write or publication that always
fails admission because of its Go payload shape. The new guard checks the
producer calls, interfaces and records used by the bounded writer. The
inventory lists 1,462 producer arguments and 125 resident fields or frame
types. Each row includes the static type, handler or job, direct mux route or
event when available, and the shape at the boundary.
[Call-site inventory](metadata-json-payload-inventory.txt).

## Findings

| Payload type | Route or job | Shape result | Fix |
| --- | --- | --- | --- |
| Former `childEventPayload.MarshalJSON` | `POST /api/projects/{id}/milestones` and `PATCH /api/projects/{id}/milestones/{milestoneId}` | Both answered 503 on SQLite when routed to the resident writer before `3d09565e5`. | `3d09565e5` replaced the custom codec with plain `*childEventPayload` fields. |
| Same former wrapper | `DELETE /api/projects/{id}/milestones/{milestoneId}` and `POST`, `PATCH`, `DELETE` for project and initiative updates | The estimator refuses the former wrapper wherever it receives it. These seven HTTP cases succeeded through the COLD fallback in the historical run. | Same plain payload as above. All nine routes now use it. |
| `webhookChild.Entity` | The three child delete handlers | The concrete values are a `domain.ProjectMilestone` with `sortOrder`, a `domain.ProjectUpdate`, and a `domain.InitiativeUpdate`. Their timestamps have an explicit estimate. The two update handlers clear the snapshot on the deleted child. | None. |
| `nil`, scalar and slice inputs, mutation DTOs, anonymous structs, string-keyed maps | `MutateWorkspace`, `MutateWorkspaceWithAggregate` and scoped/create/append variants, including scheduled workflows, loops, imports, integrations, notifications, and maintenance | Plain encodings or explicitly sized codecs. The guard checks the types boxed into maps and other interfaces. Request editor data decoded as `map[string]any` contains JSON values. | None. |
| `domain.Bootstrap` fields and typed record wrappers | Resident, scoped, batch, and discussion writers, including document edits and post-mutation publication | The guard traverses each serialized field, including nested collection entries. Records arriving already encoded use `json.RawMessage`, byte slices, or strings at the raw-size boundary. | None. |
| `time.Time`, `json.RawMessage`, `domain.OptionalInt`, `domain.ActivityMetadata`, `domain.AgentToolCall` | Timestamps across writers and frames, encoded bodies, issue mutation input, activity rows, and agent tool results | All five have explicit encoding estimates. `AgentToolCall` includes its plain fields and extra work for sanitizing tool JSON. | None. |
| `domain.DomainEvent`, normalized object maps, typed aggregate entities, `domain.RealtimeEvent`, collaboration messages | Commit-time enrichment, realtime projection, SSE, and collaboration WebSockets | The entity comes from typed records or decoded event JSON. The final event uses strings, a timestamp, and raw JSON. Collaboration uses plain structs, byte data, and scalar maps. | None. |
| `legacyWebhookEnvelope`, v2 `map[string]any`, `domain.WebhookDelivery`, failure event and application webhook envelopes | Domain event delivery, webhook health and retry writes, key events, application lifecycle and app-user/SLA sweeps | The v1 envelope and queue row carry raw JSON. V2 and application envelopes use actors, typed entities, decoded JSON, timestamps, and scalar values. The contents are encoded before the raw queue boundary. | None. |
| `RecordKey`, `MetadataRecord`, `RecordPage`, `RecordBatch`, `RecordDelta`, `CommittedChange`, `CommittedPage`, Sync V2 responses and realtime transaction frames | Canonical reads, journal commit/replay, `/api/sync` reads, shared cache, and SSE | Typed envelopes with strings, booleans, lists and raw JSON. `syncRecordSummary` selects members as `map[string]json.RawMessage` and encodes version numbers as strings. Journal and page size helpers measure wire bytes. | None. |

Integer map keys are supported by the estimator and `encoding/json`. The
production inventory found string-keyed maps at the relevant boundaries. The
estimator refuses maps with non-string `TextMarshaler` keys, including
`map[time.Time]string`. Functions, channels, complex values and maps with boolean
keys fail at `encoding/json`. The guard flags both classes. Preflight also
applies the existing 128-level traversal limit and budget checks to supported
shapes. The raw JSON estimator takes bytes or strings and has no Go method sets
to inspect.

## Guard

`TestBoundedJSONPayloadGuard` type-checks the production packages built for
`cmd/server`, using dependency export data for method sets. It checks value
and pointer receivers, promoted methods, aliases, struct fields and container
elements. It also scans repository interface assignments, arguments, returns,
literal elements, appends and generic instantiations. The scan errs on the side
of reviewing extra interface values. Function digests require re-review for
the WebAuthn response options, SAML signer options, MCP connector results,
Kubernetes policy and CRDT pending state found outside bounded writers.

The guard covers the build-selected source and the registered writer and
publication boundaries. Dynamic values returned as `any` from dependencies
or recovered through reflection still need source review. Decoded JSON,
aggregate selection from `domain.Bootstrap`, and native Go interface
assignments were reviewed separately. The `dynamic` label in the inventory
marks an interface, generic parameter or reflected value. `plain` and `sized`
describe encoding shape. Admission still depends on the actual size and depth.

`TestPayloadWorkShapeCompatibility` checks the supported codecs and rejects
unsupported JSON and text encoders before any codec runs.
`TestBoundedChildMutationPayloads` runs the nine HTTP operations, checks
committed payloads, realtime and journal markers, and checks that publication
releases its work. The project and initiative record-mutation flags are enabled
and the test-only narrow mutation oracle is disabled so the milestone create
and edit run through the resident writer. `TestChildPublicationWireGolden`
compares exact v1 and v2 webhook bodies and an SSE frame from the same
deterministic deletion fixture.

## Verification

Run from `api/` with the machine-wide lock.

```sh
~/.flow-local/heavy.sh env GOMAXPROCS=2 FLOW_TEST_BOUNDED_JSON_INVENTORY=1 \
  go test -p 1 ./internal/store ./cmd/server \
  -run 'Guard|Inventory|Reviewed|Invariant|TestResidentImageReaders|TestBorrowedSourceOriginGuard' -v -count=1

selection='^Test(BoundedChildMutationPayloads|ChildPublicationWireGolden|PayloadWork.*|AggregatePublication(Differential|PayloadShapesAndWinnerPrecedence)|MetadataJSONEncoding(Parity|PageSizes|ResidentRecordPublication)|ScaleSupportedProducerCodecs|ScaleActivationRealtimeDifferential|ScaleWebhookJSONRoutingDifferential|ScaleMutationSideEffectsDifferentialMutations|ScaleSync(V2JSONSizes|IssueProjectionBudgetInvalidatesTransaction|PageKeepsSummariesChargedThroughWrite|SharedPageKeepsSummariesCharged)|RealtimeSSEEncodingWork|CollaborationSyncEncoding)$'
~/.flow-local/heavy.sh env GOMAXPROCS=2 \
  go test -p 1 ./internal/store ./cmd/server -run "$selection" -count=1 -v

~/.flow-local/heavy.sh env GOMAXPROCS=2 go vet -p 1 ./...
```

The differential command uses temporary SQLite by default. The PostgreSQL
and MySQL runs set `FLOW_TEST_DATABASE_DRIVER` and `FLOW_TEST_DATABASE_URL`
for the two disposable databases. Both the guard set and the selected
differentials exited `0` on all three databases. `go vet` exited `0`.

The historical run selected `^Test(BoundedChildMutationPayloads|ChildPublicationWireGolden)$`
with Go's `-overlay` flag. It used the parent versions of `main.go` and
`webhook_children.go`, plus the old initializer in the deletion golden.
It exited `1` with the two expected 503s. Both webhook versions and the SSE
golden passed on the historical code.

# Bounded Metadata JSON Encoding

The metadata work budget defaults to 256 MiB. An encoding charge includes
allocations discarded during buffer growth. SQL transfer, decoded records,
retained results, and later encoding phases can each own an allowance at the
same time. The writer checks the combined demand against the process budget.

## Admission Inventory

| Encoding path | Estimator and lifetime |
| --- | --- |
| Standard `json.Marshal` and `Encoder.Encode` in bounded store and HTTP paths | `metadataAggregatePayloadEncodingWork` and `MetadataJSONEncodingWork` charge 9 times plain string wire width and 16 times escaped wire width. The allowance covers the history of the encoder buffer and the string append, the returned copy, and optional storage normalization. The traversal also charges field names, scalars, array and map entries, and map sorting. A raw value charges 8 times input bytes, the validation stack, and another 8 times the expanded width when HTML escaping grows it. Binary slices include base64 expansion. `time.Time`, `OptionalInt`, `ActivityMetadata`, and `AgentToolCall` have explicit allowances for their nested encoder or decoder work. Unsupported custom marshalers fail admission. |
| Pre-sized typed records | `metadataRecordEncodingWork` leaves the small header and fields without a dedicated encoder on the standard estimator. At 16 KiB of input, the selected strings charge 4 times their escaped width plus allocator rounding. The encoder measures them, marshals a header with placeholders, and writes the final result into one allocation. This covers issues, issue-create and issue-update inputs, documents and document inputs, content and revisions, comments, projects and updates, initiatives and updates, notifications, loops, and releases. Nested issue content and large document revisions also charge their separate encodings and the final assembly. SQL adapters retain their own column and NUL normalization allowances. |
| Issue detail, index/list columns, comparisons, and batch callbacks | `reserveIssueRecordColumns` adds the detail and list estimates. `issueRecordComparisonAdmission` charges each stored/display encode and retains the result needed for the next comparison. The combined envelope helpers charge two encodes for a new record, or four plus previous JSON work when comparing a previous record. Batch callbacks charge the largest issue plus the input estimate for each of the two sides, and content synchronization charges four times the largest individual content record. The batch event reserves its input encoding separately through publication. `reserveIssueCreateEvent` admits twice the input estimate plus the entity estimate and 64 KiB for mutation input, JSON decode, and the enriched envelope. Single-record updates encode the mutation input directly and inspect field presence without encoding for stale-version checks. They build the before/after JSON member maps only for a webhook or when the caller supplied no input. They admit content encodes per row and retain the event JSON through publication. |
| Documents and other metadata writes | `encodeOwnedMetadataRecord` charges the whole encode to the row owner. `encodeRetainedMetadataRecord` and `encodeMetadataMutationPayload` hold the encode allowance through allocation, then retain rounded output capacity with the caller. Resident document writes and rebases use separate owners for the old row transfer, typed comparison, and the final SQL write. Resident point edits reserve the delta encoding separately and hold the event input and output through publication. Body comparison and publication preserve the storage and response encodings. |
| Discussions and content rows | `boundedDiscussionAttempt.reserveContent`, `reserveIssueBatchContentEncoding`, and the row owners cover selected comments, activity, notifications, and deliveries. The content path derives identity from the first encoding and passes the same bytes to comparison and persistence. Notification point updates own their comparison encodes, stored row encode, and publication envelope separately. Notification fanout has its own recipient and delivery charge. Document callbacks reserve the content work once and reserve up to four notification waves. |
| Logical frames and raw compaction | `metadataAggregateEncodeOwned` and `IssueBootstrapRead.Prepare` own 2 MiB of stream scratch. The logical encoder streams string and raw members, with separate admissions for map ordering and supported codec work. Aggregate results that spill to disk admit the final byte slice after measuring its exact size. The deep normalized-JSON fallback uses rounded encoded width times 16 plus 64 bytes per map entry. Large `RawMessage`, raw maps and lists, and update envelopes write sorted or ordered members directly into a measured result. A pre-sized raw object charges for sorting keys, validation, two expanded input widths and allocator rounding. Logical display refresh and project schedules use the typed record and wrapper allowances. SQL staging paths for large rows, project progress, arrays and inline fields keep chunk-sized scratch, with a separate admission before any whole SQL parameter is loaded from a spool. |
| Selection validation, record diffs and legacy collection fallback | `splitMetadataImage` and `writeUpdatesFieldDelta` admit the selected, partial and prior encodings. The split phase charges five input widths, an expansion allowance, and scanner and container work for root members, nested copies, and individual update rows. Update rows additionally charge for the parent key repeated in the wrapper and hash input. Reads release validation work after checking the existing page cap. `encodeLegacyMetadataRoot` owns the shape manifest, compacted root and NUL normalization. The streamed root patch compacts members incrementally and admits its final SQL parameter. `applyMetadataRecords` owns display comparison and offload assembly for each affected row. |
| Offload assembly, marker resolution, and raw merge | `metadataOffloadAssemblyWork` includes raw input plus HTML expansion, scanner and container costs. `reserveOffloadRecordWork` charges six widths for ordinary rows, eight for trash and twelve for agent sessions. This covers copies while separating document bodies and revisions, nested session/result blobs, and compaction around markers. Rebase owns marker expansion, typed re-encoding, and raw object/list merge work in its comparison phase. |
| Bootstrap per-record output and general HTTP JSON | `GeneralBootstrapRead.EncodeRecordJSON` admits one record through its stream write. The hydrated streamer handles ordinary strings and raw JSON incrementally, and uses the same admission when a custom codec falls back to `Marshal`. SQL row transfer and typed reconstruction have separate walker allowances. `MetadataJSONRead.ReserveEncoding` uses the ordinary estimate plus 64 KiB for response producers such as issue and chat point reads. `ReserveEncoded` charges four times the rounded buffer target for cumulative growth, doubled in race builds to cover temporary slices. Both builds add 512 bytes per admitted page for bookkeeping. `ReserveEncodedCopies` charges four rounded widths for fixed-size payloads and their copies. `ReserveDecode` separately admits the destination and decoder. |
| Integration delivery responses | `ReserveIntegrationDeliveryEncoding` takes the greater of six rounded encoded widths plus a page and the sum of header, scanner, and raw-member work. Raw input is charged at five times its original size, with an additional eight times its expanded size when escaping grows it. This covers `Encoder.Encode` even when whitespace makes the raw input much larger than the result. |
| Record pages and Sync V2 HTTP | `recordJSONBytes`, `committedChangeJSONBytes` and the Sync V2 size methods measure escaped wire width, including raw member compaction, without encoding a throwaway copy. Issue projection budget failure invalidates the complete transaction, with the same sequence as the normal projection. The page owns accepted summary slices through HTTP writes or journal replay, including summaries decoded from the shared cache. `marshalSyncJSON` separately reserves `MetadataJSONRead` encoding work for page responses and shared-cache serialization. |
| Realtime events and collaboration | `ProjectEvent` charges source-sized projection work and both payload and envelope Marshal estimates. `writeRealtimeSSE` owns only header encoding and scanner scratch for single-line payloads. It writes the frame in pieces and keeps the event charge through flush. Multiline and cursorless compaction admits a single measured copy. `realtimeJSONEncoding` reserves each Sync V2 summary or journal envelope encode and retains its output through frame delivery. A transaction that exceeds the frame or encoding budget becomes an atomic invalidation. `collaborationSyncBytes` measures the full websocket body and reserves rounded output capacity plus fixed scratch before encoding it. |
| Catalog projections and journal commits | Catalog summaries stream canonical bodies under the existing chunk and capture limits. Large raw members use pre-sized compaction, and milestone projection stops as soon as the smallest possible array would exceed the cap. The SQLite journal SQL callback uses separate transaction limits of 16 entries and 8 KiB of input per projection. The general journal caps its envelope at 64 KiB. |

The existing point read, facet, directory/cache, history, schedule,
notification and comment-action preflights that call the ordinary estimator
inherit the same growth and codec charges. Legacy actor and count shape checks
validate the IDs directly, including UTF-8 repair and duplicates. Unbounded
store operations and legacy HTTP downloads have separate admission policies.

## Allocation Measurements

Measurements use the API toolchain Go 1.26.11 on Darwin arm64. The test fixture
uses a short sentence for small records, about 4 KiB of mixed text for typical
records, and `strings.Repeat("<>&", 700<<10)` for the near-limit stress case.
The largest issue fixture has a 12,903,229-byte stored row. Its cold encode
allocated 91,292,288 bytes on `223da028b` with a 51,634,254-byte charge.
The new encode allocated 12,919,304 bytes with a 51,646,417-byte charge.
The bounded issue rewrite peaked at 142,129,026 bytes of reserved work.

Each sample forces two collections before encoding to clear the buffer pool.
The baseline encode primes the code and type caches. The table tests disable
GC during the measured interval, so `TotalAlloc` includes discarded buffers
and bounds the heap increase during that interval. Returned slice capacity
measures the retained output directly. Exact reachable live peaks and
allocation inside MySQL and PostgreSQL drivers remain unmeasured.

Each cell reports `baseline TotalAlloc / new TotalAlloc / new charge /
new retained capacity` in bytes. Baselines use ordinary `json.Marshal` and
the corresponding storage normalization.

| Record | Small | Typical | Near Limit |
| --- | ---: | ---: | ---: |
| Issue | 5,016 / 5,016 / 21,694 / 640 | 55,640 / 55,640 / 123,717 / 8,192 | 91,292,888 / 12,918,648 / 51,639,213 / 12,902,967 |
| Document | 2,208 / 2,208 / 14,103 / 416 | 52,600 / 52,600 / 116,126 / 6,784 | 91,291,240 / 12,914,296 / 51,631,622 / 12,902,773 |
| Comment | 1,536 / 1,536 / 8,641 / 240 | 52,576 / 52,576 / 110,664 / 6,784 | 91,291,216 / 12,912,992 / 51,626,160 / 12,902,617 |
| Project | 3,496 / 3,496 / 24,359 / 704 | 52,680 / 52,680 / 126,382 / 8,192 | 91,289,928 / 12,915,832 / 51,641,878 / 12,903,027 |
| Project update | 2,056 / 2,056 / 9,623 / 288 | 52,504 / 52,504 / 111,646 / 6,784 | 91,291,160 / 12,913,528 / 51,627,142 / 12,902,660 |
| Activity with custom metadata | 2,264 / 2,264 / 9,256 / 224 | 52,200 / 52,200 / 136,636 / 6,784 | 117,112,296 / 117,112,296 / 258,056,956 / 12,910,592 |
| Notification | 2,216 / 2,216 / 14,525 / 416 | 38,600 / 38,600 / 116,548 / 6,784 | 91,290,568 / 12,914,072 / 51,632,044 / 12,902,771 |
| Raw JSON compaction | 824 / 848 / 1,968 / 32 | 28,376 / 28,400 / 85,872 / 6,528 | 81,773,272 / 12,910,936 / 25,813,598 / 12,902,411 |
| Document revision | 1,480 / 1,480 / 8,193 / 240 | 52,536 / 52,536 / 110,216 / 6,784 | 91,291,192 / 12,912,872 / 51,625,712 / 12,902,616 |
| Document input | 9,576 / 472 / 2,273 / 32 | 37,064 / 37,064 / 104,296 / 6,528 | 91,289,544 / 12,911,352 / 51,619,664 / 12,902,414 |
| Initiative | 3,496 / 3,496 / 26,937 / 704 | 54,808 / 54,808 / 128,960 / 8,192 | 91,292,056 / 12,915,912 / 51,644,456 / 12,903,089 |
| Initiative update | 2,032 / 2,032 / 9,785 / 288 | 51,192 / 51,192 / 111,808 / 6,784 | 91,289,824 / 12,913,448 / 51,627,304 / 12,902,666 |
| Loop | 2,264 / 2,264 / 18,297 / 480 | 51,464 / 51,464 / 120,320 / 6,912 | 91,289,992 / 12,914,664 / 51,635,816 / 12,902,858 |
| Release | 21,192 / 2,200 / 13,326 / 416 | 51,272 / 51,272 / 115,349 / 6,784 | 91,289,928 / 12,914,040 / 51,630,845 / 12,902,770 |
| Issue create input | 15,384 / 648 / 3,152 / 80 | 38,264 / 38,264 / 105,175 / 6,528 | 91,290,744 / 12,911,624 / 51,620,671 / 12,902,460 |
| Document content | 1,352 / 1,352 / 5,079 / 144 | 37,320 / 37,320 / 107,102 / 6,528 | 91,289,800 / 12,911,880 / 51,622,598 / 12,902,516 |
| Notification delivery | 8,824 / 1,528 / 7,385 / 224 | 52,696 / 52,696 / 109,408 / 6,784 | 91,291,352 / 91,291,352 / 206,445,664 / 12,910,592 |
| Issue update input | 17,352 / 488 / 2,569 / 48 | 37,064 / 37,064 / 104,592 / 6,528 | 91,289,544 / 12,911,584 / 51,619,960 / 12,902,418 |
| Issue with repeated description in document content | 5,160 / 5,160 / 26,325 / 704 | 61,832 / 61,832 / 230,371 / 13,568 | 200,527,112 / 38,732,328 / 103,269,427 / 25,805,445 |

Offload assembly includes decoding the stored-form input, splitting body
blobs, and building the row. Cells report `TotalAlloc / charge / retained
row and body capacity` in bytes.

| Offload Form | Small | Typical | Near Limit |
| --- | ---: | ---: | ---: |
| Document plus two revisions | 12,368 / 121,854 / 1,024 | 105,640 / 286,122 / 19,712 | 45,196,768 / 116,241,834 / 12,903,241 |
| Trash containing a document | 15,520 / 126,744 / 1,024 | 145,496 / 345,768 / 19,712 | 71,010,800 / 154,953,384 / 12,903,279 |
| Agent transcript plus tool result | 4,104 / 82,764 / 320 | 103,448 / 301,788 / 20,736 | 52,749,720 / 154,909,404 / 12,912,384 |

The ordinary nested-map stress case allocated 91,292,056 bytes with a
206,440,815-byte charge. The two custom codec stress cases allocated
4,256,256 bytes of 17,174,796 charged for label deltas, and 16,579,208 bytes of
29,369,020 charged for a partial tool call with a non-JSON result. Integration
delivery encoding allocated 6,300,904 bytes of 38,207,488 charged for compact
raw JSON, 4,203,752 of 21,045,584 charged for whitespace-heavy input, and
50,530,048 of 83,435,930 charged for HTML expansion.

For large SSE frames, a multiline payload used 12,914,128 bytes with
13,557,394 charged, down from 81,773,656 bytes for the old envelope encode
alone. The single-line path wrote a 2,150,541-byte envelope with 3,536 bytes of
scratch allocation. The source event's payload already had a separate lease.

## Normal-Size Timing

Five-run medians measure estimate plus encode in microseconds per operation.
The benchmark compares `origin/main` at `dd29f4020` with the rebased branch
on the same Apple M4. Both runs use the same fixtures and API toolchain.
The baseline pairs its original estimator with `json.Marshal` and
`StripJSONNUL`.

| Record | Small Before | Small After | Typical Before | Typical After |
| --- | ---: | ---: | ---: | ---: |
| Issue | 13.44 | 5.00 | 28.48 | 19.72 |
| Document | 5.95 | 2.37 | 19.57 | 10.66 |
| Comment | 4.71 | 2.08 | 18.79 | 7.44 |
| Project | 8.28 | 3.68 | 27.05 | 8.69 |

Allocation counts per operation were identical in both runs for these
small and typical fixtures.

From `api/`, run the allocation cases and timing benchmarks with the
machine-wide lock.

```sh
~/.flow-local/heavy.sh env GOMAXPROCS=2 go test -p 1 ./internal/store \
  -run 'Test(MetadataJSONEncoding|MetadataOffloadAssembly|IntegrationDeliveryEncoding|IssueRecordRewrite)' -v -count=1
~/.flow-local/heavy.sh env GOMAXPROCS=2 go test -p 1 ./internal/store \
  -run '^$' -bench '^BenchmarkMetadataJSONEncoding$' -benchmem -count=5
```

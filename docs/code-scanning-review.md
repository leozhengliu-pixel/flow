# Code scanning review: 2026-10-16

Baseline: `6e025b26`, 36 open alerts on `main` (35 CodeQL, one OSV).

Complete-source CodeQL validation on PR #124: JavaScript 15 -> 0, Go 20 -> 8. All 27 findings addressed by code changes disappeared. The remaining eight CodeQL results and the module-only OSV result were reviewed against the protections and reachability evidence below.

## Code changes

| Alerts | Change |
| --- | --- |
| 9 | Keep new API key secrets in memory for the one-time detail view; do not persist them in browser storage. |
| 16, 17 | Server-issued Figma state stays server-side; legacy client state stores only a SHA-256 fingerprint for comparison. |
| 18, 19, 20 | Restrict mobile handoff to the Flow scheme, validate the Figma authorization origin and path, and use router navigation for internal destinations. |
| 22, 23, 47, 49, 50, 51 | Sanitize clipboard HTML with DOMPurify, extract text with a parser, decode entities once, and restrict canonical image sources to HTTP(S). |
| — | Follow-up: Markdown snippets (`markdownPlainText`) strip inline tags with a regex repeated until stable, not the HTML parser. Parsing Markdown as HTML dropped everything after a bare `<` (`if a<b` → `if a`); the snippet is only rendered as text. |
| 45, 46 | Normalize non-breaking spaces in assertions instead of replacing spaces with themselves. |
| 53–59 | JSON-encode signed URLs before inserting them into JSON strings, including Markdown string values. This escapes quotes, backslashes and HTML delimiters while preserving decoded URL values. |
| 60 | Build the webhook body with append operations rather than adding an unchecked length to an allocation capacity. |
| 64 | Exclude both opening and closing braces inside SDK placeholders to prevent quadratic matching of unterminated placeholders. |
| 66–69 | Parse JSON Unicode escape code units at 16 bits before converting to a rune. |

The shared outbound transport also disables environment proxies, which could bypass destination checks, and uses the complete public-address policy from the web fetcher (including CGNAT, multicast, documentation and NAT64 ranges).

## Findings Requiring Context

These findings require reviewing the actual boundary protection. They are not a reason to disable the corresponding scanning rules.

| Alerts | Evidence |
| --- | --- |
| 4, 13: weak password hashing | The data flows originate from a rate-limit category or workspace URL key returned beside an authenticated API key. The SHA-256 operations generate Redis keys and deterministic application-mention task IDs. They never store or verify passwords. Actual account password hashing uses bcrypt. Replacing these stable identifiers with salted password hashes would break coordination or task deduplication. |
| 39, 48, 52, 65: request forgery | Public fetches validate the resolved address at connection time, without an environment proxy. Redirects reuse that transport. Attachments and web fetching reject internal destinations; link previews additionally allow only listed Figma/X hosts. GitLab allows a private host only when explicitly configured by the deployment, or loopback in development mode. Tests cover internal/metadata destinations and reserved address ranges. |
| 70, 71: email injection | The reported flows enter the message body. Bodies use MIME quoted-printable encoding; SMTP uses the standard library's dot-stuffing writer. User-controlled header fields reject CR/LF, and HTML templates escape user text. Regression tests confirm that Bcc-like text remains body content and cannot add headers. |
| 72: GO-2026-5932 | The advisory applies to the unmaintained `golang.org/x/crypto/openpgp` packages, not the entire module. Flow imports bcrypt and SSH from that module, and imports none of the affected OpenPGP packages. `govulncheck ./...` reports zero reachable or imported-package vulnerabilities. No fixed module version exists for this package-specific advisory. |

The [Go advisory](https://pkg.go.dev/vuln/GO-2026-5932) identifies the affected OpenPGP import paths. Keep the existing Go reachability scan enabled so new imports or calls are checked.

## Validation

- Full local Go tests and vet.
- Targeted race tests for outbound requests, attachments, previews, SMTP, signed files, Unicode parsing, webhooks and GitLab.
- Full local web coverage suite, targeted security regressions, lint and build.
- SDK tests, including an adversarial placeholder input, type check and build.
- Go vulnerability reachability scan, npm audit, secret scan and complete-source CodeQL analysis on the PR.

GitHub's default-branch alert status changes after the fixes reach `main` and the corresponding analyses finish. Any false-positive dismissal should retain the specific evidence above rather than hide an entire rule family.

## Follow-up Review: 2026-10-19

Baseline: `01fd06ed`, no open PRs and ten open CodeQL alerts. The latest
completed Go analysis at review time was `1978914325`, on `c0587df6`; newer
`main` analyses were still queued. The SARIF paths, rather than just their
sink locations, were reviewed against the current source.

| Alerts | Evidence and Resolution |
| --- | --- |
| 76: weak password hashing | `connectorSecretID` hashes the workspace URL key, owner ID and connector ID into a stable credential-storage lookup key. The SARIF source is `AuthenticateAPIKeyRecord` followed by selection of `Workspace.URLKey`, not a password or credential value. Stored connector credentials are encrypted separately. Keep the deterministic lookup format and classify this instance as a false positive. |
| 77: weak password hashing | `shortHash` creates turn IDs and review-trigger idempotency keys. Callers hash workspace/session identifiers, run IDs and prompt text for deduplication. SARIF propagates sensitivity through the MCP context containing an API key, including separate context values such as the run ID. This is not password storage or verification; existing trigger tests cover retry deduplication. Classify this instance as a false positive. |
| 53-59: reflected XSS in response middleware | These SARIF paths now originate at `llmproxy.streamBody`'s `io.Reader.Read` and end in generic response-writer forwarding methods. The proxy is registered only under `/internal/llm-proxy/`; the error envelope, file-URL rewriting, API compression and failed-auth recording paths only wrap `/api/` (the error envelope also covers `/uploads/`). These paths therefore cannot run for the reported proxy request. Actual signed-URL JSON still uses `json.Marshal`, with an adversarial-origin regression test. Classify the current paths as false positives; do not remove or globally suppress the XSS query. |
| 75: reflected XSS in the LLM proxy | Review found an additional response-boundary gap: provider responses could preserve an active document media type. The proxy now preserves only valid JSON and SSE media types, serves every other type as `text/plain; charset=utf-8`, and sets `nosniff` plus `sandbox; default-src 'none'` after copying upstream headers. This prevents upstream headers from overriding the protection. Raw JSON/SSE bytes and status codes remain unchanged. A remaining report on the byte-copy operation must be assessed with these enforced response headers, rather than escaping or corrupting the protocol stream. |

`TestUpstreamResponsesCannotServeActiveDocuments` covers JSON, vendor JSON,
SSE, HTML, XHTML, SVG, missing and malformed media types, for both successful
and failing upstream responses. It checks that permissive upstream security
headers cannot override the proxy's policy and that payload bytes remain
unchanged. Existing proxy tests also verify incremental SSE delivery and usage
metering. OAuth refresh, trigger deduplication, API error envelopes, compression
and signed-file URL tests exercise the other reviewed boundaries.

Keep full-source CodeQL analysis and all dependency/security checks enabled.
Each false-positive dismissal must link this evidence and the fixing commit;
it applies only to the reviewed alert, not to future findings from that rule.

## Follow-up Review: 2026-11-25

Baseline: `857198e05`, 37 open alerts on `main`. PR #253 reports the same
findings after the train-16 file renames, so the review below uses the current
paths in this worktree. Alert 132 is fixed in `4a384e3ff`; the remaining
alerts are false positives for the specific boundaries documented here.

| Alert | Rule | Current location | Why safe | Evidence and test |
| --- | --- | --- | --- | --- |
| 219 | `go/reflected-xss` | `api/cmd/server/files_api.go:739` | The middleware buffers successful JSON, rewrites only validated `/uploads/` names, and emits the replacement through `json.Marshal`; upload signing authority rejects names outside the authorized file set. | `public_api_test.go`, `file_signing_authorization_test.go`, `spool_test.go` |
| 179 / 228 (PR #253) | `go/path-injection` | `api/internal/store/migration_stage.go:50` | The stage file is created below the configured metadata spool directory with an `os.CreateTemp` generated basename; request paths never select the filename. | `logical_reload_test.go`, store coverage and inventory guards |
| 178 / 227 (PR #253) | `go/path-injection` | `api/internal/store/logical_spool_io.go:150` | Logical spool files use the configured metadata spool directory and the fixed `flow-p21-logical-*` temporary-name pattern. | `logical_reload_test.go`, store coverage and inventory guards |
| 177 / 226 (PR #253) | `go/path-injection` | `api/internal/store/logical_scratch.go:62` | Scratch files use the existing internal spool directory and the fixed `flow-p21-root-*` temporary-name pattern. | `logical_scratch_test.go`, store coverage and inventory guards |
| 176 / 222 (PR #253) | `go/reflected-xss` | `api/cmd/server/legacy_view_busy.go:61` | The writer either forwards the handler's already-started response or drops it and emits the fixed typed busy response. It never creates response content from the request body. | `legacy_view_lane_test.go`, `retryable_busy_test.go` |
| 170 | `go/weak-sensitive-data-hashing` | `api/internal/domain/resource_slug.go:56` | `DerivedSlugSuffix` hashes a record ID into a stable URL lookup suffix. It is a public identifier component, not a password verifier. | `resource_slug_test.go`, `resource_slug_migration_test.go` |
| 155 | `go/weak-sensitive-data-hashing` | `api/internal/store/issue_activity.go:172` | The digest compares an issue description's before-value during activity merge and is never used for authentication. | `issue_activity_test.go`, activity merge coverage |
| 154 | `go/weak-sensitive-data-hashing` | `api/cmd/server/issue_activity.go:56` | `descriptionHash` is an optimistic activity marker for detecting a description round trip; the original description is not treated as a credential. | activity and issue-history tests |
| 151 / 225 (PR #253) | `go/reflected-xss` | `api/cmd/server/legacy_stream.go:124` | The stream writer forwards bytes produced by an internal handler. Structured handlers encode JSON before this response boundary. | `spool_test.go`, `stream_test.go`, response contract guards |
| 150 / 224 (PR #253) | `go/reflected-xss` | `api/cmd/server/legacy_stream.go:133` | The fallback path sends the already-generated handler bytes after bounded spool draining; it does not interpolate request-derived text into markup. | `spool_test.go`, `stream_test.go`, response contract guards |
| 149 / 223 (PR #253) | `go/reflected-xss` | `api/cmd/server/legacy_stream.go:103` | The JSON-only spool accepts only successful `application/json` responses and forwards the encoded body from the internal handler. | `spool_test.go`, `stream_test.go`, response contract guards |
| 143 | `go/weak-sensitive-data-hashing` | `api/cmd/server/settings_features.go:1303` | `secretHash` stores and looks up high-entropy random API, OAuth, webhook and session tokens. It is not used to verify human passwords, which use bcrypt. | API-key, OAuth, webhook and session authentication tests |
| 141 | `go/weak-sensitive-data-hashing` | `api/internal/store/connector_secrets.go:30` | `ConnectorSecretID` is a deterministic database key from workspace, owner and connector IDs. Secret values are encrypted separately before storage. | connector credential lifecycle tests |
| 140 | `go/weak-sensitive-data-hashing` | `api/internal/store/audit_events.go:378` | SHA-256 forms a salted personal-data commitment and an append-only audit-chain digest. It is an integrity construction, not password hashing. | `audit_events_test.go`, audit-chain verification tests |
| 139 | `go/weak-sensitive-data-hashing` | `api/cmd/server/chat_workflows.go:180` | `chatKey` creates length-delimited deterministic IDs for chat actions and deduplication. It is never a credential verifier. | `chat_workflows_test.go`, `chat_requests_test.go`, chat mutation tests |
| 133 | `go/incorrect-integer-conversion` | `api/cmd/server/loop_instructions.go:79` | Heading levels are accepted only when the decoded value is between 1 and 6, so the conversion cannot overflow. | `loop_instructions_markdown_test.go`, workspace Markdown contract tests |
| 131 | `go/weak-sensitive-data-hashing` | `api/cmd/server/chat_callback.go:112` | SHA-256 binds the callback timestamp, nonce, encryption key and body for the chat provider's required signature protocol. | `chat_callback_test.go`, `chat_inbound_test.go` |
| 130 | `js/double-escaping` | `web/src/i18n/t-key-coverage.test.ts:40` | The replacement normalizes HTML entities in static test-source literals while collecting translation keys. The value never reaches a rendered HTML sink. | `t-key-coverage.test.ts` |
| 126 | `go/weak-sensitive-data-hashing` | `api/cmd/server/chat_message_actions.go:186` | SHA-1 is the external H5 SDK's required signature over the ticket, nonce, timestamp and page URL, with the URL constrained to the generated shortcut page. | `chat_message_actions_test.go` |
| 122 | `go/weak-sensitive-data-hashing` | `api/cmd/server/agent_run_outcomes.go:45` | The truncated SHA-256 value is an idempotency key from a call key, tool and target. It deduplicates run ledger rows and does not authenticate a password. | agent-run tool-call and retry tests |
| 121 | `js/unvalidated-dynamic-method-call` | `web/e2e/security/harness.ts:224` | The dynamic lookup selects a test-provided `intercept` handler by exact request path. Missing paths fall through to the proxy and are not invoked. | security harness tests |
| 117 | `go/weak-sensitive-data-hashing` | `api/internal/store/text_limits.go:132` | `indexKey` hashes oversized or invalid database-key text to keep a bounded deterministic lookup key. It is not a secret or verifier. | `issue_attribute_field_width_test.go`, collection-read tests |
| 111 | `js/bad-tag-filter` | `web/src/lib/startup-asset-reload.test.ts:7` | The regex reads the repository-controlled inline startup script from `index.html` for a test and has no user-controlled HTML source or sink. | `startup-asset-reload.test.ts` |
| 110 | `go/weak-sensitive-data-hashing` | `api/internal/store/issue_records.go:111` | SHA-256 fingerprints serialized issue rows for optimistic-concurrency checks and never leaves the store as a credential. | issue-record concurrency and write tests |
| 109 | `go/reflected-xss` | `api/cmd/server/panic_recovery.go:101` | Started responses are forwarded as-is, while an unstarted panic discards handler headers and emits a fixed JSON error envelope. | `panic_recovery_test.go` |
| 107 | `go/path-injection` | `api/cmd/server/security_headers.go:248` | The path is the operator-configured static directory plus the fixed `index.html` basename. It is read only to compute CSP hashes. | `security_headers_test.go`, `static_files_test.go` |
| 106 | `go/path-injection` | `api/cmd/server/security_headers.go:238` | `os.Stat` receives the same configured static directory plus the fixed `index.html` path; request paths do not influence it. | `security_headers_test.go`, `static_files_test.go` |
| 105 | `go/reflected-xss` | `api/cmd/server/security_headers.go:397` | The wrapper applies security headers before forwarding handler bytes and does not generate HTML from request data. | `security_headers_test.go`, `security_headers_surfaces_test.go` |
| 104 | `GO-2026-5932` | `api/go.mod:1` | The advisory targets `golang.org/x/crypto/openpgp`. Production OpenPGP code imports the maintained `github.com/ProtonMail/go-crypto/openpgp` fork; x/crypto imports are bcrypt, SSH and blake2b. | `rg` import audit and Go dependency reachability checks |
| 89 | `js/insufficient-password-hash` | `sandbox-runner/src/git/credential-helper.ts:66` | The SHA-256 is a redacted fingerprint for invalidating a rejected short-lived access token. The credential is never persisted or used as a password verifier. | `sandbox-runner/test/git-shims.test.ts` |
| 88 | `go/weak-sensitive-data-hashing` | `api/internal/codehost/gitlab/shared_tokens.go:107` | `sharedTokenID` is a deterministic storage ID from workspace and token-name metadata. The token value is stored separately and is not verified by this digest. | GitLab shared-token lifecycle tests |
| 87 | `go/weak-sensitive-data-hashing` | `api/internal/codehost/codehost.go:123` | `CredentialFingerprint` identifies a short-lived host credential for invalidation and redacted logs. It is not a password database or authentication check. | credential invalidation and GitLab replica tests |
| 86 | `js/shell-command-injection-from-environment` | `sandbox-runner/src/process.ts:41` | The shell wrapper is fixed except for the numeric `Math.floor(pidsLimit)` value; the executable and arguments are passed as quoted `$0` and `$@`. | `sandbox-runner/test/components.test.ts`, process tests |
| 85 | `js/request-forgery` | `sandbox-runner/test/proxy-env.test.ts:145` | The test proxy checks an allowlist before requesting the fixed local upstream and copies only the target pathname. | `sandbox-runner/test/proxy-env.test.ts` |
| 78 | `go/reflected-xss` | `api/cmd/server/coding_git_relay.go:139` | The route accepts only authenticated smart-HTTP Git paths and blocks redirects, auth failures, other repositories and non-Git paths before forwarding the protocol body. | `coding_git_relay_test.go` |
| 75 | `go/reflected-xss` | `api/internal/llmproxy/serve.go:351` | The proxy permits JSON and SSE media types, converts every other or malformed type to `text/plain`, and enforces `nosniff` plus `sandbox; default-src 'none'` after upstream headers. | `response_security_test.go` covers JSON, vendor JSON, SSE, HTML, XHTML, SVG, missing and malformed types |

Alert 132's code change uses the same fallback numbering for invalid list
metadata while rejecting fractional, infinite and out-of-range `start` values.
The focused regression is `api/cmd/server/chat_update_cards_test.go`.

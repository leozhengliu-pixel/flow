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

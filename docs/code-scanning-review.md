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

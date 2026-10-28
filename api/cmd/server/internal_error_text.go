package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"

	"flow/api/internal/objectstore"
	"flow/api/internal/store"
)

// isInternalError reports an error whose text must never reach a client or an
// agent model: a database or driver error, an object-storage error, or (for
// one whose type was lost to %v wrapping or string building) text that reads
// like one.
func isInternalError(err error) bool {
	if err == nil {
		return false
	}
	return store.IsDatabaseError(err) || objectstore.IsStorageError(err) || looksLikeInternalErrorText(err.Error())
}

// internalErrorTextPattern matches the fixed phrases MySQL, Postgres, SQLite,
// database/sql, the S3 SDK and the file system put in their error text.
// Deliberate client messages never contain them; a message that does is a
// backend error passed on by mistake.
var internalErrorTextPattern = regexp.MustCompile(strings.Join([]string{
	// MySQL (go-sql-driver formats "Error 1062 (23000): ..." and "Error 1062: ...").
	`\bError \d{4}( \([0-9A-Z]{5}\))?: `,
	`Duplicate entry '`,
	`Data too long for column`,
	`Incorrect (string|integer|datetime|decimal) value`,
	`Deadlock found when trying`,
	`Lock wait timeout exceeded`,
	`You have an error in your SQL syntax`,
	`Too many words in a FTS phrase`,
	// Postgres (pgx "ERROR: ... (SQLSTATE 23505)", lib/pq "pq: ...").
	`\bSQLSTATE\b`,
	`\bpq: `,
	`duplicate key value violates`,
	`violates (unique|foreign key|not-null|check|exclusion) constraint`,
	`syntax error at or near`,
	`failed to connect to ` + "`",
	// SQLite (modernc "SQL logic error: no such table: x (1)").
	`(?i)\bsqlite3?: `,
	`SQLITE_[A-Z]+`,
	`\b(UNIQUE|FOREIGN KEY|NOT NULL|CHECK|PRIMARY KEY) constraint failed`,
	`SQL logic error`,
	`no such (table|column): `,
	`database (table )?is locked`,
	// database/sql and drivers.
	`\bsql: (no rows in result set|transaction has already been|connection is already closed|database is closed|Scan error|expected \d+ destination)`,
	`driver: bad connection`,
	`\binvalid connection\b`,
	`\bdial tcp\b`,
	// The S3 SDK (S3, GCS, Azure, R2 and MinIO through the S3 API).
	`operation error S3: `,
	`https response error StatusCode: `,
	`\bapi error [A-Za-z]+: `,
	`\bRequestID: `,
	`\bNoSuch(Bucket|Key|Upload)\b`,
	`(?i)https?://[^\s"']*\.(amazonaws\.com|googleapis\.com|core\.windows\.net|cloudflarestorage\.com|digitaloceanspaces\.com|backblazeb2\.com|wasabisys\.com)`,
	`\bobject storage (put|open|delete): `,
	// File system path errors ("open /var/lib/flow/uploads/x: no such file or directory").
	`\b(open|stat|lstat|remove|unlinkat|mkdir|rename|readdirent|write|read|close) /[^\s:]*: `,
}, "|"))

// internalErrorHosts are the configured object-storage endpoint hosts; a
// message naming one is storage error text.
var internalErrorHosts atomic.Pointer[[]string]

// registerInternalErrorEndpoint adds a configured storage endpoint (a URL or a
// host) to the hosts looksLikeInternalErrorText refuses.
func registerInternalErrorEndpoint(endpoint string) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return
	}
	host := endpoint
	if parsed, err := url.Parse(endpoint); err == nil && parsed.Host != "" {
		host = parsed.Host
	}
	host = strings.ToLower(host)
	if len(host) < 4 {
		return
	}
	current := internalErrorHosts.Load()
	next := []string{host}
	if current != nil {
		next = append(append([]string{}, (*current)...), host)
	}
	internalErrorHosts.Store(&next)
}

// looksLikeInternalErrorText reports text that reads like a database, driver,
// object-storage or file system error.
func looksLikeInternalErrorText(text string) bool {
	if text == "" {
		return false
	}
	if internalErrorTextPattern.MatchString(text) {
		return true
	}
	if hosts := internalErrorHosts.Load(); hosts != nil {
		lower := strings.ToLower(text)
		for _, host := range *hosts {
			if strings.Contains(lower, host) {
				return true
			}
		}
	}
	return false
}

// clientErrorFallbackMessage replaces a 4xx message that reads like backend
// error text; the original is logged with the request id.
const clientErrorFallbackMessage = "The request could not be completed"

func clientErrorFallback(requestID string) string {
	if requestID == "" {
		return clientErrorFallbackMessage + "."
	}
	return fmt.Sprintf("%s (request id %s).", clientErrorFallbackMessage, requestID)
}

// hideInternalClientError is the 4xx half of the error envelope's last line:
// it reports whether message (or an extra field) reads like backend error
// text and, if so, logs the body with the request id.
func hideInternalClientError(r *http.Request, status int, message string, fields map[string]any) bool {
	leaked := looksLikeInternalErrorText(message)
	for key, value := range fields {
		if key == "error" || key == "message" {
			continue
		}
		if text, ok := value.(string); ok && looksLikeInternalErrorText(text) {
			leaked = true
		}
	}
	if !leaked {
		return false
	}
	slog.ErrorContext(r.Context(), "client error response carried backend error text", "request_id", requestIDFrom(r.Context()), "method", r.Method, "path", r.URL.Path, "status", status, "error", message, "fields", fields)
	return true
}

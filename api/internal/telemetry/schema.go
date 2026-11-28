// Package telemetry defines the only records Flow sends to OTLP. It does not
// register a global SDK, propagate incoming trace context, or export app logs.
package telemetry

import (
	"encoding/hex"
	"net"
	"regexp"
	"slices"
	"strings"
	"time"

	common "go.opentelemetry.io/proto/otlp/common/v1"
)

const Unmatched = "unmatched"

type Stage uint8

const (
	Authentication Stage = iota
	Authorization
	AuthorizationBody
	MetadataRead
	MetadataSelect
	MetadataHydrate
	MetadataInstall
	Mutation
	MutationLock
	MutationLoad
	MutationApply
	MutationPersist
	MutationCommit
	Commit
	SQL
	SQLBegin
	WorkAdmission
	Realtime
	DocumentSave
	MCP
	External
	Job
	stageCount
)

var stageNames = [stageCount]string{
	"authentication", "authorization", "authorization.body", "metadata.read", "metadata.select",
	"metadata.hydrate", "metadata.install", "mutation", "mutation.lock_wait", "mutation.load",
	"mutation.apply", "mutation.persist", "mutation.commit", "db.commit", "db.statement", "db.begin",
	"metadata.work.admission", "realtime.fanout", "document.save", "mcp.tool", "external.call", "background.job",
}

type WorkKind uint8

const (
	WorkOther WorkKind = iota
	WorkAuth
	WorkRead
	WorkHydrate
	WorkInstall
	WorkWrite
	WorkDocumentBody
	WorkRealtime
	WorkBackground
	WorkKindCount
)

var workNames = [WorkKindCount]string{"other", "authorization", "read", "hydration", "install", "mutation", "document_body", "realtime", "background"}

func (k WorkKind) String() string {
	if k >= WorkKindCount {
		return workNames[0]
	}
	return workNames[k]
}

type WorkHolder struct {
	Kind  WorkKind
	Route string
	Bytes int64
}

var suspicious = regexp.MustCompile(`(?i)(@|https?://|[?#\s]|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|(?:bearer|token|secret|password)[=:]|(?:sk-|gh[pousr]_|eyJ)[A-Za-z0-9_-]{12,})`)

func clean(text string) bool { return len(text) <= 180 && !suspicious.MatchString(text) }

// These values are selected from constants, never copies of a request value.
func method(s string) string {
	switch s {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
		return s
	default:
		return "OTHER"
	}
}

func statusClass(code int) string {
	switch code / 100 {
	case 1:
		return "1xx"
	case 2:
		return "2xx"
	case 3:
		return "3xx"
	case 4:
		return "4xx"
	case 5:
		return "5xx"
	default:
		return "other"
	}
}

var sqlKinds = []string{"SELECT", "INSERT", "UPDATE", "DELETE", "WITH", "PRAGMA", "SET", "SAVEPOINT", "RELEASE", "ROLLBACK", "CREATE", "ALTER", "DROP", "OTHER"}
var sqlTables = []string{
	"workspace_states", "workspace_metadata_records", "workspace_metadata_catalog",
	"workspace_record_journal", "workspace_record_versions", "workspace_record_catalog",
	"workspace_record_journal_changes", "workspace_record_catalog_checkpoints", "workspace_metadata_blobs",
	"issues", "issue_records", "issue_catalog", "issue_comments", "comments", "documents",
	"users", "sessions", "auth_sessions", "auth_users", "workspace_members", "team_members",
	"issue_change_log", "change_log", "workspace_changes", "domain_events", "webhook_deliveries",
	"notifications", "agent_runs", "agent_sessions", "metadata_blobs", "audit_logs",
	"metadata_catalog", "metadata_catalog_collections", "metadata_catalog_relations", "record_journal",
	"record_versions", "issue_search_documents", "issue_permission_records", "issue_collection_counts",
	"issue_attribute_records", "workspace_cache_revisions", "workspace_issues", "workspace_change_log",
	"document_collaboration_updates", "audit_events", "coding_sessions", "api_key_lookup",
}

// sqlIdentity only returns known verbs and schema names. Skip literals and
// comments. CTEs and unrecognized tables keep their kind with no table label.
func sqlIdentity(query string) (string, string) {
	word := func() string {
		i := 0
		for i < len(query) && (query[i] == ' ' || query[i] == '\t' || query[i] == '\r' || query[i] == '\n' || query[i] == ',') {
			i++
		}
		query = query[i:]
		i = 0
		for i < len(query) && i < 80 {
			c := query[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
				break
			}
			i++
		}
		// Bounded lookahead; a single quoted expression or identifier can be
		// skipped when searching for the first FROM.
		if i == 0 && len(query) > 0 {
			c := query[0]
			query = query[1:]
			if c == '\'' || c == '"' || c == '`' {
				if end := strings.IndexByte(query, c); end >= 0 {
					query = query[end+1:]
				} else {
					query = ""
				}
			}
			return ""
		}
		token := query[:i]
		query = query[i:]
		return token
	}
	kind := "OTHER"
	verb := word()
	for _, candidate := range sqlKinds {
		if strings.EqualFold(verb, candidate) {
			kind = candidate
			break
		}
	}
	var table string
	switch kind {
	case "UPDATE":
		table = word()
	case "INSERT", "DELETE", "SELECT":
		needle := "FROM"
		if kind == "INSERT" {
			needle = "INTO"
		}
		for i := 0; i < 48 && query != ""; i++ {
			if strings.HasPrefix(strings.TrimLeft(query, " \t\r\n"), "--") || strings.HasPrefix(strings.TrimLeft(query, " \t\r\n"), "/*") {
				break
			}
			if strings.EqualFold(word(), needle) {
				table = word()
				break
			}
		}
	}
	for _, candidate := range sqlTables {
		if strings.EqualFold(table, candidate) {
			return kind, candidate
		}
	}
	return kind, ""
}

var mutationKinds = []string{
	"issue.created", "issue.updated", "issue.deleted", "issue.restored", "issue.batch_updated",
	"comment.created", "comment.updated", "comment.deleted",
	"document.created", "document.updated", "document.deleted", "document.restored",
	"project.created", "project.updated", "project.deleted", "initiative.created", "initiative.updated",
	"agent_session.created", "agent_session.updated", "agent_run.started", "agent_run.completed",
	"triage.suggestion_created", "triage.suggestions_updated", "workspace.updated",
}

var jobKinds = []string{"webhook", "import", "triage", "recurring", "agent_run"}
var messageKinds = []string{
	"connected", "keepalive", "sync.delta", "workspace.resync_required", "workspace.updated", "workspace.settings_updated",
	"issue.created", "issue.updated", "issue.deleted", "issue.restored", "issue.batch_updated",
	"comment.created", "comment.updated", "comment.deleted", "document.created", "document.updated",
	"document.deleted", "document.restored", "presence.updated", "notification.created", "project.updated", "initiative.updated",
	"binary", "text", "document.join", "document.leave", "document.update", "document.awareness",
	"document.sync", "document.access", "document.revoked", "document.readonly", "document.conflict", "error",
}

func enum(value string, values []string) string {
	if i := slices.Index(values, value); i >= 0 {
		return values[i]
	}
	return "other"
}

var dnsName = regexp.MustCompile(`(?i)^[a-z0-9](?:[a-z0-9.-]{0,148}[a-z0-9])?$`)

func safeHost(host string) string {
	if clean(host) && dnsName.MatchString(host) && net.ParseIP(host) == nil {
		return host
	}
	return ""
}

func (l labels) attrs() []*common.KeyValue {
	var out []*common.KeyValue
	for _, item := range [4][2]string{{"http.route", l.route}, {"flow.method", l.method}, {"flow.status", l.status}, {"flow.kind", l.kind}} {
		if item[1] != "" && clean(item[1]) {
			out = append(out, strAttr(item[0], item[1]))
		}
	}
	return out
}

func strValue(v string) *common.AnyValue {
	return &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: v}}
}

func strAttr(key, v string) *common.KeyValue {
	return &common.KeyValue{Key: key, Value: strValue(v)}
}

func intAttr(key string, v int64) *common.KeyValue {
	return &common.KeyValue{Key: key, Value: &common.AnyValue{Value: &common.AnyValue_IntValue{IntValue: v}}}
}

func nano(t time.Time) uint64 { return uint64(t.UnixNano()) }
func hexID(v []byte) string   { return hex.EncodeToString(v) }

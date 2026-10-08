package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"flow/api/internal/domain"
)

func (s *SQLiteStore) ensureCustomerFilterIndex(ctx context.Context) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS customer_filter_records(workspace_key VARCHAR(191) NOT NULL,customer_id VARCHAR(191) NOT NULL,owner_id VARCHAR(191) NOT NULL,status_value VARCHAR(191) NOT NULL,tier_value VARCHAR(191) NOT NULL,revenue DOUBLE PRECISION NOT NULL,customer_size BIGINT NOT NULL,PRIMARY KEY(workspace_key,customer_id))`,
		`CREATE TABLE IF NOT EXISTS customer_request_filter_records(workspace_key VARCHAR(191) NOT NULL,request_id VARCHAR(191) NOT NULL,issue_id VARCHAR(191) NOT NULL,customer_id VARCHAR(191) NOT NULL,important INTEGER NOT NULL DEFAULT 0,project_id VARCHAR(191) NOT NULL DEFAULT '',PRIMARY KEY(workspace_key,request_id))`,
		`CREATE TABLE IF NOT EXISTS customer_filter_migrations(workspace_key VARCHAR(191) NOT NULL,field VARCHAR(191) NOT NULL,last_id VARCHAR(191) NOT NULL,complete INTEGER NOT NULL,PRIMARY KEY(workspace_key,field))`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	// Important requests (priority >= 1) feed "Important customer count" filters and ordering. Older
	// databases gain the column here and backfill it through the v2 request migration below.
	// Project-level requests (no issue) are indexed with their project for project summaries.
	for _, column := range []string{`important INTEGER NOT NULL DEFAULT 0`, `project_id VARCHAR(191) NOT NULL DEFAULT ''`} {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE customer_request_filter_records ADD COLUMN `+column); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") && !strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return err
		}
	}
	for name, columns := range map[string]string{"customer_request_issue_idx": "workspace_key,issue_id,customer_id", "customer_request_customer_idx": "workspace_key,customer_id,issue_id"} {
		statement := "CREATE INDEX IF NOT EXISTS " + name + " ON customer_request_filter_records(" + columns + ")"
		if s.dialect == "mysql" {
			statement = strings.Replace(statement, "IF NOT EXISTS ", "", 1)
		}
		if _, err := s.db.ExecContext(ctx, statement); err != nil && !(s.dialect == "mysql" && isDuplicateIndex(err)) {
			return err
		}
	}
	return nil
}

func writeCustomerFilterRecord(ctx context.Context, tx *sqlTx, workspace, field, id string, raw []byte) error {
	switch field {
	case "customers":
		if raw == nil {
			_, err := tx.ExecContext(ctx, `DELETE FROM customer_filter_records WHERE workspace_key=? AND customer_id=?`, workspace, id)
			return err
		}
		var customer domain.Customer
		if err := json.Unmarshal(raw, &customer); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO customer_filter_records(workspace_key,customer_id,owner_id,status_value,tier_value,revenue,customer_size) VALUES(?,?,?,?,?,?,?) ON CONFLICT(workspace_key,customer_id) DO UPDATE SET owner_id=excluded.owner_id,status_value=excluded.status_value,tier_value=excluded.tier_value,revenue=excluded.revenue,customer_size=excluded.customer_size`, workspace, id, customer.OwnerID, customer.Status, customer.Tier, customer.AnnualRevenue, customer.Size)
		return err
	case "customerRequests", customerRequestImportanceMigration:
		var request domain.CustomerRequest
		if raw != nil {
			if err := json.Unmarshal(raw, &request); err != nil {
				return err
			}
		}
		if raw == nil || request.ArchivedAt != nil || request.IssueID == "" && request.ProjectID == "" {
			_, err := tx.ExecContext(ctx, `DELETE FROM customer_request_filter_records WHERE workspace_key=? AND request_id=?`, workspace, id)
			return err
		}
		important := 0
		if request.Priority >= 1 {
			important = 1
		}
		projectID := ""
		if request.IssueID == "" {
			projectID = request.ProjectID
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO customer_request_filter_records(workspace_key,request_id,issue_id,customer_id,important,project_id) VALUES(?,?,?,?,?,?) ON CONFLICT(workspace_key,request_id) DO UPDATE SET issue_id=excluded.issue_id,customer_id=excluded.customer_id,important=excluded.important,project_id=excluded.project_id`, workspace, id, request.IssueID, request.CustomerID, important, projectID)
		return err
	}
	return nil
}

// customerRequestImportanceMigration re-reads customer requests once more so rows
// written before the important / project_id columns existed pick them up.
const customerRequestImportanceMigration = "customerRequests:important"

// Migrate metadata in bounded, checkpointed transactions without reading issue
// payloads. Normal writes maintain these rows in the same transaction.
func (s *SQLiteStore) migrateCustomerFilterIndex(ctx context.Context) error {
	for _, workspace := range s.WorkspaceKeys() {
		for _, field := range []string{"customers", "customerRequests", customerRequestImportanceMigration} {
			source := field
			if field == customerRequestImportanceMigration {
				source = "customerRequests"
			}
			if _, err := s.db.ExecContext(ctx, `INSERT INTO customer_filter_migrations(workspace_key,field,last_id,complete) VALUES(?,?,'',0) ON CONFLICT DO NOTHING`, workspace, field); err != nil {
				return err
			}
			for {
				var last string
				var done int
				if err := s.db.QueryRowContext(ctx, `SELECT last_id,complete FROM customer_filter_migrations WHERE workspace_key=? AND field=?`, workspace, field).Scan(&last, &done); err != nil {
					return err
				}
				if done != 0 {
					break
				}
				tx, err := s.db.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				err = func() error {
					lock := ""
					if s.dialect != "sqlite" {
						lock = " FOR UPDATE"
					}
					rows, err := tx.QueryContext(ctx, `SELECT record_key,data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key>? ORDER BY record_key LIMIT 128`+lock, workspace, source, last)
					if err != nil {
						return err
					}
					type record struct {
						id  string
						raw []byte
					}
					batch := []record{}
					for rows.Next() {
						var item record
						if err := rows.Scan(&item.id, &item.raw); err != nil {
							rows.Close()
							return err
						}
						batch = append(batch, item)
					}
					err = rows.Err()
					rows.Close()
					if err != nil {
						return err
					}
					for _, item := range batch {
						if err := writeCustomerFilterRecord(ctx, tx, workspace, field, item.id, item.raw); err != nil {
							return err
						}
						last = item.id
					}
					if len(batch) < 128 {
						done = 1
					}
					_, err = tx.ExecContext(ctx, `UPDATE customer_filter_migrations SET last_id=?,complete=? WHERE workspace_key=? AND field=?`, last, done, workspace, field)
					return err
				}()
				if err != nil {
					tx.Rollback()
					return err
				}
				if err := tx.Commit(); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func compileCustomerFilter(node IssueFilter) (string, []any, error) {
	op := strings.ToLower(node.Operator)
	if op == "" {
		op = "is"
	}
	// Number comparisons may come as the node operator (`gte` + `customer-count:3`) or
	// inside the value (`customer-count:gte:3`, what the web client sends).
	comparison := ""
	switch op {
	case "is", "in", "isnot", "notin":
	case "gte", "lte", "eq", "neq":
		comparison, op = op, "is"
	default:
		return "", nil, fmt.Errorf("%w: unsupported customer operator", ErrIssueQuery)
	}
	base := ` FROM customer_request_filter_records r LEFT JOIN customer_filter_records c ON c.workspace_key=r.workspace_key AND c.customer_id=r.customer_id WHERE r.workspace_key=i.workspace_key AND r.issue_id=i.id`
	exists := func(condition string) string { return "EXISTS (SELECT 1" + base + " AND " + condition + ")" }
	clauses := []string{}
	args := []any{}
	for _, value := range node.Values {
		if clause, number, ok, err := compileCustomerNumberCondition(value, comparison, base, exists); err != nil {
			return "", nil, err
		} else if ok {
			clauses = append(clauses, "("+clause+")")
			args = append(args, number)
			continue
		}
		if comparison != "" {
			return "", nil, fmt.Errorf("%w: invalid customer number condition", ErrIssueQuery)
		}
		var clause string
		switch {
		case value == "customer:":
			clause = exists("c.customer_id IS NULL")
		case strings.HasPrefix(value, "customer:"):
			clause = exists("c.customer_id=?")
			args = append(args, strings.TrimPrefix(value, "customer:"))
		case strings.HasPrefix(value, "customer-count:"):
			count := strings.TrimPrefix(value, "customer-count:")
			operator := map[string]string{"0": "=0", "1": "=1", "2+": ">=2"}[count]
			if operator == "" {
				return "", nil, ErrIssueQuery
			}
			clause = "(SELECT COUNT(DISTINCT COALESCE(c.customer_id,''))" + base + ")" + operator
		case value == "customer-owner:":
			clause = exists("c.customer_id IS NOT NULL") + " AND NOT " + exists("c.owner_id<>''")
		case strings.HasPrefix(value, "customer-owner:"):
			clause = exists("c.owner_id=?")
			args = append(args, strings.TrimPrefix(value, "customer-owner:"))
		case strings.HasPrefix(value, "customer-status:"):
			clause = exists("c.status_value=?")
			args = append(args, strings.TrimPrefix(value, "customer-status:"))
		case strings.HasPrefix(value, "customer-tier:"):
			clause = exists("c.tier_value=?")
			args = append(args, strings.TrimPrefix(value, "customer-tier:"))
		case value == "customer-revenue:":
			clause = exists("c.customer_id IS NOT NULL") + " AND NOT " + exists("c.revenue>0")
		case value == "customer-revenue:any":
			clause = exists("c.revenue>0")
		case value == "customer-size:":
			clause = exists("c.customer_id IS NOT NULL") + " AND NOT " + exists("c.customer_size>0")
		case value == "customer-size:any":
			clause = exists("c.customer_size>0")
		default:
			return "", nil, fmt.Errorf("%w: invalid customer condition", ErrIssueQuery)
		}
		clauses = append(clauses, "("+clause+")")
	}
	clause := "1=0"
	if len(clauses) > 0 {
		clause = "(" + strings.Join(clauses, " OR ") + ")"
	}
	if op == "isnot" || op == "notin" {
		clause = "NOT (" + clause + ")"
	}
	return clause, args, nil
}

var customerNumberSQLOperators = map[string]string{"gte": ">=", "lte": "<=", "eq": "=", "neq": "<>"}

// compileCustomerNumberCondition handles Linear's number blocks: customer count and
// important customer count compare the issue's totals; revenue and size match
// when some requesting customer satisfies the comparison.
func compileCustomerNumberCondition(value, comparison, base string, exists func(string) string) (string, float64, bool, error) {
	for _, prefix := range []string{"customer-important-count:", "customer-count:", "customer-revenue:", "customer-size:"} {
		if !strings.HasPrefix(value, prefix) {
			continue
		}
		rest := strings.TrimPrefix(value, prefix)
		op := comparison
		if parts := strings.SplitN(rest, ":", 2); len(parts) == 2 {
			if _, known := customerNumberSQLOperators[parts[0]]; !known {
				return "", 0, false, fmt.Errorf("%w: invalid customer comparison", ErrIssueQuery)
			}
			op, rest = parts[0], parts[1]
		}
		if op == "" {
			return "", 0, false, nil
		}
		number, err := strconv.ParseFloat(rest, 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return "", 0, false, fmt.Errorf("%w: invalid customer number", ErrIssueQuery)
		}
		sqlOp := customerNumberSQLOperators[op]
		switch prefix {
		case "customer-count:":
			return "(SELECT COUNT(DISTINCT COALESCE(c.customer_id,''))" + base + ")" + sqlOp + "?", number, true, nil
		case "customer-important-count:":
			return "(SELECT COUNT(DISTINCT c.customer_id)" + base + " AND r.important<>0)" + sqlOp + "?", number, true, nil
		case "customer-revenue:":
			return exists("c.revenue" + sqlOp + "?"), number, true, nil
		default:
			return exists("c.customer_size" + sqlOp + "?"), number, true, nil
		}
	}
	return "", 0, false, nil
}

// customerSortExpression orders issues by Linear's customer orderings with one
// indexed probe of customer_request_issue_idx per row.
func customerSortExpression(sort string) string {
	from := " FROM customer_request_filter_records r LEFT JOIN customer_filter_records c ON c.workspace_key=r.workspace_key AND c.customer_id=r.customer_id WHERE r.workspace_key=i.workspace_key AND r.issue_id=i.id"
	switch sort {
	case "customerCount":
		return "(SELECT COUNT(DISTINCT COALESCE(c.customer_id,''))" + from + ")"
	case "customerImportantCount":
		return "(SELECT COUNT(DISTINCT c.customer_id)" + from + " AND r.important<>0)"
	case "customerRevenue":
		return "(SELECT COALESCE(SUM(c.revenue),0) FROM customer_filter_records c WHERE c.workspace_key=i.workspace_key AND c.customer_id IN (SELECT r.customer_id FROM customer_request_filter_records r WHERE r.workspace_key=i.workspace_key AND r.issue_id=i.id))"
	}
	return ""
}

// UnknownCustomerGroup is the customer group of requests whose customer no
// longer exists (Linear's "Unknown customer").
const UnknownCustomerGroup = "__unknown__"

// customerGroupClause scopes a page to one customer group: "" is "No customer".
func customerGroupClause(value string) (string, []any) {
	base := `SELECT 1 FROM customer_request_filter_records r LEFT JOIN customer_filter_records c ON c.workspace_key=r.workspace_key AND c.customer_id=r.customer_id WHERE r.workspace_key=i.workspace_key AND r.issue_id=i.id`
	switch value {
	case "":
		return "NOT EXISTS (" + base + ")", nil
	case UnknownCustomerGroup:
		return "EXISTS (" + base + " AND c.customer_id IS NULL)", nil
	}
	return "EXISTS (" + base + " AND c.customer_id=?)", []any{value}
}

// customerIssueGroups counts issues per requesting customer through the
// customer request index, plus the "No customer" group.
func (s *SQLiteStore) customerIssueGroups(ctx context.Context, query IssueRecordQuery) ([]IssueRecordGroup, error) {
	where, args, err := issueRecordWhere(query)
	if err != nil {
		return nil, err
	}
	prefix, prefixArgs := issueAccessCTE(query)
	key := "COALESCE(c.customer_id,'" + UnknownCustomerGroup + "')"
	rows, err := s.db.QueryContext(ctx, prefix+"SELECT "+key+",COUNT(DISTINCT i.id) FROM issue_records i JOIN customer_request_filter_records r ON r.workspace_key=i.workspace_key AND r.issue_id=i.id LEFT JOIN customer_filter_records c ON c.workspace_key=r.workspace_key AND c.customer_id=r.customer_id WHERE "+where+" GROUP BY "+key+" ORDER BY "+key, append(prefixArgs, args...)...)
	if err != nil {
		return nil, err
	}
	groups := []IssueRecordGroup{}
	for rows.Next() {
		var group IssueRecordGroup
		if err := rows.Scan(&group.Value, &group.Count); err != nil {
			rows.Close()
			return nil, err
		}
		groups = append(groups, group)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	none, noneArgs := customerGroupClause("")
	var count int64
	if err := s.db.QueryRowContext(ctx, prefix+"SELECT COUNT(*) FROM issue_records i WHERE "+where+" AND "+none, append(append(prefixArgs, args...), noneArgs...)...).Scan(&count); err != nil {
		return nil, err
	}
	if count > 0 {
		groups = append(groups, IssueRecordGroup{Value: "", Count: count})
	}
	return groups, nil
}

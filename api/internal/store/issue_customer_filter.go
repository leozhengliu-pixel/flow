package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"flow/api/internal/domain"
)

func (s *SQLiteStore) ensureCustomerFilterIndex(ctx context.Context) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS customer_filter_records(workspace_key VARCHAR(191) NOT NULL,customer_id VARCHAR(191) NOT NULL,owner_id VARCHAR(191) NOT NULL,status_value VARCHAR(191) NOT NULL,tier_value VARCHAR(191) NOT NULL,revenue DOUBLE PRECISION NOT NULL,customer_size BIGINT NOT NULL,PRIMARY KEY(workspace_key,customer_id))`,
		`CREATE TABLE IF NOT EXISTS customer_request_filter_records(workspace_key VARCHAR(191) NOT NULL,request_id VARCHAR(191) NOT NULL,issue_id VARCHAR(191) NOT NULL,customer_id VARCHAR(191) NOT NULL,PRIMARY KEY(workspace_key,request_id))`,
		`CREATE TABLE IF NOT EXISTS customer_filter_migrations(workspace_key VARCHAR(191) NOT NULL,field VARCHAR(191) NOT NULL,last_id VARCHAR(191) NOT NULL,complete INTEGER NOT NULL,PRIMARY KEY(workspace_key,field))`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
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
	case "customerRequests":
		var request domain.CustomerRequest
		if raw != nil {
			if err := json.Unmarshal(raw, &request); err != nil {
				return err
			}
		}
		if raw == nil || request.ArchivedAt != nil || request.IssueID == "" {
			_, err := tx.ExecContext(ctx, `DELETE FROM customer_request_filter_records WHERE workspace_key=? AND request_id=?`, workspace, id)
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO customer_request_filter_records(workspace_key,request_id,issue_id,customer_id) VALUES(?,?,?,?) ON CONFLICT(workspace_key,request_id) DO UPDATE SET issue_id=excluded.issue_id,customer_id=excluded.customer_id`, workspace, id, request.IssueID, request.CustomerID)
		return err
	}
	return nil
}

// Migrate metadata in bounded, checkpointed transactions without reading issue
// payloads. Normal writes maintain these rows in the same transaction.
func (s *SQLiteStore) migrateCustomerFilterIndex(ctx context.Context) error {
	for _, workspace := range s.WorkspaceKeys() {
		for _, field := range []string{"customers", "customerRequests"} {
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
					rows, err := tx.QueryContext(ctx, `SELECT record_key,data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key>? ORDER BY record_key LIMIT 128`+lock, workspace, field, last)
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
	if op != "is" && op != "in" && op != "isnot" && op != "notin" {
		return "", nil, fmt.Errorf("%w: unsupported customer operator", ErrIssueQuery)
	}
	base := ` FROM customer_request_filter_records r LEFT JOIN customer_filter_records c ON c.workspace_key=r.workspace_key AND c.customer_id=r.customer_id WHERE r.workspace_key=i.workspace_key AND r.issue_id=i.id`
	exists := func(condition string) string { return "EXISTS (SELECT 1" + base + " AND " + condition + ")" }
	clauses := []string{}
	args := []any{}
	for _, value := range node.Values {
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

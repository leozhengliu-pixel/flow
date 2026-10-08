package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"flow/api/internal/domain"
)

// ProjectCustomer is one customer requesting a project (directly or through one
// of its issues), with the fields Linear's Customers filters read.
type ProjectCustomer struct {
	ID        string  `json:"id"`
	Important bool    `json:"important,omitempty"`
	OwnerID   string  `json:"-"`
	Status    string  `json:"-"`
	Tier      string  `json:"-"`
	Revenue   float64 `json:"-"`
	Size      int64   `json:"-"`
}

// ProjectCustomerSummary backs the Customers / Customer revenue properties,
// the customer orderings and the Customers filters on project lists.
type ProjectCustomerSummary struct {
	Customers       []ProjectCustomer `json:"customers"`
	UnknownCustomer bool              `json:"unknownCustomer,omitempty"`
}

// ProjectCustomerSummaries reads every project's requesting customers from the
// customer request index: one range over the workspace's request rows with a
// primary-key probe per issue request, never the issue payloads.
func (s *SQLiteStore) ProjectCustomerSummaries(ctx context.Context, workspace string) (map[string]*ProjectCustomerSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT CASE WHEN r.issue_id='' THEN r.project_id ELSE COALESCE(i.project_id,'') END,r.customer_id,r.important,c.customer_id IS NOT NULL,COALESCE(c.owner_id,''),COALESCE(c.status_value,''),COALESCE(c.tier_value,''),COALESCE(c.revenue,0),COALESCE(c.customer_size,0) FROM customer_request_filter_records r LEFT JOIN issue_records i ON r.issue_id<>'' AND i.workspace_key=r.workspace_key AND i.id=r.issue_id LEFT JOIN customer_filter_records c ON c.workspace_key=r.workspace_key AND c.customer_id=r.customer_id WHERE r.workspace_key=?`, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	summaries := map[string]*ProjectCustomerSummary{}
	for rows.Next() {
		var projectID, customerID string
		var important int
		var known bool
		var customer ProjectCustomer
		if err := rows.Scan(&projectID, &customerID, &important, &known, &customer.OwnerID, &customer.Status, &customer.Tier, &customer.Revenue, &customer.Size); err != nil {
			return nil, err
		}
		if projectID == "" {
			continue
		}
		summary := summaries[projectID]
		if summary == nil {
			summary = &ProjectCustomerSummary{Customers: []ProjectCustomer{}}
			summaries[projectID] = summary
		}
		if !known {
			summary.UnknownCustomer = true
			continue
		}
		index := slices.IndexFunc(summary.Customers, func(item ProjectCustomer) bool { return item.ID == customerID })
		if index < 0 {
			customer.ID = customerID
			summary.Customers = append(summary.Customers, customer)
			index = len(summary.Customers) - 1
		}
		if important != 0 {
			summary.Customers[index].Important = true
		}
	}
	return summaries, rows.Err()
}

// CustomersByID loads the referenced customers' records (name, logo, revenue…).
func (s *SQLiteStore) CustomersByID(ctx context.Context, workspace string, ids []string) ([]domain.Customer, error) {
	customers := []domain.Customer{}
	for start := 0; start < len(ids); start += 200 {
		chunk := ids[start:min(start+200, len(ids))]
		args := []any{workspace}
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := s.db.QueryContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='customers' AND record_key IN (`+strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var raw []byte
			var customer domain.Customer
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return nil, err
			}
			if json.Unmarshal(raw, &customer) == nil {
				customers = append(customers, customer)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return customers, nil
}

// MatchProjectCustomerFilter applies one Customers filter (the issue filter
// vocabulary: customer:<id>, customer-owner:<id>, customer-count:3 with a
// gte/lte/eq/neq operator…) to a project's summary.
func MatchProjectCustomerFilter(summary *ProjectCustomerSummary, operator string, values []string) (bool, error) {
	if summary == nil {
		summary = &ProjectCustomerSummary{}
	}
	count := len(summary.Customers)
	if summary.UnknownCustomer {
		count++
	}
	important := 0
	for _, customer := range summary.Customers {
		if customer.Important {
			important++
		}
	}
	some := func(predicate func(ProjectCustomer) bool) bool {
		return slices.ContainsFunc(summary.Customers, predicate)
	}
	op := strings.ToLower(operator)
	if sqlOp, numeric := customerNumberSQLOperators[op]; numeric {
		for _, value := range values {
			field, raw, ok := strings.Cut(value, ":")
			amount, err := strconv.ParseFloat(raw, 64)
			if !ok || err != nil || math.IsNaN(amount) || math.IsInf(amount, 0) {
				return false, fmt.Errorf("%w: invalid customer number", ErrIssueQuery)
			}
			compare := func(actual float64) bool {
				switch sqlOp {
				case ">=":
					return actual >= amount
				case "<=":
					return actual <= amount
				case "=":
					return actual == amount
				}
				return actual != amount
			}
			switch field {
			case "customer-count":
				if compare(float64(count)) {
					return true, nil
				}
			case "customer-important-count":
				if compare(float64(important)) {
					return true, nil
				}
			case "customer-revenue":
				if some(func(customer ProjectCustomer) bool { return compare(customer.Revenue) }) {
					return true, nil
				}
			case "customer-size":
				if some(func(customer ProjectCustomer) bool { return compare(float64(customer.Size)) }) {
					return true, nil
				}
			default:
				return false, fmt.Errorf("%w: invalid customer number field", ErrIssueQuery)
			}
		}
		return false, nil
	}
	if op != "is" && op != "isnot" {
		return false, fmt.Errorf("%w: unsupported customer operator", ErrIssueQuery)
	}
	matched := false
	for _, value := range values {
		switch {
		case value == "customer:":
			matched = summary.UnknownCustomer
		case strings.HasPrefix(value, "customer:"):
			id := strings.TrimPrefix(value, "customer:")
			matched = some(func(customer ProjectCustomer) bool { return customer.ID == id })
		case value == "customer-owner:":
			matched = len(summary.Customers) > 0 && !some(func(customer ProjectCustomer) bool { return customer.OwnerID != "" })
		case strings.HasPrefix(value, "customer-owner:"):
			id := strings.TrimPrefix(value, "customer-owner:")
			matched = some(func(customer ProjectCustomer) bool { return customer.OwnerID == id })
		case strings.HasPrefix(value, "customer-status:"):
			id := strings.TrimPrefix(value, "customer-status:")
			matched = some(func(customer ProjectCustomer) bool { return customer.Status == id })
		case strings.HasPrefix(value, "customer-tier:"):
			id := strings.TrimPrefix(value, "customer-tier:")
			matched = some(func(customer ProjectCustomer) bool { return customer.Tier == id })
		default:
			return false, fmt.Errorf("%w: invalid customer condition", ErrIssueQuery)
		}
		if matched {
			break
		}
	}
	if op == "isnot" {
		matched = !matched
	}
	return matched, nil
}

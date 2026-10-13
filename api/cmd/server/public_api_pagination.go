package main

import (
	"cmp"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/publicapi"
)

// Relay-style cursor pagination shared by the public REST list endpoints:
// first/after, last/before, pageInfo{hasNextPage,hasPreviousPage,startCursor,
// endCursor}, orderBy=createdAt (default) | updatedAt, newest first.

const (
	defaultPageSize = 50
	maxPageSize     = 250
)

var errPageArgs = errors.New("invalid pagination arguments")

type pageInfo = publicapi.PageInfo

// connection is the list envelope. Nodes keep the resource's full JSON shape.
type connection[T any] = publicapi.Connection[T]

type pageArgs struct {
	Size     int
	Backward bool
	After    *pageCursor
	Before   *pageCursor
	OrderBy  string
	// IncludeArchived returns archived (and retired) resources too.
	IncludeArchived bool
}

type pageCursor struct {
	OrderBy string `json:"o"`
	At      string `json:"t"`
	ID      string `json:"i"`
}

// pageKey is the sort key of one node: the ordering timestamp then the id.
type pageKey struct {
	At time.Time
	ID string
}

func (k pageKey) cursor(orderBy string) string {
	raw, _ := json.Marshal(pageCursor{OrderBy: orderBy, At: k.At.UTC().Format(time.RFC3339Nano), ID: k.ID})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodePageCursor(value, orderBy string) (*pageCursor, error) {
	if value == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	var cursor pageCursor
	if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.ID == "" {
		return nil, fmt.Errorf("%w: cursor is malformed", errPageArgs)
	}
	if cursor.OrderBy != orderBy {
		return nil, fmt.Errorf("%w: cursor was issued for orderBy=%s", errPageArgs, cursor.OrderBy)
	}
	if _, err := time.Parse(time.RFC3339Nano, cursor.At); err != nil {
		return nil, fmt.Errorf("%w: cursor is malformed", errPageArgs)
	}
	return &cursor, nil
}

func (c pageCursor) key() pageKey {
	at, _ := time.Parse(time.RFC3339Nano, c.At)
	return pageKey{At: at, ID: c.ID}
}

// parsePageArgs reads first/after/last/before/orderBy/includeArchived. `limit`
// and `cursor` are accepted as aliases of first/after for Flow clients.
func parsePageArgs(r *http.Request, orderings ...string) (pageArgs, error) {
	query := r.URL.Query()
	args := pageArgs{Size: defaultPageSize, OrderBy: "createdAt"}
	if len(orderings) == 0 {
		orderings = []string{"createdAt", "updatedAt"}
	}
	if value := strings.TrimSpace(query.Get("orderBy")); value != "" {
		if !slices.Contains(orderings, value) {
			return args, fmt.Errorf("%w: orderBy must be one of %s", errPageArgs, strings.Join(orderings, ", "))
		}
		args.OrderBy = value
	}
	readSize := func(names ...string) (int, bool, error) {
		for _, name := range names {
			raw := strings.TrimSpace(query.Get(name))
			if raw == "" {
				continue
			}
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 || value > maxPageSize {
				return 0, true, fmt.Errorf("%w: %s must be between 1 and %d", errPageArgs, name, maxPageSize)
			}
			return value, true, nil
		}
		return 0, false, nil
	}
	first, hasFirst, err := readSize("first", "limit")
	if err != nil {
		return args, err
	}
	last, hasLast, err := readSize("last")
	if err != nil {
		return args, err
	}
	if hasFirst && hasLast {
		return args, fmt.Errorf("%w: use either first or last, not both", errPageArgs)
	}
	after := query.Get("after")
	if after == "" {
		after = query.Get("cursor")
	}
	before := query.Get("before")
	if after != "" && before != "" {
		return args, fmt.Errorf("%w: use either after or before, not both", errPageArgs)
	}
	if hasLast || before != "" && !hasFirst {
		args.Backward = true
		if hasLast {
			args.Size = last
		}
	} else if hasFirst {
		args.Size = first
	}
	if args.After, err = decodePageCursor(after, args.OrderBy); err != nil {
		return args, err
	}
	if args.Before, err = decodePageCursor(before, args.OrderBy); err != nil {
		return args, err
	}
	if raw := strings.TrimSpace(query.Get("includeArchived")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return args, fmt.Errorf("%w: includeArchived must be true or false", errPageArgs)
		}
		args.IncludeArchived = value
	}
	return args, nil
}

// comparePageKeys orders newest first, ties broken by descending id so that
// cursors stay stable when timestamps collide.
func comparePageKeys(left, right pageKey) int {
	if order := right.At.Compare(left.At); order != 0 {
		return order
	}
	return cmp.Compare(right.ID, left.ID)
}

// paginateConnection pages an in-memory (already authorized and filtered)
// collection. Nodes are ordered by comparePageKeys.
func paginateConnection[T any](items []T, key func(T) pageKey, args pageArgs) connection[T] {
	sorted := slices.Clone(items)
	slices.SortStableFunc(sorted, func(left, right T) int { return comparePageKeys(key(left), key(right)) })
	start, end := 0, len(sorted)
	if args.After != nil {
		cursor := args.After.key()
		start, _ = slices.BinarySearchFunc(sorted, cursor, func(item T, target pageKey) int {
			if comparePageKeys(key(item), target) <= 0 {
				return -1
			}
			return 1
		})
	}
	if args.Before != nil {
		cursor := args.Before.key()
		end, _ = slices.BinarySearchFunc(sorted, cursor, func(item T, target pageKey) int {
			if comparePageKeys(key(item), target) < 0 {
				return -1
			}
			return 1
		})
		end = max(end, start)
	}
	// Nodes occupy sorted[from:to]; page info reports whether anything exists
	// on either side of the returned slice.
	from, to := start, min(end, start+args.Size)
	if args.Backward {
		from, to = max(start, end-args.Size), end
	}
	result := connection[T]{Nodes: sorted[from:to]}
	result.PageInfo.HasPreviousPage = from > 0
	result.PageInfo.HasNextPage = to < len(sorted)
	if result.Nodes == nil {
		result.Nodes = []T{}
	}
	if len(result.Nodes) > 0 {
		startCursor := key(result.Nodes[0]).cursor(args.OrderBy)
		endCursor := key(result.Nodes[len(result.Nodes)-1]).cursor(args.OrderBy)
		result.PageInfo.StartCursor, result.PageInfo.EndCursor = &startCursor, &endCursor
	}
	return result
}

// orderedKey picks createdAt or updatedAt for a resource.
func orderedKey(orderBy, id string, created, updated time.Time) pageKey {
	if orderBy == "updatedAt" && !updated.IsZero() {
		return pageKey{At: updated, ID: id}
	}
	return pageKey{At: created, ID: id}
}

func timeValue(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

// connectionRequested reports whether a legacy list endpoint was called with
// connection arguments; those endpoints keep their original array/page shape
// otherwise. `connection=1` (or true) asks for the connection explicitly, so a
// client can page with the defaults.
func connectionRequested(r *http.Request) bool {
	query := r.URL.Query()
	for _, name := range []string{"first", "last", "after", "before", "orderBy"} {
		if query.Has(name) {
			return true
		}
	}
	switch strings.ToLower(strings.TrimSpace(query.Get("connection"))) {
	case "1", "true":
		return true
	}
	return false
}

// connectionOr dispatches a legacy list route to its connection variant when
// pagination arguments are present.
func connectionOr(legacy, paginated http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if connectionRequested(r) {
			paginated(w, r)
			return
		}
		legacy(w, r)
	}
}

// publicConnectionPath lists routes that always return connections (used to
// price the default page size in the complexity budget).
func publicConnectionPath(path string) bool {
	switch path {
	case "/api/teams", "/api/users", "/api/workflow-states", "/api/issue-labels", "/api/cycles", "/api/project-milestones", "/api/initiatives", "/api/comments", "/api/attachments", "/api/customers", "/api/customer-requests", "/api/customer-needs", "/api/issue-records", "/api/issues":
		return true
	}
	return false
}

func writePageArgsError(w http.ResponseWriter, err error) {
	writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, err.Error())
}

func encodeCursorBytes(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

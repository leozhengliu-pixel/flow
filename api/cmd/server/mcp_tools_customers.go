package main

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Customer tools: get_customer, save_customer and save_customer_request (a
// customer need linked to an issue or project), plus save_cycle. Writes run
// the routed REST handlers so the UI's checks apply.

// mcpFindCustomer finds a customer by ID, name, domain or external ID.
func mcpFindCustomer(data domain.Bootstrap, query string) (domain.Customer, error) {
	query = strings.TrimSpace(query)
	for _, pass := range []func(domain.Customer) bool{
		func(customer domain.Customer) bool { return equalFoldAny(query, customer.ID, customer.Name) },
		func(customer domain.Customer) bool {
			return equalFoldAny(strings.TrimPrefix(strings.ToLower(query), "www."), customer.Domains...) || equalFoldAny(query, customer.ExternalIDs...)
		},
	} {
		for _, customer := range data.Customers {
			if pass(customer) {
				return customer, nil
			}
		}
	}
	return domain.Customer{}, fmt.Errorf("customer %q not found", query)
}

func mcpCustomerName(data domain.Bootstrap, id string) string {
	for _, customer := range data.Customers {
		if customer.ID == id {
			return customer.Name
		}
	}
	return ""
}

func mcpCustomerURL(actor mcpActor, args map[string]any, customer domain.Customer) string {
	suffix := customer.ID
	if len(suffix) > 12 {
		suffix = suffix[len(suffix)-12:]
	}
	return mcpWorkspaceURL(actor.WorkspaceKey, args, "customer", strings.Trim(slug(customer.Name)+"-"+suffix, "-"))
}

// visibleMCPCustomerRequests lists the unarchived customer requests the actor
// may see, with their linked issues. The paged metadata projection cannot see
// issue records, so it drops every issue-linked request: read the
// workspace's requests and apply the same rule (no guests; the linked issue
// and project must be visible) here.
func (s *server) visibleMCPCustomerRequests(ctx context.Context, actor mcpActor, data domain.Bootstrap) ([]domain.CustomerRequest, map[string]domain.Issue, error) {
	all := data.CustomerRequests
	if raw, ok := s.store.WorkspaceMetadata(data.Workspace.URLKey); ok && data.ViewerRole != "guest" {
		all = raw.CustomerRequests
	}
	issueIDs := []string{}
	for _, request := range all {
		if request.IssueID != "" {
			issueIDs = append(issueIDs, request.IssueID)
		}
	}
	issues := map[string]domain.Issue{}
	if len(issueIDs) > 0 {
		q, err := s.mcpIssueQuery(ctx, actor)
		if err != nil {
			return nil, nil, err
		}
		q.Filter, q.Limit, q.Summary = store.IssueFilter{Field: "id", Values: issueIDs}, 500, true
		page, err := s.store.QueryIssueRecords(ctx, q)
		if err != nil {
			return nil, nil, err
		}
		for _, issue := range page.Items {
			issues[issue.ID] = issue
		}
	}
	visible := []domain.CustomerRequest{}
	for _, request := range all {
		if request.ArchivedAt != nil {
			continue
		}
		if _, ok := issues[request.IssueID]; request.IssueID != "" && !ok {
			continue
		}
		if request.ProjectID != "" {
			if _, err := mcpFindProject(data, request.ProjectID); err != nil {
				continue
			}
		}
		visible = append(visible, request)
	}
	return visible, issues, nil
}

func mcpCustomerRequestItem(data domain.Bootstrap, issues map[string]domain.Issue, request domain.CustomerRequest) map[string]any {
	item := map[string]any{"id": request.ID, "body": request.Body, "source": request.Source, "important": request.Priority >= 1, "priority": request.Priority, "creator": mcpUserRef(&request.Creator), "createdAt": request.CreatedAt}
	if request.SourceURL != "" {
		item["sourceUrl"] = request.SourceURL
	}
	if issue, ok := issues[request.IssueID]; ok {
		item["issue"] = map[string]any{"id": issue.ID, "identifier": issue.Identifier, "title": issue.Title, "status": issue.State.Name}
	}
	if project, err := mcpFindProject(data, request.ProjectID); request.ProjectID != "" && err == nil {
		item["project"] = map[string]any{"id": project.ID, "name": project.Name}
	}
	return item
}

func mcpCustomerTierName(data domain.Bootstrap, value string) string {
	for _, tier := range data.CustomerTiers {
		if tier.ID == value {
			return tier.Name
		}
	}
	return value
}

func mcpCustomerStatusName(data domain.Bootstrap, value string) string {
	for _, status := range data.CustomerStatuses {
		if status.ID == value || strings.EqualFold(status.Name, value) {
			return status.Name
		}
	}
	return value
}

func mcpCustomerItem(actor mcpActor, args map[string]any, data domain.Bootstrap, customer domain.Customer) map[string]any {
	item := map[string]any{"id": customer.ID, "name": customer.Name, "status": mcpCustomerStatusName(data, customer.Status), "tier": mcpCustomerTierName(data, customer.Tier), "domains": customer.Domains, "externalIds": customer.ExternalIDs, "annualRevenue": customer.AnnualRevenue, "size": customer.Size, "owner": mcpUserName(data, customer.OwnerID), "createdAt": customer.CreatedAt, "updatedAt": customer.UpdatedAt, "url": mcpCustomerURL(actor, args, customer)}
	if customer.LogoURL != "" {
		item["logoUrl"] = customer.LogoURL
	}
	return item
}

func (s *server) getMCPCustomer(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	customer, err := mcpFindCustomer(data, stringArg(args, "query"))
	if err != nil {
		return nil, err
	}
	item := mcpCustomerItem(actor, args, data, customer)
	visible, issues, err := s.visibleMCPCustomerRequests(ctx, actor, data)
	if err != nil {
		return nil, err
	}
	requests := []map[string]any{}
	for _, request := range visible {
		if request.CustomerID == customer.ID {
			requests = append(requests, mcpCustomerRequestItem(data, issues, request))
		}
	}
	item["requests"], item["requestCount"] = requests, len(requests)
	return item, nil
}

// mcpCustomerManualEditsAllowed applies the workspace's "manual customer
// edits" setting to the in-app agent, which acts in the UI's place (keys
// minted for integrations may still sync customers, as on the REST API).
func mcpCustomerManualEditsAllowed(actor mcpActor, data domain.Bootstrap) error {
	if actor.APIKey.ID == "" && !data.WorkspaceSettings.FeatureSettings.CustomerManualEdits {
		return fmt.Errorf("manual customer attribute edits are disabled")
	}
	return nil
}

func (s *server) saveMCPCustomer(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	if err := mcpCustomerManualEditsAllowed(actor, data); err != nil {
		return nil, err
	}
	var current domain.Customer
	id := stringArg(args, "id")
	if id != "" {
		found, err := mcpFindCustomer(data, id)
		if err != nil {
			return nil, err
		}
		current = found
	} else if stringArg(args, "name") == "" {
		return nil, fmt.Errorf("name is required when creating a customer")
	}
	// Models tend to send every optional field with an empty default ("", 0, []): only non-empty values change
	// the customer, and null clears a field.
	input := customerInput{}
	for key, target := range map[string]**string{"name": &input.Name, "logoUrl": &input.LogoURL, "status": &input.Status} {
		if value := stringArg(args, key); strings.TrimSpace(value) != "" {
			*target = &value
		}
	}
	if query, set, clear := mcpOptionalArg(args, "owner"); set || clear {
		owner := ""
		if set {
			user, err := mcpFindUser(data, query)
			if err != nil {
				return nil, err
			}
			owner = user.ID
		}
		input.OwnerID = &owner
	}
	if query, set, clear := mcpOptionalArg(args, "tier"); set || clear {
		tier := ""
		if set {
			index := slices.IndexFunc(data.CustomerTiers, func(item domain.CustomerTier) bool {
				return item.ArchivedAt == nil && equalFoldAny(query, item.ID, item.Name)
			})
			if index < 0 {
				names := []string{}
				for _, item := range data.CustomerTiers {
					if item.ArchivedAt == nil {
						names = append(names, item.Name)
					}
				}
				if len(names) == 0 {
					return nil, fmt.Errorf("this workspace has no customer tiers")
				}
				return nil, fmt.Errorf("unknown customer tier %q; tiers: %s", query, strings.Join(names, ", "))
			}
			tier = data.CustomerTiers[index].ID
		}
		input.Tier = &tier
	}
	if value, ok := args["annualRevenue"].(float64); ok && value > 0 || args["annualRevenue"] == nil && hasArg(args, "annualRevenue") {
		input.AnnualRevenue = &value
	}
	if value, ok := args["size"].(float64); ok && value > 0 || args["size"] == nil && hasArg(args, "size") {
		size := int(value)
		input.Size = &size
	}
	if values := stringsArg(args, "domains"); len(values) > 0 || args["domains"] == nil && hasArg(args, "domains") {
		domains := []string{}
		for _, value := range values {
			value = strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "https://"), "http://"), "www.")
			if value = strings.TrimRight(value, "/"); value != "" {
				domains = append(domains, value)
			}
		}
		input.Domains = &domains
	}
	if ids := stringsArg(args, "externalIds"); len(ids) > 0 || args["externalIds"] == nil && hasArg(args, "externalIds") {
		input.ExternalIDs = &ids
	}
	var result any
	var err error
	if id == "" {
		result, err = s.invokeMCPRoute(ctx, actor, http.MethodPost, "/api/customers", nil, input, s.createCustomer)
	} else {
		result, err = s.invokeMCPRoute(ctx, actor, http.MethodPatch, "/api/customers/"+pathID(current.ID), map[string]string{"id": current.ID}, input, s.updateCustomer)
	}
	if err != nil {
		return nil, err
	}
	var saved domain.Customer
	if err := jsonClone(result, &saved); err != nil {
		return nil, err
	}
	item := mcpCustomerItem(actor, args, data, saved)
	item["created"] = id == ""
	return item, nil
}

func (s *server) saveMCPCustomerRequest(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	visible, _, err := s.visibleMCPCustomerRequests(ctx, actor, data)
	if err != nil {
		return nil, err
	}
	id := stringArg(args, "id")
	var current domain.CustomerRequest
	if id != "" {
		index := slices.IndexFunc(visible, func(item domain.CustomerRequest) bool { return item.ID == id })
		if index < 0 {
			return nil, fmt.Errorf("customer request %q not found", id)
		}
		current = visible[index]
	}
	input := customerRequestInput{}
	if query := stringArg(args, "customer"); query != "" {
		customer, err := mcpFindCustomer(data, query)
		if err != nil {
			return nil, fmt.Errorf("%w; create it with save_customer first", err)
		}
		input.CustomerID = &customer.ID
	} else if id == "" {
		return nil, fmt.Errorf("customer is required when logging a customer request")
	}
	if value := stringArg(args, "body"); strings.TrimSpace(value) != "" {
		input.Body = &value
	}
	if value := stringArg(args, "sourceUrl"); strings.TrimSpace(value) != "" {
		input.SourceURL = &value
	}
	if hasBoolArg(args, "important") {
		priority := 0.0
		if boolArg(args, "important") {
			priority = 1
		}
		input.Priority = &priority
	}
	// An empty value leaves the link alone; null unlinks (updates only).
	if query, set, clear := mcpOptionalArg(args, "issue"); set || clear && id != "" {
		issueID := ""
		if set {
			if err := s.hydrateMCPIssueArguments(ctx, actor, &data, map[string]any{"issue": query}); err != nil {
				return nil, err
			}
			issue, err := mcpFindIssue(data, query)
			if err != nil {
				return nil, err
			}
			issueID = issue.ID
		}
		input.IssueID = &issueID
	}
	if query, set, clear := mcpOptionalArg(args, "project"); set || clear && id != "" {
		projectID := ""
		if set {
			project, err := mcpFindProject(data, query)
			if err != nil {
				return nil, err
			}
			projectID = project.ID
		}
		input.ProjectID = &projectID
	}
	if id == "" && trimmedValue(input.IssueID) == "" && trimmedValue(input.ProjectID) == "" {
		return nil, fmt.Errorf("issue or project is required: a customer request is logged on an issue or a project")
	}
	var result any
	if id == "" {
		result, err = s.invokeMCPRoute(ctx, actor, http.MethodPost, "/api/customer-requests", nil, input, s.createCustomerRequest)
	} else {
		result, err = s.invokeMCPRoute(ctx, actor, http.MethodPatch, "/api/customer-requests/"+pathID(current.ID), map[string]string{"id": current.ID}, input, s.updateCustomerRequest)
	}
	if err != nil {
		return nil, err
	}
	var saved domain.CustomerRequest
	if err := jsonClone(result, &saved); err != nil {
		return nil, err
	}
	if saved.IssueID != "" && !slices.ContainsFunc(data.Issues, func(issue domain.Issue) bool { return issue.ID == saved.IssueID }) {
		if err := s.hydrateMCPIssueArguments(ctx, actor, &data, map[string]any{"issue": saved.IssueID}); err != nil {
			return nil, err
		}
	}
	issues := map[string]domain.Issue{}
	for _, issue := range data.Issues {
		if issue.ID == saved.IssueID {
			issues[issue.ID] = issue
		}
	}
	item := mcpCustomerRequestItem(data, issues, saved)
	// "name" is the customer, so a step row reads "Logged customer request · Acme".
	item["name"] = mcpCustomerName(data, saved.CustomerID)
	item["customer"] = map[string]any{"id": saved.CustomerID, "name": item["name"]}
	item["created"] = id == ""
	return item, nil
}

// mcpFindCycle finds a cycle by ID, name or number (with team when several
// teams have cycles), or "current", "next" or "previous" of a team.
func mcpFindCycle(data domain.Bootstrap, query, teamQuery string) (domain.Cycle, error) {
	query = strings.TrimSpace(query)
	cycles := data.Cycles
	if teamQuery != "" {
		team, err := mcpFindTeam(data, teamQuery)
		if err != nil {
			return domain.Cycle{}, err
		}
		cycles = slices.DeleteFunc(slices.Clone(cycles), func(item domain.Cycle) bool { return item.TeamID != team.ID })
	}
	matches := []domain.Cycle{}
	number, numberErr := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(query), "#"))
	for _, cycle := range cycles {
		if strings.EqualFold(cycle.ID, query) || strings.EqualFold(cycle.Name, query) && cycle.Name != "" || numberErr == nil && cycle.Number == number {
			matches = append(matches, cycle)
		}
	}
	if len(matches) == 0 && teamQuery != "" {
		which := map[string]string{"当前": "current", "本周期": "current", "下一个": "next", "下一周期": "next", "上一个": "previous", "上一周期": "previous"}[strings.ToLower(strings.TrimSpace(query))]
		if which == "" {
			which = strings.ToLower(query)
		}
		if cycle := relativeMCPCycle(cycles, which); cycle != nil {
			matches = append(matches, *cycle)
		}
	}
	switch len(matches) {
	case 0:
		choices := make([]string, 0, len(cycles))
		for _, item := range cycles {
			label := item.Name
			if label == "" {
				label = strconv.Itoa(item.Number)
			}
			if label != "" {
				choices = append(choices, label)
			}
		}
		return domain.Cycle{}, fmt.Errorf("cycle %q not found; use a cycle ID, number, name, or current/next/previous with a team%s", query, mcpChoiceHint(choices))
	case 1:
		return matches[0], nil
	}
	return domain.Cycle{}, fmt.Errorf("cycle %q matches cycles of several teams; pass team", query)
}

func relativeMCPCycle(cycles []domain.Cycle, which string) *domain.Cycle {
	sorted := slices.Clone(cycles)
	slices.SortFunc(sorted, func(a, b domain.Cycle) int { return a.StartsAt.Compare(b.StartsAt) })
	current := slices.IndexFunc(sorted, func(item domain.Cycle) bool { return item.Status == "active" || item.Status == "current" })
	if current < 0 {
		return nil
	}
	switch which {
	case "next":
		current++
	case "previous":
		current--
	}
	if current < 0 || current >= len(sorted) {
		return nil
	}
	return &sorted[current]
}

// saveMCPCycle edits a cycle's name, description and links. Cycles cannot be
// created here: teams generate them from their cycle settings.
func (s *server) saveMCPCycle(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	cycle, err := mcpFindCycle(data, stringArg(args, "id"), stringArg(args, "team"))
	if err != nil {
		return nil, err
	}
	input := domain.CycleMutationInput{}
	if value := stringArg(args, "name"); strings.TrimSpace(value) != "" {
		input.Name = &value
	}
	if value, set, clear := mcpOptionalArg(args, "description"); set || clear {
		input.Description = &value
	}
	links, _ := args["links"].([]any)
	if input.Name == nil && input.Description == nil && len(links) == 0 {
		return nil, fmt.Errorf("provide name, description, or links")
	}
	if input.Name != nil || input.Description != nil {
		result, err := s.invokeMCPRoute(ctx, actor, http.MethodPatch, "/api/cycles/"+pathID(cycle.ID), map[string]string{"id": cycle.ID}, input, s.updateCycle)
		if err != nil {
			return nil, err
		}
		if err := jsonClone(result, &cycle); err != nil {
			return nil, err
		}
	}
	added := []map[string]any{}
	for _, raw := range links {
		var link struct {
			Title string `json:"title"`
			URL   string `json:"url"`
		}
		if err := jsonClone(raw, &link); err != nil {
			return nil, err
		}
		result, err := s.invokeMCPRoute(ctx, actor, http.MethodPost, "/api/cycles/"+pathID(cycle.ID)+"/resources", map[string]string{"id": cycle.ID}, map[string]string{"type": "link", "title": link.Title, "url": link.URL}, s.createCycleResource)
		if err != nil {
			return nil, err
		}
		var resource domain.CycleResource
		if err := jsonClone(result, &resource); err != nil {
			return nil, err
		}
		added = append(added, map[string]any{"id": resource.ID, "title": resource.Title, "url": resource.URL})
	}
	name := cycle.Name
	if name == "" {
		name = fmt.Sprintf("Cycle %d", cycle.Number)
	}
	receipt := map[string]any{"id": cycle.ID, "number": cycle.Number, "name": name, "description": cycle.Description, "startsAt": cycle.StartsAt, "endsAt": cycle.EndsAt, "linksAdded": added}
	for _, team := range data.Teams {
		if team.ID == cycle.TeamID {
			receipt["team"] = map[string]any{"id": team.ID, "key": team.Key, "name": team.Name}
		}
	}
	return receipt, nil
}

func hasArg(args map[string]any, key string) bool { _, ok := args[key]; return ok }

// mcpOptionalArg reads an optional string argument the way models send them: a non-empty value sets it, null
// clears it, and "" (an unfilled default) leaves it unchanged.
func mcpOptionalArg(args map[string]any, key string) (value string, set, clear bool) {
	raw, present := args[key]
	if !present {
		return "", false, false
	}
	if raw == nil {
		return "", false, true
	}
	value = strings.TrimSpace(stringArg(args, key))
	return value, value != "", false
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// maxConnectorToolNames bounds the tool inventory kept on a connector.
const maxConnectorToolNames = 500

type connectorConnectResult struct {
	Connected bool `json:"connected"`
	// AuthorizationRequired is true when the server refused the connection for
	// missing or rejected credentials; the client then starts authorization.
	AuthorizationRequired bool               `json:"authorizationRequired,omitempty"`
	Error                 string             `json:"error,omitempty"`
	Policy                *applicationPolicy `json:"policy,omitempty"`
}

// connectConnector connects to an MCP connector as the viewer, with the
// viewer's credentials, and records the tools it offers. A connector still
// named after its host takes the name the server reports. Connection failures
// are a 200 with connected=false so the client can tell a refused
// authorization from an unreachable server.
func (s *server) connectConnector(w http.ResponseWriter, r *http.Request) {
	data, item, err := s.connectorForRequest(r)
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, connectorContextKey{}, connectorRequestContext{Workspace: workspaceKey(r), UserID: data.Viewer.ID})
	client, err := s.openConnector(ctx, item)
	if err != nil {
		writeJSON(w, http.StatusOK, connectorConnectResult{AuthorizationRequired: errors.Is(err, errConnectorUnauthorized), Error: err.Error()})
		return
	}
	defer client.close()
	tools, err := listConnectorToolNames(ctx, client)
	if err != nil {
		writeJSON(w, http.StatusOK, connectorConnectResult{Error: err.Error()})
		return
	}
	serverName := ""
	if result := client.session.InitializeResult(); result != nil && result.ServerInfo != nil {
		serverName = strings.TrimSpace(firstNonEmpty(result.ServerInfo.Title, result.ServerInfo.Name))
	}
	var updated applicationPolicy
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "application_policy.updated", item.ID, nil, func(workspace *domain.Bootstrap) error {
		items := applicationPolicies(workspace)
		index := slices.IndexFunc(items, func(candidate applicationPolicy) bool { return candidate.ID == item.ID })
		// The connector was removed or pointed elsewhere while we connected.
		if index < 0 || items[index].URL != item.URL {
			return errNotFound
		}
		now := time.Now().UTC()
		items[index].Tools = tools
		items[index].ConnectedAt = &now
		if serverName != "" && len(serverName) <= 200 && items[index].Name == connectorHostName(item.URL) {
			items[index].Name = serverName
		}
		updated = items[index]
		setApplicationPolicies(workspace, items)
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, connectorConnectResult{Connected: true, Policy: &updated})
}

func connectorHostName(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

// listConnectorToolNames pages through tools/list and returns the tool names.
func listConnectorToolNames(ctx context.Context, client *remoteMCPClient) ([]string, error) {
	names := []string{}
	cursor := ""
	for page := 0; page < 10; page++ {
		params := map[string]string{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := client.rpc(ctx, "tools/list", params, false)
		if err != nil {
			return nil, err
		}
		var result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if json.Unmarshal(raw, &result) != nil {
			return nil, errors.New("invalid MCP tool inventory")
		}
		for _, tool := range result.Tools {
			if tool.Name != "" && len(tool.Name) <= 256 && len(names) < maxConnectorToolNames {
				names = append(names, tool.Name)
			}
		}
		if result.NextCursor == "" || result.NextCursor == cursor {
			break
		}
		cursor = result.NextCursor
	}
	return names, nil
}

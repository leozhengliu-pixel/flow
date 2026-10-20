import { jsonRequest, request } from "@/lib/api-client";

/** An MCP connector or OAuth application policy, as /api/application-policies returns it. */
export type ConnectorPolicy = {
  id: string;
  name: string;
  kind: "mcp" | "oauth";
  url?: string;
  ownerId?: string;
  shared: boolean;
  status: string;
  scopes?: string[];
  codingSessions?: boolean;
  toolApproval?: "always";
  /** Overrides `name` where people see the connector; empty shows the default name. */
  displayName?: string;
  /** Tool names read the last time the connector was connected (server-set). */
  tools?: string[];
  connectedAt?: string;
  updatedAt?: string;
};

export type ConnectorConnectResult = {
  connected: boolean;
  authorizationRequired?: boolean;
  error?: string;
  policy?: ConnectorPolicy;
};

export const connectorLabel = (item: Pick<ConnectorPolicy, "name" | "displayName">) => item.displayName || item.name;

export const listConnectorPolicies = () => request<ConnectorPolicy[]>("/api/application-policies");

export const saveConnectorPolicy = (item: ConnectorPolicy) =>
  request<ConnectorPolicy>("/api/application-policies", jsonRequest("PUT", item));

export const removeConnectorPolicy = (id: string) =>
  request(`/api/application-policies/${encodeURIComponent(id)}`, { method: "DELETE" });

/** Connects to the server with the viewer's credentials and records its tools. */
export const connectConnector = (id: string) =>
  request<ConnectorConnectResult>(`/api/application-policies/${encodeURIComponent(id)}/connect`, jsonRequest("POST", {}));

/** Starts the connector's OAuth authorization in this tab. */
export async function authorizeConnector(id: string) {
  const result = await request<{ authorizationURL: string }>(
    `/api/application-policies/${encodeURIComponent(id)}/oauth/start`,
    jsonRequest("POST", {}),
  );
  window.location.assign(result.authorizationURL);
}

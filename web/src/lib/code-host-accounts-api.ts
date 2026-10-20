import { apiFetch, jsonRequest, request } from "@/lib/api-client";
import type {
  CodeHostAccount,
  CodeHostAccountsResponse,
  GitLabOAuthApplication,
} from "@/types/code-host-accounts";

export type CodeHostReturnTo = "account" | "integration";

/** Raised when the code host rejects a token; keeps the scopes it lacked. */
export class MissingScopesError extends Error {
  missingScopes: string[];
  constructor(message: string, missingScopes: string[]) {
    super(message);
    this.name = "MissingScopesError";
    this.missingScopes = missingScopes;
  }
}

export function fetchCodeHostAccounts(): Promise<CodeHostAccountsResponse> {
  return request("/api/account/code-host-accounts");
}

export function authorizeGitHubAccount(returnTo: CodeHostReturnTo): Promise<{ url: string }> {
  return request("/api/account/code-host-accounts/github/authorize", jsonRequest("POST", { returnTo }));
}

export function authorizeGitLabAccount(instanceId: string, returnTo: CodeHostReturnTo): Promise<{ url: string }> {
  return request("/api/account/code-host-accounts/gitlab/authorize", jsonRequest("POST", { instanceId, returnTo }));
}

export async function connectGitLabToken(instanceId: string, token: string): Promise<CodeHostAccount> {
  const response = await apiFetch(
    "/api/account/code-host-accounts/gitlab/token",
    jsonRequest("POST", { instanceId, token }),
  );
  return readTokenResponse(response);
}

async function readTokenResponse(response: Response): Promise<CodeHostAccount> {
  if (response.ok) return response.json();
  const payload = await response.json().catch(() => null);
  const message = payload?.error || `Request failed: ${response.status}`;
  if (Array.isArray(payload?.missingScopes) && payload.missingScopes.length) {
    throw new MissingScopesError(message, payload.missingScopes);
  }
  throw new Error(message);
}

export function disconnectCodeHostAccount(id: string): Promise<void> {
  return request(`/api/account/code-host-accounts/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export function fetchGitLabOAuthApplication(connectionId: string): Promise<GitLabOAuthApplication> {
  return request(`/api/integrations/gitlab/${encodeURIComponent(connectionId)}/oauth-application`);
}

export function saveGitLabOAuthApplication(
  connectionId: string,
  input: { applicationId: string; secret: string },
): Promise<GitLabOAuthApplication> {
  return request(
    `/api/integrations/gitlab/${encodeURIComponent(connectionId)}/oauth-application`,
    jsonRequest("PUT", input),
  );
}

export function removeGitLabOAuthApplication(connectionId: string): Promise<void> {
  return request(`/api/integrations/gitlab/${encodeURIComponent(connectionId)}/oauth-application`, {
    method: "DELETE",
  });
}

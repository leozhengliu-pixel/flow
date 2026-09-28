/**
 * LS-0280 / LS-0111 — GitHub code-access gate helpers (REST / bootstrap only).
 */
import type { BootstrapData, IntegrationConnection } from "@/types/flow";

export type CodeReviewAccessReason =
  | "workspace_connection_missing"
  | "workspace_code_access_missing"
  | "personal_connection_missing"
  | "personal_connection_reconnect_required"
  | "granted";

export function githubConnection(data: Pick<BootstrapData, "integrationConnections">): IntegrationConnection | undefined {
  return data.integrationConnections.find(
    (item) => item.provider === "github" && (item.status === "connected" || item.status === "configured"),
  );
}

export function gitlabConnection(data: Pick<BootstrapData, "integrationConnections">): IntegrationConnection | undefined {
  return data.integrationConnections.find(
    (item) => item.provider === "gitlab" && (item.status === "connected" || item.status === "configured"),
  );
}

/** True when a workspace GitHub integration has been granted code access. */
export function integrationHasCodeAccess(connection?: IntegrationConnection | null): boolean {
  if (!connection) return false;
  if (connection.status !== "connected" && connection.status !== "configured") return false;
  if (connection.config?.codeAccess === "true" || connection.config?.codeAccess === "1") return true;
  return connection.scopes.some((scope) => /^(repo|code|contents|read:code)/i.test(scope));
}

export function organizationHasCodeAccess(data: Pick<BootstrapData, "integrationConnections">): boolean {
  return integrationHasCodeAccess(githubConnection(data));
}

export function hasCodeHostConnection(data: Pick<BootstrapData, "integrationConnections">): boolean {
  return Boolean(githubConnection(data) || gitlabConnection(data));
}

export function resolveCodeReviewAccess(data: Pick<BootstrapData, "integrationConnections">): {
  reason: CodeReviewAccessReason;
  connection?: IntegrationConnection;
} {
  const github = githubConnection(data);
  if (!github) {
    if (gitlabConnection(data)) return { reason: "granted", connection: gitlabConnection(data) };
    return { reason: "workspace_connection_missing" };
  }
  if (github.status === "error" || github.status === "disconnected") {
    return { reason: "personal_connection_reconnect_required", connection: github };
  }
  if (!integrationHasCodeAccess(github)) {
    return { reason: "workspace_code_access_missing", connection: github };
  }
  return { reason: "granted", connection: github };
}

export const DEFAULT_REPOSITORY_ACCESS = {
  extendAccessToAllMembers: false,
  allowAutomationAccess: false,
  scope: "all" as "all" | "selected",
  allowedRepositories: [] as string[],
};

export type RepositoryAccessSettings = typeof DEFAULT_REPOSITORY_ACCESS;


/** Reviews chrome may render with partial GitHub ACL; diffs use stored patches. */
export function canViewReviewsWithoutFullAcl(
  data: Pick<BootstrapData, "integrationConnections">,
): boolean {
  return hasCodeHostConnection(data);
}

/** Repository is eligible for local patch DiffView even when org code access is incomplete. */
export function canRenderStoredDiff(
  data: Pick<BootstrapData, "integrationConnections">,
  _repository?: { owner?: string; name?: string },
): boolean {
  // Do not block the whole PR on full GitHub ACL — stored ReviewFile.patch is enough.
  return canViewReviewsWithoutFullAcl(data) || resolveCodeReviewAccess(data).reason === "granted";
}

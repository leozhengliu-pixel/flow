import type { IntegrationConnection } from "./flow";

export type ChatIntegrationProvider = "feishu" | "lark";

export type ChatIntegrationEvent =
  | "issue.created"
  | "issue.status_changed"
  | "issue.assignee_changed"
  | "comment.created"
  | "project.update_created"
  | "initiative.update_created";

/** Who authorized sharing a team with a group, recorded by the server. */
export interface ChatBindingAuthorization {
  userId: string;
  role: "workspace_admin" | "team_owner";
  authorizedAt: string;
  privateTeam: boolean;
}

export interface ChatTenantAuthorization {
  userId?: string;
  openId?: string;
  name?: string;
  tenantKey: string;
  authorizedAt: string;
  /** tenant_manager: verified platform tenant administrator; legacy: connected before tenant authorization was separate. */
  method: "tenant_manager" | "legacy";
}

/**
 * The three permission domains of a chat integration: the enterprise tenant
 * authorization on the chat platform, the workspace integration (workspace
 * administrators) and team bindings (team owners).
 */
export interface ChatPermissions {
  tenant: {
    status: "not_configured" | "pending" | "authorized";
    canAuthorize: boolean;
    designatedAuthorizerId?: string;
    tenantKey?: string;
    authorization?: ChatTenantAuthorization;
  };
  workspace: { canManage: boolean };
  teams: { manageableTeamIds: string[]; privateTeamIds: string[] };
}

export interface ChatIdentity {
  userId: string;
  openId: string;
  tenantKey: string;
  name?: string;
}

export interface ChatBinding {
  id: string;
  connectionId: string;
  chatId: string;
  chatName: string;
  teamId: string;
  projectId?: string;
  templateId?: string;
  events: ChatIntegrationEvent[];
  enabled: boolean;
  allowCreate: boolean;
  allowUpdate: boolean;
  createdAt?: string;
  authorization?: ChatBindingAuthorization;
  revokedAt?: string;
  revokedBy?: string;
}

export interface ChatIntegrationSettings {
  permissions?: ChatPermissions;
  connection: IntegrationConnection | null;
  /** Stable code for connection.lastError; the web app translates it. */
  lastErrorCode?: string;
  identity: ChatIdentity | null;
  /** The linked account is older than the workspace session duration and must reconnect. */
  identityExpired?: boolean;
  bindings: ChatBinding[];
  /** Teams a group binding may share, by the server's binding rule (administrators only). */
  bindableTeamIds?: string[];
  credentialsConfigured: boolean;
  webhookURL: string;
  canManage: boolean;
}

export interface ChatIntegrationCredentials {
  name?: string;
  appId: string;
  appSecret?: string;
  encryptKey?: string;
  verificationToken?: string;
}

export type ChatBindingInput = Omit<ChatBinding, "id" | "connectionId" | "authorization" | "revokedAt" | "revokedBy" | "createdAt"> & {
  id?: string;
  /** Explicit consent to share the team's selected updates and actions with everyone in the group. */
  authorize: true;
};

export interface BotChat {
  id: string;
  name: string;
  external: boolean;
}

export interface BotChatsPage {
  items: BotChat[];
  hasMore: boolean;
  pageToken?: string;
}

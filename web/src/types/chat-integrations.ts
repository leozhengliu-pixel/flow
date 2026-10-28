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

/**
 * The tenant was authorized automatically: the application's credentials
 * obtained a tenant token and read the bot's info, so the chat platform's
 * administrator approved and published the app.
 */
export interface ChatTenantAuthorization {
  /** Empty until the platform names the tenant (the bot's internal groups or a member's linked account). */
  tenantKey?: string;
  authorizedAt: string;
  /** The workspace administrator who configured or last tested the connection. */
  configuredBy?: string;
}

/**
 * The three permission domains of a chat integration: the enterprise tenant
 * (detected automatically from the chat platform's approval), the workspace
 * integration (workspace administrators) and team bindings (team owners).
 */
export interface ChatPermissions {
  tenant: {
    /** pending: configured but not checked yet; not_approved: the last check was refused by the platform. */
    status: "not_configured" | "pending" | "not_approved" | "authorized";
    tenantKey?: string;
    authorization?: ChatTenantAuthorization;
    /** When the automatic check last ran. */
    checkedAt?: string;
    /** Stable code of the last failed check, such as chat_tenant_not_approved. */
    errorCode?: string;
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

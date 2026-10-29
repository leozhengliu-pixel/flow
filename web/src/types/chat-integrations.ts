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
  /** Empty until the platform names the tenant (its tenant information, or while that is not readable the bot's internal groups or a member's linked account). */
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
    /**
     * pending: configured but not checked yet; not_approved: the last check was refused by the platform;
     * mismatch: the platform's tenant information names another tenant than the recorded one.
     */
    status: "not_configured" | "pending" | "not_approved" | "mismatch" | "authorized";
    tenantKey?: string;
    /** The tenant's name from its tenant information. */
    tenantName?: string;
    /** platform: read from the tenant information; observed: first seen in an internal group or linked account. */
    tenantKeySource?: "platform" | "observed";
    /** Why the tenant information was not readable, such as chat_tenant_info_permission_missing. */
    tenantInfoErrorCode?: string;
    /** On mismatch, the tenant key the platform reported. */
    reportedTenantKey?: string;
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
  /** "unavailable": the platform refused a personal notification because the app is not available to this account (its availability range). */
  personalDelivery?: "unavailable";
}

/**
 * A binding's request intake: an explicit request, the reaction or the
 * message shortcut turns a message into a request in the team's triage.
 */
export interface ChatBindingRequests {
  enabled: boolean;
  /** Off by default, including bindings saved before this option existed. */
  everyMention?: boolean;
  /** The reaction (the platform's emoji type, such as "OK") that makes a request; absent leaves reactions alone. */
  emoji?: string;
  templateId?: string;
  /** 0 (no priority) to 4 (low); absent uses the template's or the team's default. */
  priority?: number;
}

export interface ChatBinding {
  id: string;
  connectionId: string;
  chatId: string;
  chatName: string;
  teamId: string;
  /** The binding's project when it covers exactly one (the single-project form). */
  projectId?: string;
  /** The projects the binding covers; empty or absent covers all of the team's projects. */
  projectIds?: string[];
  templateId?: string;
  events: ChatIntegrationEvent[];
  enabled: boolean;
  allowCreate: boolean;
  allowUpdate: boolean;
  /** Request intake: members turn the group's messages into requests in the team's triage. */
  requests?: ChatBindingRequests;
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

/** Who connected an initiative to a group, recorded by the server. */
export interface ChatInitiativeBindingAuthorization {
  userId: string;
  role: "initiative_owner" | "workspace_admin";
  authorizedAt: string;
}

/** An initiative connected directly to a group: the group receives the initiative's updates. */
export interface ChatInitiativeBinding {
  id: string;
  connectionId: string;
  chatId: string;
  chatName: string;
  initiativeId: string;
  enabled: boolean;
  createdAt?: string;
  authorization?: ChatInitiativeBindingAuthorization;
  revokedAt?: string;
  revokedBy?: string;
}

export interface ChatInitiativeBindings {
  bindings: ChatInitiativeBinding[];
  /** The viewer is the initiative's owner or a workspace administrator. */
  canManage: boolean;
  /** The initiative's updates may be shared: it has no lead team, or a public team leads it. */
  shareable: boolean;
  tenantAuthorized: boolean;
}

export interface ChatInitiativeBindingInput {
  id?: string;
  initiativeId: string;
  chatId: string;
  chatName: string;
  enabled: boolean;
  /** Explicit consent to share the initiative's updates with everyone in the group. */
  authorize: true;
}

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

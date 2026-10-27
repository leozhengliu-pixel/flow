import type { IntegrationConnection } from "./flow";

export type ChatIntegrationProvider = "feishu" | "lark";

export type ChatIntegrationEvent =
  | "issue.created"
  | "issue.status_changed"
  | "issue.assignee_changed"
  | "comment.created"
  | "project.update_created";

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
}

export interface ChatIntegrationSettings {
  connection: IntegrationConnection | null;
  identity: ChatIdentity | null;
  bindings: ChatBinding[];
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

export type ChatBindingInput = Omit<ChatBinding, "id" | "connectionId"> & { id?: string };

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

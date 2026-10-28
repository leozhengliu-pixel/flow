import { ApiError, jsonRequest, request } from "@/lib/api-client";
import type {
  BotChatsPage,
  ChatBindingInput,
  ChatIntegrationCredentials,
  ChatIntegrationProvider,
  ChatIntegrationSettings,
} from "@/types/chat-integrations";

const chatPath = (provider: ChatIntegrationProvider) => `/api/integrations/${provider}/chat`;

export function fetchChatIntegration(provider: ChatIntegrationProvider): Promise<ChatIntegrationSettings> {
  return request(chatPath(provider));
}

export function saveChatIntegration(provider: ChatIntegrationProvider, input: ChatIntegrationCredentials): Promise<unknown> {
  return request(chatPath(provider), jsonRequest("PUT", input));
}

export function disconnectChatIntegration(provider: ChatIntegrationProvider): Promise<void> {
  return request(chatPath(provider), { method: "DELETE" });
}

export function testChatIntegration(provider: ChatIntegrationProvider): Promise<unknown> {
  return request(`${chatPath(provider)}/test`, { method: "POST" });
}

export function fetchChatIntegrationChats(provider: ChatIntegrationProvider, cursor?: string): Promise<BotChatsPage> {
  const query = cursor ? `?${new URLSearchParams({ cursor })}` : "";
  return request(`${chatPath(provider)}/chats${query}`);
}

export function saveChatBinding(provider: ChatIntegrationProvider, input: ChatBindingInput): Promise<unknown> {
  return request(`${chatPath(provider)}/bindings`, jsonRequest("POST", input));
}

export function removeChatBinding(provider: ChatIntegrationProvider, id: string): Promise<void> {
  return request(`${chatPath(provider)}/bindings/${encodeURIComponent(id)}`, { method: "DELETE" });
}

/** Revokes a binding: it stops delivering and accepting group actions, and stays listed as a record. */
export function revokeChatBinding(provider: ChatIntegrationProvider, id: string): Promise<unknown> {
  return request(`${chatPath(provider)}/bindings/${encodeURIComponent(id)}/revoke`, { method: "POST" });
}

/** Starts the enterprise tenant authorization; a platform tenant administrator signs in. */
export function startChatTenantAuthorization(provider: ChatIntegrationProvider): Promise<{ authorizeUrl: string }> {
  return request(`${chatPath(provider)}/tenant/start`, { method: "POST" });
}

/** Designates the member who completes the tenant authorization (empty clears it). */
export function designateChatTenantAuthorizer(provider: ChatIntegrationProvider, userId: string): Promise<ChatIntegrationSettings> {
  return request(`${chatPath(provider)}/tenant/authorizer`, jsonRequest("PUT", { userId }));
}

export function startChatIdentityBinding(provider: ChatIntegrationProvider): Promise<{ authorizeUrl: string }> {
  return request(`${chatPath(provider)}/identity/start`, { method: "POST" });
}

export function removeChatIdentity(provider: ChatIntegrationProvider): Promise<void> {
  return request(`${chatPath(provider)}/identity`, { method: "DELETE" });
}

export function chatIdentityAuthorizationURL(provider: ChatIntegrationProvider, value: string): string {
  const url = new URL(value);
  const host = provider === "feishu" ? "accounts.feishu.cn" : "accounts.larksuite.com";
  if (url.protocol !== "https:" || url.hostname !== host || url.port || url.username || url.password) {
    throw new Error("Invalid authorization address");
  }
  return url.href;
}

/**
 * Chat endpoints, the identity OAuth redirect (chat_auth_error) and the
 * connection's last error carry a stable code. Each maps to an English source
 * string that t() translates.
 */
export const CHAT_ERROR_MESSAGES: Record<string, string> = {
  chat_busy: "Chat settings are being changed elsewhere. Try again shortly.",
  chat_unsupported_provider: "This chat application is not supported.",
  chat_workspace_access_required: "You don't have access to this workspace.",
  chat_interactive_sign_in_required: "Sign in to Flow in this browser to continue.",
  chat_policy_unavailable: "The workspace authentication policy is unavailable. Try again shortly.",
  chat_session_unverified: "Your sign-in could not be verified. Try again shortly.",
  chat_session_policy: "Your sign-in does not meet the workspace authentication policy. Sign in again with an allowed method.",
  chat_admin_required: "Only workspace administrators can change this integration.",
  chat_workspace_permission_required: "Only workspace administrators can change this integration.",
  chat_team_binding_permission_required: "Only the team's owners and workspace administrators can manage this team's group bindings.",
  chat_binding_authorization_required: "Approve sharing this team with the group to save the binding.",
  chat_binding_revoked: "This binding was revoked. Add the group again to create a new binding.",
  chat_tenant_authorization_required: "The organization hasn't authorized the application yet. A tenant administrator of the chat platform needs to complete tenant authorization.",
  chat_tenant_authorizer_required: "Only workspace administrators or the designated tenant authorizer can start tenant authorization.",
  chat_tenant_manager_required: "This chat account isn't a tenant administrator of the application's organization. Ask a tenant administrator to authorize.",
  chat_tenant_verification_failed: "The application's organization could not be verified. Grant the application permission to read tenant and contact information.",
  chat_invalid_authorizer: "Choose an active member of this workspace.",
  chat_group_external: "Only internal groups of your organization can be bound.",
  chat_group_other_tenant: "This group belongs to another organization.",
  chat_bot_not_in_group: "Add the bot to this group, then try again.",
  chat_group_unverified: "The group could not be checked on the chat platform. Check the bot's permissions and try again.",
  chat_read_failed: "Could not load integration",
  chat_bindings_read_failed: "Could not load group bindings.",
  chat_secret_key_missing: "The server needs FLOW_CONNECTOR_SECRET_KEY before chat applications can connect.",
  chat_invalid_configuration: "Check the application settings and try again.",
  chat_credentials_unreadable: "Saved application credentials can't be read. Restore the server encryption key or reconnect the application.",
  chat_app_id_locked: "To use another application, disconnect this integration first.",
  chat_save_failed: "Could not save changes. Try again.",
  chat_credentials_required: "App Secret, Encrypt Key and Verification Token are required.",
  chat_check_failed: "The application check failed. Verify the app credentials and that the bot is published.",
  chat_bot_unpublished: "Enable and publish the application's bot, then try again.",
  chat_app_in_use: "This application is already connected to another workspace.",
  chat_not_configured: "The application isn't configured yet.",
  chat_tenant_unverified: "The organization hasn't authorized the application yet. A tenant administrator of the chat platform needs to complete tenant authorization.",
  chat_invalid_cursor: "The group list could not be continued. Refresh and try again.",
  chat_platform_failed: "The chat platform request failed. Check the bot's permissions and try again.",
  chat_invalid_binding: "Check the group binding and try again.",
  chat_binding_not_found: "This group binding no longer exists. Refresh and try again.",
  chat_binding_scope: "Choose an active team, and a project and template that can be shared with the group.",
  chat_binding_conflict: "This group is already bound.",
  chat_authorization_failed: "Could not start account linking. Try again.",
  chat_authorization_expired: "The authorization expired or was already used. Start linking again.",
  chat_authorization_other_session: "Finish linking in the browser where you're signed in to Flow.",
  chat_mfa_required: "This workspace requires two-factor authentication. Verify a second factor, then link again.",
  chat_authorization_declined: "Authorization was declined.",
  chat_platform_authorization_failed: "The platform could not verify your account. Check the application's OAuth settings.",
  chat_other_tenant: "This account belongs to another organization.",
  chat_admin_authorization_required: "The organization hasn't authorized the application yet. A tenant administrator of the chat platform needs to complete tenant authorization.",
  chat_identity_in_use: "This chat account is already linked to another Flow account.",
  chat_setup_incomplete: "Application setup is incomplete. Disconnect and configure it again.",
  chat_delivery_failed: "Messages could not be delivered. Check the bot's permissions, group access and application credentials.",
  chat_identity_expired: "Your linked chat account has expired. Reconnect it in Settings > Connected accounts.",
  chat_error: "The connection reported an error. Test the connection for details.",
};

const CHAT_CLIENT_ERRORS = new Set(["Invalid authorization address"]);

/** Translated message for a stable chat error code, if the code is known. */
export function chatErrorCodeMessage(code: string | null | undefined, t: (source: string) => string): string | undefined {
  const source = code ? CHAT_ERROR_MESSAGES[code] : undefined;
  return source ? t(source) : undefined;
}

/** Translated reason for a failed chat request; unknown errors use the translated fallback. */
export function chatErrorMessage(error: unknown, t: (source: string) => string, fallback: string): string {
  const known = chatErrorCodeMessage(error instanceof ApiError ? error.code : undefined, t);
  if (known) return known;
  if (error instanceof Error && CHAT_CLIENT_ERRORS.has(error.message)) return t(error.message);
  return t(fallback);
}

import { jsonRequest, request } from "@/lib/api-client";
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

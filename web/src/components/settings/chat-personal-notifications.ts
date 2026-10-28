import type { ChatIntegrationSettings } from "@/types/chat-integrations";
import type { NotificationChannelPreferences, NotificationPreferences } from "@/types/flow";

/**
 * Personal Lark/Feishu notifications: direct messages from the app's bot to
 * the member's linked account. The state says whether they can be turned on
 * and, if not, what is missing.
 */
export type ChatPersonalState = "loading" | "not_configured" | "not_approved" | "not_linked" | "unavailable" | "ready";

export function chatPersonalState(settings: ChatIntegrationSettings | null, loading: boolean): ChatPersonalState {
  if (!settings) return loading ? "loading" : "not_configured";
  if (!settings.connection || !settings.credentialsConfigured) return "not_configured";
  const authorized = settings.permissions ? settings.permissions.tenant.status === "authorized" : settings.connection.status === "connected";
  if (!authorized) return "not_approved";
  if (!settings.identity) return "not_linked";
  if (settings.identity.personalDelivery === "unavailable") return "unavailable";
  return "ready";
}

/** Source strings (translate, then fill `{name}`) for each state. */
export const CHAT_PERSONAL_STATE_COPY: Record<Exclude<ChatPersonalState, "ready">, string> = {
  loading: "Loading…",
  not_configured: "{name} isn't connected to this workspace",
  not_approved: "Waiting for the app to be approved",
  not_linked: "Link your {name} account to turn this on",
  unavailable: "The app isn't available to your {name} account. Ask your {name} admin to add you to its availability range.",
};

/** The member's chat channel preferences; off with every type selected until they choose. */
export function chatChannelPreferences(preferences: NotificationPreferences): NotificationChannelPreferences {
  const categories = Object.fromEntries(Object.keys(preferences.email.categories).map((key) => [key, true])) as NotificationChannelPreferences["categories"];
  return preferences.chat ?? { enabled: false, categories };
}

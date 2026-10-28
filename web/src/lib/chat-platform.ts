import { useI18n } from "@/i18n/i18n";
import { chatProviderName } from "@/lib/integration-catalog";
import type { ChatIntegrationProvider } from "@/types/chat-integrations";
import type { BootstrapData } from "@/types/flow";

/** The deployment's first-class chat platform, chosen with FLOW_CHAT_PLATFORM. */
export type ChatPlatform = NonNullable<BootstrapData["chatPlatform"]>;

let rememberedPlatform: ChatPlatform | undefined;

function validPlatform(value: unknown): value is ChatPlatform {
  return value === "slack" || value === "lark" || value === "feishu";
}

/** Keeps the chat platform of the last loaded bootstrap, for surfaces rendered without workspace data. */
export function rememberChatPlatform<T extends Pick<BootstrapData, "chatPlatform">>(data: T): T {
  if (validPlatform(data.chatPlatform)) rememberedPlatform = data.chatPlatform;
  return data;
}

/** Test helper: forget the remembered platform. */
export function resetRememberedChatPlatform() {
  rememberedPlatform = undefined;
}

export function chatPlatformOf(data?: Pick<BootstrapData, "chatPlatform"> | null): ChatPlatform {
  if (validPlatform(data?.chatPlatform)) return data.chatPlatform;
  return rememberedPlatform ?? "slack";
}

export interface ChatPlatformInfo {
  platform: ChatPlatform;
  /** The group chat provider when the platform is Lark or Feishu. */
  provider?: ChatIntegrationProvider;
  /** Product name in the member's language (Feishu is 飞书 in Chinese). */
  name: string;
  /** Integration catalog slug of the platform's settings page. */
  settingsSlug: string;
  /** Fills `{name}` in an already translated text with the platform name. */
  format: (translated: string) => string;
}

export function chatPlatformInfo(platform: ChatPlatform, t: (source: string) => string): ChatPlatformInfo {
  const provider = platform === "slack" ? undefined : platform;
  const name = provider ? chatProviderName(provider, t) : "Slack";
  return { platform, provider, name, settingsSlug: platform, format: (translated) => translated.replaceAll("{name}", name) };
}

/** Source strings (for t, then format) of a team's chat notifications page. */
export function chatTeamNotificationsCopy(platform: ChatPlatform) {
  if (platform === "slack") return {
    label: "Slack notifications",
    description: "Broadcast notifications to Slack",
    page: "Connect a Slack channel to receive notifications when issues are created or updated in this team.",
    menu: "Configure Slack notifications…",
    connect: "Connect channel",
  };
  return {
    label: "{name} notifications",
    description: "Broadcast notifications to {name} groups",
    page: "Connect a {name} group to receive notifications when issues are created or updated in this team.",
    menu: "Configure {name} notifications…",
    connect: "Connect {name} group",
  };
}

/**
 * The chat platform every Slack-style entry point routes to: team channels,
 * project and initiative updates, notifications, onboarding, the catalog.
 */
export function useChatPlatform(data?: Pick<BootstrapData, "chatPlatform"> | null): ChatPlatformInfo {
  const { t } = useI18n();
  return chatPlatformInfo(chatPlatformOf(data), t);
}

import type { BootstrapData } from '@/types/flow'

/** The deployment's first-class chat platform, chosen with FLOW_CHAT_PLATFORM. */
export type ChatPlatform = NonNullable<BootstrapData['chatPlatform']>

let rememberedPlatform: ChatPlatform | undefined

function validPlatform(value: unknown): value is ChatPlatform {
  return value === 'slack' || value === 'lark' || value === 'feishu'
}

/** Keeps the platform of the last loaded bootstrap for surfaces without workspace data. */
export function rememberChatPlatform<T extends Pick<BootstrapData, 'chatPlatform'>>(data: T): T {
  if (validPlatform(data.chatPlatform)) rememberedPlatform = data.chatPlatform
  return data
}

export function resetRememberedChatPlatform() {
  rememberedPlatform = undefined
}

export function chatPlatformOf(data?: Pick<BootstrapData, 'chatPlatform'> | null): ChatPlatform {
  if (validPlatform(data?.chatPlatform)) return data.chatPlatform
  return rememberedPlatform ?? 'slack'
}

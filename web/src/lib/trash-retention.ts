import type { BootstrapData } from '@/types/flow'

let rememberedDays: number | undefined

/** Keeps the trash retention of the last loaded bootstrap, for texts shown without workspace data at hand. */
export function rememberTrashRetention<T extends Pick<BootstrapData, 'trashRetentionDays'>>(data: T): T {
  if (typeof data.trashRetentionDays === 'number' && data.trashRetentionDays > 0) rememberedDays = data.trashRetentionDays
  return data
}

/** How many days deleted items stay in "Recently deleted" before they are deleted permanently (the server's trash retention). */
export function trashRetentionDays(data?: Pick<BootstrapData, 'trashRetentionDays'> | null): number {
  const days = data?.trashRetentionDays
  if (typeof days === 'number' && days > 0) return days
  return rememberedDays ?? 30
}

/** Fills `{days}` in an already translated text with the trash retention. */
export function withTrashRetention(text: string, data?: Pick<BootstrapData, 'trashRetentionDays'> | null): string {
  return text.replaceAll('{days}', String(trashRetentionDays(data)))
}

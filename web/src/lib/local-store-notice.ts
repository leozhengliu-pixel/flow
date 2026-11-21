import type { LocalStoreAuthority } from './local-store-model'
import { storageChannel } from './realtime-storage'
import type { ChannelLike } from './shared-realtime'

export type LocalStoreScope = Pick<LocalStoreAuthority, 'viewerId' | 'workspaceId'> & { workspaceKey: string }

export type LocalStoreNotice =
  | { type: 'outbox' }
  | { type: 'settled'; id: string }
  | { type: 'commit'; head: string; from: string; baseline: string; stores: ['issue']; ids: string[] }

export function localStoreOwnerName({ viewerId, workspaceId }: LocalStoreScope) {
  return `flow-sync:${encodeURIComponent(viewerId)}:${encodeURIComponent(workspaceId)}`
}

export function openLocalStoreNotices(scope: LocalStoreScope, receive: (notice: LocalStoreNotice) => void) {
  const name = `${localStoreOwnerName(scope)}:store`
  let channel: ChannelLike | undefined
  try { if (typeof BroadcastChannel === 'function') channel = new BroadcastChannel(name) as unknown as ChannelLike }
  catch { /* Storage events cover browsers without a channel. */ }
  channel ??= storageChannel(name)
  if (channel) channel.onmessage = ({ data }) => {
    if (data && typeof data === 'object' && 'type' in data) receive(data as LocalStoreNotice)
  }
  return {
    post(notice: LocalStoreNotice) {
      try { channel?.postMessage(notice) }
      catch { /* The periodic disk check recovers a missed notice. */ }
    },
    close() { channel?.close() },
  }
}

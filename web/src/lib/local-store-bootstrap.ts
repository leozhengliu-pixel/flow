import type { BootstrapData } from '@/types/flow'
import { clearLocalStores, localStoreGeneration, retainLocalStoreAuthority } from './local-store-db'
import { LOCAL_STORE_CAPABILITY, LOCAL_STORE_HEADER, localStoreEnabled, readLocalStoreStamp, type LocalStoreAuthority } from './local-store-model'

const confirmed = new WeakMap<BootstrapData, { authority: LocalStoreAuthority; generation: number }>()

export function captureLocalStoreBootstrap(headers: Record<string, string>, projection?: string) {
  if (projection || !headers['X-Workspace-Key'] || !localStoreEnabled()) return undefined
  const generation = localStoreGeneration()
  headers[LOCAL_STORE_HEADER] = LOCAL_STORE_CAPABILITY
  let authority: LocalStoreAuthority | undefined
  return {
    observe: (response: Response) => { authority = response.ok ? readLocalStoreStamp(response.headers) : undefined },
    record: async (data: BootstrapData) => {
      if (authority && data.viewer.id === authority.viewerId && data.workspace.id === authority.workspaceId && data.workspace.urlKey === headers['X-Workspace-Key']) {
        const current = await retainLocalStoreAuthority(authority, data.workspace.urlKey, generation)
        if (current !== undefined) confirmed.set(data, { authority, generation: current })
      } else if (generation === localStoreGeneration()) {
        await clearLocalStores(undefined, headers['X-Workspace-Key'])
      }
      return data
    },
  }
}

export const localStoreBootstrap = (data: BootstrapData) => confirmed.get(data)

import { cacheStringBytes } from '@/lib/realtime-cache-limits'

export const QUERY_DIRECTORY_MAX_BYTES = 8 * 1024 * 1024
const owners = new Map<object, number>()

/** Shared by the page, previews, dependency menus, core catalog and initiative membership index. */
export function retainQueryWindowBytes(owner: object, bytes: number) {
  const previous = owners.get(owner) ?? 0
  const total = [...owners.values()].reduce((sum, value) => sum + value, 0) - previous + bytes
  if (total > QUERY_DIRECTORY_MAX_BYTES) return false
  if (bytes) owners.set(owner, bytes)
  else owners.delete(owner)
  return true
}

export function releaseQueryWindowBytes(owner: object) { owners.delete(owner) }
export function retainedQueryWindowBytes() { return [...owners.values()].reduce((sum, value) => sum + value, 0) }
export function retainQueryWindowCore(owner: object, core: unknown) { return retainQueryWindowBytes(owner, cacheStringBytes(JSON.stringify(core))) }

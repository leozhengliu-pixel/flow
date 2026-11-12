import { cacheStringBytes } from '@/lib/scale-p08'

export const SCALE_P19_DIRECTORY_BYTES = 8 * 1024 * 1024
const owners = new Map<object, number>()

/** Shared by the page, dependency menus, core catalog and initiative membership index. */
export function retainScaleP19Bytes(owner: object, bytes: number) {
  const previous = owners.get(owner) ?? 0
  const total = [...owners.values()].reduce((sum, value) => sum + value, 0) - previous + bytes
  if (total > SCALE_P19_DIRECTORY_BYTES) return false
  if (bytes) owners.set(owner, bytes)
  else owners.delete(owner)
  return true
}

export function releaseScaleP19Bytes(owner: object) { owners.delete(owner) }
export function retainedScaleP19Bytes() { return [...owners.values()].reduce((sum, value) => sum + value, 0) }
export function retainScaleP19Core(owner: object, core: unknown) { return retainScaleP19Bytes(owner, cacheStringBytes(JSON.stringify(core))) }

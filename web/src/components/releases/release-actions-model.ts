import { toast } from 'sonner'

import type { BootstrapData } from '@/types/flow'

/** Small helpers shared by the release menus, rows and pages. */
export function absoluteUrl(path: string) {
  return new URL(path, window.location.origin).href
}

export async function copyToClipboard(value: string, message: string, failure: string) {
  try { await navigator.clipboard.writeText(value); toast.success(message) } catch { toast.error(failure) }
}

export function isFavorite(data: BootstrapData, resourceType: 'release' | 'release_pipeline', id: string) {
  return data.favorites.some(item => item.resourceType === resourceType && item.resourceId === id)
}

import { request } from '@/lib/api-client'

/** GET /api/integrations/link-preview: the Open Graph card data of a Figma or X link (any field can be empty). */
export type LinkPreview = {
  url: string
  siteName: string
  title: string
  description: string
  imageUrl: string
}

/** Matches the server's cache lifetime closely enough that a document with the same link twice asks once. */
const TTL = 5 * 60_000
const cache = new Map<string, { at: number; promise: Promise<LinkPreview> }>()

export function fetchLinkPreview(src: string): Promise<LinkPreview> {
  const cached = cache.get(src)
  if (cached && Date.now() - cached.at < TTL) return cached.promise
  const promise = request<LinkPreview>(`/api/integrations/link-preview?url=${encodeURIComponent(src)}`)
  const entry = { at: Date.now(), promise }
  cache.set(src, entry)
  // Failures are not kept: the next render asks again, and the card meanwhile shows the plain link.
  promise.catch(() => { if (cache.get(src) === entry) cache.delete(src) })
  if (cache.size > 100) cache.delete(cache.keys().next().value as string)
  return promise
}

export function resetLinkPreviewCache() {
  cache.clear()
}

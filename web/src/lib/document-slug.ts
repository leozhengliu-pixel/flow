import type { FlowDocument } from '@/types/flow'

/** Slugs read `<title>-<hex suffix>`; the suffix stays when the title (and so the slug) changes. */
export function documentSlugSuffix(slugId: string) {
  const match = /-([0-9a-f]{4,16})$/.exec(slugId)
  return match?.[1]
}

/**
 * Resolves a document from a link made before it was renamed: the slug is
 * unknown (or listed in previousSlugIds) but the trailing suffix still matches.
 */
export function findDocumentBySlugSuffix<T extends Pick<FlowDocument, 'slugId'> & { previousSlugIds?: string[] }>(documents: T[], slugId: string): T | undefined {
  const previous = documents.find(document => document.previousSlugIds?.includes(slugId))
  if (previous) return previous
  const suffix = documentSlugSuffix(slugId)
  return suffix ? documents.find(document => documentSlugSuffix(document.slugId) === suffix) : undefined
}

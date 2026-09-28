import { createContext, useCallback, useContext, useMemo, type ReactNode } from 'react'
import { activityTargetFromHash, type ActivityHighlightTarget } from '@/components/activity/activity-highlight'
import { findAttachmentsByURL, uploadAttachment, uploadAttachmentFromURL } from '@/lib/api'
import type { Attachment } from '@/types/flow'

const DEFAULT_MAX_UPLOAD_BYTES = 20 << 20

export type ClientEditorUploadResult = {
  url: string
  attachment?: Attachment
}

export type ClientEditorProviderValue = {
  maxUploadBytes: number
  checkUploadSize: (file: File) => { ok: boolean; message?: string }
  uploadFile: (file: File, options?: { embed?: boolean; signal?: AbortSignal }) => Promise<ClientEditorUploadResult>
  imageUploadFromUrl: (url: string, options?: { title?: string; embed?: boolean }) => Promise<ClientEditorUploadResult>
  copyTextToClipboard: (text: string) => Promise<void>
  copyImageToClipboard: (blob: Blob) => Promise<void>
  fetchCommentByHash: (hash: string) => ActivityHighlightTarget | undefined
  fetchProjectUpdateByHash: (hash: string) => { kind: 'project-update'; id: string } | undefined
  fetchInitiativeUpdateByHash: (hash: string) => { kind: 'initiative-update'; id: string } | undefined
  fetchPullRequestCommentById: (id: string) => { kind: 'pull-request-comment'; id: string }
  fetchCustomerNeedByHash: (hash: string) => { kind: 'customer-need'; id: string } | undefined
  isAnyCommentInHash: (hash?: string) => boolean
  targetCommentHash: (commentId: string) => string
  getAnchoredCommentRedirectPath: (pathname: string, commentId: string) => string
}

const ClientEditorContext = createContext<ClientEditorProviderValue | null>(null)

export type ClientEditorProviderProps = {
  children: ReactNode
  /** Host issue id for attachment uploads (issue description / comment composers). */
  issueId?: string
  maxUploadBytes?: number
  onUploadFile?: (file: File, options?: { embed?: boolean }) => Promise<Attachment | string | void>
  onUploadFromUrl?: (url: string, options?: { title?: string; embed?: boolean }) => Promise<Attachment | string | void>
}

function parseUpdateHash<K extends 'project-update' | 'initiative-update'>(hash: string, kind: K): { kind: K; id: string } | undefined {
  const match = /^#(project-update|initiative-update|update)-(.+)$/.exec(hash)
  if (!match) return undefined
  if (kind === 'project-update' && match[1] !== 'project-update' && match[1] !== 'update') return undefined
  if (kind === 'initiative-update' && match[1] !== 'initiative-update' && match[1] !== 'update') return undefined
  try {
    return { kind, id: decodeURIComponent(match[2]) }
  } catch {
    return undefined
  }
}

export function ClientEditorProvider({
  children,
  issueId,
  maxUploadBytes = DEFAULT_MAX_UPLOAD_BYTES,
  onUploadFile,
  onUploadFromUrl,
}: ClientEditorProviderProps) {
  const checkUploadSize = useCallback((file: File) => {
    if (file.size > maxUploadBytes) {
      return { ok: false, message: `File exceeds ${Math.round(maxUploadBytes / (1024 * 1024))} MB limit` }
    }
    return { ok: true }
  }, [maxUploadBytes])

  const uploadFile = useCallback(async (file: File, options?: { embed?: boolean; signal?: AbortSignal }) => {
    if (options?.signal?.aborted) throw new DOMException('Upload aborted', 'AbortError')
    const size = checkUploadSize(file)
    if (!size.ok) throw new Error(size.message)
    if (onUploadFile) {
      const result = await onUploadFile(file, { embed: options?.embed })
      if (typeof result === 'string') return { url: result }
      if (result && typeof result === 'object' && 'url' in result) return { url: result.url, attachment: result }
      throw new Error('Upload failed')
    }
    if (!issueId) throw new Error('ClientEditorProvider requires issueId or onUploadFile')
    const attachment = await uploadAttachment(issueId, file, { embed: options?.embed })
    return { url: attachment.url, attachment }
  }, [checkUploadSize, issueId, onUploadFile])

  const imageUploadFromUrl = useCallback(async (url: string, options?: { title?: string; embed?: boolean }) => {
    const existing = await findAttachmentsByURL(url).catch(() => [])
    if (existing[0]?.url) return { url: existing[0].url, attachment: existing[0] }
    if (onUploadFromUrl) {
      const result = await onUploadFromUrl(url, options)
      if (typeof result === 'string') return { url: result }
      if (result && typeof result === 'object' && 'url' in result) return { url: result.url, attachment: result }
      throw new Error('URL upload failed')
    }
    if (!issueId) throw new Error('ClientEditorProvider requires issueId or onUploadFromUrl')
    const attachment = await uploadAttachmentFromURL(issueId, url, options)
    return { url: attachment.url, attachment }
  }, [issueId, onUploadFromUrl])

  const copyTextToClipboard = useCallback(async (text: string) => {
    await navigator.clipboard.writeText(text)
  }, [])

  const copyImageToClipboard = useCallback(async (blob: Blob) => {
    if (typeof ClipboardItem === 'undefined' || !navigator.clipboard?.write) {
      throw new Error('Image clipboard is not available in this browser')
    }
    await navigator.clipboard.write([new ClipboardItem({ [blob.type || 'image/png']: blob })])
  }, [])

  const value = useMemo<ClientEditorProviderValue>(() => ({
    maxUploadBytes,
    checkUploadSize,
    uploadFile,
    imageUploadFromUrl,
    copyTextToClipboard,
    copyImageToClipboard,
    fetchCommentByHash: activityTargetFromHash,
    fetchProjectUpdateByHash: (hash) => parseUpdateHash(hash, 'project-update'),
    fetchInitiativeUpdateByHash: (hash) => parseUpdateHash(hash, 'initiative-update'),
    fetchPullRequestCommentById: (id) => ({ kind: 'pull-request-comment', id }),
    fetchCustomerNeedByHash: (hash) => {
      const match = /^#(?:customer-need|need)-(.+)$/.exec(hash)
      if (!match) return undefined
      try {
        return { kind: 'customer-need', id: decodeURIComponent(match[1]) }
      } catch {
        return undefined
      }
    },
    isAnyCommentInHash: (hash = typeof window !== 'undefined' ? window.location.hash : '') => Boolean(activityTargetFromHash(hash)?.kind === 'comment'),
    targetCommentHash: (commentId) => `#comment-${commentId}`,
    getAnchoredCommentRedirectPath: (pathname, commentId) => `${pathname.split('#')[0]}#comment-${commentId}`,
  }), [maxUploadBytes, checkUploadSize, uploadFile, imageUploadFromUrl, copyTextToClipboard, copyImageToClipboard])

  return <ClientEditorContext.Provider value={value}>{children}</ClientEditorContext.Provider>
}

export function useClientEditor(): ClientEditorProviderValue {
  const value = useContext(ClientEditorContext)
  if (!value) throw new Error('useClientEditor must be used within ClientEditorProvider')
  return value
}

export function useOptionalClientEditor() {
  return useContext(ClientEditorContext)
}

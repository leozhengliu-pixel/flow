import { createContext, useContext } from 'react'
import type { ActivityHighlightTarget } from '@/components/activity/activity-highlight'
import type { Attachment } from '@/types/flow'

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

export const ClientEditorContext = createContext<ClientEditorProviderValue | null>(null)

export function useClientEditor(): ClientEditorProviderValue {
  const value = useContext(ClientEditorContext)
  if (!value) throw new Error('useClientEditor must be used within ClientEditorProvider')
  return value
}

export function useOptionalClientEditor() {
  return useContext(ClientEditorContext)
}

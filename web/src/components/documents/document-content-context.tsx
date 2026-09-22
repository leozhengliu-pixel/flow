/**
 * LS-0211 DocumentContentContext — named provider for live document body + presence.
 * Feeds CollaborativeEditor contentContext slot and future agent/minimap hosts.
 */
import { createContext, useContext, useMemo, type ReactNode } from 'react'
import type { User } from '@/types/flow'

export interface DocumentContentValue {
  documentId: string
  content: string
  contentState?: string
  presence: User[]
}

const DocumentContentContext = createContext<DocumentContentValue | null>(null)

export function DocumentContentProvider({
  documentId,
  content,
  contentState,
  presence,
  children,
}: DocumentContentValue & { children: ReactNode }) {
  const value = useMemo(
    () => ({ documentId, content, contentState, presence }),
    [documentId, content, contentState, presence],
  )
  return (
    <DocumentContentContext.Provider value={value}>
      {children}
    </DocumentContentContext.Provider>
  )
}

export function useDocumentContent(): DocumentContentValue {
  const value = useContext(DocumentContentContext)
  if (!value) {
    throw new Error('useDocumentContent must be used within DocumentContentProvider')
  }
  return value
}

export function useDocumentContentOptional(): DocumentContentValue | null {
  return useContext(DocumentContentContext)
}

export default DocumentContentProvider

/**
 * LS-0211 DocumentContentContext — context object and hooks for the live document body + presence.
 */
import { createContext, useContext } from 'react'
import type { User } from '@/types/flow'

export interface DocumentContentValue {
  documentId: string
  content: string
  contentState?: string
  presence: User[]
}

export const DocumentContentContext = createContext<DocumentContentValue | null>(null)

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

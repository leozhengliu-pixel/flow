/**
 * LS-0211 DocumentContentContext — named provider for live document body + presence.
 * Feeds CollaborativeEditor contentContext slot and future agent/minimap hosts.
 */
import { useMemo, type ReactNode } from 'react'
import { DocumentContentContext, type DocumentContentValue } from './document-content-context-value'

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

export default DocumentContentProvider

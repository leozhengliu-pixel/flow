import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { DocumentContentProvider, useDocumentContent } from './document-content-context'

function Probe() {
  const value = useDocumentContent()
  return (
    <div>
      <span>{value.documentId}</span>
      <span>{value.content}</span>
      <span>{value.presence.length}</span>
    </div>
  )
}

describe('DocumentContentContext', () => {
  it('provides content and presence to consumers', () => {
    render(
      <DocumentContentProvider
        documentId="d1"
        content="notes"
        contentState="{}"
        presence={[{ id: 'u1', name: 'Ada', displayName: 'Ada', email: 'a@x', active: true, emailVerified: true } as never]}
      >
        <Probe />
      </DocumentContentProvider>,
    )
    expect(screen.getByText('d1')).toBeInTheDocument()
    expect(screen.getByText('notes')).toBeInTheDocument()
    expect(screen.getByText('1')).toBeInTheDocument()
  })
})

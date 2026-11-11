import { render, screen, waitFor } from '@testing-library/react'
import { useState } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData, FlowDocument } from '@/types/flow'

const api = vi.hoisted(() => ({ getDocument: vi.fn() }))
vi.mock('@/lib/api', () => api)

import { useRouteDocument } from './use-route-document'

const created = { id: 'document-new', slugId: 'release-checklist-0123456789ab', title: 'Release checklist', content: 'Body', creator: makeBootstrap().viewer, projectIds: [], teamIds: [], subscriberIds: [], favorite: false, createdAt: '2026-11-11T00:00:00Z', updatedAt: '2026-11-11T00:00:00Z', revisions: [] } satisfies FlowDocument

// The App's lookup: the route's document from the workspace data, else what the hook says.
function DocumentRoute({ slugId, initial }: { slugId: string; initial: BootstrapData }) {
  const [data, setData] = useState<BootstrapData | null>(initial)
  const document = data?.documents.find(item => item.slugId === slugId || item.id === slugId)
  const state = useRouteDocument({ data, slugId, found: Boolean(document), setData })
  return <p>{document ? `open:${document.title}` : state}</p>
}

describe('useRouteDocument', () => {
  beforeEach(() => { api.getDocument.mockReset() })

  it('opens a document the loaded data does not hold yet, such as one an Agent reply just created', async () => {
    api.getDocument.mockResolvedValue(created)
    render(<DocumentRoute slugId={created.slugId} initial={makeBootstrap({ documents: [] })}/>)
    expect(screen.getByText('loading')).toBeInTheDocument()
    expect(await screen.findByText('open:Release checklist')).toBeInTheDocument()
    expect(api.getDocument).toHaveBeenCalledWith(created.slugId)
  })

  it('does not fetch a document the data already holds', () => {
    render(<DocumentRoute slugId={created.slugId} initial={makeBootstrap({ documents: [created] })}/>)
    expect(screen.getByText('open:Release checklist')).toBeInTheDocument()
    expect(api.getDocument).not.toHaveBeenCalled()
  })

  it('reads as missing only once the server does not return the document', async () => {
    api.getDocument.mockRejectedValue(Object.assign(new Error('document not found'), { status: 404 }))
    render(<DocumentRoute slugId="no-such-document-0123456789ab" initial={makeBootstrap({ documents: [] })}/>)
    expect(screen.getByText('loading')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByText('missing')).toBeInTheDocument())
  })
})

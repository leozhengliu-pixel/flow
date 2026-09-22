import { fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { FlowDocument } from '@/types/flow'
import { DocumentsIndexPage } from './documents-index-page'

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })

const sample = {
  id: 'document-1', slugId: 'document-one', title: 'Spec', content: 'body', creator: viewer,
  projectIds: [], teamIds: ['team-1'], subscriberIds: [], favorite: false,
  createdAt: '2026-09-01T00:00:00.000Z', updatedAt: '2026-09-02T00:00:00.000Z', revisions: [],
} as FlowDocument

describe('DocumentsMultiSelectActions', () => {
  beforeEach(() => { vi.stubGlobal('ResizeObserver', TestResizeObserver) })

  it('selects documents and opens bulk Actions', async () => {
    const data = makeBootstrap({ documents: [sample], comments: {} })
    render(<I18nProvider><DocumentsIndexPage data={data} onReload={vi.fn()} /></I18nProvider>)
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select document' }))
    expect(screen.getByRole('toolbar', { name: '1 selected documents' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Open document actions' }))
    expect(screen.getByText('Archive')).toBeInTheDocument()
    expect(screen.getByText('Delete…')).toBeInTheDocument()
    expect(screen.getByText('Move to project…')).toBeInTheDocument()
  })
})

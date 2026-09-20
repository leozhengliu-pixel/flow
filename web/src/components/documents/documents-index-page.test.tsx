import { fireEvent, render, screen } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer, teammate, project } from '@/test/fixtures'
import type { FlowDocument } from '@/types/flow'
import { DocumentsIndexPage } from './documents-index-page'

it('restores document filters from the route and preserves unrelated query parameters', () => {
  const onFiltersChange = vi.fn()
  render(<I18nProvider><DocumentsIndexPage data={makeBootstrap({ documents: [] })} onReload={vi.fn()} search="?q=notes&teamId=team-1&archived=true&sort=updated" onFiltersChange={onFiltersChange}/></I18nProvider>)
  expect(screen.getByRole('textbox', { name: 'Search documents' })).toHaveValue('notes')
  expect(screen.getByRole('checkbox', { name: /Show archived|archived/i })).toBeChecked()
  fireEvent.change(screen.getByRole('textbox', { name: 'Search documents' }), { target: { value: 'spec' } })
  expect(new URLSearchParams(onFiltersChange.mock.calls[0][0]).get('q')).toBe('spec')
  expect(new URLSearchParams(onFiltersChange.mock.calls[0][0]).get('teamId')).toBe('team-1')
  expect(new URLSearchParams(onFiltersChange.mock.calls[0][0]).get('sort')).toBe('updated')
})

it('restores Creator + Project + Dates filter blocks from the route', () => {
  const docs = [
    { id: 'document-1', slugId: 'a', title: 'Alpha', content: '', creator: viewer, projectIds: [project.id], teamIds: ['team-1'], subscriberIds: [], favorite: false, createdAt: '2026-09-20T00:00:00.000Z', updatedAt: '2026-09-20T00:00:00.000Z', revisions: [] },
    { id: 'document-2', slugId: 'b', title: 'Beta', content: '', creator: teammate, projectIds: [], teamIds: ['team-1'], subscriberIds: [], favorite: false, createdAt: '2026-09-20T00:00:00.000Z', updatedAt: '2026-09-20T00:00:00.000Z', revisions: [] },
  ] as FlowDocument[]
  const onFiltersChange = vi.fn()
  render(<I18nProvider><DocumentsIndexPage data={makeBootstrap({ documents: docs })} onReload={vi.fn()} search={`?creatorId=${viewer.id}&projectId=${project.id}&dates=today`} onFiltersChange={onFiltersChange}/></I18nProvider>)
  expect(screen.getByText('Alpha')).toBeInTheDocument()
  expect(screen.queryByText('Beta')).not.toBeInTheDocument()
  expect(screen.getByRole('combobox', { name: /Creator|创建者/i })).toBeInTheDocument()
  expect(screen.getByRole('combobox', { name: /Project|项目/i })).toBeInTheDocument()
  expect(screen.getByRole('combobox', { name: /Dates|日期/i })).toBeInTheDocument()
})
